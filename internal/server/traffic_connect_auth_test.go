package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/maomada/KubeAtlas/internal/auth"
	"github.com/maomada/KubeAtlas/internal/cloud"
	"github.com/maomada/KubeAtlas/internal/k8s"
	authv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func TestTrafficConnectTunnelAuthority(t *testing.T) {
	for _, tc := range []struct {
		name       string
		user       *auth.User
		allowed    bool
		fail       bool
		noClient   bool
		wantStatus int
	}{
		{"admin", &auth.User{Username: "admin", Groups: []string{"test-admins"}}, true, false, false, http.StatusNoContent},
		{"restricted", &auth.User{Username: "reader"}, false, false, false, http.StatusForbidden},
		{"missing identity", nil, true, false, false, http.StatusForbidden},
		{"SAR failure", &auth.User{Username: "admin"}, true, true, false, http.StatusServiceUnavailable},
		{"missing Kubernetes client", &auth.User{Username: "admin"}, true, false, true, http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			var requestError string
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/apis/authorization.k8s.io/v1/subjectaccessreviews" {
					http.NotFound(w, r)
					return
				}
				calls++
				var request authv1.SubjectAccessReview
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					requestError = err.Error()
					http.Error(w, "invalid request", http.StatusBadRequest)
					return
				}
				attrs := request.Spec.ResourceAttributes
				if tc.user == nil || request.Spec.User != tc.user.Username || attrs == nil || attrs.Verb != "*" || attrs.Group != "*" || attrs.Resource != "*" || attrs.Namespace != "" {
					requestError = fmt.Sprintf("wrong authority check: %+v", request.Spec)
					http.Error(w, "invalid request", http.StatusBadRequest)
					return
				}
				if tc.fail {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					_ = json.NewEncoder(w).Encode(&metav1.Status{
						TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"},
						Status:   metav1.StatusFailure,
						Message:  "apiserver unavailable",
						Reason:   metav1.StatusReasonInternalError,
						Code:     http.StatusInternalServerError,
					})
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(&authv1.SubjectAccessReview{
					TypeMeta: metav1.TypeMeta{APIVersion: "authorization.k8s.io/v1", Kind: "SubjectAccessReview"},
					Status:   authv1.SubjectAccessReviewStatus{Allowed: tc.allowed},
				})
			}))
			defer api.Close()
			var client *kubernetes.Clientset
			if !tc.noClient {
				client = kubernetes.NewForConfigOrDie(&rest.Config{
					Host: api.URL,
					ContentConfig: rest.ContentConfig{
						ContentType:        "application/json",
						AcceptContentTypes: "application/json",
					},
				})
			}
			old := k8s.SetTestClient(client)
			defer k8s.SetTestClient(old)
			s := &Server{}
			r := requestWithUser(http.MethodPost, "/api/traffic/connect-managed", tc.user)
			w := httptest.NewRecorder()
			cloud.AuthenticatedTunnelHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.wantStatus != http.StatusNoContent {
					s.handleTrafficConnect(w, r)
					return
				}
				if s.requireTrafficConnectAuthority(w, r) {
					w.WriteHeader(http.StatusNoContent)
				}
			})).ServeHTTP(w, r)
			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", w.Code, tc.wantStatus)
			}
			if requestError != "" {
				t.Fatal(requestError)
			}
			wantCalls := 0
			if tc.user != nil && !tc.noClient {
				wantCalls = 1
			}
			if calls != wantCalls {
				t.Fatalf("SAR calls = %d, want %d", calls, wantCalls)
			}
		})
	}
}

func TestTrafficConnectLocalModeUnchanged(t *testing.T) {
	s := &Server{}
	if !s.requireTrafficConnectAuthority(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/traffic/connect", nil)) {
		t.Fatal("local kubeconfig behavior changed")
	}
}
