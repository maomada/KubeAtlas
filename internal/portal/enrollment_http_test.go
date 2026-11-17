package portal

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maomada/KubeAtlas/internal/hubartifacts"
	"github.com/maomada/KubeAtlas/internal/hubstate"
)

const enrollmentTestVersion = "1.2.3"

var enrollmentTestAccountCounter atomic.Uint64

func newEnrollmentHTTPFixture(t *testing.T) (*Handler, *http.Cookie) {
	t.Helper()
	h, _ := newPortalHubFixture(t, false)
	catalog := openEnrollmentTestCatalog(t)
	h.liveHub.catalog = catalog
	if _, err := h.liveHub.store.UpdateConnections(hubstate.ConnectionSettings{LANURL: "https://hub.example.test"}); err != nil {
		t.Fatal(err)
	}
	return h, loginForTest(t, h)
}

func openEnrollmentTestCatalog(t *testing.T) *hubartifacts.Catalog {
	t.Helper()
	root := t.TempDir()
	chart := enrollmentTestChart(t)
	installer := []byte("#!/usr/bin/env bash\nexit 0\n")
	bootstrap := []byte("#!/usr/bin/env bash\nexit 0\n")
	write := func(name string, data []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	chartFile := "kubeatlas-" + enrollmentTestVersion + ".tgz"
	write(chartFile, chart)
	write("install-hub-agent.sh", installer)
	write("bootstrap-hub-agent.sh", bootstrap)

	layout := filepath.Join(root, "oci")
	blobs := filepath.Join(layout, "blobs", "sha256")
	if err := os.MkdirAll(blobs, 0o700); err != nil {
		t.Fatal(err)
	}
	putBlob := func(data []byte) string {
		digest := sha256.Sum256(data)
		encoded := hex.EncodeToString(digest[:])
		if err := os.WriteFile(filepath.Join(blobs, encoded), data, 0o600); err != nil {
			t.Fatal(err)
		}
		return "sha256:" + encoded
	}

	const revision = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	layer := []byte("fixture-agent-layer")
	layerDigest := putBlob(layer)
	config, err := json.Marshal(map[string]any{
		"os": "linux", "architecture": "amd64",
		"config": map[string]any{"Labels": map[string]string{
			"org.opencontainers.image.version":  enrollmentTestVersion,
			"org.opencontainers.image.revision": revision,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	configDigest := putBlob(config)
	const imageMedia = "application/vnd.oci.image.manifest.v1+json"
	const configMedia = "application/vnd.oci.image.config.v1+json"
	const indexMedia = "application/vnd.oci.image.index.v1+json"
	imageManifest, err := json.Marshal(map[string]any{
		"schemaVersion": 2, "mediaType": imageMedia,
		"config": map[string]any{"mediaType": configMedia, "digest": configDigest, "size": len(config)},
		"layers": []any{map[string]any{"mediaType": "application/vnd.oci.image.layer.v1.tar+gzip", "digest": layerDigest, "size": len(layer)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	imageDigest := putBlob(imageManifest)
	imageIndex, err := json.Marshal(map[string]any{
		"schemaVersion": 2, "mediaType": indexMedia,
		"manifests": []any{map[string]any{
			"mediaType": imageMedia, "digest": imageDigest, "size": len(imageManifest),
			"platform": map[string]string{"os": "linux", "architecture": "amd64"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	indexDigest := putBlob(imageIndex)
	if err := os.WriteFile(filepath.Join(layout, "oci-layout"), []byte(`{"imageLayoutVersion":"1.0.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	rootIndex, err := json.Marshal(map[string]any{
		"schemaVersion": 2, "mediaType": indexMedia,
		"manifests": []any{map[string]any{"mediaType": indexMedia, "digest": indexDigest, "size": len(imageIndex)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout, "index.json"), rootIndex, 0o600); err != nil {
		t.Fatal(err)
	}
	hash := func(data []byte) string {
		digest := sha256.Sum256(data)
		return hex.EncodeToString(digest[:])
	}
	manifest := map[string]any{
		"schemaVersion": 1, "version": enrollmentTestVersion, "sourceRevision": revision,
		"chart":     map[string]string{"file": chartFile, "version": enrollmentTestVersion, "sha256": hash(chart)},
		"installer": map[string]string{"file": "install-hub-agent.sh", "sha256": hash(installer)},
		"bootstrap": map[string]string{"file": "bootstrap-hub-agent.sh", "sha256": hash(bootstrap)},
		"image":     map[string]string{"digest": indexDigest},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	write("manifest.json", manifestBytes)
	catalog, err := hubartifacts.Open(root, enrollmentTestVersion)
	if err != nil {
		t.Fatalf("open real fixture artifact catalog: %v", err)
	}
	if availability := catalog.Availability(); !availability.Available || !availability.BootstrapAvailable {
		_ = catalog.Close()
		t.Fatalf("fixture artifact catalog availability = %#v", availability)
	}
	return catalog
}

func enrollmentTestChart(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)
	metadata := []byte("apiVersion: v2\nname: kubeatlas\nversion: " + enrollmentTestVersion + "\nappVersion: " + enrollmentTestVersion + "\n")
	if err := archive.WriteHeader(&tar.Header{Name: "kubeatlas/Chart.yaml", Mode: 0o600, Size: int64(len(metadata)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Write(metadata); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func enrollmentHTTP(h *Handler, cookie *http.Cookie, method, requestPath, body string, authorization, rawCookies []string, remoteAddr string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://localhost"+requestPath, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	for _, value := range authorization {
		r.Header.Add("Authorization", value)
	}
	for _, value := range rawCookies {
		r.Header.Add("Cookie", value)
	}
	if remoteAddr != "" {
		r.RemoteAddr = remoteAddr
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

type enrollmentCreateResponse struct {
	ID                  string    `json:"id"`
	DeviceSecret        string    `json:"deviceSecret"`
	UserCode            string    `json:"userCode"`
	ExpiresAt           time.Time `json:"expiresAt"`
	PollIntervalSeconds int       `json:"pollIntervalSeconds"`
}

type enrollmentClaimResponse struct {
	Status         string `json:"status"`
	ClusterID      string `json:"clusterId"`
	OrganizationID string `json:"organizationId"`
	AgentToken     string `json:"agentToken"`
	Namespace      string `json:"namespace"`
	Release        string `json:"release"`
}

func createEnrollmentHTTP(t *testing.T, h *Handler, remoteAddr string) enrollmentCreateResponse {
	t.Helper()
	w := enrollmentHTTP(h, nil, http.MethodPost, "/api/hub/enrollments",
		"{\"profile\":\"lan\",\"contextName\":\"dev\",\"clusterFingerprint\":\"uid:fixture\"}", nil, nil, remoteAddr)
	if w.Code != http.StatusCreated {
		t.Fatalf("create enrollment: status=%d body=%s", w.Code, w.Body.String())
	}
	var result enrollmentCreateResponse
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !validDeviceSecret(result.DeviceSecret) || result.ID == "" || result.UserCode == "" || result.PollIntervalSeconds <= 0 {
		t.Fatalf("invalid enrollment creation response: %#v", result)
	}
	return result
}

func decodeEnrollmentResponse[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var result T
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode enrollment response %q: %v", w.Body.String(), err)
	}
	return result
}

func assertEnrollmentStatus(t *testing.T, w *httptest.ResponseRecorder, want int) {
	t.Helper()
	if w.Code != want {
		t.Fatalf("enrollment response status=%d, want=%d: %s", w.Code, want, w.Body.String())
	}
}

func assertNoEnrollmentSecrets(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if strings.Contains(w.Body.String(), "deviceSecret") || strings.Contains(w.Body.String(), "agentToken") {
		t.Fatalf("browser enrollment response exposed a CLI secret: %s", w.Body.String())
	}
}

func enrollmentLookup(t *testing.T, h *Handler, cookie *http.Cookie, code string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"userCode": code})
	return enrollmentHTTP(h, cookie, http.MethodPost, "/api/hub/enrollments/lookup", string(body), nil, nil, "")
}

func enrollmentApprove(t *testing.T, h *Handler, cookie *http.Cookie, id, code, organizationID, clusterID string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"userCode": code, "organizationId": organizationID, "name": "Fixture cluster", "clusterId": clusterID})
	return enrollmentHTTP(h, cookie, http.MethodPost, "/api/hub/enrollments/"+id+"/approve", string(body), nil, nil, "")
}

func enrollmentClaim(t *testing.T, h *Handler, id, secret string) *httptest.ResponseRecorder {
	t.Helper()
	return enrollmentHTTP(h, nil, http.MethodPost, "/api/hub/enrollments/"+id+"/claim", "", []string{"Bearer " + secret}, nil, "")
}

func TestEnrollmentHTTPLifecycleAndSecretBoundaries(t *testing.T) {
	h, ownerCookie := newEnrollmentHTTPFixture(t)
	created := createEnrollmentHTTP(t, h, "")
	h.liveHub.enrollmentMu.Lock()
	stored := h.liveHub.enrollments[created.ID]
	if stored == nil || stored.deviceHash != sha256.Sum256([]byte(created.DeviceSecret)) {
		h.liveHub.enrollmentMu.Unlock()
		t.Fatal("Portal did not retain only the device-secret hash")
	}
	h.liveHub.enrollmentMu.Unlock()

	lookup := enrollmentLookup(t, h, ownerCookie, created.UserCode)
	assertEnrollmentStatus(t, lookup, http.StatusOK)
	assertNoEnrollmentSecrets(t, lookup)
	var pendingLookup struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Code   string `json:"userCode"`
	}
	pendingLookup = decodeEnrollmentResponse[struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Code   string `json:"userCode"`
	}](t, lookup)
	if pendingLookup.ID != created.ID || pendingLookup.Status != "pending" || pendingLookup.Code != created.UserCode {
		t.Fatalf("lookup response = %#v", pendingLookup)
	}

	pendingClaim := enrollmentClaim(t, h, created.ID, created.DeviceSecret)
	assertEnrollmentStatus(t, pendingClaim, http.StatusOK)
	if got := decodeEnrollmentResponse[enrollmentClaimResponse](t, pendingClaim); got.Status != "pending" || got.AgentToken != "" {
		t.Fatalf("pending claim response = %#v", got)
	}

	wrongCode := strings.Replace(created.UserCode, "-", "", 1)
	if wrongCode[0] == 'A' {
		wrongCode = "B" + wrongCode[1:]
	} else {
		wrongCode = "A" + wrongCode[1:]
	}
	wrongLookup := enrollmentLookup(t, h, ownerCookie, wrongCode)
	assertEnrollmentStatus(t, wrongLookup, http.StatusNotFound)
	assertNoEnrollmentSecrets(t, wrongLookup)
	wrongApprove := enrollmentApprove(t, h, ownerCookie, created.ID, wrongCode, "self-hosted", "")
	assertEnrollmentStatus(t, wrongApprove, http.StatusConflict)
	assertNoEnrollmentSecrets(t, wrongApprove)

	approved := enrollmentApprove(t, h, ownerCookie, created.ID, created.UserCode, "self-hosted", "")
	assertEnrollmentStatus(t, approved, http.StatusOK)
	assertNoEnrollmentSecrets(t, approved)
	claim := enrollmentClaim(t, h, created.ID, created.DeviceSecret)
	assertEnrollmentStatus(t, claim, http.StatusOK)
	result := decodeEnrollmentResponse[enrollmentClaimResponse](t, claim)
	if result.Status != "approved" || result.ClusterID != "enrollment-"+created.ID[:12] ||
		result.OrganizationID != "self-hosted" || result.AgentToken == "" || result.Namespace == "" || result.Release == "" {
		t.Fatalf("approved claim response = %#v", result)
	}
	if _, err := h.liveHub.store.AuthenticateAgent(result.AgentToken, result.ClusterID); err != nil {
		t.Fatalf("issued HTTP claim token was not accepted: %v", err)
	}
	replay := enrollmentClaim(t, h, created.ID, created.DeviceSecret)
	assertEnrollmentStatus(t, replay, http.StatusConflict)
	assertNoEnrollmentSecrets(t, replay)
}

func TestEnrollmentHTTPRejectsCredentialsUnknownFieldsAndQueries(t *testing.T) {
	h, ownerCookie := newEnrollmentHTTPFixture(t)
	body := "{\"profile\":\"lan\",\"contextName\":\"dev\",\"clusterFingerprint\":\"uid:fixture\"}"
	for _, test := range []struct {
		name   string
		path   string
		body   string
		auth   []string
		cookie []string
		want   int
	}{
		{name: "cookie on anonymous create", path: "/api/hub/enrollments", body: body, cookie: []string{"session=forged"}, want: http.StatusBadRequest},
		{name: "authorization on anonymous create", path: "/api/hub/enrollments", body: body, auth: []string{"Bearer forged"}, want: http.StatusBadRequest},
		{name: "unknown create field", path: "/api/hub/enrollments", body: "{\"profile\":\"lan\",\"contextName\":\"dev\",\"clusterFingerprint\":\"uid:fixture\",\"extra\":true}", want: http.StatusBadRequest},
		{name: "create query", path: "/api/hub/enrollments?extra=1", body: body, want: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := enrollmentHTTP(h, nil, http.MethodPost, test.path, test.body, test.auth, test.cookie, "")
			assertEnrollmentStatus(t, w, test.want)
		})
	}
	created := createEnrollmentHTTP(t, h, "")
	unknownLookup := enrollmentHTTP(h, ownerCookie, http.MethodPost, "/api/hub/enrollments/lookup", "{\"userCode\":\""+created.UserCode+"\",\"extra\":true}", nil, nil, "")
	assertEnrollmentStatus(t, unknownLookup, http.StatusBadRequest)
	queryLookup := enrollmentLookup(t, h, ownerCookie, created.UserCode)
	if queryLookup.Code != http.StatusOK {
		t.Fatalf("valid lookup before query check: %d %s", queryLookup.Code, queryLookup.Body.String())
	}
	queryLookup = enrollmentHTTP(h, ownerCookie, http.MethodPost, "/api/hub/enrollments/lookup?extra=1", "{\"userCode\":\""+created.UserCode+"\"}", nil, nil, "")
	assertEnrollmentStatus(t, queryLookup, http.StatusBadRequest)
	unknownApprove := enrollmentHTTP(h, ownerCookie, http.MethodPost, "/api/hub/enrollments/"+created.ID+"/approve",
		"{\"userCode\":\""+created.UserCode+"\",\"organizationId\":\"self-hosted\",\"name\":\"Fixture\",\"extra\":true}", nil, nil, "")
	assertEnrollmentStatus(t, unknownApprove, http.StatusBadRequest)
	unknownDeny := enrollmentHTTP(h, ownerCookie, http.MethodPost, "/api/hub/enrollments/"+created.ID+"/deny",
		"{\"userCode\":\""+created.UserCode+"\",\"extra\":true}", nil, nil, "")
	assertEnrollmentStatus(t, unknownDeny, http.StatusBadRequest)
	queryApprove := enrollmentHTTP(h, ownerCookie, http.MethodPost, "/api/hub/enrollments/"+created.ID+"/approve?extra=1",
		"{\"userCode\":\""+created.UserCode+"\",\"organizationId\":\"self-hosted\",\"name\":\"Fixture\"}", nil, nil, "")
	assertEnrollmentStatus(t, queryApprove, http.StatusBadRequest)
	queryClaim := enrollmentHTTP(h, nil, http.MethodPost, "/api/hub/enrollments/"+created.ID+"/claim?extra=1", "",
		[]string{"Bearer " + created.DeviceSecret}, nil, "")
	assertEnrollmentStatus(t, queryClaim, http.StatusBadRequest)
	_ = ownerCookie
}

func TestEnrollmentHTTPRejectsWrongSecretCookieAndDuplicateAuthorization(t *testing.T) {
	h, _ := newEnrollmentHTTPFixture(t)
	created := createEnrollmentHTTP(t, h, "")
	wrongSecret := enrollmentClaim(t, h, created.ID, "ka_device_"+strings.Repeat("0", 64))
	assertEnrollmentStatus(t, wrongSecret, http.StatusUnauthorized)
	malformedSecret := enrollmentHTTP(h, nil, http.MethodPost, "/api/hub/enrollments/"+created.ID+"/claim", "",
		[]string{"Bearer ka_device_short"}, nil, "")
	assertEnrollmentStatus(t, malformedSecret, http.StatusUnauthorized)
	cookieClaim := enrollmentHTTP(h, nil, http.MethodPost, "/api/hub/enrollments/"+created.ID+"/claim", "",
		[]string{"Bearer " + created.DeviceSecret}, []string{"session=browser"}, "")
	assertEnrollmentStatus(t, cookieClaim, http.StatusBadRequest)
	duplicateAuthorization := enrollmentHTTP(h, nil, http.MethodPost, "/api/hub/enrollments/"+created.ID+"/claim", "",
		[]string{"Bearer " + created.DeviceSecret, "Bearer " + created.DeviceSecret}, nil, "")
	assertEnrollmentStatus(t, duplicateAuthorization, http.StatusBadRequest)
	unknownBody := enrollmentHTTP(h, nil, http.MethodPost, "/api/hub/enrollments/"+created.ID+"/claim", "{\"extra\":true}",
		[]string{"Bearer " + created.DeviceSecret}, nil, "")
	assertEnrollmentStatus(t, unknownBody, http.StatusBadRequest)
}

func addEnrollmentAccount(t *testing.T, h *Handler, role, status string) (string, *http.Cookie) {
	t.Helper()
	suffix := strconv.FormatUint(enrollmentTestAccountCounter.Add(1), 16)
	id := "oidc:" + strings.Repeat("b", 64-len(suffix)) + suffix
	if _, _, err := h.liveHub.store.UpsertOIDCMember(id, "Enrollment user"); err != nil {
		t.Fatal(err)
	}
	if status == "" {
		status = "active"
	}
	if status != "pending" {
		memberships := []hubstate.OrganizationMembership{}
		if role != "" {
			memberships = append(memberships, hubstate.OrganizationMembership{OrganizationID: "self-hosted", Role: role})
		}
		if _, err := h.liveHub.store.UpdatePlatformUser(id, status, memberships); err != nil {
			t.Fatal(err)
		}
	}
	return id, hubTestMemberSession(t, h, id)
}

func TestEnrollmentHTTPRequiresOwnerSession(t *testing.T) {
	h, ownerCookie := newEnrollmentHTTPFixture(t)
	created := createEnrollmentHTTP(t, h, "")
	lookupBody := "{\"userCode\":\"" + created.UserCode + "\"}"
	approveBody := "{\"userCode\":\"" + created.UserCode + "\",\"organizationId\":\"self-hosted\",\"name\":\"Fixture\"}"
	if got := enrollmentHTTP(h, nil, http.MethodPost, "/api/hub/enrollments/lookup", lookupBody, nil, nil, ""); got.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous lookup status=%d, body=%s", got.Code, got.Body.String())
	}
	if got := enrollmentHTTP(h, nil, http.MethodPost, "/api/hub/enrollments/"+created.ID+"/approve", approveBody, nil, nil, ""); got.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous approval status=%d, body=%s", got.Code, got.Body.String())
	}

	for _, test := range []struct {
		name   string
		role   string
		status string
		code   string
	}{
		{name: "member", role: "member", status: "active", code: "permission_denied"},
		{name: "viewer", role: "viewer", status: "active", code: "permission_denied"},
		{name: "pending", status: "pending", code: "member_pending"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, cookie := addEnrollmentAccount(t, h, test.role, test.status)
			for _, request := range []struct {
				path string
				body string
			}{
				{path: "/api/hub/enrollments/lookup", body: lookupBody},
				{path: "/api/hub/enrollments/" + created.ID + "/approve", body: approveBody},
			} {
				w := enrollmentHTTP(h, cookie, http.MethodPost, request.path, request.body, nil, nil, "")
				if w.Code != http.StatusForbidden || hubErrorCode(t, w) != test.code {
					t.Fatalf("%s: status=%d code=%s body=%s", request.path, w.Code, hubErrorCode(t, w), w.Body.String())
				}
			}
		})
	}
	if ownerCookie == nil {
		t.Fatal("fixture omitted recovery session")
	}
}

func TestEnrollmentHTTPDenyExpiryAndConcurrentClaim(t *testing.T) {
	t.Run("denied", func(t *testing.T) {
		h, ownerCookie := newEnrollmentHTTPFixture(t)
		created := createEnrollmentHTTP(t, h, "")
		body, _ := json.Marshal(map[string]string{"userCode": created.UserCode})
		denied := enrollmentHTTP(h, ownerCookie, http.MethodPost, "/api/hub/enrollments/"+created.ID+"/deny", string(body), nil, nil, "")
		assertEnrollmentStatus(t, denied, http.StatusNoContent)
		claim := enrollmentClaim(t, h, created.ID, created.DeviceSecret)
		assertEnrollmentStatus(t, claim, http.StatusConflict)
	})

	t.Run("expired", func(t *testing.T) {
		h, ownerCookie := newEnrollmentHTTPFixture(t)
		created := createEnrollmentHTTP(t, h, "")
		if approved := enrollmentApprove(t, h, ownerCookie, created.ID, created.UserCode, "self-hosted", ""); approved.Code != http.StatusOK {
			t.Fatalf("approve before expiry: %d %s", approved.Code, approved.Body.String())
		}
		h.liveHub.enrollmentMu.Lock()
		h.liveHub.enrollments[created.ID].expiresAt = time.Now().Add(-time.Second)
		h.liveHub.enrollmentMu.Unlock()
		claim := enrollmentClaim(t, h, created.ID, created.DeviceSecret)
		assertEnrollmentStatus(t, claim, http.StatusUnauthorized)
		if _, exists := h.liveHub.store.ClusterForOrganization("self-hosted", "enrollment-"+created.ID[:12]); exists {
			t.Fatal("expired claim issued a cluster")
		}
	})

	t.Run("concurrent single consumption", func(t *testing.T) {
		h, ownerCookie := newEnrollmentHTTPFixture(t)
		created := createEnrollmentHTTP(t, h, "")
		if approved := enrollmentApprove(t, h, ownerCookie, created.ID, created.UserCode, "self-hosted", ""); approved.Code != http.StatusOK {
			t.Fatalf("approve before concurrent claim: %d %s", approved.Code, approved.Body.String())
		}
		const workers = 12
		start := make(chan struct{})
		results := make(chan *httptest.ResponseRecorder, workers)
		var wait sync.WaitGroup
		for i := 0; i < workers; i++ {
			wait.Add(1)
			go func() {
				defer wait.Done()
				<-start
				results <- enrollmentClaim(t, h, created.ID, created.DeviceSecret)
			}()
		}
		close(start)
		wait.Wait()
		close(results)
		successes, unavailable := 0, 0
		for result := range results {
			switch result.Code {
			case http.StatusOK:
				successes++
				claim := decodeEnrollmentResponse[enrollmentClaimResponse](t, result)
				if claim.Status != "approved" || claim.AgentToken == "" {
					t.Fatalf("successful concurrent claim = %#v", claim)
				}
			case http.StatusConflict:
				if hubErrorCode(t, result) != "enrollment_unavailable" {
					t.Fatalf("losing claim error = %s", result.Body.String())
				}
				unavailable++
			default:
				t.Fatalf("concurrent claim status=%d body=%s", result.Code, result.Body.String())
			}
		}
		if successes != 1 || unavailable != workers-1 {
			t.Fatalf("concurrent claims: successes=%d unavailable=%d", successes, unavailable)
		}
	})
}

func TestEnrollmentHTTPRevalidatesApproverBeforeIssuance(t *testing.T) {
	for _, test := range []struct {
		name        string
		status      string
		memberships []hubstate.OrganizationMembership
		clusterID   string
	}{
		{name: "demoted", status: "active", memberships: []hubstate.OrganizationMembership{{OrganizationID: "self-hosted", Role: "member"}}, clusterID: "re-enroll"},
		{name: "removed", status: "active"},
		{name: "disabled", status: "disabled"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h, _ := newEnrollmentHTTPFixture(t)
			ownerID, ownerCookie := addEnrollmentAccount(t, h, "owner", "active")
			addEnrollmentAccount(t, h, "owner", "active")
			if test.clusterID != "" {
				if _, err := h.liveHub.store.CreateClusterForOrganization("self-hosted", hubstate.ClusterInput{ID: test.clusterID, Name: test.clusterID}); err != nil {
					t.Fatal(err)
				}
				if err := h.liveHub.store.RevokeAgentTokenForOrganization("self-hosted", test.clusterID); err != nil {
					t.Fatal(err)
				}
			}
			created := createEnrollmentHTTP(t, h, "")
			approved := enrollmentApprove(t, h, ownerCookie, created.ID, created.UserCode, "self-hosted", test.clusterID)
			assertEnrollmentStatus(t, approved, http.StatusOK)
			if _, err := h.liveHub.store.UpdatePlatformUser(ownerID, test.status, test.memberships); err != nil {
				t.Fatalf("change approver after approval: %v", err)
			}
			claim := enrollmentClaim(t, h, created.ID, created.DeviceSecret)
			if claim.Code != http.StatusForbidden || hubErrorCode(t, claim) != "permission_denied" {
				t.Fatalf("claim after approver %s: status=%d body=%s", test.name, claim.Code, claim.Body.String())
			}
			assertNoEnrollmentSecrets(t, claim)
			if _, exists := h.liveHub.store.ClusterForOrganization("self-hosted", "enrollment-"+created.ID[:12]); exists {
				t.Fatalf("claim after approver %s issued a cluster", test.name)
			}
			if test.clusterID != "" {
				cluster, exists := h.liveHub.store.ClusterForOrganization("self-hosted", test.clusterID)
				if !exists || cluster.RevokedAt == nil {
					t.Fatalf("claim after approver %s changed revoked cluster: %#v, exists=%v", test.name, cluster, exists)
				}
			}
		})
	}
}

func TestEnrollmentHTTPTransferRequiresFreshReenrollment(t *testing.T) {
	h, localCookie := newEnrollmentHTTPFixture(t)
	created := createEnrollmentHTTP(t, h, "")
	if approved := enrollmentApprove(t, h, localCookie, created.ID, created.UserCode, "self-hosted", ""); approved.Code != http.StatusOK {
		t.Fatalf("approve initial enrollment: %d %s", approved.Code, approved.Body.String())
	}
	firstClaim := enrollmentClaim(t, h, created.ID, created.DeviceSecret)
	assertEnrollmentStatus(t, firstClaim, http.StatusOK)
	first := decodeEnrollmentResponse[enrollmentClaimResponse](t, firstClaim)
	if _, err := h.liveHub.store.AuthenticateAgent(first.AgentToken, first.ClusterID); err != nil {
		t.Fatalf("initial agent token did not authenticate: %v", err)
	}

	organizationResponse := enrollmentHTTP(h, localCookie, http.MethodPost, "/api/hub/organizations",
		"{\"name\":\"Transferred\",\"ownerMemberId\":\"local\"}", nil, nil, "")
	assertEnrollmentStatus(t, organizationResponse, http.StatusCreated)
	organization := decodeEnrollmentResponse[struct {
		ID string `json:"id"`
	}](t, organizationResponse)
	transfer := enrollmentHTTP(h, localCookie, http.MethodPost, "/api/hub/clusters/"+first.ClusterID+"/transfer",
		"{\"organizationId\":\""+organization.ID+"\"}", nil, nil, "")
	assertEnrollmentStatus(t, transfer, http.StatusOK)
	selectOrg := enrollmentHTTP(h, localCookie, http.MethodPost, "/api/hub/organization/select",
		"{\"organizationId\":\""+organization.ID+"\"}", nil, nil, "")
	assertEnrollmentStatus(t, selectOrg, http.StatusNoContent)

	rotate := enrollmentHTTP(h, localCookie, http.MethodPost, "/api/hub/clusters/"+first.ClusterID+"/rotate", "{}", nil, nil, "")
	assertEnrollmentStatus(t, rotate, http.StatusConflict)
	if _, err := h.liveHub.store.AuthenticateAgent(first.AgentToken, first.ClusterID); err == nil {
		t.Fatal("transferred cluster retained its old agent token")
	}
	if cluster, ok := h.liveHub.store.ClusterForOrganization(organization.ID, first.ClusterID); !ok || cluster.RevokedAt == nil {
		t.Fatalf("failed rotation cleared transferred revocation: %#v, %v", cluster, ok)
	}

	wrongOrg := createEnrollmentHTTP(t, h, "")
	wrongApproval := enrollmentApprove(t, h, localCookie, wrongOrg.ID, wrongOrg.UserCode, "self-hosted", first.ClusterID)
	assertEnrollmentStatus(t, wrongApproval, http.StatusConflict)
	wrongPending := enrollmentClaim(t, h, wrongOrg.ID, wrongOrg.DeviceSecret)
	assertEnrollmentStatus(t, wrongPending, http.StatusOK)
	if got := decodeEnrollmentResponse[enrollmentClaimResponse](t, wrongPending); got.Status != "pending" {
		t.Fatalf("wrong-organization enrollment was not left pending: %#v", got)
	}

	reenrollment := createEnrollmentHTTP(t, h, "")
	approved := enrollmentApprove(t, h, localCookie, reenrollment.ID, reenrollment.UserCode, organization.ID, first.ClusterID)
	assertEnrollmentStatus(t, approved, http.StatusOK)
	assertNoEnrollmentSecrets(t, approved)
	reenrolled := enrollmentClaim(t, h, reenrollment.ID, reenrollment.DeviceSecret)
	assertEnrollmentStatus(t, reenrolled, http.StatusOK)
	second := decodeEnrollmentResponse[enrollmentClaimResponse](t, reenrolled)
	if second.Status != "approved" || second.ClusterID != first.ClusterID || second.OrganizationID != organization.ID || second.AgentToken == "" || second.AgentToken == first.AgentToken {
		t.Fatalf("reenrollment response = %#v", second)
	}
	if _, err := h.liveHub.store.AuthenticateAgent(first.AgentToken, first.ClusterID); err == nil {
		t.Fatal("reenrollment restored the old agent token")
	}
	if _, err := h.liveHub.store.AuthenticateAgent(second.AgentToken, second.ClusterID); err != nil {
		t.Fatalf("new reenrollment token was not accepted: %v", err)
	}

	activeClusterRequest := createEnrollmentHTTP(t, h, "")
	activeReenrollment := enrollmentApprove(t, h, localCookie, activeClusterRequest.ID, activeClusterRequest.UserCode, organization.ID, first.ClusterID)
	assertEnrollmentStatus(t, activeReenrollment, http.StatusConflict)
}

func TestEnrollmentHTTPLimits(t *testing.T) {
	t.Run("per-IP request and outstanding caps", func(t *testing.T) {
		h, ownerCookie := newEnrollmentHTTPFixture(t)
		const remote = "198.51.100.45:4321"
		created := make([]enrollmentCreateResponse, 0, 3)
		for i := 0; i < 3; i++ {
			created = append(created, createEnrollmentHTTP(t, h, remote))
		}
		fourth := enrollmentHTTP(h, nil, http.MethodPost, "/api/hub/enrollments",
			"{\"profile\":\"lan\",\"contextName\":\"dev\",\"clusterFingerprint\":\"uid:fourth\"}", nil, nil, remote)
		assertEnrollmentStatus(t, fourth, http.StatusTooManyRequests)
		for _, entry := range created {
			body, _ := json.Marshal(map[string]string{"userCode": entry.UserCode})
			denied := enrollmentHTTP(h, ownerCookie, http.MethodPost, "/api/hub/enrollments/"+entry.ID+"/deny", string(body), nil, nil, "")
			assertEnrollmentStatus(t, denied, http.StatusNoContent)
		}
		fifth := createEnrollmentHTTP(t, h, remote)
		body, _ := json.Marshal(map[string]string{"userCode": fifth.UserCode})
		denied := enrollmentHTTP(h, ownerCookie, http.MethodPost, "/api/hub/enrollments/"+fifth.ID+"/deny", string(body), nil, nil, "")
		assertEnrollmentStatus(t, denied, http.StatusNoContent)
		sixth := enrollmentHTTP(h, nil, http.MethodPost, "/api/hub/enrollments",
			"{\"profile\":\"lan\",\"contextName\":\"dev\",\"clusterFingerprint\":\"uid:sixth\"}", nil, nil, remote)
		assertEnrollmentStatus(t, sixth, http.StatusTooManyRequests)
		if sixth.Header().Get("Retry-After") == "" {
			t.Fatal("rate-limited create omitted Retry-After")
		}
	})

	t.Run("lookup and decision limits", func(t *testing.T) {
		h, ownerCookie := newEnrollmentHTTPFixture(t)
		created := createEnrollmentHTTP(t, h, "")
		for i := 0; i < 20; i++ {
			lookup := enrollmentLookup(t, h, ownerCookie, created.UserCode)
			assertEnrollmentStatus(t, lookup, http.StatusOK)
		}
		lookup := enrollmentLookup(t, h, ownerCookie, created.UserCode)
		assertEnrollmentStatus(t, lookup, http.StatusTooManyRequests)

		wrongCode := strings.Replace(created.UserCode, "-", "", 1)
		if wrongCode[0] == 'A' {
			wrongCode = "B" + wrongCode[1:]
		} else {
			wrongCode = "A" + wrongCode[1:]
		}
		approveBody, _ := json.Marshal(map[string]string{"userCode": wrongCode, "organizationId": "self-hosted", "name": "Fixture"})
		approvePath := "/api/hub/enrollments/" + created.ID + "/approve"
		for i := 0; i < 20; i++ {
			decision := enrollmentHTTP(h, ownerCookie, http.MethodPost, approvePath, string(approveBody), nil, nil, "")
			assertEnrollmentStatus(t, decision, http.StatusConflict)
		}
		decision := enrollmentHTTP(h, ownerCookie, http.MethodPost, approvePath, string(approveBody), nil, nil, "")
		assertEnrollmentStatus(t, decision, http.StatusTooManyRequests)
	})

	t.Run("claim IP limit", func(t *testing.T) {
		h, _ := newEnrollmentHTTPFixture(t)
		entries := []enrollmentCreateResponse{
			createEnrollmentHTTP(t, h, ""),
			createEnrollmentHTTP(t, h, ""),
			createEnrollmentHTTP(t, h, ""),
		}
		for _, entry := range entries {
			for i := 0; i < 10; i++ {
				claim := enrollmentClaim(t, h, entry.ID, entry.DeviceSecret)
				assertEnrollmentStatus(t, claim, http.StatusOK)
			}
		}
		limited := enrollmentClaim(t, h, entries[0].ID, entries[0].DeviceSecret)
		assertEnrollmentStatus(t, limited, http.StatusTooManyRequests)
	})

	t.Run("per-request claim limit", func(t *testing.T) {
		h, _ := newEnrollmentHTTPFixture(t)
		entry := createEnrollmentHTTP(t, h, "")
		for i := 0; i < 20; i++ {
			claim := enrollmentClaim(t, h, entry.ID, entry.DeviceSecret)
			assertEnrollmentStatus(t, claim, http.StatusOK)
		}
		limited := enrollmentClaim(t, h, entry.ID, entry.DeviceSecret)
		assertEnrollmentStatus(t, limited, http.StatusTooManyRequests)
	})
}

func TestEnrollmentHTTPOrganizationOwnerCannotApproveAnotherOrganization(t *testing.T) {
	h, localCookie := newEnrollmentHTTPFixture(t)
	organizationResponse := enrollmentHTTP(h, localCookie, http.MethodPost, "/api/hub/organizations",
		"{\"name\":\"Other organization\",\"ownerMemberId\":\"local\"}", nil, nil, "")
	assertEnrollmentStatus(t, organizationResponse, http.StatusCreated)
	organization := decodeEnrollmentResponse[struct {
		ID string `json:"id"`
	}](t, organizationResponse)
	_, ownerCookie := addEnrollmentAccount(t, h, "owner", "active")
	created := createEnrollmentHTTP(t, h, "")

	wrongOrganization := enrollmentApprove(t, h, ownerCookie, created.ID, created.UserCode, organization.ID, "")
	assertEnrollmentStatus(t, wrongOrganization, http.StatusForbidden)
	approved := enrollmentApprove(t, h, ownerCookie, created.ID, created.UserCode, "self-hosted", "")
	assertEnrollmentStatus(t, approved, http.StatusOK)
	claim := enrollmentClaim(t, h, created.ID, created.DeviceSecret)
	assertEnrollmentStatus(t, claim, http.StatusOK)
	result := decodeEnrollmentResponse[enrollmentClaimResponse](t, claim)
	if result.OrganizationID != "self-hosted" {
		t.Fatalf("owner claim escaped its organization: %#v", result)
	}
}

func TestEnrollmentHTTPLegacyRestrictedOwnerCannotBroadenScope(t *testing.T) {
	h, _ := newEnrollmentHTTPFixture(t)
	for _, id := range []string{"in-scope", "out-of-scope"} {
		if _, err := h.liveHub.store.CreateClusterForOrganization("self-hosted", hubstate.ClusterInput{ID: id, Name: id}); err != nil {
			t.Fatal(err)
		}
		if err := h.liveHub.store.RevokeAgentTokenForOrganization("self-hosted", id); err != nil {
			t.Fatal(err)
		}
	}
	ownerID, ownerCookie := addEnrollmentAccount(t, h, "owner", "active")
	if _, _, err := h.liveHub.store.UpdateMember(ownerID, hubstate.MemberInput{
		Role: "viewer", Status: "active", ClusterIDs: []string{"in-scope"}, KubernetesGroups: []string{},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.liveHub.store.UpdatePlatformUser(ownerID, "active", []hubstate.OrganizationMembership{{OrganizationID: "self-hosted", Role: "owner"}}); err != nil {
		t.Fatal(err)
	}
	member, ok := h.liveHub.store.MemberForOrganization("self-hosted", ownerID)
	if !ok || !member.LegacyRestricted || member.LegacyAllClusters || len(member.ClusterIDs) != 1 || member.ClusterIDs[0] != "in-scope" {
		t.Fatalf("legacy owner scope = %#v, ok=%v", member, ok)
	}

	newCluster := createEnrollmentHTTP(t, h, "")
	newClusterApproval := enrollmentApprove(t, h, ownerCookie, newCluster.ID, newCluster.UserCode, "self-hosted", "")
	assertEnrollmentStatus(t, newClusterApproval, http.StatusForbidden)
	if _, exists := h.liveHub.store.ClusterForOrganization("self-hosted", "enrollment-"+newCluster.ID[:12]); exists {
		t.Fatal("legacy restricted owner created a cluster outside its scope")
	}

	outside := createEnrollmentHTTP(t, h, "")
	outsideApproval := enrollmentApprove(t, h, ownerCookie, outside.ID, outside.UserCode, "self-hosted", "out-of-scope")
	assertEnrollmentStatus(t, outsideApproval, http.StatusForbidden)
	inside := createEnrollmentHTTP(t, h, "")
	insideApproval := enrollmentApprove(t, h, ownerCookie, inside.ID, inside.UserCode, "self-hosted", "in-scope")
	assertEnrollmentStatus(t, insideApproval, http.StatusOK)
	insideClaim := enrollmentClaim(t, h, inside.ID, inside.DeviceSecret)
	assertEnrollmentStatus(t, insideClaim, http.StatusOK)
	result := decodeEnrollmentResponse[enrollmentClaimResponse](t, insideClaim)
	if result.Status != "approved" || result.ClusterID != "in-scope" || result.OrganizationID != "self-hosted" || result.AgentToken == "" {
		t.Fatalf("in-scope legacy re-enrollment = %#v", result)
	}
}
