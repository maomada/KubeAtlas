package auth

import "testing"

func TestParseCredentialMode(t *testing.T) {
	for in, want := range map[string]CredentialMode{"": CredentialsImpersonate, "impersonate": CredentialsImpersonate, " user-token ": CredentialsUserToken} {
		got, err := ParseCredentialMode(in)
		if err != nil || got != want {
			t.Errorf("ParseCredentialMode(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ParseCredentialMode("token"); err == nil {
		t.Error("unknown mode accepted")
	}
}

func TestIsSystemIdentity(t *testing.T) {
	for _, tc := range []struct {
		user   string
		groups []string
		want   bool
	}{
		{"kubeatlas:system:hub-timeline-puller", []string{"kubeatlas:system"}, true},
		{"kubeatlas:ai:org_1", []string{"kubeatlas:ai:reader"}, true},
		{"kubeatlas:system:", []string{"kubeatlas:system"}, false},
		{"kubeatlas:ai:", []string{"kubeatlas:ai:reader"}, false},
		{"kubeatlas:system:x", []string{"kubeatlas:ai:reader"}, false},
		{"kubeatlas:ai:org_1", []string{"kubeatlas:system"}, false},
		{"kubeatlas:system:x", []string{"kubeatlas:system", "kubeatlas:owner"}, false},
		{"kubeatlas:system:x", nil, false},
		{"alice", []string{"kubeatlas:system"}, false},
	} {
		if got := IsSystemIdentity(tc.user, tc.groups); got != tc.want {
			t.Errorf("IsSystemIdentity(%q, %v) = %v, want %v", tc.user, tc.groups, got, tc.want)
		}
	}
}
