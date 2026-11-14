package hubstate

import "slices"

type accountAuthority struct {
	userID        string
	email         string
	passwordHash  string
	status        string
	platformOwner bool
	local         bool
	memberships   map[string]membershipAuthority
}

type membershipAuthority struct {
	role              string
	clusterIDs        []string
	kubernetesGroups  []string
	legacyRestricted  bool
	legacyAllClusters bool
}

func applyAuthorizationRevisions(previous storeFile, next *storeFile) error {
	previousAuthority := accountAuthorities(previous)
	nextAuthority := accountAuthorities(*next)
	for id, account := range next.Accounts {
		previousAccount, exists := previous.Accounts[id]
		if !exists {
			account.AuthorizationRevision = 1
		} else if !sameAccountAuthority(previousAuthority[id], nextAuthority[id]) {
			if previousAccount.AuthorizationRevision == ^uint64(0) {
				return failure("capacity_exceeded")
			}
			account.AuthorizationRevision = previousAccount.AuthorizationRevision + 1
		} else {
			account.AuthorizationRevision = previousAccount.AuthorizationRevision
		}
		next.Accounts[id] = account
	}
	return nil
}

func accountAuthorities(file storeFile) map[string]accountAuthority {
	authorities := make(map[string]accountAuthority, len(file.Accounts))
	for id, account := range file.Accounts {
		authorities[id] = accountAuthority{
			userID: account.UserID, email: account.Email, passwordHash: account.PasswordHash,
			status: account.Status, platformOwner: account.PlatformOwner, local: account.Local,
			memberships: map[string]membershipAuthority{},
		}
	}
	for organizationID, entries := range file.Memberships {
		for accountID, membership := range entries {
			authority := authorities[accountID]
			if authority.memberships == nil {
				authority.memberships = map[string]membershipAuthority{}
			}
			authority.memberships[organizationID] = membershipAuthority{
				role: membership.Role, clusterIDs: sortedAuthorityValues(membership.ClusterIDs),
				kubernetesGroups: sortedAuthorityValues(membership.KubernetesGroups),
				legacyRestricted: membership.LegacyRestricted, legacyAllClusters: membership.LegacyAllClusters,
			}
			authorities[accountID] = authority
		}
	}
	return authorities
}

func sameAccountAuthority(left, right accountAuthority) bool {
	if left.userID != right.userID || left.email != right.email || left.passwordHash != right.passwordHash ||
		left.status != right.status || left.platformOwner != right.platformOwner || left.local != right.local ||
		len(left.memberships) != len(right.memberships) {
		return false
	}
	for organizationID, leftMembership := range left.memberships {
		rightMembership, exists := right.memberships[organizationID]
		if !exists || leftMembership.role != rightMembership.role ||
			!slices.Equal(leftMembership.clusterIDs, rightMembership.clusterIDs) ||
			!slices.Equal(leftMembership.kubernetesGroups, rightMembership.kubernetesGroups) ||
			leftMembership.legacyRestricted != rightMembership.legacyRestricted ||
			leftMembership.legacyAllClusters != rightMembership.legacyAllClusters {
			return false
		}
	}
	return true
}

func sortedAuthorityValues(values []string) []string {
	result := slices.Clone(values)
	slices.Sort(result)
	return result
}
