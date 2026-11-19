package audit

import (
	"os"
	"regexp"
	"strconv"
	"testing"

	bp "github.com/maomada/KubeAtlas/pkg/audit"
)

func TestDocumentedCheckCountMatchesRegistry(t *testing.T) {
	guide, err := os.ReadFile("../../docs/cluster-checks.md")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`scanner with (\d+) checks`).FindSubmatch(guide)
	if m == nil {
		t.Fatal(`docs/cluster-checks.md no longer says "scanner with N checks"`)
	}
	if n, _ := strconv.Atoi(string(m[1])); n != len(bp.CheckRegistry) {
		t.Errorf("docs/cluster-checks.md says %d audit checks, CheckRegistry has %d", n, len(bp.CheckRegistry))
	}
}
