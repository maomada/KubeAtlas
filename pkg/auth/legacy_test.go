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

func TestCloudSystemIdentityWithEmptyHubGroupsIsReadOnly(t *testing.T) {
	for _, pair := range [][2]string{{"kubeatlas:system:fixture", "kubeatlas:system"}, {"kubeatlas:ai:fixture", "kubeatlas:ai:reader"}, {compat.LegacySystemUserPrefix + "fixture", compat.LegacySystemGroup}, {compat.LegacyAIUserPrefix + "fixture", compat.LegacyAIReaderGroup}} {
		user := &User{Username: pair[0], Groups: []string{pair[1]}, HubGroups: []string{}, Cloud: true, System: true}
		role := CloudRoleFromContext(ContextWithUser(context.Background(), user))
		if role != RoleViewer || !role.AtLeast(RoleViewer) || role.AtLeast(RoleMember) || role.AtLeast(RoleOwner) {
			t.Fatalf("service identity role=%q", role)
		}
		if len(user.HubGroups) != 0 {
			t.Fatal("role resolution injected Hub groups")
		}
	}
	for _, user := range []*User{
		{Username: "kubeatlas:ai:fixture", Groups: []string{"kubeatlas:system"}, HubGroups: []string{}, Cloud: true, System: true},
		{Username: "human", Groups: []string{"kubeatlas:owner"}, Cloud: true, System: true},
		{Username: "kubeatlas:system:fixture", Groups: []string{"kubeatlas:system", "kubeatlas:owner"}, Cloud: true, System: true},
		{Username: "human", Groups: []string{"kubeatlas:unknown"}, Cloud: true},
	} {
		if role := CloudRoleFromContext(ContextWithUser(context.Background(), user)); role != RoleDenied || role.AtLeast(RoleViewer) {
			t.Fatalf("unverified service identity role=%q", role)
		}
	}
}

func TestDuplicateMainCookieWithEmptyDeletionValueStillRejected(t *testing.T) {
	const secret = "fixture-signing-key"
	primary := mainCookie(t, CreateSessionCookie(&User{Username: "fixture"}, NewSessionID(), "", secret, time.Hour, false))
	for _, name := range []string{DefaultCookieName, compat.LegacySessionCookie} {
		r := httptest.NewRequest("GET", "http://localhost", nil)
		r.AddCookie(&http.Cookie{Name: name, Value: primary.Value})
		r.AddCookie(&http.Cookie{Name: name, Value: ""})
		if ParseSessionCookie(r, secret) != nil {
			t.Fatal("raw duplicate main cookie with empty value accepted")
		}
	}
}
