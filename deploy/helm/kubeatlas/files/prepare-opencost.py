"""Prepare pinned OpenCost chart values from read-only Kubernetes discovery."""

import argparse
import contextlib
import gzip
import hashlib
import http.client
import io
import ipaddress
import json
import pathlib
import re
import socket
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.parse


DNS_LABEL = re.compile(r"[a-z0-9](?:[-a-z0-9]{0,61}[a-z0-9])?")
LABEL_VALUE = re.compile(r"(?:[A-Za-z0-9](?:[-_.A-Za-z0-9]{0,61}[A-Za-z0-9])?)?")
LABEL_KEY = re.compile(r"[A-Za-z0-9](?:[-_.A-Za-z0-9]{0,61}[A-Za-z0-9])?")
IMAGE_DIGEST = re.compile(r"sha256:[0-9a-f]{64}")
MAX_CHART = 512 * 1024
MAX_EXPANSION = 16 * 1024 * 1024
MAX_PROXY_RESPONSE = 16 * 1024 * 1024
OPERATOR_API = "/apis/monitoring.coreos.com/v1"


class DiscoveryError(Exception):
    pass


class UnixHTTPConnection(http.client.HTTPConnection):
    def __init__(self, path, timeout):
        super().__init__("localhost", timeout=timeout)
        self.path = path

    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(self.timeout)
        self.sock.connect(self.path)


@contextlib.contextmanager
def kubectl_reader(context):
    deadline = time.monotonic() + 35
    proxy = None
    command = ["kubectl", f"--context={context}", "--request-timeout=4s"]

    def timeout(maximum=6):
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            raise DiscoveryError("Prometheus discovery timed out")
        return min(remaining, maximum)

    def read_service(path):
        nonlocal proxy
        if proxy is None:
            try:
                proxy = subprocess.Popen(
                    command + ["proxy", f"--unix-socket={proxy_path}"],
                    stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                )
                until = time.monotonic() + timeout(3)
                while not pathlib.Path(proxy_path).is_socket() and proxy.poll() is None:
                    if time.monotonic() >= until:
                        break
                    time.sleep(0.05)
            except OSError as error:
                raise DiscoveryError("Cannot start the private Kubernetes service proxy") from error
        connection = UnixHTTPConnection(proxy_path, timeout(4))
        try:
            connection.request("GET", path, headers={"Accept": "application/json"})
            response = connection.getresponse()
            # Never follow an upstream redirect using the kubeconfig transport.
            if response.status != 200:
                raise DiscoveryError("Prometheus query did not return HTTP 200")
            data = response.read(MAX_PROXY_RESPONSE + 1)
            if len(data) > MAX_PROXY_RESPONSE:
                raise DiscoveryError("Prometheus discovery response exceeds the allowed size")
            return data
        except (OSError, http.client.HTTPException) as error:
            raise DiscoveryError("The private Kubernetes service proxy is unavailable") from error
        finally:
            connection.close()

    def read_api(path, missing_ok):
        try:
            result = subprocess.run(
                command + ["get", "--raw", path], capture_output=True, timeout=timeout(), check=False,
            )
        except (OSError, subprocess.TimeoutExpired) as error:
            raise DiscoveryError("Kubernetes read timed out or kubectl could not run") from error
        if result.returncode:
            if missing_ok and b"(NotFound)" in result.stderr:
                return None
            raise DiscoveryError("Kubernetes or Prometheus did not permit the discovery request")
        return result.stdout

    def read(path, missing_ok=False):
        if re.match(r"^/api/v1/namespaces/[^/]+/services/[^/]+/proxy/", path):
            payload = read_service(path)
        else:
            payload = read_api(path, missing_ok)
        if payload is None:
            return None
        try:
            data = json.loads(payload)
        except ValueError as error:
            raise DiscoveryError("Discovery returned an unexpected response") from error
        if not isinstance(data, dict):
            raise DiscoveryError("Discovery returned an unexpected response")
        return data

    with tempfile.TemporaryDirectory(prefix="kubeatlas-prom-") as local:
        proxy_path = str(pathlib.Path(local) / "api.sock")
        try:
            yield read
        finally:
            if proxy is not None and proxy.poll() is None:
                proxy.terminate()
                try:
                    proxy.wait(timeout=2)
                except subprocess.TimeoutExpired:
                    proxy.kill()
                    proxy.wait()


def is_dns_name(value):
    return isinstance(value, str) and 0 < len(value) <= 253 and all(
        DNS_LABEL.fullmatch(label) for label in value.split(".")
    )


def valid_registry(authority):
    if authority.startswith("["):
        host, bracket, suffix = authority[1:].partition("]")
        try:
            address = ipaddress.IPv6Address(host)
        except ValueError:
            return False
        return bool(bracket) and address.scope_id is None and (
            not suffix or (suffix.startswith(":") and valid_registry_port(suffix[1:]))
        )
    host, separator, port = authority.partition(":")
    return is_dns_name(host) and ("." in host or bool(separator)) and (
        not separator or valid_registry_port(port)
    )


def valid_registry_port(port):
    return bool(re.fullmatch(r"[0-9]{1,5}", port)) and 1 <= int(port) <= 65535


def selector_terms(selector):
    terms = {}
    for key, label in selector.get("matchLabels", {}).items():
        terms.setdefault(key, []).append(("In", [label]))
    for term in selector.get("matchExpressions", []):
        terms.setdefault(term["key"], []).append((term["operator"], term.get("values", [])))
    return terms


def permits(conditions, value):
    for operation, values in conditions:
        if operation == "In" and value in values:
            continue
        if operation == "NotIn" and value not in values:
            continue
        if operation == "Exists" and value is not None:
            continue
        if operation == "DoesNotExist" and value is None:
            continue
        return False
    return True


def selector_matches(selector, labels):
    return all(permits(conditions, labels.get(key))
               for key, conditions in selector_terms(selector).items())


def valid_label_key(key):
    prefix, separator, name = key.partition("/")
    if not separator:
        return LABEL_KEY.fullmatch(prefix)
    return is_dns_name(prefix) and LABEL_KEY.fullmatch(name)


def monitor_labels(selector, defaults):
    additional = {}
    for name, conditions in selector_terms(selector).items():
        if not valid_label_key(name) or any(
            not isinstance(value, str) or not LABEL_VALUE.fullmatch(value)
            for _, values in conditions for value in values
        ):
            return None
        if permits(conditions, defaults.get(name)):
            continue
        if name in defaults:
            return None
        allowed = [value for op, values in conditions if op == "In" for value in values]
        if not allowed:
            forbidden = {value for op, values in conditions if op == "NotIn" for value in values}
            allowed = [f"kubeatlas-{i}" for i in range(len(forbidden) + 1)]
        match = next((value for value in sorted(set(allowed)) if permits(conditions, value)), None)
        if match is None:
            return None
        additional[name] = match
    return additional


def selected_monitor_namespace(prometheus, namespaces):
    metadata, spec = prometheus["metadata"], prometheus["spec"]
    owner_namespace = metadata["namespace"]
    selector = spec.get("serviceMonitorNamespaceSelector")
    can_cross_namespace = not spec.get("ignoreNamespaceSelectors", False)
    candidates = [owner_namespace, "opencost"] if can_cross_namespace else ["opencost"]
    for namespace in dict.fromkeys(candidates):
        if selector is None and namespace == owner_namespace:
            return namespace
        if selector is not None and selector_matches(selector, namespaces.get(namespace, {})):
            return namespace
    return None


def operator_backend(service, operators):
    metadata, spec = service["metadata"], service["spec"]
    namespace, name = metadata["namespace"], metadata["name"]
    selector = spec.get("selector") or {}
    candidates = [p for p in operators if p["metadata"]["namespace"] == namespace]
    # Reserved Pod labels associate aliases and custom Service names with the CR.
    for key in ("operator.prometheus.io/name", "prometheus", "app.kubernetes.io/instance"):
        matches = [p for p in candidates if selector.get(key) == p["metadata"]["name"]]
        if len(matches) == 1:
            return matches[0]
    matches = [p for p in candidates if p["spec"].get("serviceName") == name or
               f"prometheus-{p['metadata']['name']}" == name]
    if len(matches) == 1 and (selector.get("app.kubernetes.io/name") == "prometheus" or
                              selector.get("app") == "prometheus"):
        return matches[0]
    if len(candidates) == 1 and name == "prometheus-operated":
        return candidates[0]
    return None


def service_candidates(services, operators):
    candidates = []
    for service in services:
        metadata, spec = service["metadata"], service["spec"]
        namespace, name = metadata["namespace"], metadata["name"]
        if not DNS_LABEL.fullmatch(namespace) or not DNS_LABEL.fullmatch(name):
            continue
        selector = spec.get("selector") or {}
        if spec.get("type") == "ExternalName" or not selector:
            continue
        operator = operator_backend(service, operators)
        labels = metadata.get("labels", {})
        if operator is None:
            if name == "prometheus-operated":
                # The headless Service may route to independent instances.
                continue
            if not ("prometheus" in name or
                    labels.get("app.kubernetes.io/name") in ("prometheus", "prometheus-server")):
                continue
            if any(part in name for part in ("operator", "exporter", "alertmanager", "pushgateway", "adapter")):
                continue
        route = operator["spec"].get("routePrefix", "/") if operator else "/"
        if (not isinstance(route, str) or not re.fullmatch(r"/[-._/a-zA-Z0-9]*", route)
                or ".." in route or "//" in route):
            continue
        for port in spec.get("ports", []):
            number, label = port.get("port"), port.get("name", "")
            if not isinstance(number, int) or not 1 <= number <= 65535 or port.get("protocol", "TCP") != "TCP":
                continue
            if any(part in label for part in ("reload", "grpc", "metrics")) and number != 9090:
                continue
            scheme = "https" if label.startswith("https") or port.get("appProtocol") == "https" else "http"
            priority = (
                int(operator is not None),
                int(spec.get("clusterIP") not in ("None", "", None)),
                int(name in {"prometheus-k8s", "prometheus", "prometheus-server", "kube-prometheus-stack-prometheus"}),
                int(number == 9090 or port.get("targetPort") == 9090),
            )
            candidates.append((priority, namespace, name, number, route.rstrip("/"), scheme, operator))
    return sorted(candidates, key=lambda item: item[:6], reverse=True)


def annotation_scraper(read, proxy):
    try:
        targets = read(proxy + "/api/v1/targets?state=active")["data"]["activeTargets"]
    except (DiscoveryError, KeyError, TypeError):
        return False
    return any(target.get("scrapePool") == "kubernetes-service-endpoints" and
               target.get("health") == "up" and
               target.get("discoveredLabels", {}).get("__meta_kubernetes_service_annotation_prometheus_io_scrape") == "true"
               for target in targets)


def successful_probe(response):
    data = response.get("data")
    if response.get("status") != "success" or not isinstance(data, dict) or data.get("resultType") != "vector":
        return False
    result = data.get("result")
    if not isinstance(result, list) or len(result) != 1 or not isinstance(result[0], dict):
        return False
    value = result[0].get("value")
    return isinstance(value, list) and len(value) == 2 and value[1] == "1"


def discover(read, chart_labels):
    services = read("/api/v1/services")["items"]
    operator_api = read(OPERATOR_API, missing_ok=True)
    resources = {item["name"] for item in operator_api["resources"]} if operator_api else set()
    operators = read(OPERATOR_API + "/prometheuses")["items"] if "prometheuses" in resources else []
    namespaces = {"opencost": {"kubernetes.io/metadata.name": "opencost"}}
    if operators:
        namespaces.update({row["metadata"]["name"]: row["metadata"].get("labels", {})
                           for row in read("/api/v1/namespaces")["items"]})
    options = service_candidates(services, operators)
    if not options:
        return {"ready": False, "reason": "prometheus-not-found"}
    results = {}
    best = None
    reached = False
    for priority, namespace, name, number, route, scheme, operator in options[:12]:
        if best is not None and priority < best:
            break
        proxy = f"/api/v1/namespaces/{namespace}/services/{scheme}:{name}:{number}/proxy{route}"
        try:
            response = read(proxy + "/api/v1/query?query=" + urllib.parse.quote("vector(1)", safe=""))
        except DiscoveryError:
            continue
        if not successful_probe(response):
            continue
        reached = True
        endpoint = f"{scheme}://{name}.{namespace}.svc:{number}{route}"
        if operator:
            # Select the same Prometheus for querying and scraping the exporter.
            selector = operator["spec"].get("serviceMonitorSelector")
            if selector is None or "servicemonitors" not in resources:
                continue
            monitor_ns = selected_monitor_namespace(operator, namespaces)
            labels = monitor_labels(selector, chart_labels)
            if monitor_ns is None or labels is None:
                continue
            identity = (operator["metadata"]["namespace"], operator["metadata"]["name"])
            mode = "service-monitor"
        else:
            if not annotation_scraper(read, proxy):
                continue
            monitor_ns, labels = "", {}
            identity = (namespace, name, number)
            mode = "service-annotations"
        best = priority
        results.setdefault(identity, {"ready": True, "reason": "prometheus-detected",
                                      "prometheusURL": endpoint, "monitoringMode": mode,
                                      "serviceMonitorNamespace": monitor_ns, "serviceMonitorLabels": labels})
    if len(results) == 1:
        return next(iter(results.values()))
    if results:
        return {"ready": False, "reason": "prometheus-ambiguous"}
    return {"ready": False, "reason": "prometheus-scrape-configuration-required" if reached else "prometheus-unreachable"}


def read_chart(path):
    if path.stat().st_size > MAX_CHART:
        raise ValueError("OpenCost base package exceeds the allowed size")
    files = {}
    expanded_size = 0
    with tarfile.open(path, mode="r:gz") as archive:
        for entry in archive:
            path_parts = entry.name.split("/")
            if (path_parts[0] != "opencost" or ".." in path_parts or
                    entry.name in files or "/crds/" in entry.name or entry.name.endswith(".tgz")):
                raise ValueError("OpenCost base package has an unexpected entry")
            if entry.isdir():
                continue
            expanded_size += entry.size
            if not entry.isfile() or len(files) >= 1024 or expanded_size > MAX_EXPANSION:
                raise ValueError("OpenCost base package contains an unsafe entry")
            files[entry.name] = archive.extractfile(entry).read()
    metadata = files["opencost/Chart.yaml"].decode()
    fields = dict(re.findall(r"^(name|version|appVersion):\s*[\x22']?([-+._a-zA-Z0-9]+)[\x22']?\s*$", metadata, re.M))
    if fields.get("name") != "opencost" or not fields.get("version"):
        raise ValueError("OpenCost base package must have a versioned opencost chart")
    values = json.loads(files["opencost/values.yaml"])
    name = values.get("nameOverride") or "opencost"
    labels = {"helm.sh/chart": ("opencost-" + fields["version"].replace("+", "_"))[:63].rstrip("-"),
              "app.kubernetes.io/name": name[:63].rstrip("-"), "app.kubernetes.io/instance": "opencost",
              "app.kubernetes.io/part-of": name[:63].rstrip("-"), "app.kubernetes.io/managed-by": "Helm"}
    if fields.get("appVersion"):
        labels["app.kubernetes.io/version"] = fields["appVersion"]
    labels.update(values.get("commonLabels") or {})
    return files, values, labels


def write_chart(path, files, values, result, registry, digest):
    values["imagePullSecrets"] = []
    opencost = values["opencost"]
    opencost["exporter"]["image"]["fullImageName"] = f"{registry}/kubeatlas/opencost@{digest}"
    opencost["prometheus"]["internal"]["enabled"] = False
    opencost["prometheus"]["external"].update(enabled=True, url=result["prometheusURL"])
    use_monitor = result["monitoringMode"] == "service-monitor"
    opencost["metrics"]["serviceMonitor"].update(
        enabled=use_monitor, namespace=result["serviceMonitorNamespace"],
        additionalLabels=result["serviceMonitorLabels"],
    )
    if not use_monitor:
        values["service"]["annotations"].update({
            "prometheus.io/scrape": "true",
            "prometheus.io/port": str(opencost["exporter"]["apiPort"]),
            "prometheus.io/path": "/metrics",
        })
    files["opencost/values.yaml"] = (json.dumps(values, sort_keys=True) + "\n").encode()
    data = io.BytesIO()
    with gzip.GzipFile(fileobj=data, mode="wb", mtime=0) as compressed:
        with tarfile.open(fileobj=compressed, mode="w", format=tarfile.USTAR_FORMAT) as archive:
            for name, contents in sorted(files.items()):
                entry = tarfile.TarInfo(name)
                entry.size = len(contents)
                archive.addfile(entry, io.BytesIO(contents))
    compressed = data.getvalue()
    if len(compressed) > MAX_CHART:
        raise ValueError("Prepared OpenCost chart is too large for its ConfigMap")
    with path.open("xb") as target:
        target.write(compressed)
    return hashlib.sha256(compressed).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--context", required=True)
    parser.add_argument("--base", required=True, type=pathlib.Path)
    parser.add_argument("--output", required=True, type=pathlib.Path)
    parser.add_argument("--registry", required=True)
    parser.add_argument("--image-digest", required=True)
    args = parser.parse_args()
    try:
        if not IMAGE_DIGEST.fullmatch(args.image_digest):
            raise ValueError("OpenCost image digest is invalid")
        if not valid_registry(args.registry):
            print(json.dumps({"ready": False, "reason": "unsupported-opencost-registry"}))
            return 0
        files, values, labels = read_chart(args.base)
        try:
            with kubectl_reader(args.context) as read:
                result = discover(read, labels)
        except (DiscoveryError, KeyError, TypeError, ValueError):
            result = {"ready": False, "reason": "prometheus-discovery-incomplete"}
        if result["ready"]:
            result["chartSHA256"] = write_chart(args.output, files, values, result, args.registry, args.image_digest)
        print(json.dumps(result, sort_keys=True))
    except (OSError, ValueError, KeyError, TypeError, tarfile.TarError) as error:
        print(f"OpenCost bundle preparation failed: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
