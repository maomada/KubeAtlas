package portal

import (
	"net/http"
	"strings"
	"time"

	"github.com/maomada/KubeAtlas/internal/hub"
	"github.com/maomada/KubeAtlas/internal/hubstate"
)

type hubDirectoryEntry struct {
	hubstate.Cluster
	Configured bool       `json:"configured"`
	State      string     `json:"state"`
	Agent      *hub.Agent `json:"agent,omitempty"`
}

func (p *portalHub) directory() []hubDirectoryEntry {
	agents := make(map[string]hub.Agent)
	for _, agent := range p.transport.Agents() {
		agents[agent.ClusterID] = agent
	}
	entries := make([]hubDirectoryEntry, 0)
	for _, cluster := range p.store.Clusters() {
		entry := hubDirectoryEntry{Cluster: cluster, Configured: true, State: "offline"}
		if cluster.RevokedAt != nil {
			entry.State = "revoked"
		} else if agent, ok := agents[cluster.ID]; ok {
			entry.State, entry.Agent = "connected", &agent
		}
		entries = append(entries, entry)
	}
	return entries
}

func (p *portalHub) manageClusters(w http.ResponseWriter, r *http.Request, member hubstate.Member) {
	const base = "/api/hub/clusters"
	if r.URL.Path == base {
		if r.Method == http.MethodGet {
			entries := p.directory()
			if member.Role != "owner" && member.Role != "admin" {
				allowed := make(map[string]struct{}, len(member.ClusterIDs))
				for _, id := range member.ClusterIDs {
					allowed[id] = struct{}{}
				}
				filtered := make([]hubDirectoryEntry, 0, len(entries))
				for _, entry := range entries {
					if _, ok := allowed[entry.ID]; ok {
						filtered = append(filtered, entry)
					}
				}
				entries = filtered
			}
			hubJSON(w, http.StatusOK, map[string]any{"clusters": entries, "serverTime": time.Now().UTC()})
			return
		}
		if !requireHubAdmin(w, member) {
			return
		}
		var input hubstate.ClusterInput
		if !hubMethod(w, r, http.MethodPost) || !hubDecode(w, r, &input, "id", "name", "description", "environment", "region") {
			return
		}
		issued, err := p.store.CreateCluster(input)
		if err != nil {
			hubStoreError(w, err)
			return
		}
		hubJSON(w, http.StatusCreated, issued)
		return
	}
	if !requireHubAdmin(w, member) {
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, base+"/"), "/")
	if len(parts) == 1 && parts[0] != "" {
		var input hubstate.ClusterInput
		if !hubMethod(w, r, http.MethodPut) || !hubDecode(w, r, &input, "id", "name", "description", "environment", "region") {
			return
		}
		cluster, err := p.store.UpdateCluster(parts[0], input)
		if err != nil {
			hubStoreError(w, err)
			return
		}
		hubJSON(w, http.StatusOK, cluster)
		return
	}
	if len(parts) != 2 || parts[0] == "" || (parts[1] != "rotate" && parts[1] != "revoke") {
		hubHTTPError(w, http.StatusNotFound, "not_found", "Cluster route not found")
		return
	}
	if !hubMethod(w, r, http.MethodPost) || !hubDecode(w, r, &struct{}{}) {
		return
	}
	if parts[1] == "rotate" {
		issued, err := p.store.RotateAgentToken(parts[0])
		if err != nil {
			hubStoreError(w, err)
			return
		}
		p.transport.Disconnect(parts[0])
		hubJSON(w, http.StatusOK, issued)
		return
	}
	if err := p.store.RevokeAgentToken(parts[0]); err != nil {
		hubStoreError(w, err)
		return
	}
	p.transport.Disconnect(parts[0])
	w.WriteHeader(http.StatusNoContent)
}

func (p *portalHub) manageGroups(w http.ResponseWriter, r *http.Request, member hubstate.Member) {
	const base = "/api/hub/groups"
	if r.URL.Path == base && r.Method == http.MethodGet {
		groups := p.store.Groups()
		if member.Role != "owner" && member.Role != "admin" {
			allowed := make(map[string]struct{}, len(member.ClusterIDs))
			for _, id := range member.ClusterIDs {
				allowed[id] = struct{}{}
			}
			filtered := make([]hubstate.Group, 0, len(groups))
			for _, group := range groups {
				original := len(group.ClusterIDs)
				ids := make([]string, 0, original)
				for _, id := range group.ClusterIDs {
					if _, ok := allowed[id]; ok {
						ids = append(ids, id)
					}
				}
				if len(ids) != 0 || original == 0 {
					group.ClusterIDs = ids
					filtered = append(filtered, group)
				}
			}
			groups = filtered
		}
		hubJSON(w, http.StatusOK, map[string]any{"groups": groups})
		return
	}
	if !requireHubAdmin(w, member) {
		return
	}
	method, id := http.MethodPost, ""
	if r.URL.Path != base {
		id = strings.TrimPrefix(r.URL.Path, base+"/")
		if id == "" || strings.Contains(id, "/") {
			hubHTTPError(w, http.StatusNotFound, "not_found", "Group route not found")
			return
		}
		method = http.MethodPut
		if r.Method == http.MethodDelete {
			if err := p.store.DeleteGroup(id); err != nil {
				hubStoreError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	var input hubstate.GroupInput
	if !hubMethod(w, r, method) || !hubDecode(w, r, &input, "id", "name", "clusterIds") {
		return
	}
	if id != "" && input.ID != id {
		hubHTTPError(w, http.StatusBadRequest, "invalid_parameters", "Group ID cannot change")
		return
	}
	group, err := p.store.SaveGroup(input)
	if err != nil {
		hubStoreError(w, err)
		return
	}
	hubJSON(w, http.StatusOK, group)
}
