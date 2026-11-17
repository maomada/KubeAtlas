package cloud

import (
	"github.com/maomada/KubeAtlas/pkg/compat"
	"testing"
)

func TestHandshakeRetainsOlderHubMetadata(t *testing.T) {
	headers := cloudHandshakeHeaders(Config{Token: "fixture", Namespace: "fixture", Release: "fixture"}, false)
	for _, pair := range [][2]string{{compat.AgentVersionHeader, compat.LegacyAgentVersionHeader}, {kubernetesCredentialsHeader, compat.LegacyAgentCredentialsHeader}, {selfUpgradeAvailableHeader, compat.LegacyAgentSelfUpgradeHeader}, {"X-KubeAtlas-Namespace", compat.LegacyAgentNamespaceHeader}, {"X-KubeAtlas-Release", compat.LegacyAgentReleaseHeader}} {
		if len(headers.Values(pair[0])) != 1 || len(headers.Values(pair[1])) != 1 || headers.Get(pair[0]) != headers.Get(pair[1]) {
			t.Fatalf("compatibility pair %v differs", pair)
		}
	}
}
