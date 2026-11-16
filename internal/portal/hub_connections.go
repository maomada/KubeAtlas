package portal

import (
	"net/http"

	"github.com/maomada/KubeAtlas/internal/hubstate"
)

func (p *portalHub) manageConnections(w http.ResponseWriter, r *http.Request, member hubstate.Member) {
	switch r.Method {
	case http.MethodGet:
		if !requireActiveMember(w, member) {
			return
		}
		settings := p.store.Connections()
		p.writeConnections(w, http.StatusOK, settings)
	case http.MethodPut:
		if member.Role != "platformOwner" {
			hubHTTPError(w, http.StatusForbidden, "permission_denied", "Platform owner access is required")
			return
		}
		var input hubstate.ConnectionSettings
		if !hubDecode(w, r, &input, "lanURL", "publicURL") {
			return
		}
		settings, err := p.store.UpdateConnections(input)
		if err != nil {
			hubStoreError(w, err)
			return
		}
		p.writeConnections(w, http.StatusOK, settings)
	default:
		w.Header().Set("Allow", "GET, PUT")
		hubHTTPError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed")
	}
}

func (p *portalHub) writeConnections(w http.ResponseWriter, status int, settings hubstate.ConnectionSettings) {
	catalog := p.catalog.Availability()
	hubJSON(w, status, map[string]any{
		"lanURL": settings.LANURL, "publicURL": settings.PublicURL,
		"lanMCPURL": mcpURL(settings.LANURL), "publicMCPURL": mcpURL(settings.PublicURL),
		"ownerUserID": p.userID, "catalogAvailable": catalog.Available, "catalog": catalog,
	})
}

func mcpURL(origin string) string {
	if origin == "" {
		return ""
	}
	return origin + "/mcp"
}
