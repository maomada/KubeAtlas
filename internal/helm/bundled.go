package helm

import (
	"fmt"

	"github.com/maomada/KubeAtlas/internal/bundledcharts"
	"helm.sh/helm/v3/pkg/action"
)

func bundledChartDetail(name, version string) (*ChartDetail, error) {
	loaded, _, err := bundledcharts.Load(name, version)
	if err != nil {
		return nil, err
	}
	return chartDetailFromChart(loaded, ChartInfo{
		Name: loaded.Metadata.Name, Version: loaded.Metadata.Version,
		AppVersion: loaded.Metadata.AppVersion, Description: loaded.Metadata.Description,
		Repository: bundledcharts.Repository,
	}), nil
}

func installBundledChart(cfg *action.Configuration, req *InstallRequest, progress chan<- InstallProgress) (*HelmRelease, error) {
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
