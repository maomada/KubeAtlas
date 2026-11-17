package integration

// WorkspaceKind is one Kind a workspace lists from KubeAtlas's dynamic cache.
type WorkspaceKind struct {
	Key           string
	Group         string
	Kind          string
	Resource      string
	ClusterScoped bool
}
