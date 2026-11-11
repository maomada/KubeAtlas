# Bundled Helm charts

The archive is embedded into the native Agent binary, not downloaded at runtime.
The dedicated `kubeatlas-bundled` source is separate from external repositories.

| Component | Version | Source |
| --- | --- | --- |
| Caretta chart | 0.0.16 | https://github.com/groundcover-com/helm-charts/releases/download/caretta-0.0.16/caretta-0.0.16.tgz |
| Grafana subchart | 6.48.0 | https://github.com/grafana/helm-charts/tree/grafana-6.48.0/charts/grafana |
| VictoriaMetrics Single subchart | 0.8.48 | https://github.com/VictoriaMetrics/helm-charts/tree/victoria-metrics-single-0.8.48 |

Archive SHA-256: `24593ec0e9e7971520b2ab3f049d7fceebe0519a04f857f711ca5c7505e0af72`.
Verified against the upstream `https://helm.groundcover.com/index.yaml` entry.
The original 60,288-byte archive is retained unchanged, with both dependencies.
All three chart repositories use Apache-2.0; their license texts are included
alongside this file. This does not relicense the container images (not bundled).

When updating, verify the original archive against the upstream index, review
chart templates, dependencies, images and licenses, update the catalog's version
and digest, then run the bundled-chart and Helm tests. Do not regenerate the
archive or fetch dependencies during application startup or installation.

Chart values are upstream defaults. In particular, Caretta needs privileged
eBPF access and compatible node kernels; embedded chart availability does not
guarantee runtime compatibility. Image pulls, insecure upstream defaults and
the age of the pinned dependencies require separate deployment review.
