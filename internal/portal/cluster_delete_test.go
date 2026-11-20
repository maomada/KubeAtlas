package portal

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/maomada/KubeAtlas/internal/hubstate"
)

func TestClusterDeleteHTTPRequiresScopedOwnerAndRevokedCluster(t *testing.T) {
	h, _ := newPortalHubFixture(t, false)
	platformCookie := loginForTest(t, h)
	otherOrganizationID := createOrganizationForTest(t, h, platformCookie, "Other organization", "local")
	ownerID := "oidc:" + strings.Repeat("a", 64)
	memberID := "oidc:" + strings.Repeat("b", 64)
	addOrganizationAccount(t, h, ownerID, hubstate.OrganizationMembership{OrganizationID: "self-hosted", Role: "owner"})
	addOrganizationAccount(t, h, memberID, hubstate.OrganizationMembership{OrganizationID: "self-hosted", Role: "member"})
	ownerCookie := hubTestMemberSession(t, h, ownerID)
	memberCookie := hubTestMemberSession(t, h, memberID)
	for id, organizationID := range map[string]string{"owned": "self-hosted", "foreign": otherOrganizationID} {
		if _, err := h.liveHub.store.CreateClusterForOrganization(organizationID, hubstate.ClusterInput{ID: id, Name: id}); err != nil {
			t.Fatal(err)
		}
	}
	const ownedPath = "/api/hub/clusters/owned"
	const foreignPath = "/api/hub/clusters/foreign"
	hubRequest(t, h, ownerCookie, "", http.MethodDelete, ownedPath, "", http.StatusConflict)
	if err := h.liveHub.store.RevokeAgentTokenForOrganization(otherOrganizationID, "foreign"); err != nil {
		t.Fatal(err)
	}
	hubRequest(t, h, ownerCookie, "", http.MethodDelete, foreignPath, "", http.StatusNotFound)
	hubRequest(t, h, ownerCookie, "", http.MethodPost, ownedPath+"/revoke", `{}`, http.StatusNoContent)
	hubRequest(t, h, memberCookie, "", http.MethodDelete, ownedPath, "", http.StatusForbidden)
	hubRequest(t, h, nil, "", http.MethodDelete, ownedPath, "", http.StatusUnauthorized)
	foreignOrigin := httptest.NewRequest(http.MethodDelete, "http://localhost"+ownedPath, nil)
	foreignOrigin.Header.Set("Origin", "https://different.example")
	foreignOrigin.AddCookie(ownerCookie)
	rejected := httptest.NewRecorder()
	h.ServeHTTP(rejected, foreignOrigin)
	if rejected.Code != http.StatusForbidden {
		t.Fatalf("cross-origin deletion returned %d", rejected.Code)
	}
	hubRequest(t, h, ownerCookie, "", http.MethodDelete, ownedPath, "", http.StatusNoContent)
	hubRequest(t, h, ownerCookie, "", http.MethodDelete, ownedPath, "", http.StatusNotFound)
	directory := decodeHubResponse[struct{ Clusters []hubDirectoryEntry }](t,
		hubRequest(t, h, ownerCookie, "", http.MethodGet, "/api/hub/clusters", "", http.StatusOK))
	if len(directory.Clusters) != 0 {
		t.Fatalf("directory retained deleted cluster: %#v", directory.Clusters)
	}
	if _, exists := h.liveHub.store.ClusterForOrganization(otherOrganizationID, "foreign"); !exists {
		t.Fatal("cross-organization deletion changed the other cluster")
	}
}

func TestClusterDeleteInvalidatesAlreadyApprovedReenrollment(t *testing.T) {
	t.Run("same organization", func(t *testing.T) { testClusterDeleteStaleApproval(t, false) })
	t.Run("after organization transfer", func(t *testing.T) { testClusterDeleteStaleApproval(t, true) })
}

func testClusterDeleteStaleApproval(t *testing.T, transfer bool) {
	t.Helper()
	h, ownerCookie := newEnrollmentHTTPFixture(t)
	initial := createEnrollmentHTTP(t, h, "")
	assertEnrollmentStatus(t, enrollmentApprove(t, h, ownerCookie, initial.ID, initial.UserCode, "self-hosted", ""), http.StatusOK)
	claimed := enrollmentClaim(t, h, initial.ID, initial.DeviceSecret)
	assertEnrollmentStatus(t, claimed, http.StatusOK)
	issued := decodeEnrollmentResponse[enrollmentClaimResponse](t, claimed)
	path := "/api/hub/clusters/" + issued.ClusterID
	hubRequest(t, h, ownerCookie, "", http.MethodPost, path+"/revoke", `{}`, http.StatusNoContent)
	approved := createEnrollmentHTTP(t, h, "")
	assertEnrollmentStatus(t, enrollmentApprove(t, h, ownerCookie, approved.ID, approved.UserCode, "self-hosted", issued.ClusterID), http.StatusOK)
	if transfer {
		destinationID := createOrganizationForTest(t, h, ownerCookie, "Transfer destination", "local")
		hubRequest(t, h, ownerCookie, "", http.MethodPost, path+"/transfer", `{"organizationId":"`+destinationID+`"}`, http.StatusOK)
		selectOrganizationForTest(t, h, ownerCookie, destinationID)
	}
	hubRequest(t, h, ownerCookie, "", http.MethodDelete, path, "", http.StatusNoContent)
	if transfer {
		selectOrganizationForTest(t, h, ownerCookie, "self-hosted")
	}
	replacement, err := h.liveHub.store.CreateCluster(hubstate.ClusterInput{ID: issued.ClusterID, Name: "Replacement"})
	if err != nil {
		t.Fatal(err)
	}
	hubRequest(t, h, ownerCookie, "", http.MethodPost, path+"/revoke", `{}`, http.StatusNoContent)
	claim := enrollmentClaim(t, h, approved.ID, approved.DeviceSecret)
	if claim.Code != http.StatusConflict {
		t.Fatalf("stale approval claim status=%d, want=%d", claim.Code, http.StatusConflict)
	}
	assertNoEnrollmentSecrets(t, claim)
	if cluster, ok := h.liveHub.store.Cluster(issued.ClusterID); !ok || cluster.RevokedAt == nil || cluster.Name != replacement.Cluster.Name {
		t.Fatalf("stale approval changed replacement: %#v, exists=%v", cluster, ok)
	}
}

func TestClusterDeleteCancelsOnlyRevokedCredentialRequests(t *testing.T) {
	h, _ := newPortalHubFixture(t, false)
	ownerCookie := loginForTest(t, h)
	store := h.liveHub.store
	for _, id := range []string{"removed", "retained"} {
		if _, err := store.CreateCluster(hubstate.ClusterInput{ID: id, Name: id}); err != nil {
			t.Fatal(err)
		}
	}
	affected, err := store.Issue(h.liveHub.ownerID, hubstate.IssueInput{ClusterIDs: []string{"removed", "retained"}})
	if err != nil {
		t.Fatal(err)
	}
	unaffected, err := store.Issue(h.liveHub.ownerID, hubstate.IssueInput{ClusterIDs: []string{"retained"}})
	if err != nil {
		t.Fatal(err)
	}
	calls := installBlockingMCP(h)
	affectedCall, affectedDone := startBlockedMCP(t, h, calls, nil, affected.Token, "self-hosted")
	defer close(affectedCall.release)
	unaffectedCall, _ := startBlockedMCP(t, h, calls, nil, unaffected.Token, "self-hosted")
	defer close(unaffectedCall.release)
	const path = "/api/hub/clusters/removed"
	hubRequest(t, h, ownerCookie, "", http.MethodPost, path+"/revoke", `{}`, http.StatusNoContent)
	hubRequest(t, h, ownerCookie, "", http.MethodDelete, path, "", http.StatusNoContent)
	select {
	case status := <-affectedDone:
		if status != http.StatusRequestTimeout {
			t.Fatalf("revoked credential finished with status %d", status)
		}
	case <-time.After(time.Second):
		t.Fatal("revoked credential request was not cancelled")
	}
	if err := unaffectedCall.ctx.Err(); err != nil {
		t.Fatalf("unrelated credential request was cancelled: %v", err)
	}
}
