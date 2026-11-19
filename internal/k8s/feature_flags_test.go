package k8s

import (
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// kubeatlasFeaturesPath is the frontend's table of endpoints an embedding host may
// find missing on an older KubeAtlas, with the flag each one is negotiated by.
const kubeatlasFeaturesPath = "../../web/src/api/kubeatlasFeatures.ts"

var flagInKubeAtlasFeatures = regexp.MustCompile(`flag:\s*['"]([A-Za-z]+)['"]`)

// Flags that hide a control or pick a request shape rather than gate an
// endpoint, so an older KubeAtlas shows no upgrade note for them and they have no
// entry in the table.
var flagsWithoutUpgradeNote = map[string]bool{
	"yamlReview":               true,
	"yamlSchemas":              true,
	"workloadImages":           true,
	"clusterScopedPlaceholder": true,
}

// TestFeatureFlagsHaveFrontendGates fails when FeatureCapabilities and the
// frontend's feature table drift apart. A flag without an entry means a newer
// UI calls the endpoint unguarded and shows an older KubeAtlas a red error instead
// of an upgrade note; an entry without a flag waits on a flag no KubeAtlas sends.
func TestFeatureFlagsHaveFrontendGates(t *testing.T) {
	raw, err := os.ReadFile(kubeatlasFeaturesPath)
	if err != nil {
		t.Fatalf("read %s: %v", kubeatlasFeaturesPath, err)
	}
	gated := map[string]bool{}
	for _, m := range flagInKubeAtlasFeatures.FindAllStringSubmatch(string(raw), -1) {
		gated[m[1]] = true
	}
	if len(gated) == 0 {
		t.Fatalf("no flags parsed from %s; did the table format change?", kubeatlasFeaturesPath)
	}

	advertised := map[string]bool{}
	featuresType := reflect.TypeOf(FeatureCapabilities{})
	for i := 0; i < featuresType.NumField(); i++ {
		name, _, _ := strings.Cut(featuresType.Field(i).Tag.Get("json"), ",")
		advertised[name] = true
	}

	var ungated, unadvertised []string
	for flag := range advertised {
		if !gated[flag] && !flagsWithoutUpgradeNote[flag] {
			ungated = append(ungated, flag)
		}
	}
	for flag := range gated {
		if !advertised[flag] {
			unadvertised = append(unadvertised, flag)
		}
	}
	sort.Strings(ungated)
	sort.Strings(unadvertised)
	if len(ungated) > 0 {
		t.Errorf("FeatureCapabilities flags with no entry in %s: %v (add one, or list the flag in flagsWithoutUpgradeNote if it only hides a control)", kubeatlasFeaturesPath, ungated)
	}
	if len(unadvertised) > 0 {
		t.Errorf("%s gates on flags FeatureCapabilities does not advertise: %v", kubeatlasFeaturesPath, unadvertised)
	}
}
