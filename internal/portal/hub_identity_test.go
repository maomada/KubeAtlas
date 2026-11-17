package portal

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/maomada/KubeAtlas/internal/hubstate"
)

func TestHubMemberScopeAndAuthorization(t *testing.T) {
	h, _ := newPortalHubFixture(t, false)
	localCookie := loginForTest(t, h)
	hubRequest(t, h, localCookie, "", http.MethodPost, "/api/hub/clusters",
		`{"id":"alpha","name":"Alpha"}`, http.StatusCreated)
	hubRequest(t, h, localCookie, "", http.MethodPost, "/api/hub/clusters",
		`{"id":"beta","name":"Beta"}`, http.StatusCreated)
	hubRequest(t, h, localCookie, "", http.MethodPost, "/api/hub/groups",
		`{"id":"mixed","name":"Mixed","clusterIds":["alpha","beta"]}`, http.StatusOK)
	hubRequest(t, h, localCookie, "", http.MethodPost, "/api/hub/groups",
		`{"id":"beta-only","name":"Beta Only","clusterIds":["beta"]}`, http.StatusOK)
	memberID := "oidc:" + strings.Repeat("d", 64)
	if _, _, err := h.liveHub.store.UpsertOIDCMember(memberID, "Viewer"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.liveHub.store.UpdateMember(memberID, hubstate.MemberInput{
		Role: "viewer", Status: "active", ClusterIDs: []string{"alpha"},
		KubernetesGroups: []string{"radar:readers"},
	}); err != nil {
		t.Fatal(err)
	}
	session := httptest.NewRecorder()
	if !h.createSession(session, httptest.NewRequest(http.MethodGet, "http://localhost/", nil), memberID, time.Now().Add(time.Hour)) {
		t.Fatal("could not create the member session")
	}
	cookie := session.Result().Cookies()[0]
	hubResponse := decodeHubResponse[map[string]any](t,
		hubRequest(t, h, cookie, "", http.MethodGet, "/api/hub", "", http.StatusOK))
	member, ok := hubResponse["member"].(map[string]any)
	if !ok || member["id"] != memberID || member["role"] != "viewer" {
		t.Fatalf("Hub session member = %#v", hubResponse["member"])
	}
	organization := decodeHubResponse[struct {
		MemberCount int `json:"memberCount"`
	}](t, hubRequest(t, h, cookie, "", http.MethodGet, "/api/hub/organization", "", http.StatusOK))
	if organization.MemberCount != 1 {
		t.Fatalf("active member count = %d", organization.MemberCount)
	}
	directory := decodeHubResponse[struct {
		Clusters []hubDirectoryEntry `json:"clusters"`
	}](t,
		hubRequest(t, h, cookie, "", http.MethodGet, "/api/hub/clusters", "", http.StatusOK))
	if len(directory.Clusters) != 1 || directory.Clusters[0].ID != "alpha" {
		t.Fatalf("filtered directory = %#v", directory.Clusters)
	}
	groups := decodeHubResponse[struct {
		Groups []hubstate.Group `json:"groups"`
	}](t, hubRequest(t, h, cookie, "", http.MethodGet, "/api/hub/groups", "", http.StatusOK))
	if len(groups.Groups) != 1 || groups.Groups[0].ID != "mixed" ||
		len(groups.Groups[0].ClusterIDs) != 1 || groups.Groups[0].ClusterIDs[0] != "alpha" {
		t.Fatalf("filtered group directory = %#v", groups.Groups)
	}
	hubRequest(t, h, cookie, "", http.MethodPut, "/api/hub/members/"+memberID,
		`{"role":"admin","status":"active","clusterIds":[],"kubernetesGroups":[]}`, http.StatusForbidden)
	hubRequest(t, h, localCookie, "", http.MethodPut, "/api/hub/members/local",
		`{"role":"viewer","status":"active","clusterIds":[],"kubernetesGroups":[]}`, http.StatusForbidden)
}

func activeHubTestMember(t *testing.T, h *Handler, id, name string, clusters []string) {
	t.Helper()
	if _, _, err := h.liveHub.store.UpsertOIDCMember(id, name); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.liveHub.store.UpdateMember(id, hubstate.MemberInput{
		Role: "member", Status: "active", ClusterIDs: clusters, KubernetesGroups: []string{},
	}); err != nil {
		t.Fatal(err)
	}
}

func hubTestMemberSession(t *testing.T, h *Handler, id string) *http.Cookie {
	t.Helper()
	session := httptest.NewRecorder()
	if !h.createSession(session, httptest.NewRequest(http.MethodGet, "http://localhost/", nil), id, time.Now().Add(time.Hour)) {
		t.Fatal("could not create the member session")
	}
	return session.Result().Cookies()[0]
}

func hubErrorCode(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	result := decodeHubResponse[map[string]any](t, response)
	code, _ := result["error_code"].(string)
	return code
}

func TestPendingMemberCannotReachClusterAPIs(t *testing.T) {
	h, _ := newPortalHubFixture(t, false)
	localCookie := loginForTest(t, h)
	hubRequest(t, h, localCookie, "", http.MethodPost, "/api/hub/clusters",
		`{"id":"alpha","name":"Alpha"}`, http.StatusCreated)
	memberID := "oidc:" + strings.Repeat("f", 64)
	if _, _, err := h.liveHub.store.UpsertOIDCMember(memberID, "Pending User"); err != nil {
		t.Fatal(err)
	}
	cookie := hubTestMemberSession(t, h, memberID)
	hubResponse := decodeHubResponse[map[string]any](t,
		hubRequest(t, h, cookie, "", http.MethodGet, "/api/hub", "", http.StatusOK))
	member, ok := hubResponse["member"].(map[string]any)
	if !ok || member["status"] != "pending" {
		t.Fatalf("pending member Hub response = %#v", hubResponse["member"])
	}
	organization := decodeHubResponse[struct {
		MemberCount int `json:"memberCount"`
	}](t, hubRequest(t, h, localCookie, "", http.MethodGet, "/api/hub/organization", "", http.StatusOK))
	if organization.MemberCount != 0 {
		t.Fatalf("pending member was included in active member count: %d", organization.MemberCount)
	}
	if response := hubRequest(t, h, cookie, "", http.MethodGet, "/api/hub/clusters", "", http.StatusForbidden); hubErrorCode(t, response) != "member_pending" {
		t.Fatalf("pending member cluster error = %q", hubErrorCode(t, response))
	}
	browserRequest := httptest.NewRequest(http.MethodGet, "http://localhost/c/alpha/api/settings", nil)
	browserRequest.AddCookie(cookie)
	browserRequest.Header.Set("Origin", "http://localhost")
	browserRequest.Header.Set("Sec-Fetch-Site", "same-origin")
	browserResponse := httptest.NewRecorder()
	h.ServeHTTP(browserResponse, browserRequest)
	if browserResponse.Code != http.StatusForbidden || hubErrorCode(t, browserResponse) != "member_pending" {
		t.Fatalf("pending member browser status=%d error=%q", browserResponse.Code, hubErrorCode(t, browserResponse))
	}
}

func TestMemberCredentialsAreOwnerScoped(t *testing.T) {
	h, _ := newPortalHubFixture(t, false)
	localCookie := loginForTest(t, h)
	for _, cluster := range []struct {
		id   string
		name string
	}{
		{id: "alpha", name: "Alpha"},
		{id: "beta", name: "Beta"},
	} {
		hubRequest(t, h, localCookie, "", http.MethodPost, "/api/hub/clusters",
			`{"id":"`+cluster.id+`","name":"`+cluster.name+`"}`, http.StatusCreated)
	}
	aliceID := "oidc:" + strings.Repeat("1", 64)
	bobID := "oidc:" + strings.Repeat("2", 64)
	activeHubTestMember(t, h, aliceID, "Alice", []string{"alpha"})
	activeHubTestMember(t, h, bobID, "Bob", []string{"beta"})
	aliceCredential, err := h.liveHub.store.IssueForMember(aliceID, hubstate.IssueInput{
		Label: "Alice token", ClusterIDs: []string{"alpha"},
	})
	if err != nil {
		t.Fatal(err)
	}
	bobCredential, err := h.liveHub.store.IssueForMember(bobID, hubstate.IssueInput{
		Label: "Bob token", ClusterIDs: []string{"beta"},
	})
	if err != nil {
		t.Fatal(err)
	}
	aliceCookie := hubTestMemberSession(t, h, aliceID)
	bobCookie := hubTestMemberSession(t, h, bobID)
	for _, entry := range []struct {
		cookie *http.Cookie
		ownID  string
		other  string
	}{
		{cookie: aliceCookie, ownID: aliceCredential.Credential.ID, other: bobCredential.Credential.ID},
		{cookie: bobCookie, ownID: bobCredential.Credential.ID, other: aliceCredential.Credential.ID},
	} {
		response := decodeHubResponse[struct {
			Credentials []hubstate.Credential `json:"credentials"`
		}](t, hubRequest(t, h, entry.cookie, "", http.MethodGet, "/api/mcp/credentials", "", http.StatusOK))
		if len(response.Credentials) != 1 || response.Credentials[0].ID != entry.ownID {
			t.Fatalf("credential list is not owner-scoped: %#v", response.Credentials)
		}
		for _, action := range []struct {
			name string
			body string
		}{
			{name: "revoke", body: `{}`},
			{name: "rotate", body: `{"ttlSeconds":0}`},
		} {
			path := "/api/mcp/credentials/" + entry.other + "/" + action.name
			result := hubRequest(t, h, entry.cookie, "", http.MethodPost, path, action.body, http.StatusNotFound)
			if code := hubErrorCode(t, result); code != "credential_invalid" {
				t.Fatalf("cross-owner %s error = %q", action.name, code)
			}
		}
	}
	if _, err := h.liveHub.store.Authenticate(bobCredential.Token, aliceID); err == nil {
		t.Fatal("credential authenticated as a different member")
	}
	if _, err := h.liveHub.store.Authenticate(bobCredential.Token, bobID); err != nil {
		t.Fatalf("cross-owner requests affected the credential owner: %v", err)
	}
}

func TestDisablingMemberCancelsSessionAndRevokesCredentials(t *testing.T) {
	h, _ := newPortalHubFixture(t, false)
	localCookie := loginForTest(t, h)
	hubRequest(t, h, localCookie, "", http.MethodPost, "/api/hub/clusters",
		`{"id":"alpha","name":"Alpha"}`, http.StatusCreated)
	memberID := "oidc:" + strings.Repeat("3", 64)
	activeHubTestMember(t, h, memberID, "Member", []string{"alpha"})
	cookie := hubTestMemberSession(t, h, memberID)
	issued, err := h.liveHub.store.IssueForMember(memberID, hubstate.IssueInput{
		Label: "active", ClusterIDs: []string{"alpha"},
	})
	if err != nil {
		t.Fatal(err)
	}
	update := `{"status":"disabled","memberships":[]}`
	hubRequest(t, h, localCookie, "", http.MethodPut, "/api/hub/platform/users/"+memberID, update, http.StatusOK)
	if response := hubRequest(t, h, cookie, "", http.MethodGet, "/api/hub", "", http.StatusUnauthorized); hubErrorCode(t, response) != "authentication_required" {
		t.Fatalf("disabled member session error = %q", hubErrorCode(t, response))
	}
	if _, err := h.liveHub.store.Authenticate(issued.Token, memberID); err == nil {
		t.Fatal("disabled member credential remained valid")
	}
	if _, err := h.liveHub.store.IssueForMember(memberID, hubstate.IssueInput{
		Label: "disabled", ClusterIDs: []string{"alpha"},
	}); err == nil {
		t.Fatal("disabled member received a credential")
	}
}
