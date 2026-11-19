package hub

import (
	"github.com/gorilla/websocket"
	"github.com/maomada/KubeAtlas/pkg/compat"
	"net/http"
	"strings"
	"testing"
)

func TestDeployedAgentHandshakeAndMixedCredentialDenial(t *testing.T) {
	for _, tc := range []struct {
		name     string
		headers  http.Header
		accepted bool
	}{
		{"deployed", http.Header{compat.LegacyAgentCredentialsHeader: {"impersonate"}, compat.LegacyAgentVersionHeader: {"deployed-fixture"}}, true},
		{"canonical", http.Header{compat.AgentCredentialsHeader: {"impersonate"}}, true},
		{"matching", http.Header{compat.AgentCredentialsHeader: {"impersonate"}, compat.LegacyAgentCredentialsHeader: {"impersonate"}}, true},
		{"conflicting", http.Header{compat.AgentCredentialsHeader: {"impersonate"}, compat.LegacyAgentCredentialsHeader: {"user-token"}}, false},
		{"duplicate", http.Header{compat.LegacyAgentCredentialsHeader: {"impersonate", "impersonate"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, server := newTestHub(t)
			tc.headers.Set("Authorization", "Bearer token-alpha")
			connection, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/agent?cluster_id=alpha", tc.headers)
			if tc.accepted {
				if err != nil {
					t.Fatalf("valid agent handshake:%v", err)
				}
				defer connection.Close()
				_ = waitForAgent(t, h, "alpha")
			} else {
				if err == nil {
					connection.Close()
					t.Fatal("ambiguous credentials accepted")
				}
				if response == nil || response.StatusCode != http.StatusForbidden {
					t.Fatalf("denial response=%v", response)
				}
			}
		})
	}
}
