package hubstate

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"slices"
	"strings"
)

const agentPrefix = "ka_agent_"
const mcpPrefix = "ka_mcp_"

func mint(prefix string) (string, string, error) {
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return "", "", failure("store_unavailable")
	}
	token := prefix + base64.RawURLEncoding.EncodeToString(secret[:])
	return token, digest(token), nil
}

func digest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func tokenShape(token, prefix string) bool {
	if len(token) != len(prefix)+43 || !strings.HasPrefix(token, prefix) {
		return false
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(token[len(prefix):])
	return err == nil && len(decoded) == 32
}

func sameDigest(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

func (s *Store) CreateCluster(input ClusterInput) (IssuedAgent, error) {
	if !validCluster(input) {
		return IssuedAgent{}, failure("invalid_parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data.Clusters[input.ID]; ok {
		return IssuedAgent{}, failure("conflict")
	}
	if len(s.data.Clusters) >= maxClusters {
		return IssuedAgent{}, failure("capacity_exceeded")
	}
	token, hash, err := mint(agentPrefix)
	if err != nil {
		return IssuedAgent{}, err
	}
	now := s.now().UTC()
	c := Cluster{ClusterInput: input, CreatedAt: now, UpdatedAt: now}
	next := s.next(now)
	next.Clusters[input.ID] = storedCluster{Cluster: c, Digest: hash}
	if err := s.commit(next); err != nil {
		return IssuedAgent{}, err
	}
	return IssuedAgent{Cluster: cloneCluster(c), Token: token}, nil
}

func (s *Store) UpdateCluster(id string, input ClusterInput) (Cluster, error) {
	if id != input.ID || !validCluster(input) {
		return Cluster{}, failure("invalid_parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.data.Clusters[id]
	if !ok {
		return Cluster{}, failure("cluster_not_found")
	}
	now := s.now().UTC()
	if now.Before(c.UpdatedAt) {
		return Cluster{}, failure("store_unavailable")
	}
	c.ClusterInput, c.UpdatedAt = input, now
	next := s.next(now)
	next.Clusters[id] = c
	if err := s.commit(next); err != nil {
		return Cluster{}, err
	}
	return cloneCluster(c.Cluster), nil
}

func (s *Store) RotateAgentToken(id string) (IssuedAgent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.data.Clusters[id]
	if !ok {
		return IssuedAgent{}, failure("cluster_not_found")
	}
	now := s.now().UTC()
	if now.Before(c.UpdatedAt) {
		return IssuedAgent{}, failure("store_unavailable")
	}
	token, hash, err := mint(agentPrefix)
	if err != nil {
		return IssuedAgent{}, err
	}
	c.Digest, c.UpdatedAt, c.RevokedAt = hash, now, nil
	next := s.next(now)
	next.Clusters[id] = c
	if err := s.commit(next); err != nil {
		return IssuedAgent{}, err
	}
	return IssuedAgent{Cluster: cloneCluster(c.Cluster), Token: token}, nil
}

func (s *Store) RevokeAgentToken(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.data.Clusters[id]
	if !ok {
		return failure("cluster_not_found")
	}
	if c.RevokedAt != nil {
		return nil
	}
	now := s.now().UTC()
	if now.Before(c.UpdatedAt) {
		return failure("store_unavailable")
	}
	c.RevokedAt, c.UpdatedAt = &now, now
	next := s.next(now)
	next.Clusters[id] = c
	return s.commit(next)
}

func (s *Store) AuthenticateAgent(token, claimedID string) (Cluster, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.data.Clusters[claimedID]
	stored := c.Digest
	if !ok {
		stored = strings.Repeat("0", 64)
	}
	matched := sameDigest(stored, digest(token))
	if !tokenShape(token, agentPrefix) || !matched || !ok || c.RevokedAt != nil {
		return Cluster{}, failure("credential_invalid")
	}
	return cloneCluster(c.Cluster), nil
}

func (s *Store) Clusters() []Cluster {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := make([]Cluster, 0, len(s.data.Clusters))
	for _, c := range s.data.Clusters {
		list = append(list, cloneCluster(c.Cluster))
	}
	slices.SortFunc(list, func(a, b Cluster) int { return strings.Compare(a.ID, b.ID) })
	return list
}

func (s *Store) Cluster(id string) (Cluster, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.data.Clusters[id]
	return cloneCluster(c.Cluster), ok
}

func (s *Store) SaveGroup(input GroupInput) (Group, error) {
	if !idPattern.MatchString(input.ID) || !validText(input.Name, 256, true) {
		return Group{}, failure("invalid_parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ids, err := scope(input.ClusterIDs, s.data.Clusters, true)
	if err != nil {
		return Group{}, err
	}
	old, exists := s.data.Groups[input.ID]
	if !exists && len(s.data.Groups) >= maxGroups {
		return Group{}, failure("capacity_exceeded")
	}
	now := s.now().UTC()
	g := Group{ID: input.ID, Name: input.Name, ClusterIDs: ids, CreatedAt: now, UpdatedAt: now}
	if exists {
		if now.Before(old.UpdatedAt) {
			return Group{}, failure("store_unavailable")
		}
		g.CreatedAt = old.CreatedAt
	}
	next := s.next(now)
	next.Groups[g.ID] = g
	if err := s.commit(next); err != nil {
		return Group{}, err
	}
	return cloneGroup(g), nil
}

func (s *Store) DeleteGroup(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !idPattern.MatchString(id) {
		return failure("invalid_parameters")
	}
	if _, exists := s.data.Groups[id]; !exists {
		return nil
	}
	next := s.next(s.now().UTC())
	delete(next.Groups, id)
	return s.commit(next)
}

func (s *Store) Groups() []Group {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := make([]Group, 0, len(s.data.Groups))
	for _, g := range s.data.Groups {
		list = append(list, cloneGroup(g))
	}
	slices.SortFunc(list, func(a, b Group) int { return strings.Compare(a.ID, b.ID) })
	return list
}

func (s *Store) Group(id string) (Group, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.data.Groups[id]
	return cloneGroup(g), ok
}
