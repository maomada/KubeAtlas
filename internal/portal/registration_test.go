package portal

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func postRegistration(h *Handler, body []byte) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "http://localhost/api/auth/register", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://localhost")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, r)
	return response
}

func TestRegistrationRequiresSixUnicodeCharactersAndAtMost72Bytes(t *testing.T) {
	for _, test := range []struct {
		password string
		valid    bool
	}{
		{"abcde", false},
		{"abcdef", true},
		{strings.Repeat("é", 5), false},
		{strings.Repeat("é", 6), true},
		{strings.Repeat("💩", 5), false},
		{strings.Repeat("💩", 6), true},
		{strings.Repeat("💩", 18), true},
		{strings.Repeat("💩", 19), false},
		{strings.Repeat("a", 72), true},
		{strings.Repeat("a", 73), false},
		{string([]byte{0xff}) + strings.Repeat("a", 6), false},
	} {
		if got := validRegistrationPassword(test.password); got != test.valid {
			t.Errorf("password bytes=%d, valid=%v; want %v", len(test.password), got, test.valid)
		}
	}
	h, statePath := newPortalHubFixture(t, false)
	password := strings.Repeat("💩", 6)
	body, err := json.Marshal(map[string]string{"email": " Alice@Example.COM ", "password": password})
	if err != nil {
		t.Fatal(err)
	}
	response := postRegistration(h, body)
	if response.Code != http.StatusCreated || strings.TrimSpace(response.Body.String()) != "{\"status\":\"pending\"}" {
		t.Fatalf("registration response=%d %s", response.Code, response.Body.String())
	}
	if len(response.Result().Cookies()) != 0 {
		t.Fatal("registration created a session")
	}
	account, ok := h.liveHub.store.AccountByEmail("alice@example.com")
	if !ok || account.Status != "pending" || account.PasswordHash == "" || strings.Contains(account.PasswordHash, password) {
		t.Fatalf("registered account=%#v ok=%v", account, ok)
	}
	if memberships := h.liveHub.store.OrganizationsForAccount(account.ID); len(memberships) != 0 {
		t.Fatalf("pending registration received memberships: %#v", memberships)
	}
	data, err := os.ReadFile(statePath)
	if err != nil || bytes.Contains(data, []byte(password)) {
		t.Fatalf("plaintext password persisted: err=%v", err)
	}
	duplicate, _ := json.Marshal(map[string]string{"email": "alice@example.com", "password": password})
	if got := postRegistration(h, duplicate).Code; got != http.StatusConflict {
		t.Fatalf("canonical duplicate status=%d", got)
	}
	invalid := append([]byte("{\"email\":\"bad@example.com\",\"password\":\"abcdefghijk"), 0xff)
	invalid = append(invalid, []byte("\"}")...)
	if got := postRegistration(h, invalid).Code; got != http.StatusBadRequest {
		t.Fatalf("invalid UTF-8 status=%d", got)
	}
	if got := postRegistration(h, []byte("{\"email\":\"a@example.com\",\"email\":\"b@example.com\",\"password\":\"123456789012\"}")).Code; got != http.StatusBadRequest {
		t.Fatalf("duplicate JSON key status=%d", got)
	}
}

func TestRegistrationHTTPAcceptsSixCharactersAndRejectsFive(t *testing.T) {
	for _, test := range []struct {
		name     string
		password string
		status   int
	}{
		{"five ASCII characters", "abcde", http.StatusBadRequest},
		{"six ASCII characters", "abcdef", http.StatusCreated},
		{"five Unicode characters", strings.Repeat("😀", 5), http.StatusBadRequest},
		{"six Unicode characters", strings.Repeat("😀", 6), http.StatusCreated},
	} {
		t.Run(test.name, func(t *testing.T) {
			h, _ := newPortalHubFixture(t, false)
			body, err := json.Marshal(map[string]string{"email": "boundary@example.test", "password": test.password})
			if err != nil {
				t.Fatal(err)
			}
			response := postRegistration(h, body)
			if response.Code != test.status {
				t.Fatalf("registration status=%d, want %d", response.Code, test.status)
			}
			if len(response.Result().Cookies()) != 0 {
				t.Fatal("registration created a session")
			}
			account, exists := h.liveHub.store.AccountByEmail("boundary@example.test")
			if exists != (test.status == http.StatusCreated) {
				t.Fatalf("account exists=%v, want %v", exists, test.status == http.StatusCreated)
			}
			if exists && (account.Status != "pending" || len(h.liveHub.store.OrganizationsForAccount(account.ID)) != 0) {
				t.Fatal("registration bypassed approval or assigned organization access")
			}
		})
	}
}
