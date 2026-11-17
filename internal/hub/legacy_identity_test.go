package hub

import (
	"github.com/maomada/KubeAtlas/pkg/compat"
	"net/http"
	"testing"
)

func TestDeployedIdentityHeaderNamesAreStillRemoved(t *testing.T) {
	headers := http.Header{"X-Radar-User": {"forged"}, "X-Radar-Groups": {"system:masters"}, "X-KubeAtlas-User": {"forged"}, "X-KubeAtlas-Role": {"owner"}, "X-Fixture-Value": {"retained"}}
	stripInboundHeaders(headers)
	if headers.Get("X-Fixture-Value") != "retained" {
		t.Fatal("unrelated header was removed")
	}
	for key := range headers {
		if compat.IsLegacyIdentityHeader(key) || isInboundIdentityHeader(key) {
			t.Fatalf("forged identity header survived:%s", key)
		}
	}
}
