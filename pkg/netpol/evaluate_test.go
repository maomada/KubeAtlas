package netpol

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func pod(ns, name, ip string, lbls map[string]string, ports ...corev1.ContainerPort) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, Labels: lbls},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Ports: ports}}},
		Status:     corev1.PodStatus{PodIP: ip},
	}
}

func namespace(name string, lbls map[string]string) *corev1.Namespace {
	if lbls == nil {
		lbls = map[string]string{}
	}
	lbls["kubernetes.io/metadata.name"] = name
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: lbls}}
}

func policy(ns, name string, podSel map[string]string, types []networkingv1.PolicyType, ingress []networkingv1.NetworkPolicyIngressRule, egress []networkingv1.NetworkPolicyEgressRule) *networkingv1.NetworkPolicy {
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: podSel},
			PolicyTypes: types,
			Ingress:     ingress,
			Egress:      egress,
		},
	}
}

func tcpPort(n int32) networkingv1.NetworkPolicyPort {
	p := intstr.FromInt32(n)
	return networkingv1.NetworkPolicyPort{Port: &p}
}

func namedPort(n string) networkingv1.NetworkPolicyPort {
	p := intstr.FromString(n)
	return networkingv1.NetworkPolicyPort{Port: &p}
}

var (
	promLabels      = map[string]string{"app": "prometheus"}
	kubeatlasLabels = map[string]string{"app": "kubeatlas"}
	ingressT        = []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}
	egressT         = []networkingv1.PolicyType{networkingv1.PolicyTypeEgress}
)

func kubeatlas() Peer {
	return Peer{Pod: pod("kubeatlas", "kubeatlas-1", "10.0.1.5", kubeatlasLabels), Namespace: namespace("kubeatlas", map[string]string{"team": "platform"})}
}

func prom(port int32) Backend {
	return Backend{
		Peer:     Peer{Pod: pod("monitoring", "prom-0", "10.0.2.9", promLabels, corev1.ContainerPort{Name: "web", ContainerPort: 9090}), Namespace: namespace("monitoring", nil)},
		Port:     port,
		Protocol: corev1.ProtocolTCP,
	}
}

func TestEvaluate(t *testing.T) {
	fromKubeAtlasNs := networkingv1.NetworkPolicyPeer{NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": "kubeatlas"}}}
	fromOtherNs := networkingv1.NetworkPolicyPeer{NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": "other"}}}
	fromKubeAtlasPodsAnyNs := networkingv1.NetworkPolicyPeer{NamespaceSelector: &metav1.LabelSelector{}, PodSelector: &metav1.LabelSelector{MatchLabels: kubeatlasLabels}}
	fromLocalPromPods := networkingv1.NetworkPolicyPeer{PodSelector: &metav1.LabelSelector{MatchLabels: promLabels}}
	toMonitoring := networkingv1.NetworkPolicyPeer{NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": "monitoring"}}}

	tests := []struct {
		name     string
		src      Peer
		dst      []Backend
		policies []*networkingv1.NetworkPolicy
		want     Verdict
	}{
		{
			name: "no policies is allowed",
			src:  kubeatlas(), dst: []Backend{prom(9090)},
			want: Verdict{Kind: Allowed},
		},
		{
			name: "default deny ingress on the backend",
			src:  kubeatlas(), dst: []Backend{prom(9090)},
			policies: []*networkingv1.NetworkPolicy{policy("monitoring", "deny-all", nil, ingressT, nil, nil)},
			want:     Verdict{Kind: Denied, Direction: DirectionIngress, Policies: []string{"monitoring/deny-all"}},
		},
		{
			name: "allow-all rule admits",
			src:  kubeatlas(), dst: []Backend{prom(9090)},
			policies: []*networkingv1.NetworkPolicy{policy("monitoring", "open", promLabels, ingressT,
				[]networkingv1.NetworkPolicyIngressRule{{}}, nil)},
			want: Verdict{Kind: Allowed},
		},
		{
			name: "namespaceSelector matching the source namespace admits",
			src:  kubeatlas(), dst: []Backend{prom(9090)},
			policies: []*networkingv1.NetworkPolicy{policy("monitoring", "from-kubeatlas", promLabels, ingressT,
				[]networkingv1.NetworkPolicyIngressRule{{From: []networkingv1.NetworkPolicyPeer{fromKubeAtlasNs}}}, nil)},
			want: Verdict{Kind: Allowed},
		},
		{
			name: "namespaceSelector for another namespace denies",
			src:  kubeatlas(), dst: []Backend{prom(9090)},
			policies: []*networkingv1.NetworkPolicy{policy("monitoring", "from-other", promLabels, ingressT,
				[]networkingv1.NetworkPolicyIngressRule{{From: []networkingv1.NetworkPolicyPeer{fromOtherNs}}}, nil)},
			want: Verdict{Kind: Denied, Direction: DirectionIngress, Policies: []string{"monitoring/from-other"}},
		},
		{
			name: "podSelector without namespaceSelector only matches the policy's namespace",
			src:  kubeatlas(), dst: []Backend{prom(9090)},
			policies: []*networkingv1.NetworkPolicy{policy("monitoring", "local-only", promLabels, ingressT,
				[]networkingv1.NetworkPolicyIngressRule{{From: []networkingv1.NetworkPolicyPeer{fromLocalPromPods}}}, nil)},
			want: Verdict{Kind: Denied, Direction: DirectionIngress, Policies: []string{"monitoring/local-only"}},
		},
		{
			name: "combined namespace and pod selectors admit a matching pod anywhere",
			src:  kubeatlas(), dst: []Backend{prom(9090)},
			policies: []*networkingv1.NetworkPolicy{policy("monitoring", "kubeatlas-pods", promLabels, ingressT,
				[]networkingv1.NetworkPolicyIngressRule{{From: []networkingv1.NetworkPolicyPeer{fromKubeAtlasPodsAnyNs}}}, nil)},
			want: Verdict{Kind: Allowed},
		},
		{
			// Whether a range matches a pod's address is plugin-defined (the
			// reference implementation says yes, Cilium by default no).
			name: "ipBlock containing a pod source's IP is undecidable, not admission",
			src:  kubeatlas(), dst: []Backend{prom(9090)},
			policies: []*networkingv1.NetworkPolicy{policy("monitoring", "cidr", promLabels, ingressT,
				[]networkingv1.NetworkPolicyIngressRule{{From: []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: "10.0.0.0/16"}}}}}, nil)},
			want: Verdict{Kind: Unknown},
		},
		{
			name: "ipBlock except carving out the source IP is undecidable, never a denial",
			src:  kubeatlas(), dst: []Backend{prom(9090)},
			policies: []*networkingv1.NetworkPolicy{policy("monitoring", "cidr-except", promLabels, ingressT,
				[]networkingv1.NetworkPolicyIngressRule{{From: []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: "10.0.0.0/16", Except: []string{"10.0.1.0/24"}}}}}}, nil)},
			want: Verdict{Kind: Unknown},
		},
		{
			name: "ipBlock missing the source IP is undecidable: the policy may see a rewritten address",
			src:  kubeatlas(), dst: []Backend{prom(9090)},
			policies: []*networkingv1.NetworkPolicy{policy("monitoring", "other-cidr", promLabels, ingressT,
				[]networkingv1.NetworkPolicyIngressRule{{From: []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: "192.168.0.0/16"}}}}}, nil)},
			want: Verdict{Kind: Unknown},
		},
		{
			name: "ipBlock without a known source IP is undecidable",
			src:  Peer{Pod: pod("kubeatlas", "kubeatlas-1", "", kubeatlasLabels), Namespace: namespace("kubeatlas", nil)}, dst: []Backend{prom(9090)},
			policies: []*networkingv1.NetworkPolicy{policy("monitoring", "cidr", promLabels, ingressT,
				[]networkingv1.NetworkPolicyIngressRule{{From: []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: "10.0.0.0/16"}}}}}, nil)},
			want: Verdict{Kind: Unknown},
		},
		{
			name: "rule for a different port denies this one",
			src:  kubeatlas(), dst: []Backend{prom(9090)},
			policies: []*networkingv1.NetworkPolicy{policy("monitoring", "other-port", promLabels, ingressT,
				[]networkingv1.NetworkPolicyIngressRule{{Ports: []networkingv1.NetworkPolicyPort{tcpPort(8080)}, From: []networkingv1.NetworkPolicyPeer{fromKubeAtlasNs}}}, nil)},
			want: Verdict{Kind: Denied, Direction: DirectionIngress, Policies: []string{"monitoring/other-port"}},
		},
		{
			name: "named port resolved on the backend pod admits",
			src:  kubeatlas(), dst: []Backend{prom(9090)},
			policies: []*networkingv1.NetworkPolicy{policy("monitoring", "named", promLabels, ingressT,
				[]networkingv1.NetworkPolicyIngressRule{{Ports: []networkingv1.NetworkPolicyPort{namedPort("web")}, From: []networkingv1.NetworkPolicyPeer{fromKubeAtlasNs}}}, nil)},
			want: Verdict{Kind: Allowed},
		},
		{
			name: "endPort range covering the port admits",
			src:  kubeatlas(), dst: []Backend{prom(9090)},
			policies: func() []*networkingv1.NetworkPolicy {
				p := tcpPort(9000)
				end := int32(9100)
				p.EndPort = &end
				return []*networkingv1.NetworkPolicy{policy("monitoring", "range", promLabels, ingressT,
					[]networkingv1.NetworkPolicyIngressRule{{Ports: []networkingv1.NetworkPolicyPort{p}, From: []networkingv1.NetworkPolicyPeer{fromKubeAtlasNs}}}, nil)}
			}(),
			want: Verdict{Kind: Allowed},
		},
		{
			name: "egress-isolated source with no rule to the backend denies on the egress side",
			src:  kubeatlas(), dst: []Backend{prom(9090)},
			policies: []*networkingv1.NetworkPolicy{
				policy("kubeatlas", "egress-dns-only", kubeatlasLabels, egressT, nil,
					[]networkingv1.NetworkPolicyEgressRule{{Ports: []networkingv1.NetworkPolicyPort{tcpPort(53)}}}),
			},
			want: Verdict{Kind: Denied, Direction: DirectionEgress, Policies: []string{"kubeatlas/egress-dns-only"}},
		},
		{
			name: "egress rule to the monitoring namespace admits, ingress then decides",
			src:  kubeatlas(), dst: []Backend{prom(9090)},
			policies: []*networkingv1.NetworkPolicy{
				policy("kubeatlas", "egress", kubeatlasLabels, egressT, nil,
					[]networkingv1.NetworkPolicyEgressRule{{To: []networkingv1.NetworkPolicyPeer{toMonitoring}}}),
				policy("monitoring", "deny-all", nil, ingressT, nil, nil),
			},
			want: Verdict{Kind: Denied, Direction: DirectionIngress, Policies: []string{"monitoring/deny-all"}},
		},
		{
			name: "egress ipBlock rules are undecidable",
			src:  kubeatlas(), dst: []Backend{prom(9090)},
			policies: []*networkingv1.NetworkPolicy{
				policy("kubeatlas", "egress-cidr", kubeatlasLabels, egressT, nil,
					[]networkingv1.NetworkPolicyEgressRule{{To: []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: "10.0.2.0/24"}}}}}),
			},
			want: Verdict{Kind: Unknown},
		},
		{
			name: "policyTypes omitted isolates ingress only",
			src:  kubeatlas(), dst: []Backend{prom(9090)},
			policies: []*networkingv1.NetworkPolicy{policy("monitoring", "implicit", promLabels, nil, nil, nil)},
			want:     Verdict{Kind: Denied, Direction: DirectionIngress, Policies: []string{"monitoring/implicit"}},
		},
		{
			name: "policyTypes omitted with egress rules on the source isolates egress too",
			src:  kubeatlas(), dst: []Backend{prom(9090)},
			policies: []*networkingv1.NetworkPolicy{policy("kubeatlas", "implicit-egress", kubeatlasLabels, nil, nil,
				[]networkingv1.NetworkPolicyEgressRule{{Ports: []networkingv1.NetworkPolicyPort{tcpPort(53)}}})},
			want: Verdict{Kind: Denied, Direction: DirectionEgress, Policies: []string{"kubeatlas/implicit-egress"}},
		},
		{
			name: "explicit egress-only policy does not isolate ingress",
			src:  kubeatlas(), dst: []Backend{prom(9090)},
			policies: []*networkingv1.NetworkPolicy{policy("monitoring", "egress-only", promLabels, egressT, nil, nil)},
			want:     Verdict{Kind: Allowed},
		},
		{
			name: "policy in an unrelated namespace is ignored",
			src:  kubeatlas(), dst: []Backend{prom(9090)},
			policies: []*networkingv1.NetworkPolicy{policy("other", "deny-all", nil, ingressT, nil, nil)},
			want:     Verdict{Kind: Allowed},
		},
		{
			name: "additive: a second policy admitting the source wins over a deny-all",
			src:  kubeatlas(), dst: []Backend{prom(9090)},
			policies: []*networkingv1.NetworkPolicy{
				policy("monitoring", "deny-all", nil, ingressT, nil, nil),
				policy("monitoring", "from-kubeatlas", promLabels, ingressT,
					[]networkingv1.NetworkPolicyIngressRule{{From: []networkingv1.NetworkPolicyPeer{fromKubeAtlasNs}}}, nil),
			},
			want: Verdict{Kind: Allowed},
		},
		{
			name: "mixed backends: one unselected pod keeps the service reachable",
			src:  kubeatlas(),
			dst: []Backend{prom(9090), {
				Peer:     Peer{Pod: pod("monitoring", "prom-canary", "10.0.2.10", map[string]string{"app": "prometheus-canary"}), Namespace: namespace("monitoring", nil)},
				Port:     9090,
				Protocol: corev1.ProtocolTCP,
			}},
			policies: []*networkingv1.NetworkPolicy{policy("monitoring", "deny-prom", promLabels, ingressT, nil, nil)},
			want:     Verdict{Kind: Allowed},
		},
		{
			name: "heterogeneous target ports: a rule for one backend's port admits",
			src:  kubeatlas(),
			dst: []Backend{prom(9090), {
				Peer:     Peer{Pod: pod("monitoring", "prom-alt", "10.0.2.11", promLabels), Namespace: namespace("monitoring", nil)},
				Port:     9091,
				Protocol: corev1.ProtocolTCP,
			}},
			policies: []*networkingv1.NetworkPolicy{policy("monitoring", "alt-port", promLabels, ingressT,
				[]networkingv1.NetworkPolicyIngressRule{{Ports: []networkingv1.NetworkPolicyPort{tcpPort(9091)}, From: []networkingv1.NetworkPolicyPeer{fromKubeAtlasNs}}}, nil)},
			want: Verdict{Kind: Allowed},
		},
		{
			name: "namespaceSelector without the source Namespace object is undecidable",
			src:  Peer{Pod: pod("kubeatlas", "kubeatlas-1", "10.0.1.5", kubeatlasLabels)}, dst: []Backend{prom(9090)},
			policies: []*networkingv1.NetworkPolicy{policy("monitoring", "from-kubeatlas", promLabels, ingressT,
				[]networkingv1.NetworkPolicyIngressRule{{From: []networkingv1.NetworkPolicyPeer{fromKubeAtlasNs}}}, nil)},
			want: Verdict{Kind: Unknown},
		},
		{
			name: "hostNetwork backend is undecidable",
			src:  kubeatlas(),
			dst: func() []Backend {
				b := prom(9090)
				b.Pod.Spec.HostNetwork = true
				return []Backend{b}
			}(),
			policies: []*networkingv1.NetworkPolicy{policy("monitoring", "deny-all", nil, ingressT, nil, nil)},
			want:     Verdict{Kind: Unknown},
		},
		{
			name: "malformed selector is undecidable",
			src:  kubeatlas(), dst: []Backend{prom(9090)},
			policies: []*networkingv1.NetworkPolicy{{
				ObjectMeta: metav1.ObjectMeta{Namespace: "monitoring", Name: "bad"},
				Spec: networkingv1.NetworkPolicySpec{
					PodSelector: metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "app", Operator: "Bogus"}}},
					PolicyTypes: ingressT,
				},
			}},
			want: Verdict{Kind: Unknown},
		},
		{
			name: "a pod can always reach itself",
			src:  kubeatlas(),
			dst: func() []Backend {
				self := kubeatlas()
				return []Backend{{Peer: self, Port: 9090, Protocol: corev1.ProtocolTCP}}
			}(),
			policies: []*networkingv1.NetworkPolicy{policy("kubeatlas", "deny-all", nil, ingressT, nil, nil)},
			want:     Verdict{Kind: Allowed},
		},
		{
			name: "ipBlock against a dual-stack pod source stays undecidable whichever family matches",
			src: func() Peer {
				p := kubeatlas()
				p.Pod.Status.PodIPs = []corev1.PodIP{{IP: "10.0.1.5"}, {IP: "fd00::5"}}
				return p
			}(),
			dst: []Backend{prom(9090)},
			policies: []*networkingv1.NetworkPolicy{policy("monitoring", "v6-only", promLabels, ingressT,
				[]networkingv1.NetworkPolicyIngressRule{{From: []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: "fd00::/64"}}}}}, nil)},
			want: Verdict{Kind: Unknown},
		},
		{
			name: "ipBlock of a family the source has no address in is undecidable",
			src:  kubeatlas(), dst: []Backend{prom(9090)},
			policies: []*networkingv1.NetworkPolicy{policy("monitoring", "v6-only", promLabels, ingressT,
				[]networkingv1.NetworkPolicyIngressRule{{From: []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: "fd00::/64"}}}}}, nil)},
			want: Verdict{Kind: Unknown},
		},
		{
			name: "egress admitting only one backend and ingress only another is no complete path",
			src:  kubeatlas(),
			dst: func() []Backend {
				a := prom(9090)
				a.Pod.Name, a.Pod.Labels = "prom-a", map[string]string{"app": "prometheus", "shard": "a"}
				b := prom(9090)
				b.Pod.Name, b.Pod.Labels = "prom-b", map[string]string{"app": "prometheus", "shard": "b"}
				return []Backend{a, b}
			}(),
			policies: []*networkingv1.NetworkPolicy{
				// KubeAtlas may only talk to shard a...
				policy("kubeatlas", "egress-shard-a", kubeatlasLabels, egressT, nil,
					[]networkingv1.NetworkPolicyEgressRule{{To: []networkingv1.NetworkPolicyPeer{{
						NamespaceSelector: &metav1.LabelSelector{}, PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"shard": "a"}},
					}}}}),
				// ...but only shard b lets KubeAtlas in.
				policy("monitoring", "deny-all", nil, ingressT, nil, nil),
				policy("monitoring", "shard-b-from-kubeatlas", map[string]string{"shard": "b"}, ingressT,
					[]networkingv1.NetworkPolicyIngressRule{{From: []networkingv1.NetworkPolicyPeer{fromKubeAtlasNs}}}, nil),
			},
			want: Verdict{Kind: Denied, Direction: DirectionBoth, Policies: []string{"monitoring/deny-all", "monitoring/shard-b-from-kubeatlas", "kubeatlas/egress-shard-a"}},
		},
		{
			name: "an unset backend protocol means TCP",
			src:  kubeatlas(),
			dst: func() []Backend {
				b := prom(9090)
				b.Protocol = ""
				return []Backend{b}
			}(),
			policies: []*networkingv1.NetworkPolicy{policy("monitoring", "tcp-9090", promLabels, ingressT,
				[]networkingv1.NetworkPolicyIngressRule{{Ports: []networkingv1.NetworkPolicyPort{tcpPort(9090)}, From: []networkingv1.NetworkPolicyPeer{fromKubeAtlasNs}}}, nil)},
			want: Verdict{Kind: Allowed},
		},
		{
			name: "no backends is undecidable",
			src:  kubeatlas(), dst: nil,
			policies: []*networkingv1.NetworkPolicy{policy("monitoring", "deny-all", nil, ingressT, nil, nil)},
			want:     Verdict{Kind: Unknown},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Evaluate(tt.src, tt.dst, tt.policies)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Evaluate() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
