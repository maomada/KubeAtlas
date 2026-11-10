package hubstate

import (
	"slices"
	"strings"

	pkgauth "github.com/maomada/KubeAtlas/pkg/auth"
)

func (s *Store) Organization() Organization {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.Organization == nil {
		return Organization{ID: "self-hosted", Name: "KubeAtlas"}
	}
	return *s.data.Organization
}

func (s *Store) SetOrganizationName(name string) (Organization, error) {
	if !validName(name) {
		return Organization{}, failure("invalid_parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	next := s.next(now)
	next.Organization.Name = name
	if err := s.commit(next); err != nil {
		return Organization{}, err
	}
	return *next.Organization, nil
}

func (s *Store) Member(id string) (Member, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	member, ok := s.data.Members[id]
	return cloneMember(member), ok
}

func (s *Store) Members() []Member {
	s.mu.Lock()
	defer s.mu.Unlock()
	members := make([]Member, 0, len(s.data.Members))
	for _, member := range s.data.Members {
		members = append(members, cloneMember(member))
	}
	slices.SortFunc(members, func(a, b Member) int { return strings.Compare(a.ID, b.ID) })
	return members
}

func (s *Store) ActiveMemberCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, member := range s.data.Members {
		if member.Status == "active" {
			count++
		}
	}
	return count
}

func (s *Store) UpsertOIDCMember(id, displayName string) (Member, bool, error) {
	userID := "kubeatlas:" + id
	if !oidcMemberIDPattern.MatchString(id) || !validName(displayName) ||
		!validKubernetesGroups(userID, []string{}) {
		return Member{}, false, failure("invalid_parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if current, ok := s.data.Members[id]; ok {
		if current.Status == "disabled" {
			return cloneMember(current), false, failure("member_disabled")
		}
		if current.DisplayName == displayName {
			return cloneMember(current), false, nil
		}
		now := s.now().UTC()
		if now.Before(current.UpdatedAt) {
			return Member{}, false, failure("store_unavailable")
		}
		current.DisplayName, current.UpdatedAt = displayName, now
		next := s.next(now)
		next.Members[id] = current
		if err := s.commit(next); err != nil {
			return Member{}, false, err
		}
		return cloneMember(current), false, nil
	}
	if len(s.data.Members)+1 >= maxMembers {
		return Member{}, false, failure("capacity_exceeded")
	}
	now := s.now().UTC()
	member := Member{
		ID: id, UserID: userID, DisplayName: displayName, Role: "member", Status: "pending",
		ClusterIDs: []string{}, KubernetesGroups: []string{}, CreatedAt: now, UpdatedAt: now,
	}
	next := s.next(now)
	next.Members[id] = member
	if err := s.commit(next); err != nil {
		return Member{}, false, err
	}
	return cloneMember(member), true, nil
}

func (s *Store) UpdateMember(id string, input MemberInput) (Member, bool, error) {
	if !oidcMemberIDPattern.MatchString(id) {
		return Member{}, false, failure("invalid_parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.data.Members[id]
	if !ok {
		return Member{}, false, failure("member_not_found")
	}
	switch input.Role {
	case "admin", "member", "viewer":
	default:
		return Member{}, false, failure("invalid_parameters")
	}
	switch input.Status {
	case "active", "pending", "disabled":
	default:
		return Member{}, false, failure("invalid_parameters")
	}
	clusters, err := scope(input.ClusterIDs, s.data.Clusters, true)
	if err != nil {
		return Member{}, false, err
	}
	if input.Role == "admin" && len(clusters) != 0 {
		return Member{}, false, failure("invalid_scope")
	}
	groups := append([]string{}, input.KubernetesGroups...)
	if !validInputGroups(current.UserID, groups) {
		return Member{}, false, failure("invalid_parameters")
	}
	slices.Sort(groups)
	if !validKubernetesGroups(current.UserID, groups) {
		return Member{}, false, failure("invalid_parameters")
	}
	authorizationChanged := current.Role != input.Role || current.Status != input.Status ||
		!slices.Equal(current.ClusterIDs, clusters) || !slices.Equal(current.KubernetesGroups, groups)
	now := s.now().UTC()
	if now.Before(current.UpdatedAt) {
		return Member{}, false, failure("store_unavailable")
	}
	current.Role, current.Status = input.Role, input.Status
	current.ClusterIDs, current.KubernetesGroups, current.UpdatedAt = clusters, groups, now
	next := s.next(now)
	next.Members[id] = current
	if authorizationChanged {
		for credentialID, credential := range next.Credentials {
			if credential.OwnerID == id && credential.RevokedAt == nil {
				revokedAt := now
				credential.RevokedAt = &revokedAt
				next.Credentials[credentialID] = credential
			}
		}
	}
	if err := s.commit(next); err != nil {
		return Member{}, false, err
	}
	return cloneMember(current), authorizationChanged, nil
}

func validInputGroups(userID string, groups []string) bool {
	if len(groups) > 128 {
		return false
	}
	seen := make(map[string]struct{}, len(groups))
	total := 0
	for _, group := range groups {
		if !validText(group, 256, true) || strings.TrimSpace(group) != group ||
			strings.ContainsRune(group, ',') {
			return false
		}
		if _, exists := seen[group]; exists {
			return false
		}
		seen[group] = struct{}{}
		total += len(group) + 1
		if total > 8192 {
			return false
		}
	}
	return pkgauth.ForwardedIdentityAllowed(userID, groups, true)
}

func memberScopeForOwner(member Member, clusters map[string]storedCluster) []string {
	if member.Role == "admin" {
		ids := make([]string, 0, len(clusters))
		for id := range clusters {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		return ids
	}
	return append([]string{}, member.ClusterIDs...)
}

func (s *Store) activeMemberScopeLocked(ownerID string) (Member, []string, error) {
	member, ok := s.data.Members[ownerID]
	if !ok {
		return Member{}, nil, failure("member_not_found")
	}
	if member.Status == "pending" {
		return Member{}, nil, failure("member_pending")
	}
	if member.Status == "disabled" {
		return Member{}, nil, failure("member_disabled")
	}
	return cloneMember(member), memberScopeForOwner(member, s.data.Clusters), nil
}

func subsetScope(requested, allowed []string) bool {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, id := range allowed {
		allowedSet[id] = struct{}{}
	}
	for _, id := range requested {
		if _, ok := allowedSet[id]; !ok {
			return false
		}
	}
	return true
}

func intersectScope(requested, allowed []string) []string {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, id := range allowed {
		allowedSet[id] = struct{}{}
	}
	result := make([]string, 0, len(requested))
	for _, id := range requested {
		if _, ok := allowedSet[id]; ok {
			result = append(result, id)
		}
	}
	return result
}
