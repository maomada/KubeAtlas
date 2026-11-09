package portal

import (
	"net/http/httptest"
	"testing"
)

func TestHubRequestPolicy(t *testing.T) {
	policy, err := newHubRequestPolicy([]string{"hub.example:9445"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		host, origin, site string
		allowed            bool
	}{
		{"hub.example:9445", "", "", true},
		{"hub.example:9445", "https://hub.example:9445", "same-origin", true},
		{"127.0.0.1:9290", "", "", true},
		{"[::1]:9290", "", "", true},
		{"localhost:9290", "", "", true},
		{"evil.example", "", "", false},
		{"hub.example:9446", "", "", false},
		{"hub.example:9445", "https://evil.example", "", false},
		{"hub.example:9445", "https://hub.example:9445?", "", false},
		{"hub.example:9445", "https://hub.example:9445#", "", false},
		{"hub.example:9445", "", "cross-site", false},
		{"hub.example:9445", "", "same-site", false},
		{"localhost:invalid", "", "", false},
	} {
		r := httptest.NewRequest("POST", "http://localhost/", nil)
		r.Host = tc.host
		if tc.origin != "" {
			r.Header.Set("Origin", tc.origin)
		}
		r.Header.Set("Sec-Fetch-Site", tc.site)
		r.Header.Set("X-Forwarded-Host", "hub.example:9445")
		if got := policy.allows(r); got != tc.allowed {
			t.Errorf("host=%q origin=%q site=%q: allowed=%v", tc.host, tc.origin, tc.site, got)
		}
	}
	for _, host := range []string{"", "*.example", "hub.example/path", "user@hub.example", "hub.example:", "hub.example:65536", "hub.example?", "hub.example#"} {
		if _, err := newHubRequestPolicy([]string{host}); err == nil {
			t.Errorf("accepted invalid host %q", host)
		}
	}
}
