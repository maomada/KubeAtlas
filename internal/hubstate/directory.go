package hubstate

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"slices"
	"strings"
	"time"
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
	return s.CreateClusterForOrganization("self-hosted", input)
}

func (s *Store) CreateClusterForOrganization(organizationID string, input ClusterInput) (IssuedAgent, error) {
	if !validCluster(input) {
		return IssuedAgent{}, failure("invalid_parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createClusterForOrganizationLocked(organizationID, input)
}

func (s *Store) CreateClusterForEnrollment(organizationID, ownerID, localOwnerID string, input ClusterInput) (IssuedAgent, error) {
	if !validCluster(input) {
		return IssuedAgent{}, failure("invalid_parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.activeOrganizationOwnerLocked(organizationID, ownerID, localOwnerID) ||
		!s.legacyClusterScopeAllowsLocked(organizationID, ownerID, localOwnerID, input.ID, true) {
		return IssuedAgent{}, failure("permission_denied")
	}
	return s.createClusterForOrganizationLocked(organizationID, input)
}

func (s *Store) createClusterForOrganizationLocked(organizationID string, input ClusterInput) (IssuedAgent, error) {
	if _, ok := s.data.Organizations[organizationID]; !ok {
		return IssuedAgent{}, failure("organization_not_found")
	}
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
	c := Cluster{ClusterInput: input, OrganizationID: organizationID, CreatedAt: now, UpdatedAt: now}
	next := s.next(now)
	next.Clusters[input.ID] = storedCluster{Cluster: c, Digest: hash}
	if err := s.commit(next); err != nil {
		return IssuedAgent{}, err
	}
	return IssuedAgent{Cluster: cloneCluster(c), Token: token}, nil
}

func (s *Store) activeOrganizationOwnerLocked(organizationID, ownerID, localOwnerID string) bool {
	account, ok := s.data.Accounts[ownerID]
	if !ok || account.Status != "active" {
		return false
	}
	if ownerID == localOwnerID && account.Local && account.PlatformOwner {
		return true
	}
	membership, ok := s.data.Memberships[organizationID][ownerID]
	return ok && membership.Role == "owner"
}

func (s *Store) legacyClusterScopeAllowsLocked(organizationID, ownerID, localOwnerID, clusterID string, creating bool) bool {
	account := s.data.Accounts[ownerID]
	if ownerID == localOwnerID && account.Local && account.PlatformOwner {
		return true
	}
	membership, ok := s.data.Memberships[organizationID][ownerID]
	if !ok || !membership.LegacyRestricted || membership.LegacyAllClusters {
		return true
	}
	return !creating && slices.Contains(membership.ClusterIDs, clusterID)
}

func (s *Store) UpdateCluster(id string, input ClusterInput) (Cluster, error) {
	return s.UpdateClusterForOrganization("self-hosted", id, input)
}

func (s *Store) UpdateClusterForOrganization(organizationID, id string, input ClusterInput) (Cluster, error) {
	if id != input.ID || !validCluster(input) {
		return Cluster{}, failure("invalid_parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.data.Clusters[id]
	if !ok || c.OrganizationID != organizationID {
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
	return s.RotateAgentTokenForOrganization("self-hosted", id)
}

func (s *Store) RotateAgentTokenForOrganization(organizationID, id string) (IssuedAgent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.data.Clusters[id]
	if !ok || c.OrganizationID != organizationID {
		return IssuedAgent{}, failure("cluster_not_found")
	}
	if c.RevokedAt != nil {
		return IssuedAgent{}, failure("conflict")
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
	return s.RevokeAgentTokenForOrganization("self-hosted", id)
}

func (s *Store) RevokeAgentTokenForOrganization(organizationID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.data.Clusters[id]
	if !ok || c.OrganizationID != organizationID {
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

func (s *Store) DeleteRevokedCluster(organizationID, ownerID, localOwnerID, id string) ([]Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.activeOrganizationOwnerLocked(organizationID, ownerID, localOwnerID) ||
		!s.legacyClusterScopeAllowsLocked(organizationID, ownerID, localOwnerID, id, false) {
		return nil, failure("permission_denied")
	}
	cluster, exists := s.data.Clusters[id]
	if !exists || cluster.OrganizationID != organizationID {
		return nil, failure("cluster_not_found")
	}
	if cluster.RevokedAt == nil {
		return nil, failure("conflict")
	}
	now := s.now().UTC()
	if now.Before(cluster.UpdatedAt) {
		return nil, failure("store_unavailable")
	}
	next := s.next(now)
	delete(next.Clusters, id)
	removeClusterReferences(next, organizationID, id, now)
	revoked := revokeClusterCredentials(next, organizationID, id, now)
	if err := s.commit(next); err != nil {
		return nil, err
	}
	return revoked, nil
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
	return s.ClustersForOrganization("self-hosted")
}

func (s *Store) ClustersForOrganization(organizationID string) []Cluster {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := make([]Cluster, 0, len(s.data.Clusters))
	for _, c := range s.data.Clusters {
		if c.OrganizationID == organizationID {
			list = append(list, cloneCluster(c.Cluster))
		}
	}
	slices.SortFunc(list, func(a, b Cluster) int { return strings.Compare(a.ID, b.ID) })
	return list
}

func (s *Store) Cluster(id string) (Cluster, bool) {
	return s.ClusterForOrganization("self-hosted", id)
}

func (s *Store) ClusterForOrganization(organizationID, id string) (Cluster, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.data.Clusters[id]
	if ok && c.OrganizationID != organizationID {
		ok = false
	}
	return cloneCluster(c.Cluster), ok
}

func (s *Store) TransferCluster(id, organizationID string) (Cluster, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cluster, ok := s.data.Clusters[id]
	if !ok {
		return Cluster{}, failure("cluster_not_found")
	}
	if _, ok := s.data.Organizations[organizationID]; !ok {
		return Cluster{}, failure("organization_not_found")
	}
	if cluster.OrganizationID == organizationID {
		return Cluster{}, failure("conflict")
	}
	now := s.now().UTC()
	if now.Before(cluster.UpdatedAt) {
		return Cluster{}, failure("store_unavailable")
	}
	oldOrganizationID := cluster.OrganizationID
	next := s.next(now)
	cluster.OrganizationID, cluster.UpdatedAt = organizationID, now
	next.Clusters[id] = cluster
	for _, accountID := range removeClusterReferences(next, oldOrganizationID, id, now) {
		revokeCredentials(next, oldOrganizationID, accountID, now)
	}
	revokeClusterCredentials(next, oldOrganizationID, id, now)
	if err := s.commit(next); err != nil {
		return Cluster{}, err
	}
	return cloneCluster(cluster.Cluster), nil
}

func removeClusterReferences(next storeFile, organizationID, id string, now time.Time) []string {
	for groupID, group := range next.Groups {
		if group.OrganizationID != organizationID || !slices.Contains(group.ClusterIDs, id) {
			continue
		}
		group.ClusterIDs = slices.DeleteFunc(group.ClusterIDs, func(clusterID string) bool { return clusterID == id })
		group.UpdatedAt = now
		next.Groups[groupID] = group
	}
	var affectedMembers []string
	for accountID, membership := range next.Memberships[organizationID] {
		updated := slices.DeleteFunc(membership.ClusterIDs, func(clusterID string) bool { return clusterID == id })
		if len(updated) != len(membership.ClusterIDs) {
			membership.ClusterIDs, membership.UpdatedAt = updated, now
			next.Memberships[organizationID][accountID] = membership
			affectedMembers = append(affectedMembers, accountID)
		}
	}
	return affectedMembers
}

func revokeClusterCredentials(next storeFile, organizationID, id string, now time.Time) []Credential {
	var revoked []Credential
	for credentialID, credential := range next.Credentials {
		if credential.OrganizationID == organizationID && slices.Contains(credential.ClusterIDs, id) && credential.RevokedAt == nil {
			credential.RevokedAt = &now
			next.Credentials[credentialID] = credential
			revoked = append(revoked, cloneCredential(credential.Credential))
		}
	}
	return revoked
}

func (s *Store) ReenrollCluster(organizationID, id string, input ClusterInput) (IssuedAgent, error) {
	if id != input.ID || !validCluster(input) {
		return IssuedAgent{}, failure("invalid_parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reenrollClusterLocked(organizationID, id, input)
}

func (s *Store) ReenrollClusterForEnrollment(organizationID, ownerID, localOwnerID, id string, input ClusterInput) (IssuedAgent, error) {
	if id != input.ID || !validCluster(input) {
		return IssuedAgent{}, failure("invalid_parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.activeOrganizationOwnerLocked(organizationID, ownerID, localOwnerID) ||
		!s.legacyClusterScopeAllowsLocked(organizationID, ownerID, localOwnerID, id, false) {
		return IssuedAgent{}, failure("permission_denied")
	}
	return s.reenrollClusterLocked(organizationID, id, input)
}

func (s *Store) reenrollClusterLocked(organizationID, id string, input ClusterInput) (IssuedAgent, error) {
	cluster, ok := s.data.Clusters[id]
	if !ok || cluster.OrganizationID != organizationID || cluster.RevokedAt == nil {
		return IssuedAgent{}, failure("cluster_not_found")
	}
	token, hash, err := mint(agentPrefix)
	if err != nil {
		return IssuedAgent{}, err
	}
	now := s.now().UTC()
	if now.Before(cluster.UpdatedAt) {
		return IssuedAgent{}, failure("store_unavailable")
	}
	cluster.ClusterInput, cluster.Digest, cluster.RevokedAt, cluster.UpdatedAt = input, hash, nil, now
	next := s.next(now)
	next.Clusters[id] = cluster
	if err := s.commit(next); err != nil {
		return IssuedAgent{}, err
	}
	return IssuedAgent{Cluster: cloneCluster(cluster.Cluster), Token: token}, nil
}

func (s *Store) SaveGroup(input GroupInput) (Group, error) {
	return s.SaveGroupForOrganization("self-hosted", input)
}

func (s *Store) SaveGroupForOrganization(organizationID string, input GroupInput) (Group, error) {
	if !idPattern.MatchString(input.ID) || !validName(input.Name) {
		return Group{}, failure("invalid_parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data.Organizations[organizationID]; !ok {
		return Group{}, failure("organization_not_found")
	}
	ids, err := scopeOrganization(input.ClusterIDs, organizationID, s.data.Clusters, true)
	if err != nil {
		return Group{}, err
	}
	old, exists := s.data.Groups[input.ID]
	if exists && old.OrganizationID != organizationID {
		return Group{}, failure("conflict")
	}
	if !exists && len(s.data.Groups) >= maxGroups {
		return Group{}, failure("capacity_exceeded")
	}
	now := s.now().UTC()
	g := Group{ID: input.ID, OrganizationID: organizationID, Name: input.Name, ClusterIDs: ids, CreatedAt: now, UpdatedAt: now}
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
	return s.DeleteGroupForOrganization("self-hosted", id)
}

func (s *Store) DeleteGroupForOrganization(organizationID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !idPattern.MatchString(id) {
		return failure("invalid_parameters")
	}
	if group, exists := s.data.Groups[id]; !exists || group.OrganizationID != organizationID {
		return nil
	}
	next := s.next(s.now().UTC())
	delete(next.Groups, id)
	return s.commit(next)
}

func (s *Store) Groups() []Group {
	return s.GroupsForOrganization("self-hosted")
}

func (s *Store) GroupsForOrganization(organizationID string) []Group {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := make([]Group, 0, len(s.data.Groups))
	for _, g := range s.data.Groups {
		if g.OrganizationID == organizationID {
			list = append(list, cloneGroup(g))
		}
	}
	slices.SortFunc(list, func(a, b Group) int { return strings.Compare(a.ID, b.ID) })
	return list
}

func (s *Store) Group(id string) (Group, bool) {
	return s.GroupForOrganization("self-hosted", id)
}

func (s *Store) GroupForOrganization(organizationID, id string) (Group, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.data.Groups[id]
	if ok && g.OrganizationID != organizationID {
		ok = false
	}
	return cloneGroup(g), ok
}
