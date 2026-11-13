package portal

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/maomada/KubeAtlas/internal/hubartifacts"
	"github.com/maomada/KubeAtlas/internal/hubstate"
)

const enrollmentLifetime = 10 * time.Minute

type hubEnrollment struct {
	id, profile, origin, contextName, fingerprint   string
	deviceHash, userCodeHash                        [32]byte
	requesterIP, status, organizationID, approvedBy string
	name, clusterID                                 string
	expiresAt                                       time.Time
}

func enrollmentCode(value string) string {
	value = strings.ToUpper(strings.ReplaceAll(value, "-", ""))
	if len(value) != 8 {
		return ""
	}
	for _, r := range value {
		if !strings.ContainsRune("ABCDEFGHJKLMNPQRSTUVWXYZ23456789", r) {
			return ""
		}
	}
	return value
}

func validDeviceSecret(value string) bool {
	if len(value) != len("ka_device_")+64 || !strings.HasPrefix(value, "ka_device_") {
		return false
	}
	for _, digit := range value[len("ka_device_"):] {
		if digit < '0' || digit > '9' {
			if digit < 'a' || digit > 'f' {
				return false
			}
		}
	}
	return true
}

func (p *portalHub) allowEnrollment(key string, maximum int) bool {
	now := time.Now()
	p.enrollmentMu.Lock()
	defer p.enrollmentMu.Unlock()
	if _, exists := p.enrollmentAttempts[key]; !exists && len(p.enrollmentAttempts) >= 4096 {
		for oldKey, oldAttempts := range p.enrollmentAttempts {
			kept := oldAttempts[:0]
			for _, at := range oldAttempts {
				if now.Sub(at) < time.Minute {
					kept = append(kept, at)
				}
			}
			if len(kept) == 0 {
				delete(p.enrollmentAttempts, oldKey)
			} else {
				p.enrollmentAttempts[oldKey] = kept
			}
		}
		if len(p.enrollmentAttempts) >= 4096 {
			return false
		}
	}
	attempts := p.enrollmentAttempts[key][:0]
	for _, at := range p.enrollmentAttempts[key] {
		if now.Sub(at) < time.Minute {
			attempts = append(attempts, at)
		}
	}
	if len(attempts) >= maximum {
		p.enrollmentAttempts[key] = attempts
		return false
	}
	p.enrollmentAttempts[key] = append(attempts, now)
	return true
}

func enrollmentIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && net.ParseIP(host) != nil {
		return host
	}
	if net.ParseIP(r.RemoteAddr) != nil {
		return r.RemoteAddr
	}
	return "unknown"
}

func profileOrigin(settings hubstate.ConnectionSettings, profile string) (string, bool) {
	value := ""
	switch profile {
	case "lan":
		value = settings.LANURL
	case "public":
		value = settings.PublicURL
	default:
		return "", false
	}
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		u.Path != "" && u.Path != "/" || u.Scheme != "https" && !(u.Scheme == "http" && (net.ParseIP(u.Hostname()) != nil || strings.EqualFold(u.Hostname(), "localhost"))) {
		return "", false
	}
	return strings.TrimSuffix(value, "/"), true
}

func (p *portalHub) artifactAvailability() (hubartifacts.Availability, bool) {
	if p.catalog == nil {
		return hubartifacts.Availability{}, false
	}
	availability := p.catalog.Availability()
	return availability, availability.Available && availability.BootstrapAvailable
}

func (p *portalHub) enrollmentCommand(w http.ResponseWriter, r *http.Request, member hubstate.Member) {
	if !requireHubAdmin(w, member) {
		return
	}
	var input struct {
		Profile string `json:"profile"`
	}
	if !hubMethod(w, r, http.MethodPost) || !hubDecode(w, r, &input, "profile") {
		return
	}
	origin, ok := profileOrigin(p.store.Connections(), input.Profile)
	availability, available := p.artifactAvailability()
	if !ok || !available {
		hubHTTPError(w, http.StatusServiceUnavailable, "bootstrap_unavailable", "Hub Agent bootstrap artifacts or the selected origin are unavailable")
		return
	}
	command := buildEnrollmentBootstrapCommand(input.Profile, origin, availability)
	hubJSON(w, http.StatusOK, map[string]any{"command": command, "origin": origin})
}

func buildEnrollmentBootstrapCommand(profile, origin string, availability hubartifacts.Availability) string {
	script := `set -Eeuo pipefail
umask 077
tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
status="$(curl --silent --show-error --proto '=https,http' --max-redirs 0 --output "$tmp" --write-out '%{http_code}' "$KUBEATLAS_ORIGIN/downloads/agent/$KUBEATLAS_VERSION/bootstrap.sh")"
[[ "$status" == 200 ]] || { printf 'Bootstrap download returned HTTP %s\n' "$status" >&2; exit 1; }
printf '%s  %s\n' "$KUBEATLAS_BOOTSTRAP_SHA256" "$tmp" | sha256sum --check --status
bash "$tmp"`
	return "KUBEATLAS_PROFILE=" + shellQuote(profile) + " KUBEATLAS_ORIGIN=" + shellQuote(origin) +
		" KUBEATLAS_VERSION=" + shellQuote(availability.Version) + " KUBEATLAS_BOOTSTRAP_SHA256=" + shellQuote(availability.BootstrapSHA256) +
		" bash -c " + shellQuote(script)
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }

func (p *portalHub) createEnrollment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		hubHTTPError(w, http.StatusForbidden, "permission_denied", "Enrollment request URL is not allowed")
		return
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		hubHTTPError(w, http.StatusBadRequest, "invalid_parameters", "Query parameters are not supported")
		return
	}
	if len(r.Header.Values("Cookie")) != 0 || len(r.Header.Values("Authorization")) != 0 {
		hubHTTPError(w, http.StatusBadRequest, "invalid_parameters", "Enrollment requests must not include browser credentials")
		return
	}
	if !p.policySnapshot().allows(r) {
		hubHTTPError(w, http.StatusForbidden, "permission_denied", "Enrollment request URL is not allowed")
		return
	}
	ip := enrollmentIP(r)
	if !p.allowEnrollment("request:"+ip, 5) {
		w.Header().Set("Retry-After", "60")
		hubHTTPError(w, http.StatusTooManyRequests, "rate_limited", "Enrollment request limit reached")
		return
	}
	var input struct {
		Profile            string `json:"profile"`
		ContextName        string `json:"contextName"`
		ClusterFingerprint string `json:"clusterFingerprint"`
	}
	if !hubDecode(w, r, &input, "profile", "contextName", "clusterFingerprint") {
		return
	}
	if !validEnrollmentText(input.ContextName, 256) || !validEnrollmentText(input.ClusterFingerprint, 256) {
		hubHTTPError(w, http.StatusBadRequest, "invalid_parameters", "Cluster context or fingerprint is invalid")
		return
	}
	origin, ok := profileOrigin(p.store.Connections(), input.Profile)
	_, available := p.artifactAvailability()
	if !ok || !available {
		hubHTTPError(w, http.StatusServiceUnavailable, "bootstrap_unavailable", "Hub Agent bootstrap artifacts or the selected origin are unavailable")
		return
	}
	var idBytes [16]byte
	var secretBytes [32]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		hubHTTPError(w, 503, "store_unavailable", "Enrollment request is unavailable")
		return
	}
	if _, err := rand.Read(secretBytes[:]); err != nil {
		hubHTTPError(w, 503, "store_unavailable", "Enrollment request is unavailable")
		return
	}
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	secret := "ka_device_" + hex.EncodeToString(secretBytes[:])
	p.enrollmentMu.Lock()
	var id string
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			if _, err := rand.Read(idBytes[:]); err != nil {
				p.enrollmentMu.Unlock()
				hubHTTPError(w, 503, "store_unavailable", "Enrollment request is unavailable")
				return
			}
		}
		id = hex.EncodeToString(idBytes[:])
		if p.enrollments[id] == nil {
			break
		}
		id = ""
	}
	if id == "" {
		p.enrollmentMu.Unlock()
		hubHTTPError(w, 503, "store_unavailable", "Enrollment request is unavailable")
		return
	}
	for key, old := range p.enrollments {
		if !time.Now().Before(old.expiresAt) || old.status == "consumed" || old.status == "denied" {
			delete(p.enrollments, key)
		}
	}
	outstanding := 0
	for _, old := range p.enrollments {
		if old.requesterIP == ip && (old.status == "pending" || old.status == "approved") {
			outstanding++
		}
	}
	if len(p.enrollments) >= 128 || outstanding >= 3 {
		p.enrollmentMu.Unlock()
		hubHTTPError(w, http.StatusTooManyRequests, "rate_limited", "Too many outstanding enrollment requests")
		return
	}
	var userCode string
	var userCodeHash [32]byte
	for attempt := 0; attempt < 16; attempt++ {
		var codeBytes [8]byte
		if _, err := rand.Read(codeBytes[:]); err != nil {
			p.enrollmentMu.Unlock()
			hubHTTPError(w, 503, "store_unavailable", "Enrollment request is unavailable")
			return
		}
		code := make([]byte, len(codeBytes))
		for i := range code {
			code[i] = alphabet[codeBytes[i]&31]
		}
		candidate := string(code)
		hash := sha256.Sum256([]byte(candidate))
		collision := false
		for _, old := range p.enrollments {
			if time.Now().Before(old.expiresAt) && subtle.ConstantTimeCompare(hash[:], old.userCodeHash[:]) == 1 {
				collision = true
				break
			}
		}
		if !collision {
			userCode, userCodeHash = candidate, hash
			break
		}
	}
	if userCode == "" {
		p.enrollmentMu.Unlock()
		hubHTTPError(w, 503, "store_unavailable", "Enrollment request is unavailable")
		return
	}
	entry := &hubEnrollment{id: id, profile: input.Profile, origin: origin, contextName: input.ContextName,
		fingerprint: input.ClusterFingerprint, deviceHash: sha256.Sum256([]byte(secret)),
		userCodeHash: userCodeHash, requesterIP: ip, status: "pending", expiresAt: time.Now().UTC().Add(enrollmentLifetime)}
	p.enrollments[id] = entry
	p.enrollmentMu.Unlock()
	hubJSON(w, http.StatusCreated, map[string]any{"id": id, "deviceSecret": secret, "userCode": userCode[:4] + "-" + userCode[4:],
		"expiresAt": entry.expiresAt, "pollIntervalSeconds": 5})
}

func validEnrollmentText(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && utf8.ValidString(value) && strings.TrimSpace(value) == value &&
		!strings.ContainsFunc(value, unicode.IsControl)
}

func (p *portalHub) manageEnrollments(w http.ResponseWriter, r *http.Request, ownerID string, member hubstate.Member, activeOrganizationID string) {
	if !requireHubAdmin(w, member) {
		return
	}
	switch {
	case r.URL.Path == "/api/hub/enrollments/command":
		p.enrollmentCommand(w, r, member)
	case r.URL.Path == "/api/hub/enrollments/lookup":
		if !p.allowEnrollment("lookup:"+ownerID, 20) || !p.allowEnrollment("lookup-ip:"+enrollmentIP(r), 40) {
			hubHTTPError(w, 429, "rate_limited", "Enrollment lookup limit reached")
			return
		}
		var input struct {
			UserCode string `json:"userCode"`
		}
		if !hubMethod(w, r, http.MethodPost) || !hubDecode(w, r, &input, "userCode") {
			return
		}
		code := enrollmentCode(input.UserCode)
		if code == "" {
			hubHTTPError(w, 404, "not_found", "Enrollment request was not found")
			return
		}
		hash := sha256.Sum256([]byte(code))
		p.enrollmentMu.Lock()
		defer p.enrollmentMu.Unlock()
		for _, entry := range p.enrollments {
			if subtle.ConstantTimeCompare(hash[:], entry.userCodeHash[:]) == 1 && time.Now().Before(entry.expiresAt) {
				hubJSON(w, 200, map[string]any{"id": entry.id, "userCode": code[:4] + "-" + code[4:], "contextName": entry.contextName,
					"clusterFingerprint": entry.fingerprint, "status": entry.status, "expiresAt": entry.expiresAt})
				return
			}
		}
		hubHTTPError(w, 404, "not_found", "Enrollment request was not found")
	default:
		p.enrollmentDecision(w, r, ownerID, member, activeOrganizationID)
	}
}

func (p *portalHub) enrollmentDecision(w http.ResponseWriter, r *http.Request, ownerID string, member hubstate.Member, activeOrganizationID string) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/hub/enrollments/"), "/")
	if len(parts) != 2 || len(parts[0]) != 32 || (parts[1] != "approve" && parts[1] != "deny") {
		hubHTTPError(w, 404, "not_found", "Enrollment route not found")
		return
	}
	if _, err := hex.DecodeString(parts[0]); err != nil {
		hubHTTPError(w, http.StatusNotFound, "not_found", "Enrollment route not found")
		return
	}
	if !p.allowEnrollment("decision:"+ownerID, 20) || !p.allowEnrollment("decision-ip:"+enrollmentIP(r), 40) {
		hubHTTPError(w, http.StatusTooManyRequests, "rate_limited", "Enrollment decision limit reached")
		return
	}
	var input struct {
		UserCode       string `json:"userCode"`
		OrganizationID string `json:"organizationId"`
		Name           string `json:"name"`
		ClusterID      string `json:"clusterId"`
	}
	keys := []string{"userCode"}
	if parts[1] == "approve" {
		keys = append(keys, "organizationId", "name", "clusterId")
	}
	if !hubMethod(w, r, http.MethodPost) || !hubDecode(w, r, &input, keys...) {
		return
	}
	code := enrollmentCode(input.UserCode)
	if code == "" {
		hubHTTPError(w, 404, "not_found", "Enrollment request was not found")
		return
	}
	hash := sha256.Sum256([]byte(code))
	p.enrollmentMu.Lock()
	defer p.enrollmentMu.Unlock()
	entry := p.enrollments[parts[0]]
	if entry == nil || subtle.ConstantTimeCompare(hash[:], entry.userCodeHash[:]) != 1 || !time.Now().Before(entry.expiresAt) || entry.status != "pending" {
		hubHTTPError(w, 409, "enrollment_unavailable", "Enrollment request is unavailable")
		return
	}
	if parts[1] == "deny" {
		entry.status = "denied"
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !p.store.IsPlatformOwner(ownerID) {
		if input.OrganizationID != activeOrganizationID {
			hubHTTPError(w, 403, "permission_denied", "Organization access is unavailable")
			return
		}
		owner, ok := p.store.MemberForOrganization(input.OrganizationID, ownerID)
		if !ok || owner.Status != "active" || owner.Role != "owner" {
			hubHTTPError(w, 403, "permission_denied", "Organization owner access is required")
			return
		}
		if owner.LegacyRestricted && !owner.LegacyAllClusters &&
			(input.ClusterID == "" || !containsScope(owner.ClusterIDs, input.ClusterID)) {
			hubHTTPError(w, http.StatusForbidden, "permission_denied", "Legacy cluster scope does not permit this enrollment")
			return
		}
	} else if _, ok := p.store.OrganizationByID(input.OrganizationID); !ok {
		hubHTTPError(w, 404, "organization_not_found", "Organization is unavailable")
		return
	}
	if !validEnrollmentText(input.Name, 128) {
		hubHTTPError(w, 400, "invalid_parameters", "Cluster name is invalid")
		return
	}
	if input.ClusterID != "" {
		cluster, exists := p.store.ClusterForOrganization(input.OrganizationID, input.ClusterID)
		if !exists || cluster.RevokedAt == nil {
			hubHTTPError(w, http.StatusConflict, "enrollment_unavailable", "Cluster requires an explicit revoked cluster in the selected organization")
			return
		}
	}
	entry.organizationID, entry.approvedBy, entry.name, entry.clusterID = input.OrganizationID, ownerID, input.Name, input.ClusterID
	entry.status = "approved"
	hubJSON(w, 200, map[string]any{"id": entry.id, "status": "approved"})
}

func (p *portalHub) claimEnrollment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.RawQuery != "" || r.URL.ForceQuery || len(r.Header.Values("Authorization")) != 1 || len(r.Header.Values("Cookie")) != 0 {
		hubHTTPError(w, 400, "invalid_parameters", "A bearer device secret is required")
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/hub/enrollments/"), "/")
	if len(parts) != 2 || len(parts[0]) != 32 || parts[1] != "claim" {
		hubHTTPError(w, 404, "not_found", "Enrollment route not found")
		return
	}
	if _, err := hex.DecodeString(parts[0]); err != nil {
		hubHTTPError(w, http.StatusNotFound, "not_found", "Enrollment route not found")
		return
	}
	if !p.allowEnrollment("claim-ip:"+enrollmentIP(r), 30) || !p.allowEnrollment("claim:"+parts[0], 20) {
		hubHTTPError(w, http.StatusTooManyRequests, "rate_limited", "Enrollment claim limit reached")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1))
	if err != nil || len(body) != 0 {
		hubHTTPError(w, 400, "invalid_parameters", "Claim requests must have an empty body")
		return
	}
	scheme, secret, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || !validDeviceSecret(secret) {
		hubHTTPError(w, 401, "authentication_required", "Device secret is invalid")
		return
	}
	candidate := sha256.Sum256([]byte(secret))
	p.enrollmentMu.Lock()
	defer p.enrollmentMu.Unlock()
	entry := p.enrollments[parts[0]]
	if entry == nil || subtle.ConstantTimeCompare(candidate[:], entry.deviceHash[:]) != 1 || !time.Now().Before(entry.expiresAt) {
		hubHTTPError(w, 401, "authentication_required", "Enrollment request is unavailable")
		return
	}
	if entry.status == "pending" {
		hubJSON(w, 200, map[string]any{"status": "pending"})
		return
	}
	if entry.status != "approved" {
		hubHTTPError(w, 409, "enrollment_unavailable", "Enrollment request is unavailable")
		return
	}
	availability, available := p.artifactAvailability()
	origin, originOK := profileOrigin(p.store.Connections(), entry.profile)
	if !available || !originOK || origin != entry.origin {
		hubHTTPError(w, 503, "bootstrap_unavailable", "Enrollment artifacts or origin changed")
		return
	}
	clusterID := entry.clusterID
	var issued hubstate.IssuedAgent
	input := hubstate.ClusterInput{ID: clusterID, Name: entry.name}
	if clusterID == "" {
		clusterID = "enrollment-" + entry.id[:12]
		input.ID = clusterID
		issued, err = p.store.CreateClusterForEnrollment(entry.organizationID, entry.approvedBy, p.ownerID, input)
	} else {
		old, ok := p.store.ClusterForOrganization(entry.organizationID, clusterID)
		if !ok || old.RevokedAt == nil {
			hubHTTPError(w, 409, "enrollment_unavailable", "Cluster requires explicit re-enrollment")
			return
		}
		issued, err = p.store.ReenrollClusterForEnrollment(entry.organizationID, entry.approvedBy, p.ownerID, clusterID, input)
	}
	if err != nil {
		var stateError *hubstate.Error
		if errors.As(err, &stateError) && stateError.Code == "permission_denied" {
			entry.status = "denied"
		}
		hubStoreError(w, err)
		return
	}
	entry.status = "consumed"
	p.transport.Disconnect(clusterID)
	namespace := "kubeatlas-agent-" + entry.id[:10]
	release := "kubeatlas-agent-" + entry.id[:10]
	hubJSON(w, 200, map[string]any{"status": "approved", "clusterId": clusterID, "organizationId": entry.organizationID,
		"ownerUserId": p.userID, "hubOrigin": entry.origin, "version": availability.Version, "imageDigest": availability.ImageDigest,
		"chartSHA256": availability.ChartSHA256, "installerSHA256": availability.InstallerSHA256, "agentToken": issued.Token,
		"namespace": namespace, "release": release})
}
