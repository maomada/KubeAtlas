package portal

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/maomada/KubeAtlas/internal/hubstate"
	"golang.org/x/crypto/bcrypt"
)

const setupCodeFile = "setup-code"

// StartupHandler keeps the Hub unavailable until its first local owner exists.
type StartupHandler struct {
	handler *Handler
	ready   atomic.Bool
	mu      sync.Mutex
	code    [32]byte
	path    string
}

// NewStartupHandler uses the persisted local owner, optionally bootstrapping or
// explicitly resetting the credentials with the supplied startup configuration.
func NewStartupHandler(credentials Credentials, static fs.FS, stateFile string, reset bool) (*StartupHandler, error) {
	if credentials.Hub == nil || credentials.Hub.Store == nil || stateFile == "" {
		return nil, errors.New("Hub state store and state file are required")
	}
	store := credentials.Hub.Store
	account, existing := store.LocalPlatformOwner()
	provided := credentials.Username != "" || credentials.Password != ""
	if provided && !validLocalCredentials(credentials.Username, credentials.Password) {
		return nil, errors.New("provide an administrator username and a password of 16-4096 bytes together")
	}
	if reset && !provided {
		return nil, errors.New("--reset-admin requires administrator credentials")
	}
	if existing && account.PasswordHash == "" && !provided {
		return nil, errors.New("existing Hub requires its original administrator credentials or an explicit reset")
	}

	managed := credentials
	managed.managedOwner = true
	managed.Username, managed.Password = "", ""
	handler, err := NewHandler(managed, static)
	if err != nil {
		return nil, err
	}
	h := &StartupHandler{handler: handler}
	switch {
	case existing && account.PasswordHash != "" && (!reset ||
		account.DisplayName == credentials.Username && bcrypt.CompareHashAndPassword([]byte(account.PasswordHash), localPasswordInput(credentials.Password)) == nil):
		h.activate(account)
	case provided:
		if err := h.setOwner(credentials.Username, credentials.Password, reset); err != nil {
			h.Close()
			return nil, err
		}
	default:
		code, err := loadOrCreateSetupCode(stateFile)
		if err != nil {
			h.Close()
			return nil, err
		}
		h.code = sha256.Sum256([]byte(code))
		h.path = filepath.Join(filepath.Dir(stateFile), setupCodeFile)
	}
	return h, nil
}

func (h *StartupHandler) Close() {
	h.handler.Close()
}

func (h *StartupHandler) activate(account hubstate.Account) {
	h.handler.setLocalOwner(account)
	h.ready.Store(true)
	if h.path == "" {
		return
	}
	if err := os.Remove(h.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Print("Could not remove the inactive Hub setup code file")
	}
}

func persistedLocalOwner(credentials Credentials) (hubstate.Account, bool) {
	if credentials.managedOwner || credentials.Hub.Store == nil {
		return hubstate.Account{}, false
	}
	owner, exists := credentials.Hub.Store.LocalPlatformOwner()
	return owner, exists && owner.PasswordHash != ""
}

func (h *Handler) setLocalOwner(account hubstate.Account) {
	h.username = sha256.Sum256([]byte(account.DisplayName))
	h.passwordHash = account.PasswordHash
	h.liveHub.ownerID, h.liveHub.userID, h.liveHub.displayName = account.ID, account.UserID, account.DisplayName
}

// Hash the input for bcrypt so that passwords accepted by older Hub versions
// (up to 4096 bytes) still work, including ones over bcrypt's byte limit.
func localPasswordInput(password string) []byte {
	digest := sha256.Sum256([]byte("kubeatlas.hub.local-owner\x00" + password))
	return []byte(base64.RawStdEncoding.EncodeToString(digest[:]))
}

func (h *StartupHandler) setOwner(username, password string, reset bool) error {
	hash, err := bcrypt.GenerateFromPassword(localPasswordInput(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	owner, err := h.handler.liveHub.store.SetLocalPlatformOwner(username, string(hash), reset)
	if err != nil {
		return err
	}
	h.activate(owner)
	return nil
}

func (h *StartupHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/setup":
		setSecurityHeaders(w)
		h.serveSetup(w, r)
		return
	case "/setup":
		setSecurityHeaders(w)
		if !h.handler.liveHub.policySnapshot().allowsHost(r) {
			hubHTTPError(w, http.StatusForbidden, "permission_denied", "Host is not allowed")
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			hubHTTPError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed")
			return
		}
		if h.ready.Load() {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		h.handler.serveFile(w, r, "portal.html")
		return
	}
	if h.ready.Load() || r.URL.Path == "/healthz" || strings.HasPrefix(r.URL.Path, "/assets/") {
		h.handler.ServeHTTP(w, r)
		return
	}
	setSecurityHeaders(w)
	if r.Method == http.MethodGet && (r.URL.Path == "/" || r.URL.Path == "/login" ||
		r.URL.Path == "/register" || isHubPageRoute(r) && isHubDocumentNavigation(r)) {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	hubHTTPError(w, http.StatusServiceUnavailable, "setup_required", "Hub setup is required")
}

func (h *StartupHandler) serveSetup(w http.ResponseWriter, r *http.Request) {
	policy := h.handler.liveHub.policySnapshot()
	if r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.RawPath != "" || !policy.allows(r) {
		hubHTTPError(w, http.StatusForbidden, "permission_denied", "Host, origin, or request URL is not allowed")
		return
	}
	switch r.Method {
	case http.MethodGet:
		hubJSON(w, http.StatusOK, map[string]bool{"required": !h.ready.Load()})
	case http.MethodPost:
		if !policy.allowsBrowser(r) {
			hubHTTPError(w, http.StatusForbidden, "permission_denied", "Browser origin is not allowed")
			return
		}
		h.completeSetup(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		hubHTTPError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed")
	}
}

func (h *StartupHandler) completeSetup(w http.ResponseWriter, r *http.Request) {
	if h.ready.Load() {
		hubHTTPError(w, http.StatusConflict, "already_initialized", "Hub is already initialized")
		return
	}
	var request struct {
		Code     string `json:"code"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !authDecode(w, r, &request, "code", "username", "password") {
		return
	}
	code := sha256.Sum256([]byte(request.Code))
	if subtle.ConstantTimeCompare(code[:], h.code[:]) != 1 {
		hubHTTPError(w, http.StatusForbidden, "invalid_setup_code", "Setup code is invalid")
		return
	}
	if !validLocalCredentials(request.Username, request.Password) {
		hubHTTPError(w, http.StatusBadRequest, "invalid_parameters", "Enter a valid username and a password of 16-4096 bytes")
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ready.Load() {
		hubHTTPError(w, http.StatusConflict, "already_initialized", "Hub is already initialized")
		return
	}
	if err := h.setOwner(request.Username, request.Password, false); err != nil {
		hubHTTPError(w, http.StatusServiceUnavailable, "store_unavailable", "Could not initialize the Hub")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func requireFreshSetupState(stateFile string) error {
	if strings.TrimSpace(stateFile) == "" || filepath.Base(stateFile) == setupCodeFile {
		return errors.New("set a valid Hub state file path")
	}
	if _, err := os.Lstat(stateFile); !errors.Is(err, os.ErrNotExist) {
		return errors.New("setup code is unavailable when a Hub state file exists")
	}
	return nil
}

func readSetupCodeFile(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() != 44 {
		return "", errors.New("invalid private Hub setup code file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	actual, err := file.Stat()
	if err != nil || !os.SameFile(info, actual) {
		return "", errors.New("Hub setup code file changed")
	}
	data, err := io.ReadAll(io.LimitReader(file, 45))
	code := strings.TrimSuffix(string(data), "\n")
	raw, decodeErr := base64.RawURLEncoding.Strict().DecodeString(code)
	if err != nil || decodeErr != nil || len(data) != 44 || len(raw) != 32 || data[43] != '\n' {
		return "", errors.New("invalid Hub setup code")
	}
	return code, nil
}

// ReadSetupCode is for local operator use. The code is never served over HTTP.
func ReadSetupCode(stateFile string) (string, error) {
	if err := requireFreshSetupState(stateFile); err != nil {
		return "", err
	}
	return readSetupCodeFile(filepath.Join(filepath.Dir(stateFile), setupCodeFile))
}

func loadOrCreateSetupCode(stateFile string) (string, error) {
	if err := requireFreshSetupState(stateFile); err != nil {
		return "", err
	}
	dir := filepath.Dir(stateFile)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return "", errors.New("Hub state directory must be private (0700)")
	}
	path := filepath.Join(dir, setupCodeFile)
	if value, err := readSetupCodeFile(path); !errors.Is(err, os.ErrNotExist) {
		return value, err
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	code := base64.RawURLEncoding.EncodeToString(random[:])
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	_, writeErr := file.WriteString(code + "\n")
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return code, nil
}
