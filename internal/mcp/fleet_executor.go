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
	binding       string
	rootCtx       context.Context
	operationCtx  context.Context
	stopOperation func() bool
	cancel        context.CancelFunc
	client        *mcpsdk.ClientSession
	server        *mcpsdk.ServerSession
	close         sync.Once
}

func NewFleetToolExecutor(ctx context.Context, binding string, includeWrites, includeExec bool) (*FleetToolExecutor, error) {
	operationCtx := k8s.OperationContext()
	if !fleetExecutorIdentityMatches(ctx, operationCtx, binding) {
		return nil, fleetsync.ErrAgentIdentityInvalid
	}
	includeExec = includeExec && includeWrites
	rootCtx, cancel := context.WithCancel(ctx)
	stopOperation := context.AfterFunc(operationCtx, cancel)
	cleanup := func() {
		stopOperation()
		cancel()
	}
	clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()
	server, err := newServer(includeWrites, includeExec, "").Connect(rootCtx, serverTransport, nil)
	if err != nil {
		cleanup()
		if !fleetExecutorIdentityMatches(ctx, operationCtx, binding) {
			return nil, fleetsync.ErrAgentIdentityInvalid
		}
		return nil, errors.New("source MCP server could not be started")
	}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "kubeatlas-fleet-agent", Version: "1"}, nil)
	clientSession, err := client.Connect(rootCtx, clientTransport, nil)
	if err != nil {
		_ = server.Close()
		cleanup()
		if !fleetExecutorIdentityMatches(ctx, operationCtx, binding) {
			return nil, fleetsync.ErrAgentIdentityInvalid
		}
		return nil, errors.New("source MCP client could not connect")
	}
	executor := &FleetToolExecutor{
		binding: binding, rootCtx: rootCtx, operationCtx: operationCtx, stopOperation: stopOperation,
		cancel: cancel, client: clientSession, server: server,
	}
	if !executor.identityMatches() {
		executor.Close()
		return nil, fleetsync.ErrAgentIdentityInvalid
	}
	return executor, nil
}

func (e *FleetToolExecutor) ListTools(ctx context.Context) ([]*mcpsdk.Tool, error) {
	if err := e.checkIdentity(ctx); err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(e.operationCtx, cancel)
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
	callCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(e.operationCtx, cancel)
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
	if !e.identityMatches() {
		return fleetsync.ErrAgentIdentityInvalid
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}

func (e *FleetToolExecutor) identityMatches() bool {
	return fleetExecutorIdentityMatches(e.rootCtx, e.operationCtx, e.binding)
}

func fleetExecutorIdentityMatches(rootCtx, operationCtx context.Context, binding string) bool {
	return rootCtx != nil && operationCtx != nil && binding != "" &&
		rootCtx.Err() == nil && operationCtx.Err() == nil &&
		k8s.ClusterSafetyBinding(rootCtx) == binding
}

func (e *FleetToolExecutor) LifetimeContext() context.Context {
	return e.rootCtx
}

func (e *FleetToolExecutor) Close() {
	if e == nil {
		return
	}
	e.close.Do(func() {
		if e.stopOperation != nil {
			e.stopOperation()
		}
		e.cancel()
		if e.client != nil {
			_ = e.client.Close()
		}
		if e.server != nil {
			_ = e.server.Close()
		}
	})
}

var _ fleetsync.AgentToolExecutor = (*FleetToolExecutor)(nil)
