package portal

import (
	"bytes"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/maomada/KubeAtlas/internal/hubstate"
)

func TestEnrollmentHTTPActiveClusterTransferRetainsItsOriginalTokenWithoutReenrollment(t *testing.T) {
	h, ownerCookie := newEnrollmentHTTPFixture(t)
	initial := createEnrollmentHTTP(t, h, "")
	assertEnrollmentStatus(t, enrollmentApprove(t, h, ownerCookie, initial.ID, initial.UserCode, "self-hosted", ""), http.StatusOK)
	claimed := enrollmentClaim(t, h, initial.ID, initial.DeviceSecret)
	assertEnrollmentStatus(t, claimed, http.StatusOK)
	issued := decodeEnrollmentResponse[enrollmentClaimResponse](t, claimed)
	organization, err := h.liveHub.store.CreateOrganization("Active transfer target", h.liveHub.ownerID)
	if err != nil {
		t.Fatal(err)
	}
	transfer := enrollmentHTTP(h, ownerCookie, http.MethodPost, "/api/hub/clusters/"+issued.ClusterID+"/transfer",
		`{"organizationId":"`+organization.ID+`"}`, nil, nil, "")
	assertEnrollmentStatus(t, transfer, http.StatusOK)
	assertNoEnrollmentSecrets(t, transfer)
	cluster, err := h.liveHub.store.AuthenticateAgent(issued.AgentToken, issued.ClusterID)
	if err != nil || cluster.OrganizationID != organization.ID || cluster.RevokedAt != nil {
		t.Fatalf("active transfer invalidated the original Agent token: %v", err)
	}
	for _, organizationID := range []string{"self-hosted", organization.ID} {
		request := createEnrollmentHTTP(t, h, "")
		approval := enrollmentApprove(t, h, ownerCookie, request.ID, request.UserCode, organizationID, issued.ClusterID)
		assertEnrollmentStatus(t, approval, http.StatusConflict)
		assertNoEnrollmentSecrets(t, approval)
	}
	replay := enrollmentClaim(t, h, initial.ID, initial.DeviceSecret)
	assertEnrollmentStatus(t, replay, http.StatusConflict)
	assertNoEnrollmentSecrets(t, replay)
	if current, err := h.liveHub.store.AuthenticateAgent(issued.AgentToken, issued.ClusterID); err != nil || current.OrganizationID != organization.ID {
		t.Fatal("rejected approval or consumed-claim replay changed the retained Agent credential")
	}
}

func TestClusterTransferHTTPRequiresPlatformOwnerAndValidDestination(t *testing.T) {
	h, statePath := newPortalHubFixture(t, false)
	platformCookie := loginForTest(t, h)
	if _, err := h.liveHub.store.CreateClusterForOrganization("self-hosted", hubstate.ClusterInput{ID: "transfer-guard", Name: "Transfer guard"}); err != nil {
		t.Fatal(err)
	}
	destination, err := h.liveHub.store.CreateOrganization("Destination", h.liveHub.ownerID)
	if err != nil {
		t.Fatal(err)
	}
	ownerID := "oidc:" + strings.Repeat("c", 64)
	addOrganizationAccount(t, h, ownerID, hubstate.OrganizationMembership{OrganizationID: "self-hosted", Role: "owner"})
	ownerCookie := hubTestMemberSession(t, h, ownerID)
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	hubRequest(t, h, ownerCookie, "", http.MethodPost, "/api/hub/clusters/transfer-guard/transfer",
		`{"organizationId":"`+destination.ID+`"}`, http.StatusForbidden)
	hubRequest(t, h, platformCookie, "", http.MethodPost, "/api/hub/clusters/transfer-guard/transfer",
		`{"organizationId":"self-hosted"}`, http.StatusConflict)
	hubRequest(t, h, platformCookie, "", http.MethodPost, "/api/hub/clusters/transfer-guard/transfer",
		`{"organizationId":"missing"}`, http.StatusNotFound)
	after, err := os.ReadFile(statePath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("rejected transfer changed persisted state: %v", err)
	}
}
