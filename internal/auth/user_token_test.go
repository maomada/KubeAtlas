package auth

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/maomada/KubeAtlas/internal/cloud"
	pkgauth "github.com/maomada/KubeAtlas/pkg/auth"
	"github.com/maomada/KubeAtlas/pkg/compat"
)

func TestUserTokenFailureLogsDoNotExposeClaimsOrReviewErrors(t *testing.T) {
	const username = "private-user@example.invalid"
	const token = "private-token-fixture"
	const reviewError = "private-upstream-error"
	for _, tc := range []struct {
		name   string
		user   *User
		err    error
		status int
		reason string
		event  string
	}{
		{"rejected", nil, apierrors.NewUnauthorized(reviewError + " " + token), http.StatusForbidden, "token_rejected", "Kubernetes rejected the sign-in token"},
		{"outage", nil, errors.New(reviewError + " " + token), http.StatusServiceUnavailable, "token_review_unavailable", "Kubernetes token review unavailable"},
		{"mismatch", &User{Username: "other-private-user"}, nil, http.StatusForbidden, "token_identity_mismatch", "token identity does not match forwarded identity"},
		{"reserved group", &User{Username: username, Groups: []string{"kubeatlas:private-group"}}, nil, http.StatusForbidden, "token_reserved_group", "token identity carries a reserved group"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			defer log.SetOutput(log.Writer())
			log.SetOutput(&output)
			req := httptest.NewRequest(http.MethodGet, "/api/topology", nil)
			req.Header.Set(pkgauth.UserTokenHeader, token)
			rec := httptest.NewRecorder()
			user, ok := userTokenIdentity(rec, req, Config{Mode: "proxy", TokenReviewer: func(_ context.Context, got string) (*User, error) {
				if got != token {
					t.Error("reviewer did not receive the supplied token")
				}
				return tc.user, tc.err
			}}, username, nil)
			if ok || user != nil || rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.reason) {
				t.Fatalf("rejection changed: ok=%v, status=%d, reason=%s", ok, rec.Code, tc.reason)
			}
			if !strings.Contains(output.String(), tc.event) {
				t.Fatal("missing authentication outcome event")
			}
			for _, sensitive := range []string{username, token, reviewError, "other-private-user", "kubeatlas:private-group"} {
				if strings.Contains(output.String(), sensitive) {
					t.Error("diagnostic log includes a private claim or upstream error")
				}
			}
		})
	}
}

func userTokenMode(t *testing.T) {
	t.Helper()
	t.Setenv("KUBEATLAS_CLOUD_MODE", "true")
	pkgauth.SetCredentialMode(pkgauth.CredentialsUserToken)
	t.Cleanup(func() { pkgauth.SetCredentialMode(pkgauth.CredentialsImpersonate) })
}

func serveUserToken(t *testing.T, reviewer func(context.Context, string) (*User, error), user, groups, token string) (*httptest.ResponseRecorder, *User) {
	t.Helper()
	cfg := proxyConfig()
	cfg.TokenReviewer = reviewer
	var got *User
	handler := cloud.AuthenticatedTunnelHandler(Authenticate(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = UserFromContext(r.Context())
	})))
	req := httptest.NewRequest(http.MethodGet, "/api/topology", nil)
	req.Header.Set(cfg.UserHeader, user)
	req.Header.Set(cfg.GroupsHeader, groups)
	if token != "" {
		req.Header.Set(pkgauth.UserTokenHeader, token)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec, got
}

func reviewerFor(username string, groups ...string) func(context.Context, string) (*User, error) {
	return func(_ context.Context, token string) (*User, error) {
		switch token {
		case "good-token":
		case "flaky-token":
			return nil, errors.New("dial tcp: i/o timeout")
		default:
			return nil, apierrors.NewUnauthorized("token expired")
		}
		return &User{Username: username, Groups: groups, UID: "uid-1", Extra: map[string][]string{"scopes.example.io": {"a"}}}, nil
	}
}

func TestUserToken_TokenIdentityComesFromTheAPIServer(t *testing.T) {
	userTokenMode(t)
	rec, got := serveUserToken(t, reviewerFor("oidc:alice@example.com", "k8s-admins", "system:authenticated"),
		"oidc:alice@example.com", "k8s-admins-forged,kubeatlas:member,kubeatlas:org:o1,kubeatlas:user:u1", "good-token")
	if rec.Code != http.StatusOK || got == nil {
		t.Fatalf("status = %d, user = %v", rec.Code, got)
	}
	if got.Token != "good-token" || got.UID != "uid-1" || got.Extra["scopes.example.io"][0] != "a" {
		t.Fatalf("token identity not carried: %+v", got)
	}
	if !slices.Equal(got.Groups, []string{"k8s-admins", "system:authenticated"}) {
		t.Fatalf("groups = %v, want the API server's groups only", got.Groups)
	}
	if !slices.Equal(got.HubGroups, []string{"kubeatlas:member", "kubeatlas:org:o1", "kubeatlas:user:u1"}) {
		t.Fatalf("hub groups = %v", got.HubGroups)
	}
	ctx := ContextWithUser(context.Background(), got)
	if role := CloudRoleFromContext(ctx); role != RoleMember {
		t.Fatalf("cloud role = %q, want member from the Hub groups", role)
	}
}

func TestUserToken_ForwardedUserMustMatchToken(t *testing.T) {
	userTokenMode(t)
	rec, got := serveUserToken(t, reviewerFor("oidc:bob@example.com"), "oidc:alice@example.com", "kubeatlas:owner", "good-token")
	if rec.Code != http.StatusForbidden || got != nil {
		t.Fatalf("status = %d, want 403 without reaching the handler", rec.Code)
	}
}

func TestUserToken_RejectedTokenIs403(t *testing.T) {
	userTokenMode(t)
	rec, got := serveUserToken(t, reviewerFor("oidc:alice@example.com"), "oidc:alice@example.com", "kubeatlas:owner", "expired-token")
	if rec.Code != http.StatusForbidden || got != nil {
		t.Fatalf("status = %d, want 403 (a 401 reloads the web client in a loop)", rec.Code)
	}
}

func TestUserToken_ReservedTokenGroupRefused(t *testing.T) {
	userTokenMode(t)
	for _, g := range []string{"kubeatlas:system", "kubeatlas:ai:reader", "cloud:owner", "system:masters"} {
		rec, got := serveUserToken(t, reviewerFor("oidc:alice@example.com", "shop-admins", g), "oidc:alice@example.com", "kubeatlas:member", "good-token")
		if rec.Code != http.StatusForbidden || got != nil {
			t.Errorf("token group %s: status = %d, want 403", g, rec.Code)
		}
	}
}

func TestUserToken_ReviewOutageIs503(t *testing.T) {
	userTokenMode(t)
	rec, got := serveUserToken(t, reviewerFor("oidc:alice@example.com"), "oidc:alice@example.com", "kubeatlas:owner", "flaky-token")
	if rec.Code != http.StatusServiceUnavailable || got != nil {
		t.Fatalf("status = %d, want 503 so the person isn't sent to sign in again", rec.Code)
	}
}

func TestUserToken_NoTokenHumanKeepsKubernetesGroupsApartFromHubGroups(t *testing.T) {
	userTokenMode(t)
	rec, got := serveUserToken(t, nil, "oidc:alice@example.com", "k8s-admins,kubeatlas:owner,kubeatlas:org:o1", "")
	if rec.Code != http.StatusOK || got == nil {
		t.Fatalf("status = %d", rec.Code)
	}
	if got.Token != "" || got.System {
		t.Fatalf("no-token human got %+v", got)
	}
	if !slices.Equal(got.Groups, []string{"k8s-admins"}) {
		t.Fatalf("groups = %v, want kubeatlas:* groups excluded from the Kubernetes identity", got.Groups)
	}
	if !slices.Equal(got.HubGroups, []string{"kubeatlas:owner", "kubeatlas:org:o1"}) {
		t.Fatalf("hub groups = %v", got.HubGroups)
	}
}

func TestUserToken_SystemIdentityAllowList(t *testing.T) {
	userTokenMode(t)
	for _, tc := range []struct {
		user, groups string
		system       bool
	}{
		{"kubeatlas:system:hub-timeline-puller", "kubeatlas:system", true},
		{"kubeatlas:system:alerts:org-1", "kubeatlas:system", true},
		{"kubeatlas:ai:org-1", "kubeatlas:ai:reader", true},
		{compat.LegacySystemUserPrefix + "fixture", compat.LegacySystemGroup, true},
		{compat.LegacyAIUserPrefix + "fixture", compat.LegacyAIReaderGroup, true},
		{"kubeatlas:system:alerts:org-1", "kubeatlas:system,kubeatlas:owner", false},
		{"kubeatlas:ai:org-1", "kubeatlas:system", false},
		{"oidc:alice@example.com", "kubeatlas:system", false},
	} {
		rec, got := serveUserToken(t, nil, tc.user, tc.groups, "")
		if rec.Code != http.StatusOK || got == nil {
			t.Fatalf("%s/%s: status = %d", tc.user, tc.groups, rec.Code)
		}
		if got.System != tc.system {
			t.Errorf("%s/%s: system = %v, want %v", tc.user, tc.groups, got.System, tc.system)
		}
		if tc.system {
			if len(got.HubGroups) != 0 || got.Token != "" || !got.Cloud {
				t.Errorf("%s: service identity acquired Hub groups or a token", tc.user)
			}
			role := CloudRoleFromContext(ContextWithUser(context.Background(), got))
			if role != RoleViewer || !role.AtLeast(RoleViewer) || role.AtLeast(RoleMember) || role.AtLeast(RoleOwner) {
				t.Errorf("%s: service identity role=%q must allow reads and deny member/owner gates", tc.user, role)
			}
		}
	}
}

func TestUserToken_ReservedForwardedGroupRejected(t *testing.T) {
	userTokenMode(t)
	rec, got := serveUserToken(t, nil, "oidc:alice@example.com", "system:masters,kubeatlas:viewer", "")
	if rec.Code != http.StatusForbidden || got != nil {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestUserToken_OffByDefaultKeepsCloudVocabularyRule(t *testing.T) {
	t.Setenv("KUBEATLAS_CLOUD_MODE", "true")
	rec, got := serveUserToken(t, nil, "oidc:alice@example.com", "k8s-admins,kubeatlas:viewer", "good-token")
	if rec.Code != http.StatusForbidden || got != nil {
		t.Fatalf("status = %d, want impersonate mode to keep rejecting non-kubeatlas groups", rec.Code)
	}
}
