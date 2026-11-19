package portal

import (
	"github.com/maomada/KubeAtlas/pkg/compat"
	"net/http"
)

func legacyOIDCStateCookieName(h *Handler, r *http.Request) string {
	if h.cookieSecureForRequest(r) {
		return "__Host-" + compat.LegacyOIDCStateCookie
	}
	return compat.LegacyOIDCStateCookie
}
