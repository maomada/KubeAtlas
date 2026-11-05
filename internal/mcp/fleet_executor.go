package mcp

import (
	"context"
	"errors"
	"sync"

	"github.com/maomada/KubeAtlas/internal/fleetsync"
	"github.com/maomada/KubeAtlas/internal/k8s"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type FleetToolExecutor struct {
	binding     string
	identityCtx context.Context
	cancel      context.CancelFunc
	client      *mcpsdk.ClientSession
	server      *mcpsdk.ServerSession
	close       sync.Once
}

func NewFleetToolExecutor(ctx context.Context, binding string, includeWrites, includeExec bool) (*FleetToolExecutor, error) {
	if binding == "" || k8s.ClusterSafetyBinding(ctx) != binding {
		return nil, fleetsync.ErrAgentIdentityInvalid
	}
	includeExec = includeExec && includeWrites
	workerCtx, cancel := context.WithCancel(ctx)
	clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()
	server, err := newServer(includeWrites, includeExec, "").Connect(workerCtx, serverTransport, nil)
	if err != nil {
		cancel()
		return nil, errors.New("source MCP server could not be started")
	}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "kubeatlas-fleet-agent", Version: "1"}, nil)
	clientSession, err := client.Connect(workerCtx, clientTransport, nil)
	if err != nil {
		_ = server.Close()
		cancel()
		return nil, errors.New("source MCP client could not connect")
	}
	return &FleetToolExecutor{binding: binding, identityCtx: workerCtx, cancel: cancel, client: clientSession, server: server}, nil
}

func (e *FleetToolExecutor) ListTools(ctx context.Context) ([]*mcpsdk.Tool, error) {
	if err := e.checkIdentity(ctx); err != nil {
		return nil, err
	}
	operation := k8s.OperationContext()
	callCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(operation, cancel)
	defer stop()
	defer cancel()
	if err := e.checkIdentity(callCtx); err != nil {
		return nil, err
	}
	listed, err := e.client.ListTools(callCtx, nil)
	if !e.identityMatches() {
		return nil, fleetsync.ErrAgentIdentityInvalid
	}
	if err != nil {
		return nil, err
	}
	return listed.Tools, nil
}

func (e *FleetToolExecutor) CallTool(ctx context.Context, name string, arguments map[string]any) (*mcpsdk.CallToolResult, error) {
	if err := e.checkIdentity(ctx); err != nil {
		return nil, err
	}
	operation := k8s.OperationContext()
	callCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(operation, cancel)
	defer stop()
	defer cancel()
	if err := e.checkIdentity(callCtx); err != nil {
		return nil, err
	}
	result, err := e.client.CallTool(callCtx, &mcpsdk.CallToolParams{Name: name, Arguments: arguments})
	if !e.identityMatches() {
		return nil, fleetsync.ErrAgentIdentityInvalid
	}
	return result, err
}

func (e *FleetToolExecutor) CheckIdentity(ctx context.Context) error {
	return e.checkIdentity(ctx)
}

func (e *FleetToolExecutor) checkIdentity(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !e.identityMatches() {
		return fleetsync.ErrAgentIdentityInvalid
	}
	return nil
}

func (e *FleetToolExecutor) identityMatches() bool {
	return k8s.ClusterSafetyBinding(e.identityCtx) == e.binding
}

func (e *FleetToolExecutor) Close() {
	if e == nil {
		return
	}
	e.close.Do(func() {
		e.cancel()
		_ = e.client.Close()
		_ = e.server.Close()
	})
}

var _ fleetsync.AgentToolExecutor = (*FleetToolExecutor)(nil)
