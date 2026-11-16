package portal

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

func normalizeHTTPIPOrigin(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if strings.TrimSpace(value) != value || strings.ContainsFunc(value, func(r rune) bool {
		return unicode.IsControl(r) || unicode.IsSpace(r)
	}) || strings.Contains(value, "\\") {
		return "", errors.New("HTTP IP origin must be an exact root HTTP origin")
	}
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "http" || u.Opaque != "" || u.User != nil || u.Host == "" ||
		u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" ||
		strings.Contains(value, "#") || strings.HasSuffix(u.Host, ":") {
		return "", errors.New("HTTP IP origin must be an exact root HTTP origin")
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if ip == nil {
		return "", errors.New("HTTP IP origin host must be a literal IP address")
	}
	ipv6 := ip.To4() == nil
	if !ipv6 && ip.To4().String() != host {
		return "", errors.New("HTTP IP origin IPv4 host must use canonical dotted-decimal form")
	}
	if ipv6 != strings.HasPrefix(u.Host, "[") || strings.HasSuffix(u.Host, "]") && !ipv6 {
		return "", errors.New("HTTP IP origin must use brackets only for IPv6 authorities")
	}
	canonicalIP := ip.String()
	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
			return "", errors.New("HTTP IP origin port must be between 1 and 65535")
		}
		return "http://" + net.JoinHostPort(canonicalIP, strconv.Itoa(n)), nil
	}
	if ipv6 {
		return "http://[" + canonicalIP + "]", nil
	}
	return "http://" + canonicalIP, nil
}
