package main

import (
	"github.com/maomada/KubeAtlas/internal/reachability"
	versionpkg "github.com/maomada/KubeAtlas/internal/version"
)

func configureReachabilityImages() {
	reachability.DefaultImageRef = "ghcr.io/maomada/kubeatlas:" + versionpkg.Current
	reachability.LatestReleaseImage = nil
}
