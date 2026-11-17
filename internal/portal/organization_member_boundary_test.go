package portal

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/maomada/KubeAtlas/internal/hubstate"
)

type organizationMemberHTTPFixture struct {
	handler                                 *Handler
	statePath, organizationA, organizationB string
	ownerID, memberID, foreignOwnerID       string
	platformCookie, ownerCookie             *http.Cookie
}

func newOrganizationMemberHTTPFixture(t *testing.T) organizationMemberHTTPFixture {
	t.Helper()
	h, path := newPortalHubFixture(t, false)
	f := organizationMemberHTTPFixture{handler: h, statePath: path, platformCookie: loginForTest(t, h),
		ownerID: "oidc:" + strings.Repeat("1", 64), memberID: "oidc:" + strings.Repeat("2", 64), foreignOwnerID: "oidc:" + strings.Repeat("3", 64)}
	f.organizationA = createOrganizationForTest(t, h, f.platformCookie, "Boundary A", "local")
	f.organizationB = createOrganizationForTest(t, h, f.platformCookie, "Boundary B", "local")
	addOrganizationAccount(t, h, f.ownerID, hubstate.OrganizationMembership{OrganizationID: f.organizationA, Role: "owner"})
	addOrganizationAccount(t, h, f.memberID, hubstate.OrganizationMembership{OrganizationID: f.organizationA, Role: "viewer"})
	addOrganizationAccount(t, h, f.foreignOwnerID, hubstate.OrganizationMembership{OrganizationID: f.organizationB, Role: "owner"})
	f.ownerCookie = hubTestMemberSession(t, h, f.ownerID)
	selectOrganizationForTest(t, h, f.ownerCookie, f.organizationA)
	return f
}

func TestOrganizationMemberHTTPDirectoryExcludesPlatformAndForeignAccounts(t *testing.T) {
	f := newOrganizationMemberHTTPFixture(t)
	h := f.handler
	before, err := os.ReadFile(f.statePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, cookie := range []*http.Cookie{f.ownerCookie, f.platformCookie} {
		if cookie == f.platformCookie {
			selectOrganizationForTest(t, h, cookie, f.organizationA)
		}
		response := organizationTestRequest(h, cookie, http.MethodGet, "/api/hub/members", "")
		if response.Code != http.StatusOK {
			t.Fatalf("organization member list status=%d", response.Code)
		}
		var envelope struct {
			Members []hubstate.Member `json:"members"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		ids := make([]string, 0, len(envelope.Members))
		for _, member := range envelope.Members {
			ids = append(ids, member.ID)
		}
		if !slices.Equal(ids, []string{f.ownerID, f.memberID}) {
			t.Fatalf("organization member IDs=%v", ids)
		}
		if bytes.Contains(response.Body.Bytes(), []byte(h.liveHub.ownerID)) || bytes.Contains(response.Body.Bytes(), []byte(h.liveHub.userID)) {
			t.Fatal("organization member response exposed platform account identifiers")
		}
		organization := decodeHubResponse[struct {
			MemberCount int `json:"memberCount"`
		}](t,
			hubRequest(t, h, cookie, "", http.MethodGet, "/api/hub/organization", "", http.StatusOK))
		if organization.MemberCount != 2 {
			t.Fatalf("organization member count=%d, want 2 ordinary members", organization.MemberCount)
		}
	}
	hubRequest(t, h, f.ownerCookie, "", http.MethodGet, "/api/hub/platform/users", "", http.StatusForbidden)
	selection, _ := json.Marshal(map[string]string{"organizationId": f.organizationB})
	hubRequest(t, h, f.ownerCookie, "", http.MethodPost, "/api/hub/organization/select", string(selection), http.StatusForbidden)
	var users struct {
		Users []hubstate.PlatformUser `json:"users"`
	}
	response := hubRequest(t, h, f.platformCookie, "", http.MethodGet, "/api/hub/platform/users", "", http.StatusOK)
	if err := json.Unmarshal(response.Body.Bytes(), &users); err != nil {
		t.Fatal(err)
	}
	platformFound := false
	for _, user := range users.Users {
		platformFound = platformFound || user.ID == hubstate.LocalMemberID && user.Local && user.PlatformOwner
	}
	if !platformFound {
		t.Fatal("platform management lost its recovery account")
	}
	after, err := os.ReadFile(f.statePath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("member listing changed compatibility or business state")
	}
}

func TestOrganizationMemberHTTPRejectsProtectedAndForeignDirectIDsWithoutMutation(t *testing.T) {
	f := newOrganizationMemberHTTPFixture(t)
	for _, target := range []struct {
		id     string
		status int
	}{{hubstate.LocalMemberID, http.StatusForbidden}, {f.handler.liveHub.ownerID, http.StatusForbidden}, {f.foreignOwnerID, http.StatusNotFound}} {
		for _, mutation := range []struct{ method, body string }{{http.MethodPut, `{"role":"owner"}`}, {http.MethodPut, `{"role":"viewer"}`}, {http.MethodDelete, "{}"}} {
			before, err := os.ReadFile(f.statePath)
			if err != nil {
				t.Fatal(err)
			}
			beforeAccount, _ := f.handler.liveHub.store.Account(target.id)
			hubRequest(t, f.handler, f.ownerCookie, "", mutation.method, "/api/hub/members/"+target.id, mutation.body, target.status)
			afterAccount, _ := f.handler.liveHub.store.Account(target.id)
			after, err := os.ReadFile(f.statePath)
			if err != nil || !bytes.Equal(before, after) || beforeAccount.AuthorizationRevision != afterAccount.AuthorizationRevision {
				t.Fatal("rejected direct-ID mutation changed persisted state or authority")
			}
		}
	}
	hubRequest(t, f.handler, f.ownerCookie, "", http.MethodGet, "/api/hub/members", "", http.StatusOK)
}

func TestLastOrdinaryOrganizationOwnerHTTPGuardIncludesPlatformMutations(t *testing.T) {
	f := newOrganizationMemberHTTPFixture(t)
	h := f.handler
	tests := []struct {
		name, method, path, body string
		cookie                   *http.Cookie
	}{
		{"owner-demotion", http.MethodPut, "/api/hub/members/" + f.ownerID, `{"role":"viewer"}`, f.ownerCookie},
		{"owner-removal", http.MethodDelete, "/api/hub/members/" + f.ownerID, "{}", f.ownerCookie},
	}
	for _, update := range []struct {
		name, status string
		memberships  []hubstate.OrganizationMembership
	}{
		{"platform-disable", "disabled", []hubstate.OrganizationMembership{{OrganizationID: f.organizationA, Role: "owner"}}},
		{"platform-pending", "pending", []hubstate.OrganizationMembership{{OrganizationID: f.organizationA, Role: "owner"}}},
		{"platform-removal", "active", []hubstate.OrganizationMembership{}},
		{"platform-demotion", "active", []hubstate.OrganizationMembership{{OrganizationID: f.organizationA, Role: "member"}}},
	} {
		body, _ := json.Marshal(map[string]any{"status": update.status, "memberships": update.memberships})
		tests = append(tests, struct {
			name, method, path, body string
			cookie                   *http.Cookie
		}{
			update.name, http.MethodPut, "/api/hub/platform/users/" + f.ownerID, string(body), f.platformCookie,
		})
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before, err := os.ReadFile(f.statePath)
			if err != nil {
				t.Fatal(err)
			}
			response := hubRequest(t, h, test.cookie, "", test.method, test.path, test.body, http.StatusConflict)
			if hubErrorCode(t, response) != "last_owner" {
				t.Fatal("last ordinary owner was not protected")
			}
			after, err := os.ReadFile(f.statePath)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("rejected last-owner HTTP mutation changed state")
			}
			hubRequest(t, h, f.ownerCookie, "", http.MethodGet, "/api/hub", "", http.StatusOK)
		})
	}
}
