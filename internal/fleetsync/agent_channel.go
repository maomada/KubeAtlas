package fleetsync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	MaxAgentFrameBytes = 32 << 20
	MaxAgentTools      = 512
	MaxAgentCalls      = 32
	DefaultToolTimeout = 180
	MaxToolTimeout     = 600
	AgentPingInterval  = 30 * time.Second
	AgentPongWait      = 60 * time.Second
	AgentWriteWait     = 10 * time.Second
)

var (
	ErrAgentIdentityInvalid = errors.New("Fleet agent identity changed")
	ErrAgentUnavailable     = errors.New("Fleet source agent is unavailable")
	validToolName           = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
)

type AgentMessage struct {
	Type           string                 `json:"type"`
	ID             string                 `json:"id,omitempty"`
	SourceID       string                 `json:"sourceId,omitempty"`
	Tools          []*mcpsdk.Tool         `json:"tools,omitempty"`
	Tool           string                 `json:"tool,omitempty"`
	Arguments      map[string]any         `json:"arguments,omitempty"`
	TimeoutSeconds int                    `json:"timeoutSeconds,omitempty"`
	Result         *mcpsdk.CallToolResult `json:"result,omitempty"`
	Error          string                 `json:"error,omitempty"`
}

type ClusterTools struct {
	ClusterID string         `json:"clusterId"`
	State     string         `json:"state"`
	Tools     []*mcpsdk.Tool `json:"tools"`
}

type ClusterToolCall struct {
	ClusterID      string         `json:"clusterId"`
	FleetID        string         `json:"fleetId,omitempty"`
	Tool           string         `json:"tool"`
	Arguments      map[string]any `json:"arguments"`
	TimeoutSeconds int            `json:"timeoutSeconds,omitempty"`
}

type ToolBridge interface {
	ListClusterTools(context.Context, Selection) ([]ClusterTools, error)
	CallClusterTool(context.Context, ClusterToolCall) (*mcpsdk.CallToolResult, error)
}

type AgentToolExecutor interface {
	ListTools(context.Context) ([]*mcpsdk.Tool, error)
	CallTool(context.Context, string, map[string]any) (*mcpsdk.CallToolResult, error)
}

type AgentIdentityChecker interface {
	CheckIdentity(context.Context) error
}

func ValidateToolCatalog(tools []*mcpsdk.Tool) error {
	if len(tools) > MaxAgentTools {
		return errors.New("source tool catalog exceeds the limit")
	}
	seen := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		if tool == nil || !ValidToolName(tool.Name) || tool.InputSchema == nil {
			return errors.New("source tool catalog contains an invalid tool name")
		}
		if _, exists := seen[tool.Name]; exists {
			return errors.New("source tool catalog contains duplicate names")
		}
		seen[tool.Name] = struct{}{}
		if _, err := json.Marshal(tool); err != nil {
			return errors.New("source tool catalog contains an invalid descriptor")
		}
	}
	return nil
}

func NormalizeToolTimeout(seconds int) (int, error) {
	if seconds == 0 {
		return DefaultToolTimeout, nil
	}
	if seconds < 1 || seconds > MaxToolTimeout {
		return 0, errors.New("tool timeout must be between 1 and 600 seconds")
	}
	return seconds, nil
}

func SourceIDForBinding(binding string) string {
	if binding == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(binding))
	return hex.EncodeToString(digest[:])
}

func ValidToolName(name string) bool {
	return validToolName.MatchString(name) && strings.TrimSpace(name) == name
}

func HasTool(tools []*mcpsdk.Tool, name string) bool {
	return slices.ContainsFunc(tools, func(tool *mcpsdk.Tool) bool {
		return tool != nil && tool.Name == name
	})
}
