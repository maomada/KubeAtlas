package auth

import (
	"net/http"
	"strings"
)

func sessionCookieFamilyPresent(r *http.Request, name string) bool {
	for _, cookie := range r.Cookies() {
		if cookie.Value != "" && (cookie.Name == name || cookie.Name == name+cookieChunkCountSuffix || strings.HasPrefix(cookie.Name, name+cookieChunkSuffix)) {
			return true
		}
	}
	return false
}
