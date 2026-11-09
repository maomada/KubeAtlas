package mcp

import (
	"bytes"
	"context"
	"errors"
	"log"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/maomada/KubeAtlas/pkg/readreason"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestObservedAtPreservesResultAndExtra(t *testing.T) {
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })
	text := &mcp.TextContent{Text: `{"items":["unchanged"]}`}
	payload := map[string]any{"items": []string{"unchanged"}}
	result := &mcp.CallToolResult{Content: []mcp.Content{text}, StructuredContent: payload}
	result.Meta = mcp.Meta{"existing": "preserved"}
	extra := &struct{ Value string }{"extra"}
	input := listResourcesInput{Kind: "pods", Limit: 1, Cursor: "private-cursor"}
	wrapped := logToolCall("list_resources", func(_ context.Context, _ *mcp.CallToolRequest, got listResourcesInput) (*mcp.CallToolResult, any, error) {
		if got != input {
			t.Fatal("logging changed handler input")
		}
		return result, extra, nil
	})
	before := time.Now()
	got, gotExtra, err := wrapped(context.Background(), nil, input)
	after := time.Now()
	if err != nil || got != result || gotExtra != extra || got.Content[0] != text || !reflect.DeepEqual(got.StructuredContent, payload) || got.Meta["existing"] != "preserved" {
		t.Fatal("completion metadata rewrote payload, extra, or other metadata")
	}
	stamp, ok := got.Meta["kubeatlas/observedAt"].(string)
	if !ok {
		t.Fatal("missing observation timestamp")
	}
	parsed, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil || parsed.Before(before) || parsed.After(after) || !strings.HasSuffix(stamp, "Z") {
		t.Fatal("observation is not UTC completion time")
	}
	if strings.Contains(logs.String(), input.Cursor) {
		t.Fatal("cursor logged")
	}
}

func TestObservedAtAfterReadErrorConversion(t *testing.T) {
	wrapped := logToolCall("list_resources", func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return nil, nil, paginationError(readreason.InvalidCursor)
	})
	result, _, err := wrapped(context.Background(), nil, struct{}{})
	if err != nil || result == nil || !result.IsError || result.StructuredContent == nil {
		t.Fatal("read error not converted")
	}
	if _, err := time.Parse(time.RFC3339Nano, result.Meta["kubeatlas/observedAt"].(string)); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("protocol failure")
	noResult := logToolCall("test", func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return nil, nil, failure
	})
	result, _, err = noResult(context.Background(), nil, struct{}{})
	if result != nil || err != failure {
		t.Fatal("fabricated observation for missing result")
	}
}
