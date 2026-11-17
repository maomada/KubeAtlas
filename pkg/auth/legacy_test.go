package auth

import (
	"context"
	"github.com/maomada/KubeAtlas/pkg/compat"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLegacyCloudTierCannotBecomeLocalBypass(t *testing.T) {
	for _, group := range []string{compat.LegacyTierPrefix + "viewer", "cloud:viewer", "kubeatlas:viewer", "kubeatlas:hub:viewer"} {
		ctx := ContextWithUser(context.Background(), &User{Username: "fixture", Groups: []string{group}, Cloud: true})
		role := CloudRoleFromContext(ctx)
		if role != RoleViewer || role.AtLeast(RoleOwner) {
			t.Fatalf("viewergroup%q role=%q", group, role)
		}
	}
	unknown := ContextWithUser(context.Background(), &User{Username: "fixture", Groups: []string{"kubeatlas:unknown"}, Cloud: true})
	if CloudRoleFromContext(unknown).AtLeast(RoleViewer) {
		t.Fatal("unrecognized cloud tier bypassed gate")
	}
	if !RoleNone.AtLeast(RoleOwner) {
		t.Fatal("standalone behavior changed")
	}
	if !IsKubeAtlasVocabularyGroup(compat.LegacyTierPrefix + "owner") {
		t.Fatal("old reserved vocabulary became an IdP group")
	}
	if IsSystemIdentity("kubeatlas:system:job", []string{compat.LegacySystemGroup}) {
		t.Fatal("mixed synthetic identities accepted")
	}
}

func TestSignedLegacySessionRotationAndMixedIdentityDenial(t *testing.T) {
	secret := "fixture-signing-key"
	cookie := mainCookie(t, CreateSessionCookie(&User{Username: "old-user"}, NewSessionID(), "", secret, time.Hour, false))
	old := *cookie
	old.Name = compat.LegacySessionCookie
	r := httptest.NewRequest("GET", "http://localhost", nil)
	r.AddCookie(&old)
	if got := ParseSessionCookie(r, secret); got == nil || got.User.Username != "old-user" {
		t.Fatal("signed old session lost")
	}
	r.AddCookie(cookie)
	if ParseSessionCookie(r, secret) != nil {
		t.Fatal("mixed naming families accepted")
	}
	for _, name := range []string{DefaultCookieName, compat.LegacySessionCookie} {
		duplicate := httptest.NewRequest("GET", "http://localhost", nil)
		duplicate.AddCookie(&http.Cookie{Name: name, Value: cookie.Value})
		duplicate.AddCookie(&http.Cookie{Name: name, Value: cookie.Value})
		if ParseSessionCookie(duplicate, secret) != nil {
			t.Fatal("duplicate session name accepted")
		}
	}
	cleared := ClearSessionCookie(r)
	seen := map[string]bool{}
	for _, c := range cleared {
		if c.MaxAge < 0 {
			seen[c.Name] = true
		}
	}
	if !seen[DefaultCookieName] || !seen[compat.LegacySessionCookie] || !seen[compat.LegacySessionCookie+cookieChunkCountSuffix] {
		t.Fatal("logout left old session representation")
	}
}
