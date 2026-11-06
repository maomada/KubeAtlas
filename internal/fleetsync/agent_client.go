package fleetsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	agentReconnectDelay = 5 * time.Second
)

type AgentClient struct {
	config   ReporterConfig
	sourceID string
	executor AgentToolExecutor
	endpoint string
	dialer   websocket.Dialer
}

type sourceCall struct {
	cancel context.CancelFunc
}

func NewAgentClient(config ReporterConfig, sourceID string, executor AgentToolExecutor) (*AgentClient, error) {
	if err := validateReporterConfig(config); err != nil {
		return nil, err
	}
	if sourceID == "" || len(sourceID) > 256 || executor == nil {
		return nil, errors.New("Fleet agent identity or executor is invalid")
	}
	endpoint, err := agentChannelURL(config.PortalURL, config.ClusterID)
	if err != nil {
		return nil, errors.New("Fleet agent Portal URL is invalid")
	}
	return &AgentClient{
		config: config, sourceID: sourceID, executor: executor, endpoint: endpoint,
		dialer: websocket.Dialer{HandshakeTimeout: 10 * time.Second},
	}, nil
}

func (c *AgentClient) Run(ctx context.Context, onError func(error)) {
	for ctx.Err() == nil {
		err := c.serve(ctx)
		if errors.Is(err, ErrAgentIdentityInvalid) {
			return
		}
		if ctx.Err() != nil {
			return
		}
		if onError != nil {
			onError(errors.New("Fleet agent channel unavailable"))
		}
		timer := time.NewTimer(agentReconnectDelay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-timer.C:
		}
	}
}

func (c *AgentClient) serve(ctx context.Context) error {
	tools, err := c.executor.ListTools(ctx)
	if err != nil {
		return err
	}
	if err := ValidateToolCatalog(tools); err != nil {
		return err
	}
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+c.config.Token)
	conn, response, err := c.dialer.DialContext(ctx, c.endpoint, headers)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		return errors.New("Fleet agent channel connection failed")
	}
	defer conn.Close()
	closeWatchDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-closeWatchDone:
		}
	}()
	defer close(closeWatchDone)
	conn.SetReadLimit(MaxAgentFrameBytes)
	_ = conn.SetReadDeadline(time.Now().Add(AgentPongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(AgentPongWait))
	})
	state := &sourceChannel{
		conn: conn, tools: tools, executor: c.executor,
		pending: make(map[string]sourceCall), ctx: ctx,
	}
	if err := state.write(AgentMessage{Type: "hello", SourceID: c.sourceID, Tools: tools}); err != nil {
		return err
	}
	defer state.cancelPending()

	pingDone := make(chan struct{})
	go state.ping(ctx, pingDone)
	defer close(pingDone)
	for {
		_, payload, err := conn.ReadMessage()
		if err != nil {
			return errors.New("Fleet agent channel disconnected")
		}
		if len(payload) > MaxAgentFrameBytes {
			return errors.New("Fleet agent frame exceeds the size limit")
		}
		var message AgentMessage
		if err := json.Unmarshal(payload, &message); err != nil {
			return errors.New("Fleet agent received an invalid message")
		}
		switch message.Type {
		case "call":
			state.startCall(message)
		case "cancel":
			state.cancelCall(message.ID)
		default:
			return errors.New("Fleet agent received an unsupported message")
		}
	}
}

type sourceChannel struct {
	conn     *websocket.Conn
	tools    []*mcpsdk.Tool
	executor AgentToolExecutor
	ctx      context.Context

	writeMu sync.Mutex
	mu      sync.Mutex
	pending map[string]sourceCall
}

func (s *sourceChannel) write(message AgentMessage) error {
	payload, err := json.Marshal(message)
	if err != nil || len(payload) > MaxAgentFrameBytes {
		return errors.New("Fleet agent message exceeds the size limit")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_ = s.conn.SetWriteDeadline(time.Now().Add(AgentWriteWait))
	if err := s.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		return errors.New("Fleet agent message could not be sent")
	}
	return nil
}

func (s *sourceChannel) ping(ctx context.Context, done chan struct{}) {
	ticker := time.NewTicker(AgentPingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			if checker, ok := s.executor.(AgentIdentityChecker); ok && checker.CheckIdentity(ctx) != nil {
				_ = s.conn.Close()
				return
			}
			s.writeMu.Lock()
			_ = s.conn.SetWriteDeadline(time.Now().Add(AgentWriteWait))
			err := s.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(AgentWriteWait))
			s.writeMu.Unlock()
			if err != nil {
				_ = s.conn.Close()
				return
			}
		}
	}
}

func (s *sourceChannel) startCall(message AgentMessage) {
	if message.ID == "" || len(message.ID) > 256 {
		_ = s.write(AgentMessage{Type: "result", ID: message.ID, Error: "invalid call"})
		return
	}
	seconds, err := NormalizeToolTimeout(message.TimeoutSeconds)
	if err != nil || !HasTool(s.tools, message.Tool) {
		_ = s.write(AgentMessage{Type: "result", ID: message.ID, Error: "tool is unavailable"})
		return
	}
	if message.Arguments == nil {
		message.Arguments = map[string]any{}
	}
	callCtx, cancel := context.WithTimeout(s.ctx, time.Duration(seconds)*time.Second)
	s.mu.Lock()
	if _, exists := s.pending[message.ID]; exists || len(s.pending) >= MaxAgentCalls {
		s.mu.Unlock()
		cancel()
		_ = s.write(AgentMessage{Type: "result", ID: message.ID, Error: "source is busy"})
		return
	}
	s.pending[message.ID] = sourceCall{cancel: cancel}
	s.mu.Unlock()
	go s.execute(callCtx, message)
}

func (s *sourceChannel) execute(ctx context.Context, message AgentMessage) {
	result, err := s.executor.CallTool(ctx, message.Tool, message.Arguments)
	s.finish(message.ID)
	if errors.Is(err, ErrAgentIdentityInvalid) {
		s.cancelPending()
		_ = s.conn.Close()
		return
	}
	if err != nil || result == nil {
		_ = s.write(AgentMessage{Type: "result", ID: message.ID, Error: "source tool execution failed"})
		return
	}
	if err := s.write(AgentMessage{Type: "result", ID: message.ID, Result: result}); err != nil {
		_ = s.conn.Close()
	}
}

func (s *sourceChannel) finish(id string) {
	s.mu.Lock()
	call, ok := s.pending[id]
	delete(s.pending, id)
	s.mu.Unlock()
	if ok {
		call.cancel()
	}
}

func (s *sourceChannel) cancelCall(id string) {
	s.mu.Lock()
	call, ok := s.pending[id]
	s.mu.Unlock()
	if ok {
		call.cancel()
	}
}

func (s *sourceChannel) cancelPending() {
	s.mu.Lock()
	pending := s.pending
	s.pending = make(map[string]sourceCall)
	s.mu.Unlock()
	for _, call := range pending {
		call.cancel()
	}
}

func agentChannelURL(baseURL, clusterID string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") || strings.Contains(baseURL, "\\") {
		return "", fmt.Errorf("invalid Portal URL")
	}
	if parsed.Scheme == "https" {
		parsed.Scheme = "wss"
	} else {
		parsed.Scheme = "ws"
	}
	parsed.Path = "/api/fleet/agents/" + url.PathEscape(clusterID) + "/channel"
	return parsed.String(), nil
}
