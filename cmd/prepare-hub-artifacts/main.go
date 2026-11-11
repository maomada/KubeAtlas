package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/maomada/KubeAtlas/internal/hubartifacts"
)

func main() {
	chart := flag.String("chart", "deploy/helm/kubeatlas", "Source KubeAtlas Helm chart directory")
	layout := flag.String("layout", "", "Existing local OCI image layout directory")
	installer := flag.String("installer", "scripts/install-hub-agent.sh", "Hub Agent installer script")
	output := flag.String("output", "", "New private output directory (must not exist)")
	version := flag.String("version", "", "Exact Portal, chart, and Agent image version")
	revision := flag.String("revision", "", "40-character source revision encoded in Agent image labels")
	digest := flag.String("image-digest", "", "Pinned sha256 image manifest or index digest")
	helm := flag.String("helm", "helm", "Existing Helm executable")
	flag.Parse()
	if *layout == "" || *output == "" || *version == "" || *revision == "" || *digest == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := hubartifacts.Prepare(hubartifacts.PrepareOptions{
		ChartDir: *chart, LayoutDir: *layout, InstallerPath: *installer, OutputDir: *output,
		Version: *version, Revision: *revision, ImageDigest: *digest, HelmBinary: *helm,
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("Prepared private Hub artifact catalog at %s\n", *output)
}
