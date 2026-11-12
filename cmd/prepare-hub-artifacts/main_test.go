package main

import "testing"

func TestParseOptionsKeepsBuildAndChartVersionsSeparate(t *testing.T) {
	args := []string{
		"--layout", "image-layout", "--output", "bundle", "--version", "96bbb02",
		"--revision", "0123456789abcdef0123456789abcdef01234567",
		"--image-digest", "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"--chart-version", "1.2.3",
	}
	options, err := parseOptions(args)
	if err != nil {
		t.Fatal(err)
	}
	if options.Version != "96bbb02" || options.ChartVersion != "1.2.3" {
		t.Fatalf("parsed versions = build %q, chart %q", options.Version, options.ChartVersion)
	}
}

func TestParseOptionsDefaultsChartVersion(t *testing.T) {
	args := []string{
		"--layout", "image-layout", "--output", "bundle", "--version", "96bbb02",
		"--revision", "0123456789abcdef0123456789abcdef01234567",
		"--image-digest", "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	options, err := parseOptions(args)
	if err != nil {
		t.Fatal(err)
	}
	if options.ChartVersion != "" {
		t.Fatalf("default chart version should be derived by Prepare, got %q", options.ChartVersion)
	}
}
