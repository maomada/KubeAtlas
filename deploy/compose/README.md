# Deploy KubeAtlas Hub with Docker Compose

Use Docker Engine and Docker Compose v2 on a Linux `amd64` or `arm64` host. Select a release that publishes the same-version Hub image with the built-in Agent catalog. The example publishes Hub on `127.0.0.1:9290` for a TLS reverse proxy on the same host.

1. Set up a dedicated HTTPS root address for Hub. Configure the proxy to preserve the external `Host`, allow WebSocket upgrades, and stream responses. [Proxy and node trust requirements](../../docs/deploy-with-ai.md#1-choose-how-to-deploy-hub)
2. Create a private file, such as `/etc/kubeatlas/hub-compose.env`, with mode `0600`. Add these keys and replace the example values. Use an administrator password at least 16 bytes long. Single quotes keep dollar signs literal. To include a single quote in the password, escape it as `\'`.

```dotenv
KUBEATLAS_PORTAL_USERNAME=admin
KUBEATLAS_PORTAL_PASSWORD='<your-password>'
KUBEATLAS_PORTAL_HUB_ORIGIN=https://hub.example.com
KUBEATLAS_PORTAL_MCP_ALLOWED_HOSTS=hub.example.com
```

3. Run these commands in the directory containing [`compose.yml`](compose.yml), using the published image tag without a leading `v`.

```bash
export KUBEATLAS_HUB_VERSION='<version>'
export KUBEATLAS_HUB_ENV_FILE='/etc/kubeatlas/hub-compose.env'
docker compose pull
docker compose up -d
```

4. Confirm `curl --fail https://hub.example.com/healthz` succeeds and sign in to Hub. Use the Hub-generated command to [connect a cluster](../../docs/deploy-with-ai.md#2-connect-a-cluster).

## Upgrade

Back up the `hub-state` volume before changing versions. Keep the same Compose project and environment file so the replacement uses the same credentials and state. Set `KUBEATLAS_HUB_VERSION` to the new tag, pull the image, and run `docker compose up -d`. Verify the HTTPS endpoint and existing Agent connections after the change.

While an Agent still uses an older image digest, retain its prepared catalog using a Compose override. Replace the host path and previous version in this example and pass both files with `docker compose -f compose.yml -f retain-previous.yml up -d`.

```yaml
services:
  hub:
    command:
      - --listen=0.0.0.0:9290
      - --hub-state-file=/var/lib/kubeatlas-portal/hub/state.json
      - --hub-previous-artifacts-dir=/opt/kubeatlas/previous-agent-catalog
      - --hub-previous-artifacts-version=<previous-version>
    volumes:
      - /srv/kubeatlas/previous-agent-catalog:/opt/kubeatlas/previous-agent-catalog:ro
```

To use a private catalog for the current version, mount it read-only and set `KUBEATLAS_PORTAL_HUB_ARTIFACTS_DIR` to its container path. [Agent catalogs and upgrades](../../docs/hub-agent-onboarding.md#retain-a-previous-catalog)
