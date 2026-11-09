package mcp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/maomada/KubeAtlas/internal/fleetsync"
	"github.com/maomada/KubeAtlas/internal/k8s"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"k8s.io/client-go/kubernetes/fake"
)

func TestFleetExecutorReadOnlyCatalog(t *testing.T) {
	ctx := context.Background()
	server := newServer(false, false, "")
	clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "fleet-executor-test", Version: "1"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		_ = serverSession.Close()
		t.Fatal(err)
	}
	defer func() {
		_ = clientSession.Close()
		_ = serverSession.Close()
	}()
	listed, err := clientSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	forbidden := map[string]bool{
		"apply_resource": true, "delete_resource": true, "manage_node": true, "exec_pod": true,
	}
	for _, tool := range listed.Tools {
		if forbidden[tool.Name] {
			t.Fatalf("read-only source catalog exposed %q", tool.Name)
		}
	}
}

func TestFleetExecutorIdentityCheckSeparatesCallCancellationFromPinChange(t *testing.T) {
	k8s.CancelOngoingOperations()
	t.Cleanup(k8s.SetTestProfileSource("/test/team", "dev", "reader"))
	identityCtx := context.Background()
	executor := &FleetToolExecutor{
		binding: k8s.ClusterSafetyBinding(identityCtx), rootCtx: identityCtx,
		operationCtx: k8s.OperationContext(),
	}
	if executor.binding == "" {
		t.Fatal("test source binding is empty")
	}

	callCtx, cancelCall := context.WithCancel(identityCtx)
	cancelCall()
	if err := executor.checkIdentity(callCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled call identity check = %v", err)
	}
	if !executor.identityMatches() {
		t.Fatal("ordinary call cancellation invalidated the source identity")
	}

	t.Cleanup(k8s.SetTestProfileSource("/test/team", "other", "reader"))
	if executor.identityMatches() {
		t.Fatal("source binding change did not invalidate the executor")
	}
}

func TestFleetExecutorRejectsCancelledOperationGenerationWithSameBinding(t *testing.T) {
	t.Cleanup(k8s.ResetTestState)
	k8s.ResetTestState()
	t.Cleanup(k8s.SetTestLocalMode())
	t.Cleanup(k8s.SetTestProfileSource("/test/team", "dev", "reader"))
	k8s.CancelOngoingOperations()
	if err := k8s.InitTestResourceCache(fake.NewClientset()); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	binding := k8s.ClusterSafetyBinding(ctx)
	operationCtx := k8s.OperationContext()
	executor, err := NewFleetToolExecutor(ctx, binding, false, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(executor.Close)
	if tools, err := executor.ListTools(ctx); err != nil || len(tools) == 0 {
		t.Fatalf("initial source catalog: tools=%d err=%v", len(tools), err)
	}

	callCtx, cancelCall := context.WithCancel(ctx)
	cancelCall()
	if _, err := executor.CallTool(callCtx, "get_dashboard", map[string]any{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("ordinary call cancellation = %v", err)
	}
	if !executor.identityMatches() {
		t.Fatal("ordinary call cancellation invalidated the executor")
	}
	if _, err := executor.ListTools(ctx); err != nil {
		t.Fatalf("executor stopped after ordinary call cancellation: %v", err)
	}

	k8s.CancelOngoingOperations()
	if operationCtx.Err() == nil {
		t.Fatal("CancelOngoingOperations did not cancel the pinned operation context")
	}
	if got := k8s.ClusterSafetyBinding(ctx); got != binding {
		t.Fatalf("test binding changed after cancellation: got %q, want %q", got, binding)
	}
	if executor.identityMatches() {
		t.Fatal("executor remained valid after its pinned operation context was canceled")
	}
	select {
	case <-executor.LifetimeContext().Done():
	case <-time.After(2 * time.Second):
		t.Fatal("operation cancellation did not stop the executor lifetime")
	}
	if _, err := executor.ListTools(ctx); !errors.Is(err, fleetsync.ErrAgentIdentityInvalid) {
		t.Fatalf("ListTools after operation cancellation = %v", err)
	}
	if _, err := executor.CallTool(ctx, "get_dashboard", map[string]any{}); !errors.Is(err, fleetsync.ErrAgentIdentityInvalid) {
		t.Fatalf("CallTool after operation cancellation = %v", err)
	}
}

func TestFleetExecutorStartsOnPostInitializationGeneration(t *testing.T) {
	t.Cleanup(k8s.ResetTestState)
	k8s.ResetTestState()
	t.Cleanup(k8s.SetTestLocalMode())
	t.Cleanup(k8s.SetTestProfileSource("/test/team", "dev", "reader"))
	beforeInitialization := k8s.OperationContext()
	k8s.CancelOngoingOperations()
	if beforeInitialization.Err() == nil {
		t.Fatal("startup operation context was not cancelled")
	}
	if err := k8s.InitTestResourceCache(fake.NewClientset()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	executor, err := NewFleetToolExecutor(ctx, k8s.ClusterSafetyBinding(ctx), false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer executor.Close()
	if _, err := executor.ListTools(ctx); err != nil {
		t.Fatalf("post-initialization catalog: %v", err)
	}
	result, err := executor.CallTool(ctx, "get_dashboard", map[string]any{})
	if err != nil || result == nil || result.IsError {
		t.Fatalf("post-initialization tool call: result=%+v err=%v", result, err)
	}
}
