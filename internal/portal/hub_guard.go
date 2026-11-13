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

func (p *hubRequestPolicy) withOrigins(origins ...string) *hubRequestPolicy {
	hosts := make(map[string]bool, len(p.hosts)+len(origins))
	for host, allowed := range p.hosts {
		hosts[host] = allowed
	}
	for _, origin := range origins {
		if origin == "" {
			continue
		}
		u, err := url.Parse(origin)
		if err == nil && u.Host != "" {
			hosts[strings.ToLower(u.Host)] = true
		}
	}
	return &hubRequestPolicy{hosts: hosts}
}

func (p *hubRequestPolicy) allows(r *http.Request) bool {
	if !p.allowsHost(r) {
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

func (p *hubRequestPolicy) allowsHost(r *http.Request) bool {
	u, ok := hubAuthority(r.Host)
	return ok && (p.hosts[strings.ToLower(r.Host)] || strings.EqualFold(u.Hostname(), "localhost") ||
		net.ParseIP(u.Hostname()) != nil && net.ParseIP(u.Hostname()).IsLoopback())
}

func (p *hubRequestPolicy) allowsBrowser(r *http.Request) bool {
	if !p.allows(r) {
		return false
	}
	sites := r.Header.Values("Sec-Fetch-Site")
	if len(sites) != 1 || !(strings.EqualFold(sites[0], "same-origin") || strings.EqualFold(sites[0], "none")) {
		return false
	}
	origins := r.Header.Values("Origin")
	if len(origins) > 1 {
		return false
	}
	var origin *url.URL
	if len(origins) == 1 {
		var err error
		origin, err = url.Parse(origins[0])
		if err != nil || !(origin.Scheme == "http" || origin.Scheme == "https") ||
			!strings.EqualFold(origin.Host, r.Host) || origin.User != nil || origin.Path != "" ||
			origin.RawQuery != "" || origin.Fragment != "" {
			return false
		}
	}
	referers := r.Header.Values("Referer")
	if len(referers) > 1 {
		return false
	}
	if len(referers) == 1 {
		referer, err := url.Parse(referers[0])
		if err != nil || !(referer.Scheme == "http" || referer.Scheme == "https") ||
			!strings.EqualFold(referer.Host, r.Host) || referer.User != nil || referer.Fragment != "" ||
			origin != nil && !strings.EqualFold(referer.Scheme, origin.Scheme) {
			return false
		}
	}
	return true
}
