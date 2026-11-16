package hubstate

import (
	"crypto/rand"
	"encoding/hex"
	"net/mail"
	"slices"
	"strings"
	"time"
)

type OrganizationMembership struct {
	OrganizationID string `json:"organizationId"`
	Role           string `json:"role"`
}

type PlatformUser struct {
	ID            string                   `json:"id"`
	Email         string                   `json:"email"`
	DisplayName   string                   `json:"displayName"`
	Status        string                   `json:"status"`
	Local         bool                     `json:"local"`
	PlatformOwner bool                     `json:"platformOwner"`
	Memberships   []OrganizationMembership `json:"memberships"`
}

func (s *Store) Account(id string) (Account, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	account, ok := s.data.Accounts[id]
	return cloneAccount(account), ok
}

func (s *Store) AccountByEmail(email string) (Account, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, account := range s.data.Accounts {
		if account.Email == email && email != "" {
			return cloneAccount(account), true
		}
	}
	return Account{}, false
}

func (s *Store) RegisterPendingAccount(email, passwordHash string) (Account, error) {
	if !validEmail(email) || !validPasswordHash(passwordHash) {
		return Account{}, failure("invalid_parameters")
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return Account{}, failure("store_unavailable")
	}
	id := "acct:" + hex.EncodeToString(random[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.data.Accounts) >= maxAccounts {
		return Account{}, failure("capacity_exceeded")
	}
	for _, account := range s.data.Accounts {
		if account.Email == email {
			return Account{}, failure("email_exists")
		}
	}
	now := s.now().UTC()
	account := Account{ID: id, UserID: "kubeatlas:" + id, Email: email, DisplayName: email,
		Status: "pending", PasswordHash: passwordHash, CreatedAt: now, UpdatedAt: now}
	next := s.next(now)
	next.Accounts[id] = account
	syncAliasesFromPrimary(&next)
	if err := s.commit(next); err != nil {
		return Account{}, err
	}
	return cloneAccount(s.data.Accounts[id]), nil
}

func validEmail(email string) bool {
	if email == "" || len(email) > 254 || strings.TrimSpace(email) != email || email != strings.ToLower(email) || strings.ContainsAny(email, " \t\r\n") {
		return false
	}
	parsed, err := mail.ParseAddress(email)
	return err == nil && parsed.Address == email
}

func (s *Store) EnsurePlatformOwner(id, userID, displayName string) error {
	if !validOwner(id) || !validOwner(userID) || !validName(displayName) {
		return failure("invalid_parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	next := s.next(now)
	account, exists := next.Accounts[id]
	if exists && account.UserID != userID {
		return failure("store_unavailable")
	}
	changed := false
	for accountID, current := range next.Accounts {
		if accountID != id && (current.PlatformOwner || current.Local) {
			recoveryOwner := current.PlatformOwner && current.Local
			current.PlatformOwner, current.Local, current.Status, current.UpdatedAt = false, false, "disabled", now
			next.Accounts[accountID] = current
			for organizationID, members := range next.Memberships {
				if membership, exists := members[accountID]; exists {
					if recoveryOwner {
						replacement, hasReplacement := members[id]
						if !hasReplacement || membership.Role == "owner" && replacement.Role != "owner" {
							members[id] = membership
						}
					}
					delete(members, accountID)
					revokeCredentials(next, organizationID, accountID, now)
				}
			}
			changed = true
		}
	}
	if !exists {
		account = Account{ID: id, UserID: userID, DisplayName: displayName, Status: "active", CreatedAt: now, UpdatedAt: now}
		changed = true
	}
	if !account.PlatformOwner || !account.Local || account.DisplayName != displayName || account.Status != "active" || account.Email != "" || account.PasswordHash != "" {
		account.PlatformOwner, account.Local, account.DisplayName, account.Status, account.Email, account.PasswordHash, account.UpdatedAt = true, true, displayName, "active", "", "", now
		changed = true
	}
	next.Accounts[id] = account
	if next.Memberships["self-hosted"] == nil {
		next.Memberships["self-hosted"] = map[string]Membership{}
	}
	membership, hasMembership := next.Memberships["self-hosted"][id]
	if !hasMembership {
		membership = Membership{Role: "owner", ClusterIDs: []string{}, KubernetesGroups: []string{}, CreatedAt: now, UpdatedAt: now}
		next.Memberships["self-hosted"][id] = membership
		changed = true
	} else if membership.Role != "owner" {
		membership.Role, membership.UpdatedAt = "owner", now
		next.Memberships["self-hosted"][id] = membership
		revokeCredentials(next, "self-hosted", id, now)
		changed = true
	}
	if !changed {
		return nil
	}
	syncAliasesFromPrimary(&next)
	return s.commit(next)
}

func (s *Store) IsPlatformOwner(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	account, ok := s.data.Accounts[id]
	return ok && account.Status == "active" && account.PlatformOwner && account.Local
}

func (s *Store) Organizations() []Organization {
	s.mu.Lock()
	defer s.mu.Unlock()
	organizations := make([]Organization, 0, len(s.data.Organizations))
	for _, organization := range s.data.Organizations {
		organizations = append(organizations, organization)
	}
	slices.SortFunc(organizations, func(a, b Organization) int { return strings.Compare(a.ID, b.ID) })
	return organizations
}

func (s *Store) OrganizationByID(id string) (Organization, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	organization, ok := s.data.Organizations[id]
	return organization, ok
}

func (s *Store) OrganizationsForAccount(accountID string) []OrganizationMembership {
	s.mu.Lock()
	defer s.mu.Unlock()
	account, ok := s.data.Accounts[accountID]
	if !ok || account.Status != "active" {
		return []OrganizationMembership{}
	}
	result := make([]OrganizationMembership, 0)
	if account.PlatformOwner && account.Local {
		for id := range s.data.Organizations {
			result = append(result, OrganizationMembership{OrganizationID: id, Role: "platformOwner"})
		}
	} else {
		for organizationID, entries := range s.data.Memberships {
			if membership, exists := entries[accountID]; exists {
				result = append(result, OrganizationMembership{OrganizationID: organizationID, Role: membership.Role})
			}
		}
	}
	slices.SortFunc(result, func(a, b OrganizationMembership) int { return strings.Compare(a.OrganizationID, b.OrganizationID) })
	return result
}

func (s *Store) CreateOrganization(name, ownerAccountID string) (Organization, error) {
	if !validName(name) || ownerAccountID == "" {
		return Organization{}, failure("invalid_parameters")
	}
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		return Organization{}, failure("store_unavailable")
	}
	id := "org-" + hex.EncodeToString(random[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	owner, ok := s.data.Accounts[ownerAccountID]
	if !ok || owner.Status != "active" || len(s.data.Organizations) >= maxOrganizations {
		return Organization{}, failure("permission_denied")
	}
	now := s.now().UTC()
	organization := Organization{ID: id, Name: name}
	next := s.next(now)
	next.Organizations[id] = organization
	next.Memberships[id] = map[string]Membership{ownerAccountID: {Role: "owner", ClusterIDs: []string{}, KubernetesGroups: []string{}, CreatedAt: now, UpdatedAt: now}}
	syncAliasesFromPrimary(&next)
	if err := s.commit(next); err != nil {
		return Organization{}, err
	}
	return organization, nil
}

func (s *Store) SetOrganizationNameFor(id, name string) (Organization, error) {
	if !validName(name) {
		return Organization{}, failure("invalid_parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	organization, ok := s.data.Organizations[id]
	if !ok {
		return Organization{}, failure("organization_not_found")
	}
	organization.Name = name
	next := s.next(s.now().UTC())
	next.Organizations[id] = organization
	if id == "self-hosted" {
		copy := organization
		next.Organization = &copy
	}
	if err := s.commit(next); err != nil {
		return Organization{}, err
	}
	return organization, nil
}

func (s *Store) MembersForOrganization(organizationID string) []Member {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries := s.data.Memberships[organizationID]
	members := make([]Member, 0, len(entries))
	for id, membership := range entries {
		if account, ok := s.data.Accounts[id]; ok {
			members = append(members, memberFrom(account, membership))
		}
	}
	slices.SortFunc(members, func(a, b Member) int { return strings.Compare(a.ID, b.ID) })
	return members
}

func memberFrom(account Account, membership Membership) Member {
	return Member{ID: account.ID, UserID: account.UserID, DisplayName: account.DisplayName, Role: membership.Role,
		Status: account.Status, AuthorizationRevision: account.AuthorizationRevision, ClusterIDs: append([]string{}, membership.ClusterIDs...),
		KubernetesGroups: append([]string{}, membership.KubernetesGroups...), LegacyRestricted: membership.LegacyRestricted,
		LegacyAllClusters: membership.LegacyAllClusters, CreatedAt: membership.CreatedAt, UpdatedAt: membership.UpdatedAt}
}

func lowerRole(role string) int {
	switch role {
	case "owner", "admin":
		return 3
	case "member":
		return 2
	case "viewer":
		return 1
	default:
		return 0
	}
}

func attenuateLegacyMembership(membership *Membership, oldRole, newRole string) {
	if lowerRole(newRole) < lowerRole(oldRole) {
		membership.KubernetesGroups = []string{}
		if membership.LegacyRestricted && membership.LegacyAllClusters {
			membership.LegacyAllClusters = false
			membership.ClusterIDs = []string{}
		}
	}
}

func (s *Store) MemberForOrganization(organizationID, id string) (Member, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	membership, ok := s.data.Memberships[organizationID][id]
	if !ok {
		return Member{}, false
	}
	account, ok := s.data.Accounts[id]
	if !ok {
		return Member{}, false
	}
	return cloneMember(memberFrom(account, membership)), true
}

func (s *Store) RoleGroupForMember(organizationID, accountID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	membership, ok := s.data.Memberships[organizationID][accountID]
	if !ok || membership.LegacyRestricted {
		return ""
	}
	switch membership.Role {
	case "owner":
		return "kubeatlas:hub:owner"
	case "member":
		return "kubeatlas:hub:member"
	case "viewer":
		return "kubeatlas:hub:viewer"
	default:
		return ""
	}
}

func (s *Store) UpdateOrganizationMemberRole(organizationID, accountID, role string) (Member, bool, error) {
	if !validRole(role) {
		return Member{}, false, failure("invalid_parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	account, ok := s.data.Accounts[accountID]
	membership, exists := s.data.Memberships[organizationID][accountID]
	if !ok || account.Status != "active" || !exists {
		return Member{}, false, failure("member_not_found")
	}
	if membership.Role == role {
		return memberFrom(account, membership), false, nil
	}
	if membership.Role == "owner" && role != "owner" && !hasOtherActiveOwner(s.data, organizationID, accountID) {
		return Member{}, false, failure("last_owner")
	}
	now := s.now().UTC()
	if now.Before(membership.UpdatedAt) {
		return Member{}, false, failure("store_unavailable")
	}
	attenuateLegacyMembership(&membership, membership.Role, role)
	membership.Role, membership.UpdatedAt = role, now
	next := s.next(now)
	next.Memberships[organizationID][accountID] = membership
	revokeCredentials(next, organizationID, accountID, now)
	syncAliasesFromPrimary(&next)
	if err := s.commit(next); err != nil {
		return Member{}, false, err
	}
	return memberFrom(s.data.Accounts[accountID], s.data.Memberships[organizationID][accountID]), true, nil
}

func (s *Store) RemoveOrganizationMember(organizationID, accountID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	membership, ok := s.data.Memberships[organizationID][accountID]
	if !ok {
		return false, failure("member_not_found")
	}
	account := s.data.Accounts[accountID]
	if membership.Role == "owner" && account.Status == "active" && !hasOtherActiveOwner(s.data, organizationID, accountID) {
		return false, failure("last_owner")
	}
	now := s.now().UTC()
	next := s.next(now)
	delete(next.Memberships[organizationID], accountID)
	revokeCredentials(next, organizationID, accountID, now)
	syncAliasesFromPrimary(&next)
	if err := s.commit(next); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) UpdatePlatformUser(id, status string, memberships []OrganizationMembership) (PlatformUser, error) {
	if status != "active" && status != "pending" && status != "disabled" {
		return PlatformUser{}, failure("invalid_parameters")
	}
	roles := make(map[string]string, len(memberships))
	for _, membership := range memberships {
		if membership.OrganizationID == "" || !validRole(membership.Role) {
			return PlatformUser{}, failure("invalid_parameters")
		}
		if _, duplicate := roles[membership.OrganizationID]; duplicate {
			return PlatformUser{}, failure("invalid_parameters")
		}
		roles[membership.OrganizationID] = membership.Role
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	account, ok := s.data.Accounts[id]
	if !ok {
		return PlatformUser{}, failure("account_not_found")
	}
	if account.Local || account.PlatformOwner {
		return PlatformUser{}, failure("immutable_account")
	}
	for organizationID := range roles {
		if _, ok := s.data.Organizations[organizationID]; !ok {
			return PlatformUser{}, failure("organization_not_found")
		}
	}
	for organizationID, existing := range s.data.Memberships {
		old, hasOld := existing[id]
		newRole, hasNew := roles[organizationID]
		oldOwner := hasOld && old.Role == "owner" && account.Status == "active"
		newOwner := hasNew && newRole == "owner" && status == "active"
		if oldOwner && !newOwner && !hasOtherActiveOwner(s.data, organizationID, id) {
			return PlatformUser{}, failure("last_owner")
		}
	}
	now := s.now().UTC()
	if now.Before(account.UpdatedAt) {
		return PlatformUser{}, failure("store_unavailable")
	}
	next := s.next(now)
	oldStatus := account.Status
	account.Status, account.UpdatedAt = status, now
	next.Accounts[id] = account
	for organizationID, entries := range next.Memberships {
		if _, keep := roles[organizationID]; !keep {
			if _, exists := entries[id]; exists {
				delete(entries, id)
				revokeCredentials(next, organizationID, id, now)
			}
			continue
		}
		membership := entries[id]
		if _, exists := s.data.Memberships[organizationID][id]; !exists {
			membership = Membership{ClusterIDs: []string{}, KubernetesGroups: []string{}, CreatedAt: now, UpdatedAt: now}
		}
		if membership.Role != roles[organizationID] || status != oldStatus {
			membership.UpdatedAt = now
			revokeCredentials(next, organizationID, id, now)
		}
		attenuateLegacyMembership(&membership, membership.Role, roles[organizationID])
		membership.Role = roles[organizationID]
		entries[id] = membership
	}
	for organizationID, role := range roles {
		if next.Memberships[organizationID] == nil {
			next.Memberships[organizationID] = map[string]Membership{}
		}
		membership, exists := next.Memberships[organizationID][id]
		if !exists {
			membership = Membership{ClusterIDs: []string{}, KubernetesGroups: []string{}, CreatedAt: now, UpdatedAt: now}
		}
		membership.Role = role
		membership.UpdatedAt = now
		next.Memberships[organizationID][id] = membership
	}
	if status == "disabled" || status != oldStatus {
		for credentialID, credential := range next.Credentials {
			if credential.OwnerID == id && credential.RevokedAt == nil {
				credential.RevokedAt = &now
				next.Credentials[credentialID] = credential
			}
		}
	}
	syncAliasesFromPrimary(&next)
	if err := s.commit(next); err != nil {
		return PlatformUser{}, err
	}
	return platformUserFrom(account, roles), nil
}

func platformUserFrom(account Account, roles map[string]string) PlatformUser {
	memberships := make([]OrganizationMembership, 0, len(roles))
	for organizationID, role := range roles {
		memberships = append(memberships, OrganizationMembership{OrganizationID: organizationID, Role: role})
	}
	slices.SortFunc(memberships, func(a, b OrganizationMembership) int { return strings.Compare(a.OrganizationID, b.OrganizationID) })
	id := account.ID
	if account.Local {
		id = LocalMemberID
	}
	return PlatformUser{ID: id, Email: account.Email, DisplayName: account.DisplayName, Status: account.Status,
		Local: account.Local, PlatformOwner: account.PlatformOwner, Memberships: memberships}
}

func (s *Store) PlatformUsers() []PlatformUser {
	s.mu.Lock()
	defer s.mu.Unlock()
	users := make([]PlatformUser, 0, len(s.data.Accounts))
	for _, account := range s.data.Accounts {
		memberships := map[string]string{}
		for organizationID, entries := range s.data.Memberships {
			if membership, ok := entries[account.ID]; ok {
				memberships[organizationID] = membership.Role
			}
		}
		users = append(users, platformUserFrom(account, memberships))
	}
	slices.SortFunc(users, func(a, b PlatformUser) int { return strings.Compare(a.ID, b.ID) })
	return users
}

func hasOtherActiveOwner(file storeFile, organizationID, excluding string) bool {
	for accountID, membership := range file.Memberships[organizationID] {
		account := file.Accounts[accountID]
		if accountID != excluding && membership.Role == "owner" && account.Status == "active" {
			return true
		}
	}
	return false
}

func validRole(role string) bool { return role == "owner" || role == "member" || role == "viewer" }

func revokeCredentials(file storeFile, organizationID, ownerID string, now time.Time) {
	for id, credential := range file.Credentials {
		if credential.OwnerID == ownerID && credential.OrganizationID == organizationID && credential.RevokedAt == nil {
			revokedAt := now
			credential.RevokedAt = &revokedAt
			file.Credentials[id] = credential
		}
	}
}
