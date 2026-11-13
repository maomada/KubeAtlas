package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/maomada/KubeAtlas/internal/hubartifacts"
)

func main() {
	options, err := parseOptions(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := hubartifacts.Prepare(options); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("Prepared private Hub artifact catalog at %s\n", options.OutputDir)
}

func parseOptions(args []string) (hubartifacts.PrepareOptions, error) {
	set := flag.NewFlagSet("prepare-hub-artifacts", flag.ContinueOnError)
	chart := set.String("chart", "deploy/helm/kubeatlas", "Source KubeAtlas Helm chart directory")
	layout := set.String("layout", "", "Existing local OCI image layout directory")
	installer := set.String("installer", "scripts/install-hub-agent.sh", "Hub Agent installer script")
	bootstrap := set.String("bootstrap", "scripts/bootstrap-hub-agent.sh", "Optional same-origin Hub Agent bootstrap script")
	output := set.String("output", "", "New private output directory (must not exist)")
	version := set.String("version", "", "Exact Portal and Agent image build version")
	chartVersion := set.String("chart-version", "", "Helm chart SemVer (defaults to 0.0.0-dev.<revision12>)")
	revision := set.String("revision", "", "40-character source revision encoded in Agent image labels")
	digest := set.String("image-digest", "", "Pinned sha256 image manifest or index digest")
	helm := set.String("helm", "helm", "Existing Helm executable")
	if err := set.Parse(args); err != nil {
		return hubartifacts.PrepareOptions{}, err
	}
	if *layout == "" || *output == "" || *version == "" || *revision == "" || *digest == "" {
		return hubartifacts.PrepareOptions{}, fmt.Errorf("--layout, --output, --version, --revision, and --image-digest are required")
	}
	return hubartifacts.PrepareOptions{
		ChartDir: *chart, LayoutDir: *layout, InstallerPath: *installer, BootstrapPath: *bootstrap, OutputDir: *output,
		Version: *version, ChartVersion: *chartVersion, Revision: *revision, ImageDigest: *digest, HelmBinary: *helm,
	}, nil
}
