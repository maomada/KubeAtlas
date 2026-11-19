package hubstate

import (
	"bytes"
	"github.com/maomada/KubeAtlas/pkg/compat"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestDeployedCustomIdentityGroupsPersistAndReopenVerbatim(t *testing.T) {
	store, _ := testStore(t)
	cluster(t, store, "alpha")
	id := "oidc:" + strings.Repeat("d", 64)
	if _, _, err := store.UpsertOIDCMember(id, "Legacy custom groups"); err != nil {
		t.Fatal(err)
	}
	groups := []string{"cloud:custom-reader/team", compat.LegacyTierPrefix + "custom-reader/team"}
	if _, _, err := store.UpdateMember(id, MemberInput{Role: "viewer", Status: "active", ClusterIDs: []string{"alpha"}, KubernetesGroups: groups}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(store.path)
	if err != nil {
		t.Fatal("deployed custom identity made stored state unreadable:", err)
	}
	member, ok := reopened.MemberForOrganization("self-hosted", id)
	if !ok || !slices.Equal(member.KubernetesGroups, groups) || member.Role != "viewer" || !slices.Equal(member.ClusterIDs, []string{"alpha"}) {
		t.Fatal("persisted group or authority scope changed")
	}
	after, err := os.ReadFile(store.path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("opening deployed groups rewrote stored state")
	}
	for _, invalid := range [][]string{{"kubeatlas:custom-reader"}, {"system:masters"}, {compat.LegacyTierPrefix + "org:"}} {
		if _, _, err := reopened.UpdateMember(id, MemberInput{Role: "viewer", Status: "active", ClusterIDs: []string{"alpha"}, KubernetesGroups: invalid}); err == nil {
			t.Fatalf("unsafe new group accepted:%v", invalid)
		}
	}
}
