package fleetsync

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
)

const (
	stateVersion    = 1
	maxStateBytes   = 256 << 20
	maxClusters     = 100
	maxGroups       = 100
	maxGroupMembers = 100
)

type persistedState struct {
	Version  int       `json:"version"`
	Clusters []Cluster `json:"clusters"`
	Groups   []Group   `json:"groups"`
}

type stateWriter func(path string, data []byte) (committed bool, err error)

type Store struct {
	mu       sync.RWMutex
	path     string
	lock     *flock.Flock
	write    stateWriter
	clusters map[string]Cluster
	groups   map[string]Group
	closed   bool
}

func OpenStore(path string) (*Store, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, errors.New("Fleet state path is invalid")
	}
	parent := filepath.Dir(absolute)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return nil, errors.New("Fleet state directory is unavailable")
	}
	info, err := os.Stat(parent)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("Fleet state directory must be private")
	}

	lock := flock.New(absolute + ".lock")
	locked, err := lock.TryLock()
	if err != nil {
		return nil, errors.New("Fleet state lock is unavailable")
	}
	if !locked {
		return nil, errors.New("Fleet state is already in use")
	}
	if err := os.Chmod(absolute+".lock", 0o600); err != nil {
		_ = lock.Unlock()
		_ = lock.Close()
		return nil, errors.New("Fleet state lock is unavailable")
	}
	store := &Store{path: absolute, lock: lock, write: writeStateFile, clusters: map[string]Cluster{}, groups: map[string]Group{}}
	data, err := readStateFile(absolute)
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	if data != nil {
		state, err := decodeState(data)
		if err != nil {
			_ = store.Close()
			return nil, errors.New("Fleet state is invalid")
		}
		if err := store.loadState(state); err != nil {
			_ = store.Close()
			return nil, errors.New("Fleet state is invalid")
		}
	}
	return store, nil
}

func readStateFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("Fleet state file must be a private regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("Fleet state cannot be read")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxStateBytes+1))
	if err != nil || len(data) > maxStateBytes {
		return nil, errors.New("Fleet state exceeds the size limit")
	}
	return data, nil
}

func decodeState(data []byte) (persistedState, error) {
	var state persistedState
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return persistedState{}, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return persistedState{}, errors.New("Fleet state has trailing data")
	}
	return state, nil
}

func (s *Store) loadState(state persistedState) error {
	if state.Version != stateVersion || len(state.Clusters) > maxClusters || len(state.Groups) > maxGroups {
		return errors.New("invalid state version or size")
	}
	now := time.Now()
	for _, cluster := range state.Clusters {
		if err := cluster.Snapshot.Validate(now); err != nil {
			return err
		}
		if cluster.ReceivedAt.IsZero() || cluster.ReceivedAt.After(now.Add(time.Minute)) {
			return errors.New("invalid receive time")
		}
		if _, exists := s.clusters[cluster.ClusterID]; exists {
			return errors.New("duplicate cluster")
		}
		s.clusters[cluster.ClusterID] = cloneCluster(cluster)
	}
	for _, group := range state.Groups {
		if err := validateGroup(group); err != nil {
			return err
		}
		if _, exists := s.groups[group.ID]; exists {
			return errors.New("duplicate group")
		}
		s.groups[group.ID] = cloneGroup(group)
	}
	if len(allClusterIDs(s.clusters, s.groups)) > maxClusters {
		return errors.New("too many cluster IDs")
	}
	return nil
}

func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	lock := s.lock
	s.lock = nil
	s.mu.Unlock()
	if lock == nil {
		return nil
	}
	unlockErr := lock.Unlock()
	closeErr := lock.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}

func (s *Store) Ingest(snapshot Snapshot) error {
	if err := snapshot.Validate(time.Now()); err != nil {
		return err
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil || len(encoded) > MaxSnapshotBytes {
		return errors.New("snapshot exceeds the size limit")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("Fleet state is closed")
	}
	existing, found := s.clusters[snapshot.ClusterID]
	if found {
		if existing.SourceID != snapshot.SourceID {
			return errors.New("cluster source identity changed")
		}
		if snapshot.ObservedAt.Before(existing.ObservedAt) {
			return errors.New("snapshot is older than the stored observation")
		}
		if snapshot.ObservedAt.Equal(existing.ObservedAt) {
			oldPayload, _ := json.Marshal(existing.Snapshot)
			if bytes.Equal(oldPayload, encoded) {
				return nil
			}
			return errors.New("snapshot observation time was reused with different data")
		}
	}
	if !found && len(s.clusters) >= maxClusters {
		return errors.New("cluster limit reached")
	}
	next := cloneClusters(s.clusters)
	next[snapshot.ClusterID] = Cluster{Snapshot: cloneSnapshot(snapshot), ReceivedAt: time.Now().UTC()}
	if len(allClusterIDs(next, s.groups)) > maxClusters {
		return errors.New("cluster limit reached")
	}
	committed, err := s.persistLocked(next, s.groups)
	if committed {
		s.clusters = next
	}
	return err
}

func (s *Store) PutGroup(group Group) error {
	if err := validateGroup(group); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("Fleet state is closed")
	}
	next := cloneGroups(s.groups)
	next[group.ID] = cloneGroup(group)
	if len(next) > maxGroups || len(allClusterIDs(s.clusters, next)) > maxClusters {
		return errors.New("Fleet group or cluster limit reached")
	}
	committed, err := s.persistLocked(s.clusters, next)
	if committed {
		s.groups = next
	}
	return err
}

func (s *Store) DeleteGroup(id string) error {
	if !ValidateID(id) {
		return errors.New("invalid fleet ID")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("Fleet state is closed")
	}
	if _, exists := s.groups[id]; !exists {
		return errors.New("unknown fleet")
	}
	next := cloneGroups(s.groups)
	delete(next, id)
	committed, err := s.persistLocked(s.clusters, next)
	if committed {
		s.groups = next
	}
	return err
}

func validateGroup(group Group) error {
	if !ValidateID(group.ID) || strings.TrimSpace(group.Name) == "" || len(group.Name) > 256 || len(group.ClusterIDs) > maxGroupMembers {
		return errors.New("invalid fleet")
	}
	seen := make(map[string]struct{}, len(group.ClusterIDs))
	for _, id := range group.ClusterIDs {
		if !ValidateID(id) {
			return errors.New("invalid fleet cluster ID")
		}
		if _, exists := seen[id]; exists {
			return errors.New("duplicate fleet cluster ID")
		}
		seen[id] = struct{}{}
	}
	return nil
}

func (s *Store) Context(selection Selection) (AgentContext, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return AgentContext{}, errors.New("Fleet state is closed")
	}
	return AssembleContext(selection, cloneClusters(s.clusters), cloneGroups(s.groups), time.Now())
}

func (s *Store) Resource(selection ResourceSelection) (ClusterContext, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return ClusterContext{}, errors.New("Fleet state is closed")
	}
	cluster, ok := s.clusters[selection.ClusterID]
	if !ok {
		return ClusterContext{}, errors.New("unknown clusterId")
	}
	return ResourceContext(selection, cloneCluster(cluster), time.Now())
}

func (s *Store) Inventory() []ClusterContext {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.clusters))
	for id := range s.clusters {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]ClusterContext, 0, len(ids))
	now := time.Now()
	for _, id := range ids {
		view := contextForCluster(s.clusters[id], now)
		view.Documents = []Document{}
		result = append(result, view)
	}
	return result
}

func (s *Store) Groups() []Group {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.groups))
	for id := range s.groups {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]Group, 0, len(ids))
	for _, id := range ids {
		result = append(result, cloneGroup(s.groups[id]))
	}
	return result
}

func (s *Store) SourceMatches(clusterID, sourceID string) bool {
	if s == nil || sourceID == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	cluster, ok := s.clusters[clusterID]
	return !s.closed && ok && cluster.SourceID == sourceID
}

func (s *Store) persistLocked(clusters map[string]Cluster, groups map[string]Group) (bool, error) {
	state := persistedState{Version: stateVersion, Clusters: make([]Cluster, 0, len(clusters)), Groups: make([]Group, 0, len(groups))}
	clusterIDs := make([]string, 0, len(clusters))
	for id := range clusters {
		clusterIDs = append(clusterIDs, id)
	}
	sort.Strings(clusterIDs)
	for _, id := range clusterIDs {
		state.Clusters = append(state.Clusters, cloneCluster(clusters[id]))
	}
	groupIDs := make([]string, 0, len(groups))
	for id := range groups {
		groupIDs = append(groupIDs, id)
	}
	sort.Strings(groupIDs)
	for _, id := range groupIDs {
		state.Groups = append(state.Groups, cloneGroup(groups[id]))
	}
	data, err := json.Marshal(state)
	if err != nil || len(data) > maxStateBytes {
		return false, errors.New("Fleet state exceeds the size limit")
	}
	return s.write(s.path, data)
}

func writeStateFile(path string, data []byte) (bool, error) {
	return writeStateFileWithSync(path, data, func(dir *os.File) error { return dir.Sync() })
}

func writeStateFileWithSync(path string, data []byte, syncDirectory func(*os.File) error) (bool, error) {
	temp, err := os.CreateTemp(filepath.Dir(path), ".fleet-state-*")
	if err != nil {
		return false, errors.New("Fleet state cannot be written")
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return false, errors.New("Fleet state cannot be secured")
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return false, errors.New("Fleet state cannot be written")
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return false, errors.New("Fleet state cannot be synchronized")
	}
	if err := temp.Close(); err != nil {
		return false, errors.New("Fleet state cannot be closed")
	}
	if err := os.Rename(tempPath, path); err != nil {
		return false, errors.New("Fleet state cannot be replaced")
	}
	committed := true
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return committed, errors.New("Fleet state directory cannot be synchronized")
	}
	defer dir.Close()
	if err := syncDirectory(dir); err != nil {
		return committed, errors.New("Fleet state directory cannot be synchronized")
	}
	return committed, nil
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	snapshot.Coverage = cloneCollectionCoverage(snapshot.Coverage)
	snapshot.Capabilities = cloneSourceCapabilities(snapshot.Capabilities)
	snapshot.RelationshipFacts = slices.Clone(snapshot.RelationshipFacts)
	snapshot.Documents = slices.Clone(snapshot.Documents)
	for i := range snapshot.Documents {
		snapshot.Documents[i] = cloneDocument(snapshot.Documents[i])
	}
	snapshot.Limitations = slices.Clone(snapshot.Limitations)
	return snapshot
}

func cloneCluster(cluster Cluster) Cluster {
	cluster.Snapshot = cloneSnapshot(cluster.Snapshot)
	return cluster
}

func cloneGroup(group Group) Group {
	group.ClusterIDs = slices.Clone(group.ClusterIDs)
	return group
}

func cloneClusters(clusters map[string]Cluster) map[string]Cluster {
	result := make(map[string]Cluster, len(clusters))
	for id, cluster := range clusters {
		result[id] = cloneCluster(cluster)
	}
	return result
}

func cloneGroups(groups map[string]Group) map[string]Group {
	result := make(map[string]Group, len(groups))
	for id, group := range groups {
		result[id] = cloneGroup(group)
	}
	return result
}

func allClusterIDs(clusters map[string]Cluster, groups map[string]Group) map[string]struct{} {
	ids := make(map[string]struct{}, len(clusters))
	for id := range clusters {
		ids[id] = struct{}{}
	}
	for _, group := range groups {
		for _, id := range group.ClusterIDs {
			ids[id] = struct{}{}
		}
	}
	return ids
}
