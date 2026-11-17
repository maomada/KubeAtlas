package k8s

import (
	"reflect"
	"strings"
	"testing"

	"github.com/maomada/KubeAtlas/pkg/compat"
	rbacv1 "k8s.io/api/rbac/v1"
)

func TestCloudBindingsRetainDeployedIdentitySubjects(t *testing.T) {
	_, docs := renderIntegrationReadChart(t, "cloud.systemRbac=true,cloud.aiRbac=true,rbac.helm=true,cloud.defaultRbac.nodeOps=true", false, false)
	counts := map[string]int{}
	for _, doc := range docs {
		if doc.Kind != "ClusterRoleBinding" || !strings.HasPrefix(doc.Name, "kubeatlas-cloud-") {
			continue
		}
		if len(doc.Subjects) == 0 {
			t.Fatalf("empty binding %s", doc.Name)
		}
		group := doc.Subjects[0].Name
		role := strings.TrimPrefix(group, "kubeatlas:")
		if !strings.HasPrefix(group, "kubeatlas:") {
			t.Fatalf("missing canonical identity in %s", doc.Name)
		}
		var legacy string
		switch role {
		case "owner", "member", "viewer", "system":
			legacy = compat.LegacyTierPrefix + role
		case "ai:reader":
			legacy = compat.LegacyAIReaderGroup
		default:
			t.Fatalf("unexpected identity %s in %s", group, doc.Name)
		}
		want := []rbacv1.Subject{
			{Kind: "Group", Name: group, APIGroup: rbacv1.GroupName},
			{Kind: "Group", Name: legacy, APIGroup: rbacv1.GroupName},
		}
		if role == "owner" || role == "member" || role == "viewer" {
			want = append(want, rbacv1.Subject{Kind: "Group", Name: "cloud:" + role, APIGroup: rbacv1.GroupName})
		}
		if !reflect.DeepEqual(doc.Subjects, want) {
			t.Fatalf("%s subjects=%+v, want exactly %+v", doc.Name, doc.Subjects, want)
		}
		counts[role]++
	}
	if !reflect.DeepEqual(counts, map[string]int{"owner": 7, "member": 5, "viewer": 4, "system": 5, "ai:reader": 4}) {
		t.Fatalf("missing or unexpected Cloud bindings: %v", counts)
	}
	for _, flags := range []string{"cloud.systemRbac=false,cloud.aiRbac=false,cloud.defaultRbac.create=false", "rbac.create=false"} {
		_, disabled := renderIntegrationReadChart(t, flags, false, false)
		for _, doc := range disabled {
			if doc.Kind == "ClusterRoleBinding" && strings.HasPrefix(doc.Name, "kubeatlas-cloud-") {
				t.Fatalf("opt-out %s retained %s", flags, doc.Name)
			}
		}
	}
}
