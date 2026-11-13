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
	organization := next.Organizations["self-hosted"]
	organization.Name = name
	next.Organizations["self-hosted"] = organization
	if err := s.commit(next); err != nil {
		return Organization{}, err
	}
	return next.Organizations["self-hosted"], nil
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
	account, exists := s.data.Accounts[id]
	if exists && account.Status == "disabled" {
		return Member{}, false, failure("member_disabled")
	}
	created := !exists
	if created && len(s.data.Accounts) >= maxAccounts {
		return Member{}, false, failure("capacity_exceeded")
	}
	now := s.now().UTC()
	if created {
		account = Account{ID: id, UserID: userID, DisplayName: displayName, Status: "pending", CreatedAt: now, UpdatedAt: now}
	} else if account.DisplayName != displayName {
		if now.Before(account.UpdatedAt) {
			return Member{}, false, failure("store_unavailable")
		}
		account.DisplayName, account.UpdatedAt = displayName, now
	} else {
		if membership, ok := s.data.Memberships["self-hosted"][id]; ok {
			return cloneMember(memberFrom(account, membership)), false, nil
		}
		return Member{ID: id, UserID: userID, DisplayName: displayName, Status: account.Status,
			ClusterIDs: []string{}, KubernetesGroups: []string{}, CreatedAt: account.CreatedAt, UpdatedAt: account.UpdatedAt}, false, nil
	}
	next := s.next(now)
	next.Accounts[id] = account
	syncAliasesFromPrimary(&next)
	if err := s.commit(next); err != nil {
		return Member{}, false, err
	}
	if membership, ok := next.Memberships["self-hosted"][id]; ok {
		return cloneMember(memberFrom(account, membership)), created, nil
	}
	return Member{ID: id, UserID: userID, DisplayName: displayName, Status: account.Status,
		ClusterIDs: []string{}, KubernetesGroups: []string{}, CreatedAt: account.CreatedAt, UpdatedAt: account.UpdatedAt}, created, nil
}

func (s *Store) UpdateMember(id string, input MemberInput) (Member, bool, error) {
	if !oidcMemberIDPattern.MatchString(id) {
		return Member{}, false, failure("invalid_parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.data.Members[id]
	if !ok {
		account, exists := s.data.Accounts[id]
		if !exists {
			return Member{}, false, failure("member_not_found")
		}
		current = Member{ID: account.ID, UserID: account.UserID, DisplayName: account.DisplayName, Status: account.Status,
			ClusterIDs: []string{}, KubernetesGroups: []string{}, CreatedAt: account.CreatedAt, UpdatedAt: account.UpdatedAt}
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
	clusters, err := scopeOrganization(input.ClusterIDs, "self-hosted", s.data.Clusters, true)
	if err != nil {
		return Member{}, false, err
	}
	if input.Role == "admin" && len(clusters) != 0 {
		return Member{}, false, failure("invalid_scope")
	}
	legacy := s.data.Memberships["self-hosted"][id]
	if input.Role == "admin" {
		input.Role = "owner"
		legacy.LegacyRestricted, legacy.LegacyAllClusters = true, true
	} else {
		attenuateLegacyMembership(&legacy, current.Role, input.Role)
		if !legacy.LegacyRestricted {
			legacy.LegacyRestricted = true
		}
	}
	groups := append([]string{}, input.KubernetesGroups...)
	if !validInputGroups(current.UserID, groups) {
		return Member{}, false, failure("invalid_parameters")
	}
	slices.Sort(groups)
	if !validKubernetesGroups(current.UserID, groups) {
		return Member{}, false, failure("invalid_parameters")
	}
	if lowerRole(input.Role) < lowerRole(current.Role) {
		groups = []string{}
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
	account := next.Accounts[id]
	account.DisplayName, account.Status, account.UpdatedAt = current.DisplayName, current.Status, current.UpdatedAt
	next.Accounts[id] = account
	if next.Memberships["self-hosted"] == nil {
		next.Memberships["self-hosted"] = map[string]Membership{}
	}
	membership := next.Memberships["self-hosted"][id]
	if membership.CreatedAt.IsZero() {
		membership.CreatedAt = current.CreatedAt
	}
	membership.Role, membership.ClusterIDs, membership.KubernetesGroups = current.Role, append([]string{}, current.ClusterIDs...), append([]string{}, current.KubernetesGroups...)
	membership.LegacyRestricted, membership.LegacyAllClusters = legacy.LegacyRestricted, legacy.LegacyAllClusters
	membership.UpdatedAt = current.UpdatedAt
	next.Memberships["self-hosted"][id] = membership
	if authorizationChanged {
		for credentialID, credential := range next.Credentials {
			if credential.OwnerID == id && credential.OrganizationID == "self-hosted" && credential.RevokedAt == nil {
				revokedAt := now
				credential.RevokedAt = &revokedAt
				next.Credentials[credentialID] = credential
			}
		}
	}
	syncAliasesFromPrimary(&next)
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
	if member.Role == "admin" || member.Role == "owner" {
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
	return s.activeMemberScopeInOrganizationLocked("self-hosted", ownerID)
}

func (s *Store) activeMemberScopeInOrganizationLocked(organizationID, ownerID string) (Member, []string, error) {
	account, ok := s.data.Accounts[ownerID]
	if !ok {
		return Member{}, nil, failure("member_not_found")
	}
	if account.Status == "pending" {
		return Member{}, nil, failure("member_pending")
	}
	if account.Status == "disabled" {
		return Member{}, nil, failure("member_disabled")
	}
	membership, ok := s.data.Memberships[organizationID][ownerID]
	if !ok {
		return Member{}, nil, failure("member_not_found")
	}
	member := memberFrom(account, membership)
	if !membership.LegacyRestricted || membership.LegacyAllClusters {
		return cloneMember(member), clusterIDsForOrganization(organizationID, s.data.Clusters), nil
	}
	return cloneMember(member), intersectScope(membership.ClusterIDs, clusterIDsForOrganization(organizationID, s.data.Clusters)), nil
}

func (s *Store) MemberClusterIDsForOrganization(organizationID, ownerID string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ids, err := s.activeMemberScopeInOrganizationLocked(organizationID, ownerID)
	return ids, err
}

func clusterIDsForOrganization(organizationID string, clusters map[string]storedCluster) []string {
	ids := make([]string, 0)
	for id, cluster := range clusters {
		if cluster.OrganizationID == organizationID {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
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
