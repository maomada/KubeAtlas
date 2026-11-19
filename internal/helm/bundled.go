package helm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/maomada/KubeAtlas/internal/bundledcharts"
	"helm.sh/helm/v3/pkg/action"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

var (
	errOpenCostExists     = errors.New("OpenCost already exists in this cluster; review the existing installation")
	errOpenCostListDenied = errors.New("OpenCost installation requires permission to check Deployments and Services across the cluster")
)

func bundledChartDetail(name, version string) (*ChartDetail, error) {
	loaded, _, err := bundledcharts.Load(name, version)
	if err != nil {
		if errors.Is(err, bundledcharts.ErrOpenCostUnavailable) {
			return nil, fmt.Errorf("%w: %v", errSourceUnavailable, err)
		}
		return nil, err
	}
	if name == bundledcharts.OpenCostName {
		return &ChartDetail{ChartInfo: ChartInfo{Name: loaded.Name(), Version: loaded.Metadata.Version, Repository: bundledcharts.Repository}}, nil
	}
	return chartDetailFromChart(loaded, ChartInfo{
		Name: loaded.Metadata.Name, Version: loaded.Metadata.Version,
		AppVersion: loaded.Metadata.AppVersion, Description: loaded.Metadata.Description,
		Repository: bundledcharts.Repository,
	}), nil
}

func installBundledChart(cfg *action.Configuration, req *InstallRequest, progress chan<- InstallProgress) (*HelmRelease, error) {
	if req.ChartName == bundledcharts.OpenCostName {
		return installOpenCostChart(cfg, req, progress)
	}
	send := progressSender(progress)
	mode, err := preInstallCheck(cfg, req.ReleaseName, req.Namespace)
	if err != nil {
		return nil, err
	}
	loaded, source, err := bundledcharts.Load(req.ChartName, req.Version)
	if err != nil {
		return nil, err
	}
	request := *req
	request.Version = loaded.Metadata.Version
	request.resolvedSource = &ChartSourceCandidate{Type: "repository", Reference: source.Repository, URL: source.URL}
	send("loading", fmt.Sprintf("Loading bundled chart %s-%s (SHA-256 verified); no chart download", source.Name, source.Version), "")
	send("installing", fmt.Sprintf("Installing %s into %s; container images must be available to cluster nodes", req.ReleaseName, req.Namespace), "")
	rel, err := runInstallOrUpgrade(cfg, &request, loaded, mode)
	if err != nil {
		return nil, fmt.Errorf("install failed: %w", err)
	}
	send("complete", fmt.Sprintf("Successfully installed %s", req.ReleaseName), "")
	return installedHelmRelease(rel), nil
}

type openCostRenderer struct{ registry string }

func (renderer openCostRenderer) Run(manifests *bytes.Buffer) (*bytes.Buffer, error) {
	if err := bundledcharts.ValidateOpenCostManifests(manifests.Bytes(), renderer.registry); err != nil {
		return nil, fmt.Errorf("%w: %v", bundledcharts.ErrOpenCostUnavailable, err)
	}
	return manifests, nil
}

func installOpenCostChart(cfg *action.Configuration, req *InstallRequest, progress chan<- InstallProgress) (*HelmRelease, error) {
	if req.ReleaseName != bundledcharts.OpenCostName || req.Namespace != bundledcharts.OpenCostName ||
		req.Version == "" || req.Values != nil || req.RepositoryName != "" {
		return nil, fmt.Errorf("%w: OpenCost requires its configured version, release and namespace opencost, and no values overrides", errInvalidChartSource)
	}
	if err := freshInstallCheck(cfg, req.ReleaseName, req.Namespace); err != nil {
		return nil, err
	}
	loaded, source, err := bundledcharts.Load(req.ChartName, req.Version)
	if err != nil {
		return nil, err
	}
	if err := checkOpenCostWorkloadAbsence(cfg); err != nil {
		return nil, err
	}
	send := progressSender(progress)
	send("loading", fmt.Sprintf("Verified local OpenCost chart %s", source.Version), "")
	send("installing", "Installing OpenCost in namespace opencost", "")
	install := action.NewInstall(cfg)
	install.ReleaseName, install.Namespace = req.ReleaseName, req.Namespace
	install.Version, install.Timeout = source.Version, 40*time.Second
	install.Wait = true
	install.CreateNamespace, install.DisableHooks, install.SkipCRDs = true, true, true
	install.PostRenderer = openCostRenderer{registry: source.ImageRegistry}
	rel, err := install.Run(loaded, nil)
	if err != nil {
		return nil, fmt.Errorf("install failed: %w", err)
	}
	send("complete", "OpenCost release installed; verify metrics before checking costs", "")
	return installedHelmRelease(rel), nil
}

func checkOpenCostWorkloadAbsence(cfg *action.Configuration) error {
	if cfg.RESTClientGetter == nil {
		return bundledcharts.ErrOpenCostUnavailable
	}
	config, err := cfg.RESTClientGetter.ToRESTConfig()
	if err != nil {
		return bundledcharts.ErrOpenCostUnavailable
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return bundledcharts.ErrOpenCostUnavailable
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	options := metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name in (opencost,prometheus-opencost-exporter)", Limit: 1,
	}
	deployments, err := client.AppsV1().Deployments(metav1.NamespaceAll).List(ctx, options)
	if err != nil {
		return openCostListError(err)
	}
	if len(deployments.Items) != 0 {
		return errOpenCostExists
	}
	if deployments.Continue != "" {
		return bundledcharts.ErrOpenCostUnavailable
	}
	services, err := client.CoreV1().Services(metav1.NamespaceAll).List(ctx, options)
	if err != nil {
		return openCostListError(err)
	}
	if len(services.Items) != 0 {
		return errOpenCostExists
	}
	if services.Continue != "" {
		return bundledcharts.ErrOpenCostUnavailable
	}
	return nil
}

func openCostListError(err error) error {
	if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
		return errOpenCostListDenied
	}
	return fmt.Errorf("%w: could not check for existing OpenCost workloads", bundledcharts.ErrOpenCostUnavailable)
}
