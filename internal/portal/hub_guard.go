package portal

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type hubRequestPolicy struct {
	hosts map[string]bool
}

func hubAuthority(authority string) (*url.URL, bool) {
	u, err := url.Parse("https://" + authority)
	if err != nil || authority == "" || strings.ContainsAny(authority, "*\\ \t\r\n#") ||
		u.Host != authority || u.Hostname() == "" || u.User != nil || u.Path != "" ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.HasSuffix(authority, ":") {
		return nil, false
	}
	if port := u.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return nil, false
		}
	}
	return u, true
}

func newHubRequestPolicy(hosts []string) (*hubRequestPolicy, error) {
	policy := &hubRequestPolicy{hosts: make(map[string]bool, len(hosts))}
	for _, host := range hosts {
		if _, ok := hubAuthority(host); !ok {
			return nil, errors.New("Hub allowed hosts must be exact host[:port] authorities without wildcards")
		}
		policy.hosts[strings.ToLower(host)] = true
	}
	return policy, nil
}

func (p *hubRequestPolicy) allows(r *http.Request) bool {
	u, ok := hubAuthority(r.Host)
	if !ok || (!p.hosts[strings.ToLower(r.Host)] && !strings.EqualFold(u.Hostname(), "localhost") && !net.ParseIP(u.Hostname()).IsLoopback()) {
		return false
	}
	if !sameSiteRequest(r) {
		return false
	}
	origins := r.Header.Values("Origin")
	if len(origins) == 0 {
		return true
	}
	if len(origins) != 1 || origins[0] == "" {
		return false
	}
	origin, err := url.Parse(origins[0])
	return err == nil && (origin.Scheme == "http" || origin.Scheme == "https") &&
		strings.EqualFold(origin.Host, r.Host) && origin.User == nil && origin.Path == "" &&
		origin.RawQuery == "" && !origin.ForceQuery && origin.Fragment == "" && !strings.Contains(origins[0], "#")
}
