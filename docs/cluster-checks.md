# Cluster checks

Open **Checks** to run the cluster audit scanner with 44 checks for security, reliability, and resource efficiency. It evaluates the Kubernetes resources KubeAtlas can read from its cache.

- Security checks include privileged containers, dangerous capabilities, exposed host paths, and secrets in ConfigMaps.
- Reliability checks include missing health probes, replica placement, orphaned Services and Ingresses, and deprecated API versions.
- Resource checks include missing CPU or memory requests, unused configuration, and volume binding or retention issues.

Filter checks by category, severity, or framework. Expand a check to review its affected resources and remediation guidance. Configure namespace or check exclusions in the UI, or add a comma-separated list of check IDs to the affected resource's `kubeatlas.io/ignore-checks` annotation.

MCP clients can query the same findings with `get_cluster_audit`. See [audit coverage and exclusions](configuration.md#audit-evidence-under-partial-access).
