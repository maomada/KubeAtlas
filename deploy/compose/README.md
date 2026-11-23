# Deploy KubeAtlas Hub with Docker Compose

Use Docker Engine and Docker Compose 2.24 or later on a Linux `amd64` or `arm64` host. Select a release that publishes the same-version Hub image with the built-in Agent catalog. The example publishes Hub on `127.0.0.1:1333` for a TLS reverse proxy on the same host. [Docker Compose environment files](https://docs.docker.com/compose/how-tos/environment-variables/set-environment-variables/)

1. Set up a dedicated HTTPS root address for Hub. Configure the proxy to preserve the external `Host`, allow WebSocket upgrades, and stream responses. [Proxy and node trust requirements](../../docs/deploy-with-ai.md#1-choose-how-to-deploy-hub)
2. Save these settings as `hub.env` beside `compose.yml`, or point `KUBEATLAS_HUB_ENV_FILE` at a different file.

```dotenv
KUBEATLAS_PORTAL_HUB_ORIGIN=https://hub.example.com
KUBEATLAS_PORTAL_MCP_ALLOWED_HOSTS=hub.example.com
```

3. Run these commands in the directory containing [`compose.yml`](compose.yml), using the published image tag without a leading `v`.

```bash
export KUBEATLAS_HUB_VERSION='<version>'
docker compose pull
docker compose up -d
```

4. Confirm `curl --fail https://hub.example.com/healthz` succeeds. Read the one-time code and open `https://hub.example.com` to create the platform administrator. Then use the Hub-generated command to [connect a cluster](../../docs/deploy-with-ai.md#2-connect-a-cluster).

```bash
docker compose exec hub /kubeatlas-portal \
  --hub-state-file=/var/lib/kubeatlas-portal/hub/state.json --show-setup-code
```

## Upgrade

Back up the `hub-state` volume before changing versions. Keep the same Compose project and environment file so the replacement uses the same state and can migrate credentials from earlier versions. Set `KUBEATLAS_HUB_VERSION` to the new tag, pull the image, and run `docker compose up -d`. Set `KUBEATLAS_HUB_PORT=9290` if your current reverse proxy still forwards to that port. Verify the HTTPS endpoint and existing Agent connections after the change.

If your existing `retain-previous.yml` still uses `--listen=0.0.0.0:9290`, set `KUBEATLAS_HUB_CONTAINER_PORT=9290` in your shell or Compose `.env` file before upgrading. This keeps Docker's target port aligned with the old override. You can also change that override's listener to `1333` and use the default container port. In either case, keep the same host port for your reverse proxy.

To roll back to v0.1.3 or earlier, stop Hub and restore the pre-upgrade state volume and environment file before starting the old image.

While an Agent still uses an older image digest, retain its prepared catalog using a Compose override. Replace the host path and previous version in this example and pass both files with `docker compose -f compose.yml -f retain-previous.yml up -d`.

```yaml
services:
  hub:
    command:
      - --listen=0.0.0.0:1333
      - --hub-state-file=/var/lib/kubeatlas-portal/hub/state.json
      - --hub-previous-artifacts-dir=/opt/kubeatlas/previous-agent-catalog
      - --hub-previous-artifacts-version=<previous-version>
    volumes:
      - /srv/kubeatlas/previous-agent-catalog:/opt/kubeatlas/previous-agent-catalog:ro
```

To use a private catalog for the current version, mount it read-only and set `KUBEATLAS_PORTAL_HUB_ARTIFACTS_DIR` to its container path. [Agent catalogs and upgrades](../../docs/hub-agent-onboarding.md#retain-a-previous-catalog)
