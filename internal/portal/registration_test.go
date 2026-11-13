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

func TestRegistrationRequiresTwelveUnicodeCharactersAndAtMost72Bytes(t *testing.T) {
	for _, test := range []struct {
		password string
		valid    bool
	}{
		{strings.Repeat("é", 12), true},
		{strings.Repeat("💩", 18), true},
		{strings.Repeat("💩", 11), false},
		{strings.Repeat("💩", 19), false},
		{string([]byte{0xff}) + strings.Repeat("a", 12), false},
	} {
		if got := validRegistrationPassword(test.password); got != test.valid {
			t.Errorf("password bytes=%d, valid=%v; want %v", len(test.password), got, test.valid)
		}
	}
	h, statePath := newPortalHubFixture(t, false)
	password := strings.Repeat("💩", 12)
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
