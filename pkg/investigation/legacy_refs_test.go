package investigation

import (
	"github.com/maomada/KubeAtlas/pkg/compat"
	"strings"
	"testing"
)

func TestDeployedEvidenceMarkersRemainTypedAndAnchored(t *testing.T) {
	ref := "ev_" + strings.Repeat("a", 26) + "_" + strings.Repeat("b", 26)
	for _, prefix := range []string{refMarkerPrefix, compat.LegacyEvidenceRefPrefix} {
		text := prefix + ref + "]]\nretained result"
		clean, got := SplitRefMarker(text)
		if clean != "retained result" || got != ref {
			t.Fatal("deployed evidence marker not recognized")
		}
		if unwrapRef(prefix+ref+"]]") != ref {
			t.Fatal("whole marker transcription lost")
		}
		if _, got := SplitRefMarker("prefix " + text); got != "" {
			t.Fatal("unanchored marker became evidence")
		}
	}
}
