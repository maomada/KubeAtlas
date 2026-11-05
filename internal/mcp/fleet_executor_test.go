package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/maomada/KubeAtlas/internal/k8s"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
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
	t.Cleanup(k8s.SetTestProfileSource("/test/team", "dev", "reader"))
	identityCtx := context.Background()
	executor := &FleetToolExecutor{binding: k8s.ClusterSafetyBinding(identityCtx), identityCtx: identityCtx}
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
