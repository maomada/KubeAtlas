package bundledcharts

import "testing"

func TestLoadBundledCaretta(t *testing.T) {
	for _, version := range []string{"", "latest", CarettaVersion} {
		loaded, source, err := Load("caretta", version)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.Metadata.Version != CarettaVersion || source.Version != CarettaVersion {
			t.Fatal("bundle did not resolve to its pinned version")
		}
		dependencies := map[string]string{}
		for _, dependency := range loaded.Dependencies() {
			dependencies[dependency.Name()] = dependency.Metadata.Version
		}
		if dependencies["grafana"] != "6.48.0" || dependencies["victoria-metrics-single"] != "0.8.48" {
			t.Fatalf("dependencies missing from bundle: %v", dependencies)
		}
		loaded.Values["testMutation"] = true
		again, _, err := Load("caretta", version)
		if err != nil || again.Values["testMutation"] != nil {
			t.Fatal("chart instances must not share mutable values")
		}
	}
}

func TestBundledChartFailsClosed(t *testing.T) {
	for _, input := range [][2]string{{"other", CarettaVersion}, {"caretta", "0.0.15"}, {"../caretta", CarettaVersion}} {
		if _, _, err := Load(input[0], input[1]); err == nil {
			t.Fatalf("accepted unavailable bundle: %v", input)
		}
	}
	_, source, err := Load("caretta", CarettaVersion)
	if err != nil {
		t.Fatal(err)
	}
	data := append([]byte(nil), carettaArchive...)
	data[len(data)-1] ^= 1
	if _, err := loadArchive(data, source); err == nil {
		t.Fatal("accepted corrupt archive")
	}
}
