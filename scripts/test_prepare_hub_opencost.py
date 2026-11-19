import hashlib
import importlib.util
import json
import pathlib
import tarfile
import tempfile
import unittest


SPEC = importlib.util.spec_from_file_location("prepare_opencost", pathlib.Path(__file__).resolve().parents[1] / "deploy/helm/kubeatlas/files/prepare-opencost.py")
prepare = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(prepare)


def service(name, target, *, namespace="monitoring", cluster_ip="172.19.0.18", port=9090):
    return {"metadata": {"name": name, "namespace": namespace},
            "spec": {"selector": {"app.kubernetes.io/name": "prometheus", **target},
                     "clusterIP": cluster_ip, "ports": [{"name": "web", "port": port}]}}


def operator(name, *, namespace="monitoring", selector=None, namespace_selector=None, **spec):
    return {"metadata": {"name": name, "namespace": namespace},
            "spec": {"serviceMonitorSelector": selector,
                     "serviceMonitorNamespaceSelector": namespace_selector, **spec}}


class ClusterFixture:
    def __init__(self, services=(), operators=(), monitor_labels=None, successful=(), annotation_targets=()):
        self.services = services
        self.operators = operators
        self.monitor_labels = monitor_labels or {}
        self.successful = set(successful)
        self.annotation_targets = annotation_targets
        self.queried = []

    def read(self, path, missing_ok=False):
        if path == "/api/v1/services":
            return {"items": self.services}
        if path == prepare.OPERATOR_API and missing_ok:
            return {"resources": [{"name": "prometheuses"}, {"name": "servicemonitors"}]} if self.operators else None
        if path == prepare.OPERATOR_API + "/prometheuses":
            return {"items": self.operators}
        if path == "/api/v1/namespaces":
            return {"items": [{"metadata": {"name": "monitoring", "labels": self.monitor_labels}}]}
        if "/proxy" in path:
            self.queried.append(path)
            service_name = path.split("/services/")[1].split(":")[1]
            if service_name not in self.successful:
                raise prepare.DiscoveryError("failed to query")
            if path.endswith("/api/v1/targets?state=active"):
                return {"data": {"activeTargets": self.annotation_targets}}
            return {"status": "success", "data": {"resultType": "vector", "result": [{"value": [1, "1"]}]}}
        raise AssertionError(f"Unexpected request: {path}")


class DiscoveryTests(unittest.TestCase):
    def test_112_specific_service_wins_over_operator_headless_alias(self):
        instance = "monitoring-kube-prometheus-prometheus"
        clustered = ClusterFixture(
            [service("prometheus-operated", {}, cluster_ip="None"),
             service(instance, {"operator.prometheus.io/name": instance})],
            [operator(instance, selector={}, namespace_selector={})], successful=[instance, "prometheus-operated"],
        )
        result = prepare.discover(clustered.read, {})
        self.assertTrue(result["ready"])
        self.assertEqual(result["prometheusURL"], f"http://{instance}.monitoring.svc:9090")
        self.assertEqual(result["serviceMonitorNamespace"], "monitoring")
        self.assertEqual(result["serviceMonitorLabels"], {})
        self.assertEqual(len(clustered.queried), 1)

    def test_167_uses_workload_prometheus_label_and_namespace(self):
        clustered = ClusterFixture(
            [service("prometheus-k8s", {"app.kubernetes.io/instance": "k8s"}),
             service("prometheus-k8s-system", {"app.kubernetes.io/instance": "k8s-system"}),
             service("prometheus-operated", {}, cluster_ip="None")],
            [operator("k8s", selector={"matchLabels": {"app.kubernetes.io/prometheusby": "k8s-user"}},
                      namespace_selector={"matchLabels": {"monitoring.example.com/enabled": "true"}}),
             operator("k8s-system", selector={})],
            monitor_labels={"monitoring.example.com/enabled": "true"},
            successful=["prometheus-k8s", "prometheus-k8s-system", "prometheus-operated"],
        )
        result = prepare.discover(clustered.read, {})
        self.assertEqual(result["serviceMonitorLabels"], {"app.kubernetes.io/prometheusby": "k8s-user"})
        self.assertEqual(result["serviceMonitorNamespace"], "monitoring")
        self.assertEqual(result["prometheusURL"], "http://prometheus-k8s.monitoring.svc:9090")

    def test_equally_ranked_distinct_prometheus_is_ambiguous(self):
        names = ["team-a-prometheus", "team-b-prometheus"]
        clustered = ClusterFixture(
            [service(name, {"operator.prometheus.io/name": name}) for name in names],
            [operator(name, selector={}) for name in names], successful=names,
        )
        self.assertEqual(prepare.discover(clustered.read, {}), {"ready": False, "reason": "prometheus-ambiguous"})

    def test_null_monitor_selector_is_not_wildcard(self):
        clustered = ClusterFixture(
            [service("prometheus-k8s", {"app.kubernetes.io/instance": "k8s"})],
            [operator("k8s")], successful=["prometheus-k8s"],
        )
        result = prepare.discover(clustered.read, {})
        self.assertEqual(result["reason"], "prometheus-scrape-configuration-required")
        self.assertFalse(result["ready"])

    def test_monitor_placement_respects_ignore_namespace_selectors(self):
        instance = operator("k8s", selector={}, namespace_selector={}, ignoreNamespaceSelectors=True)
        self.assertEqual(prepare.selected_monitor_namespace(instance, {}), "opencost")
        instance["spec"]["serviceMonitorNamespaceSelector"] = None
        self.assertIsNone(prepare.selected_monitor_namespace(instance, {}))

    def test_expression_label_selection_preserves_chart_labels(self):
        selector = {
            "matchLabels": {"app.kubernetes.io/name": "opencost"},
            "matchExpressions": [
                {"key": "tenant", "operator": "In", "values": ["a", "b"]},
                {"key": "tenant", "operator": "NotIn", "values": ["a"]},
                {"key": "metrics", "operator": "Exists"},
                {"key": "private", "operator": "DoesNotExist"},
            ],
        }
        defaults = {"app.kubernetes.io/name": "opencost"}
        self.assertEqual(prepare.monitor_labels(selector, defaults), {"tenant": "b", "metrics": "kubeatlas-0"})
        self.assertIsNone(prepare.monitor_labels(selector, {"app.kubernetes.io/name": "other"}))

    def test_standalone_requires_service_annotation_discovery(self):
        instance = service("prometheus-server", {"app.kubernetes.io/component": "server"}, port=80)
        clustered = ClusterFixture([instance], successful=["prometheus-server"])
        self.assertEqual(prepare.discover(clustered.read, {})["reason"], "prometheus-scrape-configuration-required")
        clustered.annotation_targets = [
            {"scrapePool": "kubernetes-service-endpoints", "health": "up",
             "discoveredLabels": {"__meta_kubernetes_service_annotation_prometheus_io_scrape": "true"}},
        ]
        result = prepare.discover(clustered.read, {})
        self.assertEqual(result["monitoringMode"], "service-annotations")
        self.assertEqual(result["prometheusURL"], "http://prometheus-server.monitoring.svc:80")

    def test_missing_and_unreachable_are_distinct(self):
        self.assertEqual(prepare.discover(ClusterFixture().read, {})["reason"], "prometheus-not-found")
        clustered = ClusterFixture([service("prometheus-server", {})])
        self.assertEqual(prepare.discover(clustered.read, {})["reason"], "prometheus-unreachable")

    def test_probe_requires_the_expected_query_result(self):
        self.assertFalse(prepare.successful_probe({"status": "success", "data": {"resultType": "vector"}}))
        self.assertFalse(prepare.successful_probe({"status": "success", "data": {
            "resultType": "vector", "result": [{"value": [1, "0"]}],
        }}))


class ChartTests(unittest.TestCase):
    def test_package_pins_image_sets_prometheus_and_writes_deterministic_checksum(self):
        values = {"imagePullSecrets": [{"name": "old"}], "service": {"annotations": {}},
                  "opencost": {"exporter": {"image": {}, "apiPort": 9003},
                               "prometheus": {"internal": {"enabled": True}, "external": {}},
                               "metrics": {"serviceMonitor": {}}}}
        chart = b"name: opencost\nversion: 2.5.34-kubeatlas.2\nappVersion: 1.121.4\n"
        with tempfile.TemporaryDirectory() as staging:
            output = pathlib.Path(staging) / "opencost.tgz"
            prepared = prepare.write_chart(output, {"opencost/Chart.yaml": chart}, values, {
                "prometheusURL": "http://prometheus-server.monitoring.svc:80", "monitoringMode": "service-annotations",
                "serviceMonitorNamespace": "", "serviceMonitorLabels": {},
            }, "hub.example", "sha256:" + "f" * 64)
            _, config, labels = prepare.read_chart(output)
            self.assertEqual(prepared, hashlib.sha256(output.read_bytes()).hexdigest())
            self.assertEqual(config["imagePullSecrets"], [])
            self.assertEqual(config["opencost"]["exporter"]["image"]["fullImageName"],
                             "hub.example/kubeatlas/opencost@sha256:" + "f" * 64)
            self.assertEqual(config["opencost"]["prometheus"]["external"]["url"], "http://prometheus-server.monitoring.svc:80")
            self.assertEqual(config["service"]["annotations"]["prometheus.io/port"], "9003")
            self.assertFalse(config["opencost"]["metrics"]["serviceMonitor"]["enabled"])
            self.assertEqual(labels["app.kubernetes.io/version"], "1.121.4")

    def test_rejects_chart_extraction_paths_and_ambiguous_registries(self):
        with tempfile.TemporaryDirectory() as staging:
            archive = pathlib.Path(staging) / "opencost.tgz"
            with tarfile.open(archive, mode="w:gz") as writer:
                writer.addfile(tarfile.TarInfo("opencost/../Chart.yaml"))
            with self.assertRaises(ValueError):
                prepare.read_chart(archive)
        self.assertFalse(prepare.valid_registry("https://hub.example"))
        self.assertFalse(prepare.valid_registry("hub.example:99999"))
        self.assertTrue(prepare.valid_registry("hub.example"))
        self.assertTrue(prepare.valid_registry("192.0.2.10:8080"))
        self.assertTrue(prepare.valid_registry("[2001:db8::10]"))
        self.assertTrue(prepare.valid_registry("[2001:db8::10]:8443"))
        self.assertFalse(prepare.valid_registry("2001:db8::10"))
        self.assertFalse(prepare.valid_registry("[fe80::1%eth0]"))
        self.assertFalse(prepare.valid_registry("[2001:db8::10]:+8080"))


if __name__ == "__main__":
    unittest.main()
