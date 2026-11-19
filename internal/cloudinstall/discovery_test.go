package cloudinstall

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
)

var (
	argoApplicationGVR   = schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}
	fluxKustomizationGVR = schema.GroupVersionResource{Group: "kustomize.toolkit.fluxcd.io", Version: "v1", Resource: "kustomizations"}
	fluxHelmReleaseGVR   = schema.GroupVersionResource{Group: "helm.toolkit.fluxcd.io", Version: "v2", Resource: "helmreleases"}
)

func kubeatlasDeployment(namespace, name, release string, labels, annotations map[string]string) *appsv1.Deployment {
	allLabels := map[string]string{
		"helm.sh/chart":                "kubeatlas-1.6.0",
		"app.kubernetes.io/name":       "kubeatlas",
		"app.kubernetes.io/instance":   release,
		"app.kubernetes.io/managed-by": "Helm",
	}
	for key, value := range labels {
		allLabels[key] = value
	}
	return &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
		Namespace:   namespace,
		Name:        name,
		Labels:      allLabels,
		Annotations: annotations,
	}}
}

func controllerObject(apiVersion, kind, namespace, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata":   map[string]any{"namespace": namespace, "name": name},
	}}
}

func fluxHelmRelease(namespace, name, releaseName, targetNamespace, storageNamespace string) *unstructured.Unstructured {
	obj := controllerObject("helm.toolkit.fluxcd.io/v2", "HelmRelease", namespace, name)
	obj.Object["spec"] = map[string]any{
		"releaseName":      releaseName,
		"targetNamespace":  targetNamespace,
		"storageNamespace": storageNamespace,
	}
	return obj
}

func fakeControllerClient(objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			argoApplicationGVR:   "ApplicationList",
			fluxKustomizationGVR: "KustomizationList",
			fluxHelmReleaseGVR:   "HelmReleaseList",
		},
		objects...,
	)
}

func TestDiscoverKubeAtlasTargetsNativeHelmSelectedTarget(t *testing.T) {
	deployment := kubeatlasDeployment("kubeatlas", "kubeatlas", "kubeatlas", nil, map[string]string{
		"meta.helm.sh/release-name":      "kubeatlas",
		"meta.helm.sh/release-namespace": "kubeatlas",
	})
	replicas := int32(2)
	deployment.Spec.Replicas = &replicas
	deployment.Generation = 3
	deployment.Status.ObservedGeneration = 3
	deployment.Status.Replicas = 2
	deployment.Status.UpdatedReplicas = 1
	deployment.Status.ReadyReplicas = 1
	deployment.Status.AvailableReplicas = 1
	kc := kubernetesfake.NewSimpleClientset(deployment)

	result, err := DiscoverKubeAtlasTargets(context.Background(), kc, nil, DiscoveryOptions{})
	if err != nil {
		t.Fatalf("DiscoverKubeAtlasTargets: %v", err)
	}
	if len(result.Selected) != 1 {
		t.Fatalf("selected targets = %d, want 1: %#v", len(result.Selected), result.Selected)
	}
	target := result.Selected[0]
	if target.Namespace != "kubeatlas" || target.DeploymentName != "kubeatlas" || target.ReleaseName != "kubeatlas" || target.Chart != "kubeatlas-1.6.0" {
		t.Fatalf("unexpected target: %#v", target)
	}
	if target.Runtime.DesiredReplicas != 2 || target.Runtime.TotalReplicas != 2 || target.Runtime.UpdatedReplicas != 1 || target.Runtime.ReadyReplicas != 1 || target.Runtime.AvailableReplicas != 1 || !target.Runtime.StatusObserved {
		t.Fatalf("runtime replicas = %#v, want 1/2 updated, ready, and available", target.Runtime)
	}
	if target.Ownership.Classification != OwnershipNativeHelm {
		t.Fatalf("ownership = %q, want %q", target.Ownership.Classification, OwnershipNativeHelm)
	}
	if target.Ownership.NativeHelm == nil || target.Ownership.NativeHelm.Name != "kubeatlas" || target.Ownership.NativeHelm.Namespace != "kubeatlas" {
		t.Fatalf("native Helm ref = %#v", target.Ownership.NativeHelm)
	}
	if !target.Ownership.NativeHelmMatchesTarget {
		t.Fatal("native Helm metadata should match selected target")
	}
}

func TestDiscoverKubeAtlasTargetsMarksSourcedCloudModeUnresolved(t *testing.T) {
	deployment := kubeatlasDeployment("kubeatlas", "kubeatlas", "kubeatlas", nil, nil)
	deployment.Spec.Template.Spec.Containers = []corev1.Container{{
		Name: "kubeatlas",
		Env: []corev1.EnvVar{{
			Name: "KUBEATLAS_CLOUD_MODE",
			ValueFrom: &corev1.EnvVarSource{ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "kubeatlas-config"}, Key: "cloud-mode",
			}},
		}},
	}}

	result, err := DiscoverKubeAtlasTargets(context.Background(), kubernetesfake.NewSimpleClientset(deployment), nil, DiscoveryOptions{})
	if err != nil {
		t.Fatalf("DiscoverKubeAtlasTargets: %v", err)
	}
	runtime := result.Selected[0].Runtime
	if !runtime.CloudModeConfigured || !runtime.CloudModeUnresolved || !runtime.AlreadyCloud {
		t.Fatalf("cloud runtime = %#v", runtime)
	}
}

func TestDiscoverKubeAtlasTargetsMarksSourcedCloudIdentityUnresolved(t *testing.T) {
	deployment := kubeatlasDeployment("kubeatlas", "kubeatlas", "kubeatlas", nil, nil)
	deployment.Spec.Template.Spec.Containers = []corev1.Container{{
		Name: "kubeatlas",
		Env: []corev1.EnvVar{
			{Name: "KUBEATLAS_CLOUD_URL", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "kubeatlas-cloud"}, Key: "url"}}},
			{Name: "KUBEATLAS_CLOUD_CLUSTER_NAME", ValueFrom: &corev1.EnvVarSource{ConfigMapKeyRef: &corev1.ConfigMapKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "kubeatlas-config"}, Key: "cluster"}}},
		},
	}}

	result, err := DiscoverKubeAtlasTargets(context.Background(), kubernetesfake.NewSimpleClientset(deployment), nil, DiscoveryOptions{})
	if err != nil {
		t.Fatalf("DiscoverKubeAtlasTargets: %v", err)
	}
	runtime := result.Selected[0].Runtime
	if !runtime.CloudURLConfigured || !runtime.CloudURLUnresolved || !runtime.ClusterNameConfigured || !runtime.ClusterNameUnresolved {
		t.Fatalf("cloud runtime = %#v", runtime)
	}
}

func TestDiscoverKubeAtlasTargetsNeverRetainsLiteralCloudToken(t *testing.T) {
	const token = "rhc_super_secret_literal"
	deployment := kubeatlasDeployment("kubeatlas", "kubeatlas", "kubeatlas", nil, nil)
	deployment.Spec.Template.Spec.Containers = []corev1.Container{{
		Name: "kubeatlas",
		Args: []string{"--cloud-token", token},
	}}

	result, err := DiscoverKubeAtlasTargets(context.Background(), kubernetesfake.NewSimpleClientset(deployment), nil, DiscoveryOptions{})
	if err != nil {
		t.Fatalf("DiscoverKubeAtlasTargets: %v", err)
	}
	runtime := result.Selected[0].Runtime
	if !runtime.CloudTokenConfigured {
		t.Fatal("literal Cloud token was not detected as configured")
	}
	if runtime.CloudTokenSecretName != "" || runtime.CloudTokenSecretKey != "" {
		t.Fatalf("literal Cloud token invented Secret reference: %#v", runtime)
	}
	if strings.Contains(fmt.Sprintf("%#v", runtime), token) {
		t.Fatal("deployment runtime retained the literal Cloud token")
	}
}

func TestDiscoverKubeAtlasTargetsNoHistoryFluxWorkload(t *testing.T) {
	deployment := kubeatlasDeployment("observability", "prod-kubeatlas", "prod", map[string]string{
		"helm.toolkit.fluxcd.io/name":      "kubeatlas-prod",
		"helm.toolkit.fluxcd.io/namespace": "flux-system",
	}, nil)
	dc := fakeControllerClient(fluxHelmRelease("flux-system", "kubeatlas-prod", "prod", "observability", "flux-system"))

	result, err := DiscoverKubeAtlasTargets(context.Background(), kubernetesfake.NewSimpleClientset(deployment), dc, DiscoveryOptions{
		Namespace: "observability", ReleaseName: "prod",
	})
	if err != nil {
		t.Fatalf("DiscoverKubeAtlasTargets: %v", err)
	}
	if len(result.Selected) != 1 {
		t.Fatalf("selected targets = %d, want 1", len(result.Selected))
	}
	ownership := result.Selected[0].Ownership
	if ownership.Classification != OwnershipGitOpsVerified {
		t.Fatalf("ownership = %q, want %q: %#v", ownership.Classification, OwnershipGitOpsVerified, ownership)
	}
	if len(ownership.Controllers) != 1 || ownership.Controllers[0].Verification != ControllerVerified {
		t.Fatalf("controllers = %#v", ownership.Controllers)
	}
	if ownership.Controllers[0].ReleaseName != "prod" || ownership.Controllers[0].TargetNamespace != "observability" || ownership.Controllers[0].StorageNamespace != "flux-system" {
		t.Fatalf("Flux target = %#v", ownership.Controllers[0])
	}
}

func TestDiscoverKubeAtlasTargetsVerifiesFluxKustomization(t *testing.T) {
	deployment := kubeatlasDeployment("kubeatlas", "kubeatlas", "kubeatlas", map[string]string{
		"kustomize.toolkit.fluxcd.io/name":      "platform",
		"kustomize.toolkit.fluxcd.io/namespace": "flux-system",
	}, nil)
	dc := fakeControllerClient(controllerObject("kustomize.toolkit.fluxcd.io/v1", "Kustomization", "flux-system", "platform"))

	result, err := DiscoverKubeAtlasTargets(context.Background(), kubernetesfake.NewSimpleClientset(deployment), dc, DiscoveryOptions{})
	if err != nil {
		t.Fatalf("DiscoverKubeAtlasTargets: %v", err)
	}
	if got := result.Selected[0].Ownership.Classification; got != OwnershipGitOpsVerified {
		t.Fatalf("ownership = %q, want %q", got, OwnershipGitOpsVerified)
	}
}

func TestDiscoverKubeAtlasTargetsVerifiesNamespacedArgoApplication(t *testing.T) {
	deployment := kubeatlasDeployment("kubeatlas", "kubeatlas", "kubeatlas", nil, map[string]string{
		"argocd.argoproj.io/tracking-id": "argocd_kubeatlas-app:apps/Deployment:kubeatlas/kubeatlas",
	})
	dc := fakeControllerClient(controllerObject("argoproj.io/v1alpha1", "Application", "argocd", "kubeatlas-app"))

	result, err := DiscoverKubeAtlasTargets(context.Background(), kubernetesfake.NewSimpleClientset(deployment), dc, DiscoveryOptions{})
	if err != nil {
		t.Fatalf("DiscoverKubeAtlasTargets: %v", err)
	}
	controller := result.Selected[0].Ownership.Controllers[0]
	if result.Selected[0].Ownership.Classification != OwnershipGitOpsVerified || controller.Ref.Namespace != "argocd" {
		t.Fatalf("ownership = %#v", result.Selected[0].Ownership)
	}
}

func TestDiscoverKubeAtlasTargetsResolvesLegacyArgoApplicationNamespace(t *testing.T) {
	deployment := kubeatlasDeployment("kubeatlas", "kubeatlas", "kubeatlas", map[string]string{
		"argocd.argoproj.io/instance": "kubeatlas-app",
	}, nil)
	dc := fakeControllerClient(controllerObject("argoproj.io/v1alpha1", "Application", "argocd", "kubeatlas-app"))

	result, err := DiscoverKubeAtlasTargets(context.Background(), kubernetesfake.NewSimpleClientset(deployment), dc, DiscoveryOptions{})
	if err != nil {
		t.Fatalf("DiscoverKubeAtlasTargets: %v", err)
	}
	controller := result.Selected[0].Ownership.Controllers[0]
	if controller.Verification != ControllerVerified || controller.Ref.Namespace != "argocd" || len(controller.Matches) != 1 {
		t.Fatalf("controller = %#v", controller)
	}
}

func TestDiscoverKubeAtlasTargetsAmbiguousLegacyArgoApplication(t *testing.T) {
	deployment := kubeatlasDeployment("kubeatlas", "kubeatlas", "kubeatlas", map[string]string{
		"argocd.argoproj.io/instance": "kubeatlas-app",
	}, nil)
	dc := fakeControllerClient(
		controllerObject("argoproj.io/v1alpha1", "Application", "argocd-a", "kubeatlas-app"),
		controllerObject("argoproj.io/v1alpha1", "Application", "argocd-b", "kubeatlas-app"),
	)

	result, err := DiscoverKubeAtlasTargets(context.Background(), kubernetesfake.NewSimpleClientset(deployment), dc, DiscoveryOptions{})
	if err != nil {
		t.Fatalf("DiscoverKubeAtlasTargets: %v", err)
	}
	ownership := result.Selected[0].Ownership
	if ownership.Classification != OwnershipAmbiguous || ownership.Controllers[0].Verification != ControllerAmbiguous {
		t.Fatalf("ownership = %#v", ownership)
	}
}

func TestDiscoverKubeAtlasTargetsGitOpsEvidenceStates(t *testing.T) {
	deployment := kubeatlasDeployment("kubeatlas", "kubeatlas", "kubeatlas", map[string]string{
		"helm.toolkit.fluxcd.io/name":      "kubeatlas",
		"helm.toolkit.fluxcd.io/namespace": "flux-system",
	}, map[string]string{
		"meta.helm.sh/release-name":      "kubeatlas",
		"meta.helm.sh/release-namespace": "kubeatlas",
	})

	t.Run("suspected without dynamic client", func(t *testing.T) {
		result, err := DiscoverKubeAtlasTargets(context.Background(), kubernetesfake.NewSimpleClientset(deployment), nil, DiscoveryOptions{})
		if err != nil {
			t.Fatalf("DiscoverKubeAtlasTargets: %v", err)
		}
		ownership := result.Selected[0].Ownership
		if ownership.Classification != OwnershipGitOpsSuspected || ownership.NativeHelm == nil {
			t.Fatalf("ownership = %#v", ownership)
		}
	})

	t.Run("stale strong marker", func(t *testing.T) {
		result, err := DiscoverKubeAtlasTargets(context.Background(), kubernetesfake.NewSimpleClientset(deployment), fakeControllerClient(), DiscoveryOptions{})
		if err != nil {
			t.Fatalf("DiscoverKubeAtlasTargets: %v", err)
		}
		ownership := result.Selected[0].Ownership
		if ownership.Classification != OwnershipGitOpsStale || ownership.NativeHelm == nil || ownership.Controllers[0].Verification != ControllerStale {
			t.Fatalf("ownership = %#v", ownership)
		}
	})

	t.Run("unreadable controller", func(t *testing.T) {
		dc := fakeControllerClient()
		dc.PrependReactor("get", "helmreleases", func(action clienttesting.Action) (bool, runtime.Object, error) {
			return true, nil, apierrors.NewForbidden(schema.GroupResource{
				Group: "helm.toolkit.fluxcd.io", Resource: "helmreleases",
			}, "kubeatlas", errors.New("denied"))
		})
		result, err := DiscoverKubeAtlasTargets(context.Background(), kubernetesfake.NewSimpleClientset(deployment), dc, DiscoveryOptions{})
		if err != nil {
			t.Fatalf("DiscoverKubeAtlasTargets: %v", err)
		}
		ownership := result.Selected[0].Ownership
		if ownership.Classification != OwnershipGitOpsUnreadable || ownership.Controllers[0].Verification != ControllerUnreadable || ownership.Controllers[0].Error == "" {
			t.Fatalf("ownership = %#v", ownership)
		}
	})
}

func TestDiscoverKubeAtlasTargetsMultipleGitOpsOwnersAreAmbiguous(t *testing.T) {
	deployment := kubeatlasDeployment("kubeatlas", "kubeatlas", "kubeatlas", map[string]string{
		"helm.toolkit.fluxcd.io/name":           "kubeatlas-release",
		"helm.toolkit.fluxcd.io/namespace":      "flux-system",
		"kustomize.toolkit.fluxcd.io/name":      "platform",
		"kustomize.toolkit.fluxcd.io/namespace": "flux-system",
	}, nil)
	dc := fakeControllerClient(
		fluxHelmRelease("flux-system", "kubeatlas-release", "kubeatlas", "kubeatlas", "flux-system"),
		controllerObject("kustomize.toolkit.fluxcd.io/v1", "Kustomization", "flux-system", "platform"),
	)

	result, err := DiscoverKubeAtlasTargets(context.Background(), kubernetesfake.NewSimpleClientset(deployment), dc, DiscoveryOptions{})
	if err != nil {
		t.Fatalf("DiscoverKubeAtlasTargets: %v", err)
	}
	if got := result.Selected[0].Ownership.Classification; got != OwnershipAmbiguous {
		t.Fatalf("ownership = %q, want %q", got, OwnershipAmbiguous)
	}
}

func TestDiscoverKubeAtlasTargetsRejectsUnrelatedFluxHelmRelease(t *testing.T) {
	deployment := kubeatlasDeployment("kubeatlas", "kubeatlas", "kubeatlas", map[string]string{
		"helm.toolkit.fluxcd.io/name":      "shared-name",
		"helm.toolkit.fluxcd.io/namespace": "flux-system",
	}, nil)
	dc := fakeControllerClient(fluxHelmRelease("flux-system", "shared-name", "other", "other-namespace", "flux-system"))

	result, err := DiscoverKubeAtlasTargets(context.Background(), kubernetesfake.NewSimpleClientset(deployment), dc, DiscoveryOptions{})
	if err != nil {
		t.Fatalf("DiscoverKubeAtlasTargets: %v", err)
	}
	controller := result.Selected[0].Ownership.Controllers[0]
	if result.Selected[0].Ownership.Classification != OwnershipGitOpsStale || controller.Verification != ControllerStale || controller.Error == "" {
		t.Fatalf("ownership = %#v", result.Selected[0].Ownership)
	}
}

func TestDiscoverKubeAtlasTargetsUsesFluxDefaultReleaseName(t *testing.T) {
	deployment := kubeatlasDeployment("observability", "observability-kubeatlas", "observability-kubeatlas-prod", map[string]string{
		"helm.toolkit.fluxcd.io/name":      "kubeatlas-prod",
		"helm.toolkit.fluxcd.io/namespace": "flux-system",
	}, nil)
	hr := fluxHelmRelease("flux-system", "kubeatlas-prod", "", "observability", "")
	dc := fakeControllerClient(hr)

	result, err := DiscoverKubeAtlasTargets(context.Background(), kubernetesfake.NewSimpleClientset(deployment), dc, DiscoveryOptions{
		Namespace: "observability", ReleaseName: "observability-kubeatlas-prod",
	})
	if err != nil {
		t.Fatalf("DiscoverKubeAtlasTargets: %v", err)
	}
	controller := result.Selected[0].Ownership.Controllers[0]
	if result.Selected[0].Ownership.Classification != OwnershipGitOpsVerified || controller.ReleaseName != "observability-kubeatlas-prod" || controller.StorageNamespace != "flux-system" {
		t.Fatalf("ownership = %#v", result.Selected[0].Ownership)
	}
}

func TestDiscoverKubeAtlasTargetsRequiresNativeHelmMetadataToMatchTarget(t *testing.T) {
	deployment := kubeatlasDeployment("kubeatlas", "kubeatlas", "kubeatlas", nil, map[string]string{
		"meta.helm.sh/release-name":      "other",
		"meta.helm.sh/release-namespace": "kubeatlas",
	})

	result, err := DiscoverKubeAtlasTargets(context.Background(), kubernetesfake.NewSimpleClientset(deployment), nil, DiscoveryOptions{})
	if err != nil {
		t.Fatalf("DiscoverKubeAtlasTargets: %v", err)
	}
	ownership := result.Selected[0].Ownership
	if ownership.Classification != OwnershipAmbiguous || ownership.NativeHelmMatchesTarget {
		t.Fatalf("ownership = %#v", ownership)
	}
}

func TestDiscoverKubeAtlasTargetsClusterWideIsBestEffortAndNeverSelects(t *testing.T) {
	kc := kubernetesfake.NewSimpleClientset(
		kubeatlasDeployment("kubeatlas", "kubeatlas", "kubeatlas", nil, nil),
		kubeatlasDeployment("observability", "prod-kubeatlas", "prod", nil, nil),
		kubeatlasDeployment("monitoring", "staging-kubeatlas", "staging", nil, nil),
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
			Namespace: "kubeatlas-hub", Name: "kubeatlas-hub", Labels: map[string]string{"helm.sh/chart": "kubeatlas-hub-0.1.0"},
		}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
			Namespace: "other", Name: "other", Labels: map[string]string{"helm.sh/chart": "prometheus-1.0.0"},
		}},
	)

	result, err := DiscoverKubeAtlasTargets(context.Background(), kc, nil, DiscoveryOptions{ClusterWide: true})
	if err != nil {
		t.Fatalf("DiscoverKubeAtlasTargets: %v", err)
	}
	if len(result.Selected) != 1 || result.Selected[0].DeploymentName != "kubeatlas" {
		t.Fatalf("selected = %#v", result.Selected)
	}
	if len(result.Namespace) != 1 {
		t.Fatalf("namespace targets = %#v", result.Namespace)
	}
	if len(result.ClusterWide) != 2 {
		t.Fatalf("cluster-wide targets = %#v", result.ClusterWide)
	}
	if result.ClusterWide[0].Namespace != "monitoring" || result.ClusterWide[1].Namespace != "observability" {
		t.Fatalf("cluster-wide order = %#v", result.ClusterWide)
	}
}

func TestDiscoverKubeAtlasTargetsClusterWideForbiddenKeepsSelectedInspection(t *testing.T) {
	kc := kubernetesfake.NewSimpleClientset(kubeatlasDeployment("kubeatlas", "kubeatlas", "kubeatlas", nil, nil))
	kc.PrependReactor("list", "deployments", func(action clienttesting.Action) (bool, runtime.Object, error) {
		list := action.(clienttesting.ListAction)
		if list.GetNamespace() != metav1.NamespaceAll {
			return false, nil, nil
		}
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "deployments"}, "", errors.New("denied"))
	})

	result, err := DiscoverKubeAtlasTargets(context.Background(), kc, nil, DiscoveryOptions{ClusterWide: true})
	if err != nil {
		t.Fatalf("DiscoverKubeAtlasTargets: %v", err)
	}
	if len(result.Selected) != 1 || result.ClusterWideError == nil || len(result.ClusterWide) != 0 {
		t.Fatalf("result = %#v", result)
	}
}

func TestDiscoverKubeAtlasTargetsSelectedNamespaceForbiddenIsFatal(t *testing.T) {
	kc := kubernetesfake.NewSimpleClientset()
	kc.PrependReactor("list", "deployments", func(action clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "deployments"}, "kubeatlas", errors.New("denied"))
	})

	_, err := DiscoverKubeAtlasTargets(context.Background(), kc, nil, DiscoveryOptions{})
	if err == nil {
		t.Fatal("expected selected namespace lookup failure")
	}
}

func TestDiscoverKubeAtlasTargetsMissingSelectedNamespaceIsFresh(t *testing.T) {
	kc := kubernetesfake.NewSimpleClientset()
	kc.PrependReactor("list", "deployments", func(action clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewNotFound(schema.GroupResource{Group: "apps", Resource: "deployments"}, "kubeatlas")
	})

	result, err := DiscoverKubeAtlasTargets(context.Background(), kc, nil, DiscoveryOptions{})
	if err != nil {
		t.Fatalf("DiscoverKubeAtlasTargets: %v", err)
	}
	if len(result.Selected) != 0 || len(result.Namespace) != 0 {
		t.Fatalf("result = %#v", result)
	}
}

func TestDiscoverKubeAtlasTargetsDoesNotMatchAnotherRelease(t *testing.T) {
	kc := kubernetesfake.NewSimpleClientset(kubeatlasDeployment("kubeatlas", "other-kubeatlas", "other", nil, nil))

	result, err := DiscoverKubeAtlasTargets(context.Background(), kc, nil, DiscoveryOptions{})
	if err != nil {
		t.Fatalf("DiscoverKubeAtlasTargets: %v", err)
	}
	if len(result.Selected) != 0 || len(result.Namespace) != 1 {
		t.Fatalf("result = %#v", result)
	}
}

func TestDiscoverKubeAtlasTargetsGenericOfficialDeployment(t *testing.T) {
	deployment := kubeatlasDeployment("kubeatlas", "kubeatlas", "kubeatlas", nil, nil)
	deployment.Labels["app.kubernetes.io/managed-by"] = "custom-controller"

	result, err := DiscoverKubeAtlasTargets(context.Background(), kubernetesfake.NewSimpleClientset(deployment), nil, DiscoveryOptions{})
	if err != nil {
		t.Fatalf("DiscoverKubeAtlasTargets: %v", err)
	}
	if got := result.Selected[0].Ownership.Classification; got != OwnershipGeneric {
		t.Fatalf("ownership = %q, want %q", got, OwnershipGeneric)
	}
}

func TestDiscoverKubeAtlasTargetsSummarizesLiveRuntime(t *testing.T) {
	deployment := kubeatlasDeployment("kubeatlas", "kubeatlas", "kubeatlas", nil, nil)
	deployment.Spec.Template.Spec.Containers = []corev1.Container{
		{Name: "sidecar", Image: "example.com/sidecar:v1"},
		{
			Name:  "kubeatlas",
			Image: "ghcr.io/maomada/kubeatlas:1.5.8",
			Args: []string{
				"--auth-mode=oidc",
				"--cloud-url", "wss://api.example.test/agent",
				"--cluster-name=cluster-id",
			},
			Env: []corev1.EnvVar{
				{Name: "KUBEATLAS_CLOUD_MODE", Value: "true"},
				{Name: "KUBEATLAS_CLOUD_TOKEN", ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "kubeatlas-cloud-config"}, Key: "token"},
				}},
			},
		},
	}

	result, err := DiscoverKubeAtlasTargets(context.Background(), kubernetesfake.NewSimpleClientset(deployment), nil, DiscoveryOptions{})
	if err != nil {
		t.Fatalf("DiscoverKubeAtlasTargets: %v", err)
	}
	runtime := result.Selected[0].Runtime
	if runtime.Image != "ghcr.io/maomada/kubeatlas:1.5.8" || runtime.AuthMode != "oidc" {
		t.Fatalf("runtime identity = %#v", runtime)
	}
	if !runtime.CloudModeConfigured || !runtime.CloudMode || runtime.CloudURL != "wss://api.example.test/agent" || !runtime.CloudURLConfigured || runtime.ClusterName != "cluster-id" || !runtime.ClusterNameConfigured || !runtime.CloudTokenConfigured || !runtime.AlreadyCloud {
		t.Fatalf("cloud runtime = %#v", runtime)
	}
	if runtime.CloudTokenSecretName != "kubeatlas-cloud-config" || runtime.CloudTokenSecretKey != "token" {
		t.Fatalf("Cloud token Secret reference = %q/%q", runtime.CloudTokenSecretName, runtime.CloudTokenSecretKey)
	}
}

func TestDiscoverKubeAtlasTargetsAcceptsUnversionedOfficialChartLabel(t *testing.T) {
	deployment := kubeatlasDeployment("kubeatlas", "kubeatlas", "kubeatlas", nil, nil)
	deployment.Labels["helm.sh/chart"] = "kubeatlas"

	result, err := DiscoverKubeAtlasTargets(context.Background(), kubernetesfake.NewSimpleClientset(deployment), nil, DiscoveryOptions{})
	if err != nil {
		t.Fatalf("DiscoverKubeAtlasTargets: %v", err)
	}
	if len(result.Selected) != 1 || result.Selected[0].Chart != "kubeatlas" {
		t.Fatalf("selected = %#v", result.Selected)
	}
}

func TestOfficialKubeAtlasChartLabel(t *testing.T) {
	tests := []struct {
		label string
		want  bool
	}{
		{label: "kubeatlas", want: true},
		{label: "kubeatlas-1.8.1", want: true},
		{label: "kubeatlas-1.9.0-rc.1", want: true},
		{label: "kubeatlas-hub-0.1.0", want: false},
		{label: "kubeatlas-custom", want: false},
		{label: "kubeatlas-1.8", want: false},
		{label: "kubeatlas-", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			if got := isOfficialKubeAtlasChartLabel(tt.label); got != tt.want {
				t.Fatalf("isOfficialKubeAtlasChartLabel(%q) = %t, want %t", tt.label, got, tt.want)
			}
		})
	}
}

func TestDiscoverKubeAtlasTargetsRuntimeDefaultsToOSS(t *testing.T) {
	deployment := kubeatlasDeployment("kubeatlas", "kubeatlas", "kubeatlas", nil, nil)
	deployment.Spec.Template.Spec.Containers = []corev1.Container{{Name: "kubeatlas", Image: "ghcr.io/maomada/kubeatlas:1.6.0"}}

	result, err := DiscoverKubeAtlasTargets(context.Background(), kubernetesfake.NewSimpleClientset(deployment), nil, DiscoveryOptions{})
	if err != nil {
		t.Fatalf("DiscoverKubeAtlasTargets: %v", err)
	}
	runtime := result.Selected[0].Runtime
	if runtime.AuthMode != "none" || runtime.AlreadyCloud || runtime.Image != "ghcr.io/maomada/kubeatlas:1.6.0" {
		t.Fatalf("runtime = %#v", runtime)
	}
}
