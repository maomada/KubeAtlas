package portal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/maomada/KubeAtlas/internal/fleetsync"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const agentHandshakeTimeout = 10 * time.Second

type AgentHub struct {
	mu     sync.RWMutex
	store  *fleetsync.Store
	agents map[string]*agentConnection
	closed bool
}

type agentConnection struct {
	clusterID string
	sourceID  string
	tools     []*mcpsdk.Tool
	conn      *websocket.Conn
	hub       *AgentHub

	writeMu  sync.Mutex
	mu       sync.Mutex
	pending  map[string]chan agentReply
	done     chan struct{}
	close    sync.Once
	prefix   string
	sequence atomic.Uint64
}

type agentReply struct {
	result *mcpsdk.CallToolResult
	err    string
}

func NewAgentHub(store *fleetsync.Store) *AgentHub {
	return &AgentHub{store: store, agents: make(map[string]*agentConnection)}
}

func (h *AgentHub) Close() {
	if h == nil {
		return
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	agents := make([]*agentConnection, 0, len(h.agents))
	for _, agent := range h.agents {
		agents = append(agents, agent)
	}
	h.agents = make(map[string]*agentConnection)
	h.mu.Unlock()
	for _, agent := range agents {
		agent.shutdown()
	}
}

func (h *AgentHub) ServeAgentChannel(w http.ResponseWriter, r *http.Request, clusterID string) {
	if h == nil || h.store == nil {
		writeFleetHTTPError(w, http.StatusServiceUnavailable, "Fleet synchronization is unavailable")
		return
	}
	if r.Method != http.MethodGet {
		writeFleetHTTPError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !fleetsync.ValidateID(clusterID) {
		writeFleetHTTPError(w, http.StatusNotFound, "not found")
		return
	}
	h.mu.RLock()
	closed := h.closed
	_, duplicate := h.agents[clusterID]
	h.mu.RUnlock()
	if closed {
		writeFleetHTTPError(w, http.StatusServiceUnavailable, "Fleet agent hub is shutting down")
		return
	}
	if duplicate {
		writeFleetHTTPError(w, http.StatusConflict, "a source channel is already active")
		return
	}

	conn, err := (&websocket.Upgrader{HandshakeTimeout: agentHandshakeTimeout}).Upgrade(w, r, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(fleetsync.MaxAgentFrameBytes)
	_ = conn.SetReadDeadline(time.Now().Add(agentHandshakeTimeout))
	_, payload, err := conn.ReadMessage()
	if err != nil {
		_ = conn.Close()
		return
	}
	var hello fleetsync.AgentMessage
	if err := json.Unmarshal(payload, &hello); err != nil || hello.Type != "hello" ||
		hello.SourceID == "" || len(hello.SourceID) > 256 ||
		!h.store.SourceMatches(clusterID, hello.SourceID) ||
		fleetsync.ValidateToolCatalog(hello.Tools) != nil {
		_ = conn.WriteJSON(fleetsync.AgentMessage{Type: "error", Error: "source identity or tool catalog is invalid"})
		_ = conn.Close()
		return
	}
	_ = conn.SetReadDeadline(time.Now().Add(fleetsync.AgentPongWait))
	agent := &agentConnection{
		clusterID: clusterID, sourceID: hello.SourceID, tools: hello.Tools, conn: conn, hub: h,
		pending: make(map[string]chan agentReply), done: make(chan struct{}), prefix: agentSessionPrefix(),
	}
	agent.conn.SetPongHandler(func(string) error {
		return agent.conn.SetReadDeadline(time.Now().Add(fleetsync.AgentPongWait))
	})
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		_ = conn.WriteJSON(fleetsync.AgentMessage{Type: "error", Error: "agent hub is shutting down"})
		_ = conn.Close()
		return
	}
	if _, exists := h.agents[clusterID]; exists {
		h.mu.Unlock()
		_ = conn.WriteJSON(fleetsync.AgentMessage{Type: "error", Error: "a source channel is already active"})
		_ = conn.Close()
		return
	}
	h.agents[clusterID] = agent
	h.mu.Unlock()

	pingDone := make(chan struct{})
	go agent.ping(r.Context(), pingDone)
	defer close(pingDone)
	defer h.removeAgent(agent)
	for {
		_, payload, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if len(payload) > fleetsync.MaxAgentFrameBytes {
			return
		}
		var message fleetsync.AgentMessage
		if err := json.Unmarshal(payload, &message); err != nil {
			return
		}
		switch message.Type {
		case "result":
			agent.receiveResult(message)
		default:
			return
		}
	}
}

func (h *AgentHub) ListClusterTools(_ context.Context, selection fleetsync.Selection) ([]fleetsync.ClusterTools, error) {
	if h == nil || h.store == nil {
		return nil, errors.New("Fleet synchronization is unavailable")
	}
	var clusterIDs []string
	switch {
	case selection.ClusterID != "" && selection.FleetID == "" && fleetsync.ValidateID(selection.ClusterID):
		clusterIDs = []string{selection.ClusterID}
	case selection.ClusterID == "" && fleetsync.ValidateID(selection.FleetID):
		found := false
		for _, group := range h.store.Groups() {
			if group.ID == selection.FleetID {
				clusterIDs = append(make([]string, 0, len(group.ClusterIDs)), group.ClusterIDs...)
				found = true
				break
			}
		}
		if !found {
			return nil, errors.New("unknown fleetId")
		}
	default:
		return nil, errors.New("exactly one valid clusterId or fleetId is required")
	}
	result := make([]fleetsync.ClusterTools, 0, len(clusterIDs))
	for _, clusterID := range clusterIDs {
		item := fleetsync.ClusterTools{ClusterID: clusterID, State: "unavailable", Tools: []*mcpsdk.Tool{}}
		if agent := h.connectionFor(clusterID); agent != nil {
			item.State = "connected"
			item.Tools = cloneToolCatalog(agent.tools)
		}
		result = append(result, item)
	}
	return result, nil
}

func (h *AgentHub) CallClusterTool(ctx context.Context, request fleetsync.ClusterToolCall) (*mcpsdk.CallToolResult, error) {
	if h == nil || h.store == nil {
		return nil, errors.New("Fleet synchronization is unavailable")
	}
	if !fleetsync.ValidateID(request.ClusterID) || !fleetsync.ValidToolName(request.Tool) {
		return nil, errors.New("clusterId and tool are required")
	}
	seconds, err := fleetsync.NormalizeToolTimeout(request.TimeoutSeconds)
	if err != nil {
		return nil, err
	}
	if request.Arguments == nil {
		request.Arguments = map[string]any{}
	}
	if request.FleetID != "" {
		if !fleetContains(h.store.Groups(), request.FleetID, request.ClusterID) {
			return nil, errors.New("clusterId is not a member of fleetId")
		}
	}
	agent := h.connectionFor(request.ClusterID)
	if agent == nil {
		return nil, fleetsync.ErrAgentUnavailable
	}
	return agent.call(ctx, request, seconds)
}

func fleetContains(groups []fleetsync.Group, fleetID, clusterID string) bool {
	for _, group := range groups {
		if group.ID == fleetID {
			return slices.Contains(group.ClusterIDs, clusterID)
		}
	}
	return false
}

func (h *AgentHub) connectionFor(clusterID string) *agentConnection {
	h.mu.RLock()
	agent := h.agents[clusterID]
	h.mu.RUnlock()
	if agent == nil {
		return nil
	}
	if !h.store.SourceMatches(clusterID, agent.sourceID) {
		h.removeAgent(agent)
		return nil
	}
	select {
	case <-agent.done:
		return nil
	default:
		return agent
	}
}

func (h *AgentHub) removeAgent(agent *agentConnection) {
	h.mu.Lock()
	if h.agents[agent.clusterID] == agent {
		delete(h.agents, agent.clusterID)
	}
	h.mu.Unlock()
	agent.shutdown()
}

func (a *agentConnection) call(ctx context.Context, request fleetsync.ClusterToolCall, seconds int) (*mcpsdk.CallToolResult, error) {
	if !fleetsync.HasTool(a.tools, request.Tool) {
		return nil, errors.New("tool is unavailable on the selected cluster")
	}
	callCtx, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	defer cancel()
	id := fmt.Sprintf("%s-%d", a.prefix, a.sequence.Add(1))
	reply := make(chan agentReply, 1)
	a.mu.Lock()
	if len(a.pending) >= fleetsync.MaxAgentCalls {
		a.mu.Unlock()
		return nil, errors.New("cluster agent is busy")
	}
	select {
	case <-a.done:
		a.mu.Unlock()
		return nil, errors.New("cluster agent is unavailable")
	default:
	}
	a.pending[id] = reply
	a.mu.Unlock()

	message := fleetsync.AgentMessage{
		Type: "call", ID: id, Tool: request.Tool, Arguments: request.Arguments, TimeoutSeconds: seconds,
	}
	payload, err := json.Marshal(message)
	if err != nil || len(payload) > fleetsync.MaxAgentFrameBytes {
		a.removePending(id)
		return nil, errors.New("tool arguments exceed the request limit")
	}

	a.writeMu.Lock()
	if callCtx.Err() != nil {
		a.writeMu.Unlock()
		a.removePending(id)
		return nil, callCtx.Err()
	}
	select {
	case <-a.done:
		a.writeMu.Unlock()
		a.removePending(id)
		return nil, errors.New("cluster agent is unavailable")
	default:
	}
	_ = a.conn.SetWriteDeadline(time.Now().Add(fleetsync.AgentWriteWait))
	err = a.conn.WriteMessage(websocket.TextMessage, payload)
	a.writeMu.Unlock()
	if err != nil {
		a.removePending(id)
		return unknownToolResult(request), nil
	}

	select {
	case outcome := <-reply:
		if outcome.err != "" {
			return unknownToolResult(request), nil
		}
		if outcome.result == nil {
			return unknownToolResult(request), nil
		}
		return outcome.result, nil
	case <-callCtx.Done():
		a.removePending(id)
		_ = a.write(fleetsync.AgentMessage{Type: "cancel", ID: id})
		if ctx.Err() != nil && !errors.Is(callCtx.Err(), context.DeadlineExceeded) {
			return unknownToolResult(request), nil
		}
		return unknownToolResult(request), nil
	case <-a.done:
		a.removePending(id)
		return unknownToolResult(request), nil
	}
}

func (a *agentConnection) receiveResult(message fleetsync.AgentMessage) {
	if message.ID == "" {
		return
	}
	a.mu.Lock()
	reply := a.pending[message.ID]
	if reply != nil {
		delete(a.pending, message.ID)
	}
	a.mu.Unlock()
	if reply == nil {
		return
	}
	select {
	case reply <- agentReply{result: message.Result, err: message.Error}:
	default:
	}
}

func (a *agentConnection) removePending(id string) {
	a.mu.Lock()
	delete(a.pending, id)
	a.mu.Unlock()
}

func (a *agentConnection) write(message fleetsync.AgentMessage) error {
	payload, err := json.Marshal(message)
	if err != nil || len(payload) > fleetsync.MaxAgentFrameBytes {
		return errors.New("Fleet agent message exceeds the size limit")
	}
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	_ = a.conn.SetWriteDeadline(time.Now().Add(fleetsync.AgentWriteWait))
	return a.conn.WriteMessage(websocket.TextMessage, payload)
}

func (a *agentConnection) ping(ctx context.Context, done chan struct{}) {
	ticker := time.NewTicker(fleetsync.AgentPingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			if !a.hub.store.SourceMatches(a.clusterID, a.sourceID) {
				a.shutdown()
				return
			}
			a.writeMu.Lock()
			_ = a.conn.SetWriteDeadline(time.Now().Add(fleetsync.AgentWriteWait))
			err := a.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(fleetsync.AgentWriteWait))
			a.writeMu.Unlock()
			if err != nil {
				a.shutdown()
				return
			}
		}
	}
}

func (a *agentConnection) shutdown() {
	a.close.Do(func() {
		close(a.done)
		a.mu.Lock()
		a.pending = make(map[string]chan agentReply)
		a.mu.Unlock()
		_ = a.conn.Close()
	})
}

func agentSessionPrefix() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err == nil {
		return hex.EncodeToString(value[:])
	}
	return fmt.Sprintf("agent-%d", time.Now().UnixNano())
}

func cloneToolCatalog(tools []*mcpsdk.Tool) []*mcpsdk.Tool {
	data, err := json.Marshal(tools)
	if err != nil {
		return []*mcpsdk.Tool{}
	}
	var cloned []*mcpsdk.Tool
	if json.Unmarshal(data, &cloned) != nil || cloned == nil {
		return []*mcpsdk.Tool{}
	}
	return cloned
}

func unknownToolResult(request fleetsync.ClusterToolCall) *mcpsdk.CallToolResult {
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "The source did not provide a definitive outcome after dispatch; reconcile cluster state before retrying."}},
		StructuredContent: map[string]any{
			"state": "outcome_unknown", "clusterId": request.ClusterID, "tool": request.Tool,
		},
		IsError: true,
	}
}

func writeFleetHTTPError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

var _ fleetsync.ToolBridge = (*AgentHub)(nil)
