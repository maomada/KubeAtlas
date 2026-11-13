package portal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/maomada/KubeAtlas/internal/hub"
	"github.com/maomada/KubeAtlas/internal/hubstate"
)

type blockedMCPCall struct {
	principal hub.Principal
	ctx       context.Context
	release   chan struct{}
}

func installBlockingMCP(h *Handler) chan *blockedMCPCall {
	calls := make(chan *blockedMCPCall, 8)
	h.liveHub.mcp = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := hub.PrincipalFromContext(r.Context())
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		call := &blockedMCPCall{principal: principal, ctx: r.Context(), release: make(chan struct{})}
		calls <- call
		select {
		case <-r.Context().Done():
			w.WriteHeader(http.StatusRequestTimeout)
		case <-call.release:
			w.WriteHeader(http.StatusOK)
		}
	})
	return calls
}

func startBlockedMCP(t *testing.T, h *Handler, calls chan *blockedMCPCall, cookie *http.Cookie, token, organizationID string) (*blockedMCPCall, <-chan int) {
	t.Helper()
	done := make(chan int, 1)
	go func() {
		r := httptest.NewRequest(http.MethodPost, "http://localhost/mcp", strings.NewReader("{}"))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-KubeAtlas-Organization-ID", organizationID)
		r.Header.Set("X-Kubernetes-Groups", "radar:spoofed")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		done <- w.Code
	}()
	select {
	case call := <-calls:
		return call, done
	case <-time.After(time.Second):
		t.Fatal("MCP request did not reach the in-process handler")
		return nil, done
	}
}

func organizationTestRequest(h *Handler, cookie *http.Cookie, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-KubeAtlas-Organization-ID", "attacker-org")
	r.Header.Set("X-Organization-ID", "attacker-org")
	r.Header.Set("X-Kubernetes-Groups", "radar:spoofed")
	r.Header.Set("X-Forwarded-Groups", "radar:spoofed")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func createOrganizationForTest(t *testing.T, h *Handler, cookie *http.Cookie, name, ownerID string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"name": name, "ownerMemberId": ownerID})
	response := hubRequest(t, h, cookie, "", http.MethodPost, "/api/hub/organizations", string(body), http.StatusCreated)
	return decodeHubResponse[map[string]any](t, response)["id"].(string)
}

func selectOrganizationForTest(t *testing.T, h *Handler, cookie *http.Cookie, organizationID string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"organizationId": organizationID})
	hubRequest(t, h, cookie, "", http.MethodPost, "/api/hub/organization/select", string(body), http.StatusNoContent)
}

func addOrganizationAccount(t *testing.T, h *Handler, id string, memberships ...hubstate.OrganizationMembership) {
	t.Helper()
	if _, _, err := h.liveHub.store.UpsertOIDCMember(id, "Organization user"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.liveHub.store.UpdatePlatformUser(id, "active", memberships); err != nil {
		t.Fatal(err)
	}
}

func TestOrganizationIsolationSwitchPinnedMCPAndTransferCancellation(t *testing.T) {
	h, _ := newPortalHubFixture(t, false)
	calls := installBlockingMCP(h)
	localCookie := loginForTest(t, h)
	orgA := createOrganizationForTest(t, h, localCookie, "A", "local")
	orgB := createOrganizationForTest(t, h, localCookie, "B", "local")
	clusterA, err := h.liveHub.store.CreateClusterForOrganization(orgA, hubstate.ClusterInput{ID: "org-a", Name: "A cluster"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.liveHub.store.CreateClusterForOrganization(orgB, hubstate.ClusterInput{ID: "org-b", Name: "B cluster"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.liveHub.store.SaveGroupForOrganization(orgA, hubstate.GroupInput{ID: "group-a", Name: "A group", ClusterIDs: []string{"org-a"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.liveHub.store.SaveGroupForOrganization(orgB, hubstate.GroupInput{ID: "group-b", Name: "B group", ClusterIDs: []string{"org-b"}}); err != nil {
		t.Fatal(err)
	}
	sharedID := "oidc:" + strings.Repeat("a", 64)
	addOrganizationAccount(t, h, sharedID,
		hubstate.OrganizationMembership{OrganizationID: orgA, Role: "owner"},
		hubstate.OrganizationMembership{OrganizationID: orgB, Role: "viewer"})
	aOnlyID := "oidc:" + strings.Repeat("b", 64)
	addOrganizationAccount(t, h, aOnlyID, hubstate.OrganizationMembership{OrganizationID: orgA, Role: "member"})
	sharedCookie := hubTestMemberSession(t, h, sharedID)
	selectOrganizationForTest(t, h, sharedCookie, orgA)
	snapshotRequest := httptest.NewRequest(http.MethodGet, "http://localhost/api/hub", nil)
	snapshotRequest.AddCookie(sharedCookie)
	bound, releaseSnapshot := h.bindViewerSession(snapshotRequest)
	defer releaseSnapshot()
	snapshot, ok := viewerSnapshot(bound)
	if !ok || snapshot.organizationID != orgA {
		t.Fatal("request did not capture the A organization snapshot")
	}
	browserContext, cancelBrowser, ok := h.viewerSessionContext(bound)
	if !ok {
		t.Fatal("A browser request did not receive a session context")
	}
	defer cancelBrowser()

	credentialBody, _ := json.Marshal(map[string]any{"label": "A", "clusterIds": []string{"org-a"}, "readOnly": false})
	aCredential := decodeHubResponse[hubstate.IssuedCredential](t,
		hubRequest(t, h, sharedCookie, "", http.MethodPost, "/api/mcp/credentials", string(credentialBody), http.StatusCreated))
	aCall, aDone := startBlockedMCP(t, h, calls, sharedCookie, aCredential.Token, orgB)
	if aCall.principal.OrganizationID != orgA || aCall.principal.AccountID != sharedID || aCall.principal.ReadOnly {
		t.Fatalf("A credential principal = %#v", aCall.principal)
	}
	selectOrganizationForTest(t, h, sharedCookie, orgB)
	if snapshot.organizationID != orgA || snapshot.baseCtx.Err() == nil {
		t.Fatal("organization switch changed its snapshot or failed to cancel the old session context")
	}
	select {
	case <-browserContext.Done():
	case <-time.After(time.Second):
		t.Fatal("organization switch did not cancel the bound browser request context")
	}
	select {
	case <-aCall.ctx.Done():
		t.Fatal("browser switch canceled the separately pinned A MCP request")
	default:
	}
	hubB := decodeHubResponse[map[string]any](t, organizationTestRequest(h, sharedCookie, http.MethodGet, "/api/hub", ""))
	if hubB["activeOrganizationId"] != orgB || hubB["member"].(map[string]any)["role"] != "viewer" {
		t.Fatalf("switched browser identity = %#v", hubB)
	}
	directory := decodeHubResponse[map[string]any](t, organizationTestRequest(h, sharedCookie, http.MethodGet, "/api/hub/clusters", ""))
	clusters := directory["clusters"].([]any)
	if len(clusters) != 1 || clusters[0].(map[string]any)["id"] != "org-b" {
		t.Fatalf("B directory leaked another organization: %#v", directory)
	}
	groupDirectory := decodeHubResponse[map[string]any](t, organizationTestRequest(h, sharedCookie, http.MethodGet, "/api/hub/groups", ""))
	groups := groupDirectory["groups"].([]any)
	if len(groups) != 1 || groups[0].(map[string]any)["id"] != "group-b" {
		t.Fatalf("B groups leaked another organization: %#v", groupDirectory)
	}
	if guess := organizationTestRequest(h, sharedCookie, http.MethodGet, "/api/hub/clusters/org-a", ""); guess.Code != http.StatusForbidden {
		t.Fatalf("cross-organization cluster guess status=%d, want 403", guess.Code)
	}
	if guess := organizationTestRequest(h, sharedCookie, http.MethodGet, "/c/org-a/api/health", ""); guess.Code != http.StatusForbidden {
		t.Fatalf("native cross-organization cluster guess status=%d, want 403", guess.Code)
	}
	credentialBody, _ = json.Marshal(map[string]any{"label": "B viewer", "clusterIds": []string{"org-b"}, "readOnly": false})
	viewerCredential := decodeHubResponse[hubstate.IssuedCredential](t,
		hubRequest(t, h, sharedCookie, "", http.MethodPost, "/api/mcp/credentials", string(credentialBody), http.StatusCreated))
	bCall, bDone := startBlockedMCP(t, h, calls, sharedCookie, viewerCredential.Token, orgA)
	if bCall.principal.OrganizationID != orgB || !bCall.principal.ReadOnly {
		t.Fatalf("B viewer credential was not bound/read-only: %#v", bCall.principal)
	}
	close(bCall.release)
	select {
	case <-bDone:
	case <-time.After(time.Second):
		t.Fatal("B MCP request did not finish")
	}

	aOnlyCookie := hubTestMemberSession(t, h, aOnlyID)
	selectOrganizationForTest(t, h, aOnlyCookie, orgA)
	forbidden := organizationTestRequest(h, aOnlyCookie, http.MethodPost, "/api/hub/organization/select",
		fmt.Sprintf("{\"organizationId\":%q}", orgB))
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("A-only account selected B: status=%d", forbidden.Code)
	}
	transferBody, _ := json.Marshal(map[string]string{"organizationId": orgB})
	hubRequest(t, h, localCookie, "", http.MethodPost, "/api/hub/clusters/org-a/transfer", string(transferBody), http.StatusOK)
	select {
	case <-aCall.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("cluster transfer did not cancel its live MCP request")
	}
	select {
	case <-aDone:
	case <-time.After(time.Second):
		t.Fatal("transferred MCP request did not finish")
	}
	if _, err := h.liveHub.store.AuthenticateAgent(clusterA.Token, "org-a"); err == nil {
		t.Fatal("cluster transfer retained the old Agent token")
	}
	if groups := h.liveHub.store.GroupsForOrganization(orgA); len(groups) != 1 || len(groups[0].ClusterIDs) != 0 {
		t.Fatalf("transfer retained old group scope: %#v", groups)
	}
	selectOrganizationForTest(t, h, localCookie, orgB)
	hubRequest(t, h, localCookie, "", http.MethodPost, "/api/hub/clusters/org-a/rotate", "{}", http.StatusConflict)
	hubRequest(t, h, nil, aCredential.Token, http.MethodPost, "/mcp", "{}", http.StatusUnauthorized)
}

func TestOrganizationAuthorizationMutationsCancelAndRevokeMCP(t *testing.T) {
	h, _ := newPortalHubFixture(t, false)
	calls := installBlockingMCP(h)
	localCookie := loginForTest(t, h)
	orgA := createOrganizationForTest(t, h, localCookie, "Mutation org", "local")
	orgB := createOrganizationForTest(t, h, localCookie, "Other org", "local")
	if _, err := h.liveHub.store.CreateClusterForOrganization(orgA, hubstate.ClusterInput{ID: "mutation-cluster", Name: "Cluster"}); err != nil {
		t.Fatal(err)
	}
	id := "oidc:" + strings.Repeat("c", 64)
	memberships := []hubstate.OrganizationMembership{{OrganizationID: orgA, Role: "owner"}, {OrganizationID: orgB, Role: "viewer"}}
	addOrganizationAccount(t, h, id, memberships...)
	mutationSets := []struct {
		name        string
		status      string
		memberships []hubstate.OrganizationMembership
	}{
		{name: "demotion", status: "active", memberships: []hubstate.OrganizationMembership{{OrganizationID: orgA, Role: "member"}, {OrganizationID: orgB, Role: "viewer"}}},
		{name: "removal", status: "active", memberships: []hubstate.OrganizationMembership{{OrganizationID: orgB, Role: "viewer"}}},
		{name: "disable", status: "disabled", memberships: memberships},
	}
	for _, mutation := range mutationSets {
		cookie := hubTestMemberSession(t, h, id)
		selectOrganizationForTest(t, h, cookie, orgA)
		credentialBody, _ := json.Marshal(map[string]any{"label": "live", "clusterIds": []string{"mutation-cluster"}, "readOnly": false})
		issued := decodeHubResponse[hubstate.IssuedCredential](t,
			hubRequest(t, h, cookie, "", http.MethodPost, "/api/mcp/credentials", string(credentialBody), http.StatusCreated))
		call, done := startBlockedMCP(t, h, calls, nil, issued.Token, "forged-org")
		body, _ := json.Marshal(map[string]any{"status": mutation.status, "memberships": mutation.memberships})
		hubRequest(t, h, localCookie, "", http.MethodPut, "/api/hub/platform/users/"+id, string(body), http.StatusOK)
		select {
		case <-call.ctx.Done():
		case <-time.After(time.Second):
			t.Fatalf("%s did not cancel its live MCP request", mutation.name)
		}
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("%s MCP handler did not finish", mutation.name)
		}
		hubRequest(t, h, nil, issued.Token, http.MethodPost, "/mcp", "{}", http.StatusUnauthorized)
		restore, _ := json.Marshal(map[string]any{"status": "active", "memberships": memberships})
		hubRequest(t, h, localCookie, "", http.MethodPut, "/api/hub/platform/users/"+id, string(restore), http.StatusOK)
		hubRequest(t, h, nil, issued.Token, http.MethodPost, "/mcp", "{}", http.StatusUnauthorized)
	}
}

func TestLastOrganizationOwnerMutationsFailAtomicallyOverHTTP(t *testing.T) {
	h, _ := newPortalHubFixture(t, false)
	localCookie := loginForTest(t, h)
	ownerID := "oidc:" + strings.Repeat("d", 64)
	addOrganizationAccount(t, h, ownerID)
	org := createOrganizationForTest(t, h, localCookie, "Sole owner", ownerID)
	hubRequest(t, h, localCookie, "", http.MethodPut, "/api/hub/platform/users/"+ownerID,
		"{\"status\":\"disabled\",\"memberships\":[]}", http.StatusConflict)
	selectOrganizationForTest(t, h, localCookie, org)
	demotion, _ := json.Marshal(map[string]any{"status": "active", "memberships": []hubstate.OrganizationMembership{
		{OrganizationID: org, Role: "member"},
	}})
	hubRequest(t, h, localCookie, "", http.MethodPut, "/api/hub/platform/users/"+ownerID, string(demotion), http.StatusConflict)
	hubRequest(t, h, localCookie, "", http.MethodDelete, "/api/hub/members/"+ownerID, "{}", http.StatusConflict)
	account, ok := h.liveHub.store.Account(ownerID)
	member, memberOK := h.liveHub.store.MemberForOrganization(org, ownerID)
	if !ok || account.Status != "active" || !memberOK || member.Role != "owner" {
		t.Fatalf("failed last-owner mutations changed state: account=%#v member=%#v", account, member)
	}
}
