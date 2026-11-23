package hubstate

import (
	"crypto/sha256"
	"encoding/hex"
)

// LocalPlatformOwner returns the persisted recovery account, including legacy
// accounts that still need to migrate their configured password.
func (s *Store) LocalPlatformOwner() (Account, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.localPlatformOwner()
}

// localPlatformOwner requires s.mu to be held.
func (s *Store) localPlatformOwner() (Account, bool) {
	for _, account := range s.data.Accounts {
		if account.Local && account.PlatformOwner {
			return cloneAccount(account), true
		}
	}
	return Account{}, false
}

// SetLocalPlatformOwner initializes the recovery account or explicitly
// changes its login credentials. Its account and Kubernetes identities are
// permanently fixed at initialization.
func (s *Store) SetLocalPlatformOwner(username, passwordHash string, reset bool) (Account, error) {
	if !validName(username) || !validPasswordHash(passwordHash) {
		return Account{}, failure("invalid_parameters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	account, initialized := s.localPlatformOwner()
	if initialized && !reset && (account.PasswordHash != "" || account.DisplayName != username) {
		return Account{}, failure("already_initialized")
	}
	if initialized && account.DisplayName == username && account.PasswordHash == passwordHash {
		return account, nil
	}

	now := s.now().UTC()
	next := s.next(now)
	if !initialized {
		sum := sha256.Sum256([]byte(username))
		id := "portal:" + hex.EncodeToString(sum[:])
		userID := "kubeatlas:" + id
		previous, exists := next.Accounts[id]
		switch {
		case exists:
			// State migrated from format 1 may include this identity as an
			// ordinary account because that format had no platform-owner flag.
			if previous.UserID != userID || previous.Email != "" || previous.PasswordHash != "" {
				return Account{}, failure("store_unavailable")
			}
			account = previous
		case len(next.Accounts) >= maxAccounts:
			return Account{}, failure("capacity_exceeded")
		default:
			account = Account{ID: id, UserID: userID, CreatedAt: now}
		}
		account.Status, account.Local, account.PlatformOwner = "active", true, true
		ensureDefaultOwnerMembership(next, account.ID, now)
	}
	account.DisplayName, account.PasswordHash, account.UpdatedAt = username, passwordHash, now
	next.Accounts[account.ID] = account
	if err := s.commit(next); err != nil {
		return Account{}, err
	}
	return cloneAccount(s.data.Accounts[account.ID]), nil
}
