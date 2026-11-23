package portal

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/maomada/KubeAtlas/internal/hubstate"
)

func (p *portalHub) manageCredentials(w http.ResponseWriter, r *http.Request, ownerID string, member hubstate.Member, organizationID string) {
	const base = "/api/mcp/credentials"
	if r.URL.Path == base {
		if r.Method == http.MethodGet {
			hubJSON(w, http.StatusOK, map[string]any{"credentials": p.store.CredentialsForOrganization(organizationID, ownerID), "serverTime": time.Now().UTC()})
			return
		}
		var input struct {
			Label        string   `json:"label"`
			ClusterIDs   []string `json:"clusterIds"`
			ReadOnly     *bool    `json:"readOnly"`
			TTLSeconds   int      `json:"ttlSeconds"`
			NeverExpires bool     `json:"neverExpires"`
		}
		if !hubMethod(w, r, http.MethodPost) || !hubDecode(w, r, &input, "label", "clusterIds", "readOnly", "ttlSeconds", "neverExpires") {
			return
		}
		if input.ReadOnly == nil {
			hubHTTPError(w, http.StatusBadRequest, "invalid_parameters", "readOnly must be an explicit boolean")
			return
		}
		issue := hubstate.IssueInput{
			Label: input.Label, ClusterIDs: input.ClusterIDs, ReadOnly: *input.ReadOnly,
			TTLSeconds: input.TTLSeconds, NeverExpires: input.NeverExpires,
		}
		var issued hubstate.IssuedCredential
		var err error
		if p.store.IsPlatformOwner(ownerID) {
			issued, err = p.store.IssueForOrganization(organizationID, ownerID, issue)
		} else {
			issued, err = p.store.IssueForMemberInOrganization(organizationID, ownerID, issue)
		}
		if err != nil {
			hubStoreError(w, err)
			return
		}
		hubJSON(w, http.StatusCreated, issued)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, base+"/"), "/")
	if len(parts) != 2 || parts[0] == "" || (parts[1] != "rotate" && parts[1] != "revoke") {
		hubHTTPError(w, http.StatusNotFound, "not_found", "Credential route not found")
		return
	}
	if !hubMethod(w, r, http.MethodPost) {
		return
	}
	if parts[1] == "revoke" {
		if !hubDecode(w, r, &struct{}{}) {
			return
		}
		if err := p.store.RevokeForOrganization(organizationID, ownerID, parts[0]); err != nil {
			hubCredentialManagementError(w, err)
			return
		}
		p.cancelCredentialRequests(ownerID, organizationID, parts[0])
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var input struct {
		TTLSeconds int `json:"ttlSeconds"`
	}
	if !hubDecode(w, r, &input, "ttlSeconds") {
		return
	}
	var issued hubstate.IssuedCredential
	var err error
	if p.store.IsPlatformOwner(ownerID) {
		issued, err = p.store.RotateForOrganization(organizationID, ownerID, parts[0], input.TTLSeconds)
	} else {
		issued, err = p.store.RotateForMemberInOrganization(organizationID, ownerID, parts[0], input.TTLSeconds)
	}
	if err != nil {
		hubCredentialManagementError(w, err)
		return
	}
	p.cancelCredentialRequests(ownerID, organizationID, parts[0])
	hubJSON(w, http.StatusOK, issued)
}

func hubCredentialManagementError(w http.ResponseWriter, err error) {
	var stateError *hubstate.Error
	if errors.As(err, &stateError) {
		switch stateError.Code {
		case "credential_invalid":
			hubHTTPError(w, http.StatusNotFound, stateError.Code, "Credential not found")
			return
		case "credential_expired", "credential_revoked", "credential_refresh_expired":
			hubHTTPError(w, http.StatusConflict, stateError.Code, "Credential cannot be renewed; issue a new credential")
			return
		}
	}
	hubStoreError(w, err)
}
