package portal

import (
	"net/http"
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

func TestHubRequestPolicyAllowsBrowserMetadata(t *testing.T) {
	policy, err := newHubRequestPolicy([]string{"172.18.0.1:9461", "hub.example:9445"})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		method   string
		scheme   string
		host     string
		origins  []string
		referers []string
		sites    []string
		allow    bool
	}{
		{name: "HTTP IP GET with browser Referer only", method: http.MethodGet, host: "172.18.0.1:9461", referers: []string{"http://172.18.0.1:9461/install"}, allow: true},
		{name: "HTTP IP POST with Origin only", method: http.MethodPost, host: "172.18.0.1:9461", origins: []string{"http://172.18.0.1:9461"}, allow: true},
		{name: "same-origin Fetch Metadata", method: http.MethodGet, host: "hub.example:9445", sites: []string{"same-origin"}, allow: true},
		{name: "none Fetch Metadata", method: http.MethodGet, host: "hub.example:9445", sites: []string{"none"}, allow: true},
		{name: "secure same-origin request", method: http.MethodGet, scheme: "https", host: "hub.example:9445", origins: []string{"https://hub.example:9445"}, referers: []string{"https://hub.example:9445/install"}, sites: []string{"same-origin"}, allow: true},
		{name: "secure origin without Fetch Metadata", method: http.MethodPost, scheme: "https", host: "hub.example:9445", origins: []string{"https://hub.example:9445"}, allow: true},
		{name: "foreign Origin", method: http.MethodGet, host: "172.18.0.1:9461", origins: []string{"http://evil.example"}, allow: false},
		{name: "invalid Referer", method: http.MethodGet, host: "172.18.0.1:9461", referers: []string{"not a URL"}, allow: false},
		{name: "foreign Referer", method: http.MethodGet, host: "172.18.0.1:9461", referers: []string{"http://evil.example/install"}, allow: false},
		{name: "duplicate Origin", method: http.MethodGet, host: "172.18.0.1:9461", origins: []string{"http://172.18.0.1:9461", "http://172.18.0.1:9461"}, allow: false},
		{name: "duplicate Referer", method: http.MethodGet, host: "172.18.0.1:9461", referers: []string{"http://172.18.0.1:9461/", "http://172.18.0.1:9461/install"}, allow: false},
		{name: "duplicate Fetch Metadata", method: http.MethodGet, host: "172.18.0.1:9461", sites: []string{"same-origin", "same-origin"}, allow: false},
		{name: "empty Fetch Metadata", method: http.MethodGet, host: "172.18.0.1:9461", sites: []string{""}, allow: false},
		{name: "cross-site Fetch Metadata", method: http.MethodGet, host: "172.18.0.1:9461", sites: []string{"cross-site"}, allow: false},
		{name: "same-site Fetch Metadata", method: http.MethodGet, host: "172.18.0.1:9461", sites: []string{"same-site"}, allow: false},
		{name: "Origin and Referer schemes differ", method: http.MethodGet, host: "hub.example:9445", origins: []string{"https://hub.example:9445"}, referers: []string{"http://hub.example:9445/install"}, allow: false},
		{name: "unknown Host", method: http.MethodGet, host: "unknown.example", origins: []string{"http://unknown.example"}, allow: false},
		{name: "metadata-free GET", method: http.MethodGet, host: "172.18.0.1:9461", allow: false},
		{name: "metadata-free HEAD", method: http.MethodHead, host: "172.18.0.1:9461", allow: false},
		{name: "metadata-free POST", method: http.MethodPost, host: "172.18.0.1:9461", allow: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scheme := test.scheme
			if scheme == "" {
				scheme = "http"
			}
			r := httptest.NewRequest(test.method, scheme+"://"+test.host+"/c/cluster/api/connection", nil)
			r.Host = test.host
			for _, origin := range test.origins {
				r.Header.Add("Origin", origin)
			}
			for _, referer := range test.referers {
				r.Header.Add("Referer", referer)
			}
			for _, site := range test.sites {
				r.Header.Add("Sec-Fetch-Site", site)
			}
			if got := policy.allowsBrowser(r); got != test.allow {
				t.Fatalf("allowsBrowser() = %v, want %v", got, test.allow)
			}
		})
	}
}
