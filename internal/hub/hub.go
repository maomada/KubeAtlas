package hub

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"

	"github.com/maomada/KubeAtlas/internal/cloud"
	pkgauth "github.com/maomada/KubeAtlas/pkg/auth"
)

const (
	maxAgents       = 100
	maxAgentStreams = 32
	maxTokenBytes   = 4096
	maxVersionBytes = 128
	maxNameBytes    = 256
)

var clusterIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

type AgentIdentity struct {
	ClusterID string
	Name      string
}

type AgentAuthenticator func(context.Context, string, string) (AgentIdentity, error)

type Config struct {
	AuthenticateAgent AgentAuthenticator
}

type Principal struct {
	UserID     string
	Groups     []string
	ClusterIDs []string
	ReadOnly   bool
}

type Agent struct {
	ClusterID      string    `json:"clusterId"`
	Name           string    `json:"name"`
	Version        string    `json:"version"`
	CredentialMode string    `json:"credentialMode"`
	ConnectionID   string    `json:"connectionId"`
	ConnectedAt    time.Time `json:"connectedAt"`
}

type ErrorCode string

const (
	CodeInvalidParameters  ErrorCode = "invalid_parameters"
	CodePermissionDenied   ErrorCode = "permission_denied"
	CodeClusterUnavailable ErrorCode = "cluster_unavailable"
	CodeSourceDisconnected ErrorCode = "source_disconnected"
	CodeSourceChanged      ErrorCode = "source_changed"
	CodeSourceTimeout      ErrorCode = "source_timeout"
)

type Error struct {
	Code           ErrorCode
	OutcomeUnknown bool
	cause          error
}

func (e *Error) Error() string {
	switch e.Code {
	case CodeInvalidParameters:
		return "invalid tunnel request"
	case CodePermissionDenied:
		return "tunnel request is not permitted"
	case CodeClusterUnavailable:
		return "cluster is unavailable"
	case CodeSourceChanged:
		return "cluster connection changed before the request was sent"
	case CodeSourceTimeout:
		if e.OutcomeUnknown {
			return "source timed out after dispatch; outcome is unknown"
		}
		return "source request timed out"
	default:
		if e.OutcomeUnknown {
			return "source disconnected after dispatch; outcome is unknown"
		}
		return "source is disconnected"
	}
}

func (e *Error) Unwrap() error { return e.cause }

func tunnelError(code ErrorCode, unknown bool, cause error) error {
	return &Error{Code: code, OutcomeUnknown: unknown, cause: cause}
}

type Hub struct {
	mu     sync.RWMutex
	config Config
	agents map[string]*agentState
	closed bool
}

type agentState struct {
	mu        sync.Mutex
	agent     Agent
	session   *yamux.Session
	conn      *websocket.Conn
	active    bool
	closed    bool
	slots     chan struct{}
	closeOnce sync.Once
}

func New(config Config) (*Hub, error) {
	if config.AuthenticateAgent == nil {
		return nil, errors.New("hub agent authenticator is required")
	}
	return &Hub{config: config, agents: make(map[string]*agentState)}, nil
}

func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/agent" || (r.URL.RawPath != "" && r.URL.RawPath != "/agent") {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if len(r.Header.Values("Origin")) > 0 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !websocket.IsWebSocketUpgrade(r) {
		http.Error(w, "websocket upgrade required", http.StatusUpgradeRequired)
		return
	}
	authorization := r.Header.Values("Authorization")
	if len(authorization) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	token, ok := bearerToken(authorization[0])
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if len(r.URL.RawQuery) > 2048 {
		http.Error(w, "invalid cluster identity", http.StatusBadRequest)
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		http.Error(w, "invalid cluster identity", http.StatusBadRequest)
		return
	}
	ids := query["cluster_id"]
	if len(ids) != 1 || !validClusterID(ids[0]) {
		http.Error(w, "invalid cluster identity", http.StatusBadRequest)
		return
	}
	claimedID := ids[0]
	modes := r.Header.Values("X-Radar-Kubernetes-Credentials")
	if len(modes) != 1 || modes[0] != "impersonate" {
		http.Error(w, "unsupported Kubernetes credential mode", http.StatusForbidden)
		return
	}
	identity, err := h.config.AuthenticateAgent(r.Context(), token, claimedID)
	if err != nil || identity.ClusterID != claimedID || !validDisplayName(identity.Name, maxNameBytes) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	versions := r.Header.Values("X-Radar-Version")
	if len(versions) > 1 || (len(versions) == 1 && versions[0] != "" && !validDisplayName(versions[0], maxVersionBytes)) {
		http.Error(w, "invalid agent metadata", http.StatusBadRequest)
		return
	}
	version := ""
	if len(versions) == 1 {
		version = versions[0]
	}
	connectionID, err := newConnectionID()
	if err != nil {
		http.Error(w, "agent connection unavailable", http.StatusServiceUnavailable)
		return
	}
	state := &agentState{
		agent: Agent{
			ClusterID: claimedID, Name: identity.Name, Version: version,
			CredentialMode: "impersonate", ConnectionID: connectionID,
		},
		slots: make(chan struct{}, maxAgentStreams),
	}
	if err := h.reserve(state); err != nil {
		if errors.Is(err, errDuplicateAgent) {
			http.Error(w, "cluster agent already connected", http.StatusConflict)
		} else {
			http.Error(w, "hub is unavailable", http.StatusServiceUnavailable)
		}
		return
	}
	keepReservation := false
	defer func() {
		if !keepReservation {
			h.remove(state)
			state.shutdown()
		}
	}()

	conn, err := (&websocket.Upgrader{
		HandshakeTimeout: 10 * time.Second,
		CheckOrigin:      func(request *http.Request) bool { return request.Header.Get("Origin") == "" },
	}).Upgrade(w, r, nil)
	if err != nil {
		return
	}
	if !state.setConn(conn) {
		_ = conn.Close()
		return
	}
	session, err := cloud.NewHubSession(conn)
	if err != nil {
		return
	}
	if !state.setSession(session) || !h.activate(state) {
		state.shutdown()
		return
	}
	keepReservation = true
	go h.watchSession(state)
	<-session.CloseChan()
	h.remove(state)
	state.shutdown()
}

var errDuplicateAgent = errors.New("duplicate agent")
var errHubClosed = errors.New("hub closed")

func (h *Hub) reserve(state *agentState) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return errHubClosed
	}
	if _, exists := h.agents[state.agent.ClusterID]; exists {
		return errDuplicateAgent
	}
	if len(h.agents) >= maxAgents {
		return errHubClosed
	}
	h.agents[state.agent.ClusterID] = state
	return nil
}

func (h *Hub) activate(state *agentState) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || h.agents[state.agent.ClusterID] != state {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed || state.session == nil {
		return false
	}
	state.agent.ConnectedAt = time.Now().UTC()
	state.active = true
	return true
}

func (h *Hub) watchSession(state *agentState) {
	<-state.session.CloseChan()
	h.remove(state)
	state.shutdown()
}

func (h *Hub) remove(state *agentState) {
	h.mu.Lock()
	if h.agents[state.agent.ClusterID] == state {
		delete(h.agents, state.agent.ClusterID)
	}
	h.mu.Unlock()
}

func (h *Hub) Agents() []Agent {
	h.mu.RLock()
	result := make([]Agent, 0, len(h.agents))
	for _, state := range h.agents {
		state.mu.Lock()
		if state.active && !state.closed {
			result = append(result, state.agent)
		}
		state.mu.Unlock()
	}
	h.mu.RUnlock()
	slices.SortFunc(result, func(a, b Agent) int {
		if a.ClusterID < b.ClusterID {
			return -1
		}
		if a.ClusterID > b.ClusterID {
			return 1
		}
		return 0
	})
	return result
}

func (h *Hub) Disconnect(clusterID string) {
	h.mu.Lock()
	state := h.agents[clusterID]
	delete(h.agents, clusterID)
	h.mu.Unlock()
	if state != nil {
		state.shutdown()
	}
}

func (h *Hub) Close() error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	h.closed = true
	states := make([]*agentState, 0, len(h.agents))
	for _, state := range h.agents {
		states = append(states, state)
	}
	h.agents = make(map[string]*agentState)
	h.mu.Unlock()
	for _, state := range states {
		state.shutdown()
	}
	return nil
}

func (s *agentState) setConn(conn *websocket.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.conn = conn
	return true
}

func (s *agentState) setSession(session *yamux.Session) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		_ = session.Close()
		return false
	}
	s.session = session
	return true
}

func (s *agentState) currentSession() (*yamux.Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.session, s.active && !s.closed && s.session != nil
}

func (s *agentState) shutdown() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.active = false
		session, conn := s.session, s.conn
		s.mu.Unlock()
		if session != nil {
			_ = session.Close()
		} else if conn != nil {
			_ = conn.Close()
		}
	})
}

func bearerToken(value string) (string, bool) {
	if len(value) < len("Bearer ")+1 || len(value) > maxTokenBytes+len("Bearer ") {
		return "", false
	}
	scheme, token, ok := strings.Cut(value, " ")
	return token, ok && strings.EqualFold(scheme, "Bearer") && token != "" &&
		!strings.ContainsAny(token, " \t\r\n")
}

func validClusterID(id string) bool { return clusterIDPattern.MatchString(id) }

func validDisplayName(value string, maxBytes int) bool {
	if value == "" || len(value) > maxBytes || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return false
		}
	}
	return true
}

func newConnectionID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func principalAllowed(principal Principal, clusterID string) bool {
	if !validDisplayName(principal.UserID, 256) || !slices.Contains(principal.ClusterIDs, clusterID) ||
		len(principal.ClusterIDs) == 0 || len(principal.ClusterIDs) > maxAgents ||
		len(principal.Groups) > 128 || !pkgauth.ForwardedIdentityAllowed(principal.UserID, principal.Groups, true) {
		return false
	}
	groupBytes := 0
	for _, id := range principal.ClusterIDs {
		if !validClusterID(id) {
			return false
		}
	}
	for _, group := range principal.Groups {
		if !validDisplayName(group, 256) || strings.Contains(group, ",") {
			return false
		}
		groupBytes += len(group) + 1
		if groupBytes > 8192 {
			return false
		}
	}
	return true
}
