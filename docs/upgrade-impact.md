# Kubernetes upgrade impact

Open **Checks → Upgrade impact** before upgrading the control plane and select your target Kubernetes minor version. KubeAtlas runs the checks that apply to the selected upgrade path and orders results by the action they require. The bundled catalog covers Kubernetes releases through 1.37.

## What KubeAtlas checks

- Version and compatibility blockers, including skipped minor versions, unsupported component version skew, APIs removed in the target release, and removed or locked feature gates.
- Whether workloads can drain safely, including overlapping PodDisruptionBudgets.
- Runtime and configuration changes such as the removal of the `gitRepo` volume driver, deprecated Service external IPs, and renamed control-plane metrics.

The scan uses live resources, API availability, Helm release manifests, last-applied configuration, API server usage metrics, and PrometheusRule expressions where available.

## How to read the results

**Blocked** identifies a blocker. **Warning** flags likely impact, and **Review** calls for operator judgment. **Incomplete** means the check lacks evidence. **Passed** and **Not applicable** distinguish a completed check from one outside the selected path.

The scan covers every namespace the current identity can read. The header namespace picker filters browsing views. Expand a result to see its evidence, including sampling, unavailable data, and RBAC limits.
