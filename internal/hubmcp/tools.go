package hubmcp

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/maomada/KubeAtlas/internal/hub"
	"github.com/maomada/KubeAtlas/pkg/readreason"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type listInput struct {
	ClusterID string `json:"clusterId,omitempty" jsonschema:"select one cluster; mutually exclusive with fleetId"`
	FleetID   string `json:"fleetId,omitempty" jsonschema:"optional Hub group, intersected with your explicit cluster grant"`
	Limit     int    `json:"limit,omitempty" jsonschema:"page size, default 20, maximum 100"`
	Cursor    string `json:"cursor,omitempty" jsonschema:"nextCursor from the same selection and credential"`
}
type schemaInput struct {
	ClusterID   string `json:"clusterId"`
	Tool        string `json:"tool"`
	CatalogHash string `json:"catalogHash,omitempty" jsonschema:"optional expected catalog version"`
}
type callInput struct {
	ClusterID   string                     `json:"clusterId"`
	Tool        string                     `json:"tool"`
	CatalogHash string                     `json:"catalogHash,omitempty"`
	Arguments   map[string]json.RawMessage `json:"arguments,omitempty" jsonschema:"unmodified native tool arguments, except default list_resources page size"`
}
type clusterInput struct {
	ClusterID string `json:"clusterId"`
}

type directoryEntry struct {
	ClusterID    string `json:"clusterId"`
	Name         string `json:"name"`
	Environment  string `json:"environment,omitempty"`
	Region       string `json:"region,omitempty"`
	State        string `json:"state"`
	ConnectionID string `json:"connectionId,omitempty"`
	AgentVersion string `json:"agentVersion,omitempty"`
	CatalogHash  string `json:"catalogHash,omitempty"`
	ReadOnly     *bool  `json:"readOnly,omitempty"`
	ErrorCode    string `json:"error_code,omitempty"`
}

func (f *facade) selected(p hub.Principal, input listInput) ([]directoryEntry, error) {
	if input.ClusterID != "" && input.FleetID != "" {
		return nil, invalid("clusterId and fleetId are mutually exclusive")
	}
	if input.Limit < 0 || input.Limit > 100 || len(input.Cursor) > 4096 {
		return nil, invalid("Invalid page size or cursor length")
	}
	ids := p.ClusterIDs
	if input.ClusterID != "" {
		if !slices.Contains(ids, input.ClusterID) {
			return nil, &fault{code: "permission_denied", message: "Cluster is outside this credential's scope."}
		}
		ids = []string{input.ClusterID}
	}
	if input.FleetID != "" {
		group, found := f.store.Group(input.FleetID)
		if !found {
			return nil, &fault{code: "group_not_found", message: "Hub group not found."}
		}
		intersection := []string{}
		for _, id := range ids {
			if slices.Contains(group.ClusterIDs, id) {
				intersection = append(intersection, id)
			}
		}
		ids = intersection
	}
	result := []directoryEntry{}
	agents := map[string]hub.Agent{}
	for _, agent := range f.hub.Agents() {
		agents[agent.ClusterID] = agent
	}
	for _, id := range ids {
		cluster, found := f.store.Cluster(id)
		if !found {
			continue
		}
		entry := directoryEntry{ClusterID: id, Name: cluster.Name, Environment: cluster.Environment, Region: cluster.Region, State: "offline"}
		if cluster.RevokedAt != nil {
			entry.State = "revoked"
		} else if agent, connected := agents[id]; connected {
			entry.State, entry.ConnectionID, entry.AgentVersion = "connected", agent.ConnectionID, agent.Version
		}
		result = append(result, entry)
	}
	slices.SortFunc(result, func(a, b directoryEntry) int { return strings.Compare(a.ClusterID, b.ClusterID) })
	return result, nil
}

func listQuery(tool string, p hub.Principal, input listInput) string {
	input.Cursor = ""
	return fingerprint(struct {
		Tool      string
		Principal hub.Principal
		Input     listInput
	}{tool, p, input})
}

func (f *facade) listClusters(_ context.Context, p hub.Principal, e *envelope, input listInput) error {
	rows, err := f.selected(p, input)
	if err != nil {
		return err
	}
	items, pagination, err := pageRows(f.pages, rows, input.Limit, input.Cursor, listQuery("list_clusters", p, input), fingerprint(rows))
	if err != nil {
		return err
	}
	e.Source = "directory"
	e.Data = struct {
		Clusters []directoryEntry `json:"clusters"`
		Page     page             `json:"page"`
	}{items, pagination}
	return nil
}

type catalogReference struct {
	Hash      string `json:"hash"`
	ToolCount int    `json:"toolCount"`
}
type toolSummary struct {
	CatalogHash          string               `json:"catalogHash"`
	Name                 string               `json:"name"`
	Description          string               `json:"description"`
	DescriptionTruncated bool                 `json:"descriptionTruncated"`
	Annotations          *mcp.ToolAnnotations `json:"annotations,omitempty"`
}

func (f *facade) listTools(ctx context.Context, p hub.Principal, e *envelope, input listInput) error {
	clusters, err := f.selected(p, input)
	if err != nil {
		return err
	}
	if len(clusters) > maxSelectedClusters {
		return &fault{code: "selection_too_large", message: "Tool discovery is limited to eight explicitly scoped clusters per request.", next: "Select clusterId or a smaller fleetId group."}
	}
	catalogs := map[string][]*mcp.Tool{}
	for index := range clusters {
		entry := &clusters[index]
		if entry.State != "connected" {
			entry.ErrorCode = "cluster_unavailable"
			continue
		}
		connection, openErr := f.open(ctx, p, entry.ClusterID, e.RequestID)
		if openErr != nil {
			entry.ErrorCode = faultCode(openErr)
			continue
		}
		tools, hash, catalogErr := connection.tools(ctx)
		_ = connection.session.Close()
		if catalogErr != nil {
			entry.ErrorCode = faultCode(catalogErr)
			continue
		}
		entry.ConnectionID, entry.AgentVersion, entry.CatalogHash, entry.ReadOnly = connection.agent.ConnectionID, connection.agent.Version, hash, new(connection.readOnly)
		catalogs[hash] = tools
	}
	keys := make([]string, 0, len(catalogs))
	for hash := range catalogs {
		keys = append(keys, hash)
	}
	slices.Sort(keys)
	references, rows := []catalogReference{}, []toolSummary{}
	for _, hash := range keys {
		references = append(references, catalogReference{hash, len(catalogs[hash])})
		for _, tool := range catalogs[hash] {
			description := []rune(tool.Description)
			rows = append(rows, toolSummary{hash, tool.Name, string(description[:min(len(description), 160)]), len(description) > 160, tool.Annotations})
		}
	}
	items, pagination, err := pageRows(f.pages, rows, input.Limit, input.Cursor, listQuery("list_cluster_tools", p, input), fingerprint(clusters))
	if err != nil {
		return err
	}
	responded := 0
	for _, cluster := range clusters {
		if cluster.ErrorCode == "" {
			responded++
		}
	}
	e.Source = "live"
	e.Data = struct {
		Clusters          []directoryEntry   `json:"clusters"`
		Catalogs          []catalogReference `json:"catalogs"`
		Tools             []toolSummary      `json:"tools"`
		Page              page               `json:"page"`
		Partial           bool               `json:"partial"`
		RespondedClusters int                `json:"respondedClusters"`
	}{clusters, references, items, pagination, responded < len(clusters), responded}
	if len(clusters) > 0 && responded == 0 {
		return &fault{code: "cluster_unavailable", message: "No selected source returned a tool catalog; per-cluster errors are in data.", next: "Check each source's connection and access before retrying.", retryable: true}
	}
	return nil
}

func faultCode(err error) string {
	if f, ok := err.(*fault); ok {
		return f.code
	}
	return "source_error"
}

func (f *facade) toolSource(ctx context.Context, p hub.Principal, e *envelope, input schemaInput) (*source, *mcp.Tool, string, error) {
	if input.ClusterID == "" {
		return nil, nil, "", invalid("clusterId is required")
	}
	if !toolNamePattern.MatchString(input.Tool) {
		return nil, nil, "", invalid("tool must start with a lowercase letter and contain only lowercase letters, digits or underscores (maximum 128 characters)")
	}
	if len(input.ClusterID) > 128 || len(input.CatalogHash) > 64 {
		return nil, nil, "", invalid("clusterId or catalogHash is too long")
	}
	e.ClusterID, e.Tool, e.Source = input.ClusterID, input.Tool, "live"
	connection, err := f.open(ctx, p, input.ClusterID, e.RequestID)
	if err != nil {
		return nil, nil, "", err
	}
	e.ConnectionID, e.AgentVersion = connection.agent.ConnectionID, connection.agent.Version
	tools, hash, err := connection.tools(ctx)
	if err != nil {
		_ = connection.session.Close()
		return nil, nil, "", err
	}
	if input.CatalogHash != "" && hash != input.CatalogHash {
		_ = connection.session.Close()
		return nil, nil, "", &fault{code: "source_changed", message: "Source tool catalog changed.", next: "Fetch the current schema before calling the tool."}
	}
	for _, tool := range tools {
		if tool.Name == input.Tool {
			return connection, tool, hash, nil
		}
	}
	_ = connection.session.Close()
	return nil, nil, "", &fault{code: "tool_not_found", message: "Tool is not exposed by this source endpoint and credential mode.", next: "Inspect list_cluster_tools for this cluster; source read-only mode may omit write tools."}
}

func (f *facade) toolSchema(ctx context.Context, p hub.Principal, e *envelope, input schemaInput) error {
	connection, tool, hash, err := f.toolSource(ctx, p, e, input)
	if err != nil {
		return err
	}
	defer connection.session.Close()
	e.Data = struct {
		CatalogHash string    `json:"catalogHash"`
		Tool        *mcp.Tool `json:"tool"`
		ReadOnly    bool      `json:"readOnly"`
	}{hash, tool, connection.readOnly}
	return nil
}

func (f *facade) capabilities(ctx context.Context, p hub.Principal, e *envelope, input clusterInput) error {
	return f.callTool(ctx, p, e, callInput{ClusterID: input.ClusterID, Tool: "get_capabilities"})
}

func (f *facade) callTool(ctx context.Context, p hub.Principal, e *envelope, input callInput) error {
	connection, tool, _, err := f.toolSource(ctx, p, e, schemaInput{input.ClusterID, input.Tool, input.CatalogHash})
	if err != nil {
		return err
	}
	defer connection.session.Close()
	arguments := make(map[string]json.RawMessage, len(input.Arguments)+1)
	for key, value := range input.Arguments {
		arguments[key] = value
	}
	if input.Tool == "list_resources" {
		encoded, _ := json.Marshal(tool.InputSchema)
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		_ = json.Unmarshal(encoded, &schema)
		if _, supported := schema.Properties["limit"]; !supported {
			return &fault{code: "source_upgrade_required", message: "Source lacks native resource pagination.", next: "Upgrade the source Agent before listing resources through this Hub."}
		}
		if _, supplied := arguments["limit"]; !supplied {
			arguments["limit"] = json.RawMessage(`50`)
		}
		var limit int
		if json.Unmarshal(arguments["limit"], &limit) != nil || limit < 1 || limit > 200 {
			return invalid("list_resources limit must be an integer from 1 to 200")
		}
	}
	encoded, marshalErr := json.Marshal(arguments)
	if marshalErr != nil || len(encoded) > maxRequestBytes/2 {
		return invalid("Native arguments must be valid JSON within 512 KiB")
	}
	// Tool annotations are advisory; only the read-only transport is a boundary.
	mayWrite := !connection.readOnly
	if mayWrite {
		e.Outcome = "unknown"
	}
	result, err := connection.session.CallTool(ctx, &mcp.CallToolParams{Name: input.Tool, Arguments: arguments})
	if err != nil {
		return classify(ctx, connection, err, mayWrite)
	}
	if result == nil {
		return &fault{code: "source_protocol_error", message: "Source returned no tool result.", unknown: mayWrite}
	}
	if mayWrite {
		e.Outcome = "reported"
	}
	if stamp, ok := result.Meta["kubeatlas/observedAt"].(string); ok {
		if observed, err := time.Parse(time.RFC3339Nano, stamp); err == nil {
			observed = observed.UTC()
			e.ObservedAt = &observed
		}
	}
	e.Data = result
	if result.IsError {
		code := sourceErrorCode(result)
		return &fault{code: code, message: "Source tool returned an error; its original result is retained in data.", next: "Inspect data for source coverage and corrective guidance.", unknown: mayWrite}
	}
	return nil
}

func sourceErrorCode(result *mcp.CallToolResult) string {
	var payload struct {
		Code string `json:"error_code"`
	}
	if result.StructuredContent != nil {
		body, _ := json.Marshal(result.StructuredContent)
		_ = json.Unmarshal(body, &payload)
	}
	if payload.Code == "" && len(result.Content) == 1 {
		if text, ok := result.Content[0].(*mcp.TextContent); ok {
			_ = json.Unmarshal([]byte(text.Text), &payload)
		}
	}
	if payload.Code == "permission_denied" || readreason.Known(readreason.Code(payload.Code)) {
		return payload.Code
	}
	return "source_tool_error"
}
