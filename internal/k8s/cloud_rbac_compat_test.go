package k8s

import (
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/maomada/KubeAtlas/pkg/compat"
	rbacv1 "k8s.io/api/rbac/v1"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
)

func renderedCloudBindingDocuments(t *testing.T, overrides string) []integrationRBACDocument {
	t.Helper()
	rendered, _ := renderIntegrationReadChart(t, overrides, false, false)
	var documents []integrationRBACDocument
	for name, content := range rendered {
		if !strings.HasPrefix(name, "kubeatlas/templates/cloud-rbac") {
			continue
		}
		decoder := yamlutil.NewYAMLOrJSONDecoder(strings.NewReader(content), 4096)
		for {
			var document integrationRBACDocument
			if err := decoder.Decode(&document); err == io.EOF {
				break
			} else if err != nil {
				t.Fatalf("parse %s: %v", name, err)
			}
			if document.Kind != "" {
				documents = append(documents, document)
			}
		}
	}
	return documents
}

func TestCloudBindingsRetainDeployedIdentitySubjects(t *testing.T) {
	docs := renderedCloudBindingDocuments(t, "cloud.systemRbac=true,cloud.aiRbac=true,rbac.helm=true,cloud.defaultRbac.nodeOps=true")
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
		disabled := renderedCloudBindingDocuments(t, flags)
		basic := map[string]string{}
		for _, doc := range disabled {
			if doc.Kind == "ClusterRoleBinding" && strings.HasPrefix(doc.Name, "kubeatlas-cloud-") {
				if flags != "rbac.create=false" {
					t.Fatalf("opt-out %s retained %s", flags, doc.Name)
				}
				// Standard admin/edit/view bindings have their own defaultRbac
				// switch; rbac.create only disables chart-owned add-on roles.
				basic[doc.Name] = doc.RoleRef.Name
			}
		}
		if flags == "rbac.create=false" && !reflect.DeepEqual(basic, map[string]string{
			"kubeatlas-cloud-owner": "admin", "kubeatlas-cloud-member": "edit", "kubeatlas-cloud-viewer": "view",
		}) {
			t.Fatalf("rbac.create changed standard-role bindings or retained add-ons: %v", basic)
		}
	}
}
