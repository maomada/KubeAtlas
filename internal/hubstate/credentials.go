package hubstate

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"slices"
	"strings"
	"time"
)

func newCredentialID() (string, error) {
	var id [32]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", failure("store_unavailable")
	}
	return hex.EncodeToString(id[:]), nil
}

func (s *Store) Issue(ownerID string, input IssueInput) (IssuedCredential, error) {
	return s.IssueForOrganization("self-hosted", ownerID, input)
}

func (s *Store) IssueForOrganization(organizationID, ownerID string, input IssueInput) (IssuedCredential, error) {
	if !validOwner(ownerID) || !validText(input.Label, 128, false) {
		return IssuedCredential{}, failure("invalid_parameters")
	}
	ttl, err := ttlSeconds(input.TTLSeconds, defaultTTL)
	if err != nil {
		return IssuedCredential{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data.Organizations[organizationID]; !ok {
		return IssuedCredential{}, failure("organization_not_found")
	}
	ids, err := scopeOrganization(input.ClusterIDs, organizationID, s.data.Clusters, false)
	if err != nil {
		return IssuedCredential{}, err
	}
	return s.issueLocked(ownerID, organizationID, input, ids, ttl)
}

func (s *Store) IssueForMember(ownerID string, input IssueInput) (IssuedCredential, error) {
	return s.IssueForMemberInOrganization("self-hosted", ownerID, input)
}

func (s *Store) IssueForMemberInOrganization(organizationID, ownerID string, input IssueInput) (IssuedCredential, error) {
	if !validOwner(ownerID) || !validText(input.Label, 128, false) {
		return IssuedCredential{}, failure("invalid_parameters")
	}
	ttl, err := ttlSeconds(input.TTLSeconds, defaultTTL)
	if err != nil {
		return IssuedCredential{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	member, allowed, err := s.activeMemberScopeInOrganizationLocked(organizationID, ownerID)
	if err != nil {
		return IssuedCredential{}, err
	}
	ids, err := scopeOrganization(input.ClusterIDs, organizationID, s.data.Clusters, false)
	if err != nil {
		return IssuedCredential{}, err
	}
	if !subsetScope(ids, allowed) {
		return IssuedCredential{}, failure("permission_denied")
	}
	if member.Role == "viewer" {
		input.ReadOnly = true
	}
	return s.issueLocked(ownerID, organizationID, input, ids, ttl)
}

func (s *Store) issueLocked(ownerID, organizationID string, input IssueInput, ids []string, ttl int) (IssuedCredential, error) {
	now := s.now().UTC()
	next := s.next(now)
	if len(next.Credentials) >= maxCredentials {
		return IssuedCredential{}, failure("capacity_exceeded")
	}
	id, err := newCredentialID()
	if err != nil {
		return IssuedCredential{}, err
	}
	if _, exists := s.data.Credentials[id]; exists {
		return IssuedCredential{}, failure("store_unavailable")
	}
	token, hash, err := mint(mcpPrefix)
	if err != nil {
		return IssuedCredential{}, err
	}
	c := storedCredential{Credential: Credential{ID: id, OwnerID: ownerID, OrganizationID: organizationID, Label: input.Label, ClusterIDs: ids, ReadOnly: input.ReadOnly, CreatedAt: now, ExpiresAt: now.Add(time.Duration(ttl) * time.Second), RefreshUntil: now.Add(familyLifetime)}, Digest: hash, FamilyID: id, FamilyCreatedAt: now, OriginalTTLSeconds: ttl, TTLSeconds: ttl}
	next.Credentials[id] = c
	if err := s.commit(next); err != nil {
		return IssuedCredential{}, err
	}
	return IssuedCredential{Credential: cloneCredential(c.Credential), Token: token}, nil
}

func (s *Store) matchCredential(token, ownerID string) (storedCredential, error) {
	if !tokenShape(token, mcpPrefix) {
		return storedCredential{}, failure("credential_invalid")
	}
	hash := digest(token)
	var matched storedCredential
	var found bool
	for _, c := range s.data.Credentials {
		if sameDigest(c.Digest, hash) {
			matched, found = c, true
		}
	}
	if !found || matched.OwnerID != ownerID {
		return storedCredential{}, failure("credential_invalid")
	}
	return matched, nil
}

func (s *Store) CredentialOwner(token string) (string, error) {
	if !tokenShape(token, mcpPrefix) {
		return "", failure("credential_invalid")
	}
	candidate := digest(token)
	s.mu.Lock()
	defer s.mu.Unlock()
	var ownerID string
	var found int
	for _, credential := range s.data.Credentials {
		match := subtle.ConstantTimeCompare([]byte(credential.Digest), []byte(candidate))
		if match == 1 {
			ownerID = credential.OwnerID
			found = 1
		}
	}
	if found != 1 {
		return "", failure("credential_invalid")
	}
	return ownerID, nil
}

func (s *Store) CredentialScope(token string) (Credential, error) {
	if !tokenShape(token, mcpPrefix) {
		return Credential{}, failure("credential_invalid")
	}
	candidate := digest(token)
	s.mu.Lock()
	defer s.mu.Unlock()
	var matched Credential
	found := 0
	for _, credential := range s.data.Credentials {
		if subtle.ConstantTimeCompare([]byte(credential.Digest), []byte(candidate)) == 1 {
			matched, found = credential.Credential, 1
		}
	}
	if found != 1 || matched.OwnerID == "" || matched.OrganizationID == "" {
		return Credential{}, failure("credential_invalid")
	}
	return matched, nil
}

func (s *Store) CredentialOrganization(token string) (string, error) {
	if !tokenShape(token, mcpPrefix) {
		return "", failure("credential_invalid")
	}
	candidate := digest(token)
	s.mu.Lock()
	defer s.mu.Unlock()
	organizationID := ""
	found := 0
	for _, credential := range s.data.Credentials {
		if subtle.ConstantTimeCompare([]byte(credential.Digest), []byte(candidate)) == 1 {
			organizationID, found = credential.OrganizationID, 1
		}
	}
	if found != 1 || organizationID == "" {
		return "", failure("credential_invalid")
	}
	return organizationID, nil
}

func credentialStatus(c storedCredential, now time.Time) error {
	if now.Before(c.CreatedAt) {
		return failure("credential_invalid")
	}
	if c.RevokedAt != nil {
		return failure("credential_revoked")
	}
	if !now.Before(c.ExpiresAt) {
		return failure("credential_expired")
	}
	return nil
}

func (s *Store) Authenticate(token, ownerID string) (Credential, error) {
	return s.AuthenticateForOrganization(token, ownerID, "self-hosted")
}

func (s *Store) AuthenticateForOrganization(token, ownerID, organizationID string) (Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.matchCredential(token, ownerID)
	if err != nil {
		return Credential{}, err
	}
	if err := credentialStatus(c, s.now().UTC()); err != nil {
		return Credential{}, err
	}
	if c.OrganizationID != organizationID {
		return Credential{}, failure("credential_invalid")
	}
	return cloneCredential(c.Credential), nil
}

func (s *Store) Refresh(token, ownerID string, ttlSeconds int) (IssuedCredential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.matchCredential(token, ownerID)
	if err != nil {
		return IssuedCredential{}, err
	}
	return s.renew(c, ttlSeconds)
}

func (s *Store) RefreshForOrganization(organizationID, token, ownerID string, ttlSeconds int) (IssuedCredential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	credential, err := s.matchCredential(token, ownerID)
	if err != nil {
		return IssuedCredential{}, err
	}
	if credential.OrganizationID != organizationID {
		return IssuedCredential{}, failure("credential_invalid")
	}
	return s.renew(credential, ttlSeconds)
}

func (s *Store) RefreshForMember(token, ownerID string, ttlSeconds int) (IssuedCredential, error) {
	return s.RefreshForMemberInOrganization("self-hosted", token, ownerID, ttlSeconds)
}

func (s *Store) RefreshForMemberInOrganization(organizationID, token, ownerID string, ttlSeconds int) (IssuedCredential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, err := s.matchCredential(token, ownerID)
	if err != nil {
		return IssuedCredential{}, err
	}
	if old.OrganizationID != organizationID {
		return IssuedCredential{}, failure("credential_invalid")
	}
	member, allowed, err := s.activeMemberScopeInOrganizationLocked(organizationID, ownerID)
	if err != nil {
		return IssuedCredential{}, err
	}
	scope := intersectScope(old.ClusterIDs, allowed)
	if len(scope) == 0 {
		return IssuedCredential{}, failure("permission_denied")
	}
	if member.Role == "viewer" {
		old.ReadOnly = true
	}
	return s.renewWithScope(old, ttlSeconds, scope)
}

func (s *Store) Rotate(ownerID, id string, ttlSeconds int) (IssuedCredential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.data.Credentials[id]
	if !ok || c.OwnerID != ownerID {
		return IssuedCredential{}, failure("credential_invalid")
	}
	return s.renew(c, ttlSeconds)
}

func (s *Store) RotateForOrganization(organizationID, ownerID, id string, ttlSeconds int) (IssuedCredential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	credential, ok := s.data.Credentials[id]
	if !ok || credential.OwnerID != ownerID || credential.OrganizationID != organizationID {
		return IssuedCredential{}, failure("credential_invalid")
	}
	return s.renew(credential, ttlSeconds)
}

func (s *Store) RotateForMember(ownerID, id string, ttlSeconds int) (IssuedCredential, error) {
	return s.RotateForMemberInOrganization("self-hosted", ownerID, id, ttlSeconds)
}

func (s *Store) RotateForMemberInOrganization(organizationID, ownerID, id string, ttlSeconds int) (IssuedCredential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.data.Credentials[id]
	if !ok || old.OwnerID != ownerID || old.OrganizationID != organizationID {
		return IssuedCredential{}, failure("credential_invalid")
	}
	member, allowed, err := s.activeMemberScopeInOrganizationLocked(organizationID, ownerID)
	if err != nil {
		return IssuedCredential{}, err
	}
	scope := intersectScope(old.ClusterIDs, allowed)
	if len(scope) == 0 {
		return IssuedCredential{}, failure("permission_denied")
	}
	if member.Role == "viewer" {
		old.ReadOnly = true
	}
	return s.renewWithScope(old, ttlSeconds, scope)
}

func (s *Store) renew(old storedCredential, requestedTTL int) (IssuedCredential, error) {
	return s.renewWithScope(old, requestedTTL, old.ClusterIDs)
}

func (s *Store) renewWithScope(old storedCredential, requestedTTL int, clusterIDs []string) (IssuedCredential, error) {
	now := s.now().UTC()
	if err := credentialStatus(old, now); err != nil {
		return IssuedCredential{}, err
	}
	ttl, err := ttlSeconds(requestedTTL, old.OriginalTTLSeconds)
	if err != nil {
		return IssuedCredential{}, err
	}
	if old.RefreshUntil.Sub(now) < time.Duration(minTTL)*time.Second {
		return IssuedCredential{}, failure("credential_refresh_expired")
	}
	ids, err := scopeOrganization(clusterIDs, old.OrganizationID, s.data.Clusters, false)
	if err != nil || !slices.Equal(ids, clusterIDs) {
		return IssuedCredential{}, failure("permission_denied")
	}
	next := s.next(now)
	if len(next.Credentials) >= maxCredentials {
		return IssuedCredential{}, failure("capacity_exceeded")
	}
	id, err := newCredentialID()
	if err != nil {
		return IssuedCredential{}, err
	}
	if _, exists := s.data.Credentials[id]; exists {
		return IssuedCredential{}, failure("store_unavailable")
	}
	token, hash, err := mint(mcpPrefix)
	if err != nil {
		return IssuedCredential{}, err
	}
	c := old
	c.Credential = cloneCredential(old.Credential)
	c.ID, c.Digest, c.CreatedAt, c.TTLSeconds = id, hash, now, ttl
	c.ClusterIDs = append([]string{}, clusterIDs...)
	if old.ReadOnly {
		c.ReadOnly = true
	}
	c.ExpiresAt = now.Add(time.Duration(ttl) * time.Second)
	if c.ExpiresAt.After(c.RefreshUntil) {
		c.ExpiresAt = c.RefreshUntil
	}
	old.RevokedAt = &now
	next.Credentials[old.ID], next.Credentials[id] = old, c
	if err := s.commit(next); err != nil {
		return IssuedCredential{}, err
	}
	return IssuedCredential{Credential: cloneCredential(c.Credential), Token: token}, nil
}

func (s *Store) Revoke(ownerID, id string) error {
	return s.RevokeForOrganization("self-hosted", ownerID, id)
}

func (s *Store) RevokeForOrganization(organizationID, ownerID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.data.Credentials[id]
	if !ok || c.OwnerID != ownerID || c.OrganizationID != organizationID {
		return failure("credential_invalid")
	}
	if c.RevokedAt != nil {
		return nil
	}
	now := s.now().UTC()
	if now.Before(c.CreatedAt) {
		return failure("store_unavailable")
	}
	c.RevokedAt = &now
	next := s.next(now)
	// A newly recorded revocation extends this record's retention period.
	next.Credentials[id] = c
	return s.commit(next)
}

func (s *Store) Credentials(ownerID string) []Credential {
	return s.CredentialsForOrganization("", ownerID)
}

func (s *Store) CredentialsForOrganization(organizationID, ownerID string) []Credential {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := []Credential{}
	for _, c := range s.data.Credentials {
		if c.OwnerID == ownerID && (organizationID == "" || c.OrganizationID == organizationID) {
			list = append(list, cloneCredential(c.Credential))
		}
	}
	slices.SortFunc(list, func(a, b Credential) int { return strings.Compare(a.ID, b.ID) })
	return list
}
