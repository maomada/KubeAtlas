package hubstate

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net/mail"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/maomada/KubeAtlas/internal/cloud"
	pkgauth "github.com/maomada/KubeAtlas/pkg/auth"
)

func requiredFields(data []byte) bool {
	var version struct {
		Version int `json:"version"`
	}
	if json.Unmarshal(data, &version) != nil {
		return false
	}
	if version.Version == 2 {
		return requiredV2Fields(data)
	}
	if version.Version != 1 {
		return false
	}
	return requiredV1Fields(data)
}

func requiredV1Fields(data []byte) bool {
	var file map[string]json.RawMessage
	if json.Unmarshal(data, &file) != nil {
		return false
	}
	connections := file["connectionSettings"] != nil
	extension := file["organization"] != nil || file["members"] != nil || connections
	if (len(file) != 4 && len(file) != 6 && len(file) != 7) ||
		file["version"] == nil || file["clusters"] == nil || file["groups"] == nil ||
		file["credentials"] == nil || extension && (file["organization"] == nil || file["members"] == nil) {
		return false
	}
	if extension && (bytes.Equal(bytes.TrimSpace(file["organization"]), []byte("null")) ||
		bytes.Equal(bytes.TrimSpace(file["members"]), []byte("null"))) {
		return false
	}
	if connections && bytes.Equal(bytes.TrimSpace(file["connectionSettings"]), []byte("null")) {
		return false
	}
	for section, fields := range map[string][]string{
		"clusters":    {"id", "name", "description", "environment", "region", "createdAt", "updatedAt", "digest"},
		"groups":      {"id", "name", "clusterIds", "createdAt", "updatedAt"},
		"credentials": {"id", "ownerId", "label", "clusterIds", "readOnly", "createdAt", "expiresAt", "refreshUntil", "digest", "familyId", "familyCreatedAt", "originalTtlSeconds", "ttlSeconds"},
		"members":     {"id", "userId", "displayName", "role", "status", "clusterIds", "kubernetesGroups", "createdAt", "updatedAt"},
	} {
		if section == "members" && !extension {
			continue
		}
		var records map[string]map[string]json.RawMessage
		if json.Unmarshal(file[section], &records) != nil || records == nil {
			return false
		}
		for _, record := range records {
			for _, field := range fields {
				if _, ok := record[field]; !ok {
					return false
				}
			}
			for field := range record {
				if !slices.Contains(fields, field) && !(section != "groups" && field == "revokedAt") {
					return false
				}
			}
			if section == "members" {
				for _, field := range []string{"clusterIds", "kubernetesGroups"} {
					if bytes.Equal(bytes.TrimSpace(record[field]), []byte("null")) {
						return false
					}
				}
			}
		}
	}
	if extension {
		var organization map[string]json.RawMessage
		if json.Unmarshal(file["organization"], &organization) != nil || len(organization) != 2 ||
			organization["id"] == nil || organization["name"] == nil {
			return false
		}
		for field := range organization {
			if field != "id" && field != "name" {
				return false
			}
		}
	}
	if connections {
		var settings map[string]json.RawMessage
		if json.Unmarshal(file["connectionSettings"], &settings) != nil || len(settings) != 2 ||
			settings["lanURL"] == nil || settings["publicURL"] == nil ||
			bytes.Equal(bytes.TrimSpace(settings["lanURL"]), []byte("null")) ||
			bytes.Equal(bytes.TrimSpace(settings["publicURL"]), []byte("null")) {
			return false
		}
		for field := range settings {
			if field != "lanURL" && field != "publicURL" {
				return false
			}
		}
	}
	return true
}

func requiredV2Fields(data []byte) bool {
	var file map[string]json.RawMessage
	if json.Unmarshal(data, &file) != nil || len(file) != 8 {
		return false
	}
	for _, section := range []string{"version", "clusters", "groups", "credentials", "organizations", "accounts", "memberships", "connectionSettings"} {
		if file[section] == nil || bytes.Equal(bytes.TrimSpace(file[section]), []byte("null")) {
			return false
		}
	}
	for section, fields := range map[string][]string{
		"clusters":    {"id", "name", "description", "environment", "region", "organizationId", "createdAt", "updatedAt", "digest"},
		"groups":      {"id", "name", "organizationId", "clusterIds", "createdAt", "updatedAt"},
		"credentials": {"id", "ownerId", "organizationId", "label", "clusterIds", "readOnly", "createdAt", "expiresAt", "refreshUntil", "digest", "familyId", "familyCreatedAt", "originalTtlSeconds", "ttlSeconds"},
	} {
		var records map[string]map[string]json.RawMessage
		if json.Unmarshal(file[section], &records) != nil || records == nil {
			return false
		}
		for _, record := range records {
			for _, field := range fields {
				if record[field] == nil {
					return false
				}
			}
			for field := range record {
				if !slices.Contains(fields, field) && field != "revokedAt" {
					return false
				}
			}
		}
	}
	var organizations map[string]map[string]json.RawMessage
	if json.Unmarshal(file["organizations"], &organizations) != nil || organizations == nil {
		return false
	}
	for _, organization := range organizations {
		if len(organization) != 2 || organization["id"] == nil || organization["name"] == nil {
			return false
		}
	}
	var accounts map[string]map[string]json.RawMessage
	if json.Unmarshal(file["accounts"], &accounts) != nil || accounts == nil {
		return false
	}
	accountFields := []string{"id", "userId", "displayName", "status", "createdAt", "updatedAt"}
	for _, account := range accounts {
		for _, field := range accountFields {
			if account[field] == nil {
				return false
			}
		}
		for field := range account {
			if !slices.Contains(append(accountFields, "email", "passwordHash", "platformOwner", "local", "authorizationRevision"), field) {
				return false
			}
		}
		if raw, exists := account["authorizationRevision"]; exists {
			var revision uint64
			if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &revision) != nil {
				return false
			}
		}
	}
	var memberships map[string]map[string]map[string]json.RawMessage
	if json.Unmarshal(file["memberships"], &memberships) != nil || memberships == nil {
		return false
	}
	for _, entries := range memberships {
		if entries == nil {
			return false
		}
		for _, membership := range entries {
			for _, field := range []string{"role", "clusterIds", "kubernetesGroups", "createdAt", "updatedAt"} {
				if membership[field] == nil {
					return false
				}
			}
			for _, field := range []string{"clusterIds", "kubernetesGroups"} {
				if bytes.Equal(bytes.TrimSpace(membership[field]), []byte("null")) {
					return false
				}
			}
			for field := range membership {
				if !slices.Contains([]string{"role", "clusterIds", "kubernetesGroups", "createdAt", "updatedAt", "legacyRestricted", "legacyAllClusters"}, field) {
					return false
				}
			}
		}
	}
	var connections map[string]json.RawMessage
	if json.Unmarshal(file["connectionSettings"], &connections) != nil || len(connections) != 2 || connections["lanURL"] == nil || connections["publicURL"] == nil {
		return false
	}
	return true
}

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
var oidcMemberIDPattern = regexp.MustCompile(`^oidc:[0-9a-f]{64}$`)

func validText(value string, limit int, required bool) bool {
	if len(value) > limit || !utf8.ValidString(value) || required && strings.TrimSpace(value) == "" {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func validName(name string) bool {
	return validText(name, 256, true) && strings.TrimSpace(name) == name
}

func validCluster(c ClusterInput) bool {
	return idPattern.MatchString(c.ID) && validName(c.Name) && validText(c.Description, 1000, false) && validText(c.Environment, 128, false) && validText(c.Region, 128, false)
}

func validOwner(owner string) bool { return validText(owner, maxStoreBytes, true) }

func validKubernetesGroups(userID string, groups []string) bool {
	if len(groups) > 128 || !pkgauth.ForwardedIdentityAllowed(userID, groups, true) {
		return false
	}
	total := 0
	for i, group := range groups {
		if !validText(group, 256, true) || strings.TrimSpace(group) != group ||
			strings.ContainsRune(group, ',') || i > 0 && groups[i-1] >= group {
			return false
		}
		total += len(group) + 1
		if total > 8192 {
			return false
		}
	}
	return true
}

func validMember(m Member, clusters map[string]storedCluster) bool {
	if !oidcMemberIDPattern.MatchString(m.ID) || m.UserID != "kubeatlas:"+m.ID ||
		!validName(m.DisplayName) || !validTimes(m.CreatedAt, m.UpdatedAt, nil) {
		return false
	}
	switch m.Role {
	case "admin", "member", "viewer":
	default:
		return false
	}
	switch m.Status {
	case "active", "pending", "disabled":
	default:
		return false
	}
	ids, err := scope(m.ClusterIDs, clusters, true)
	if err != nil || !slices.Equal(ids, m.ClusterIDs) {
		return false
	}
	if m.Role == "admin" && len(m.ClusterIDs) != 0 {
		return false
	}
	return validKubernetesGroups(m.UserID, m.KubernetesGroups)
}

func scope(ids []string, clusters map[string]storedCluster, allowEmpty bool) ([]string, error) {
	if len(ids) > maxClusters || !allowEmpty && len(ids) == 0 {
		return nil, failure("invalid_scope")
	}
	copy := append([]string{}, ids...)
	slices.Sort(copy)
	for i, id := range copy {
		if _, ok := clusters[id]; !ok || i > 0 && id == copy[i-1] {
			return nil, failure("invalid_scope")
		}
	}
	return copy, nil
}

func validHex(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func ambiguousConnectionSchemes(settings *ConnectionSettings) bool {
	if settings == nil || settings.LANURL == "" || settings.PublicURL == "" {
		return false
	}
	lan, lanErr := url.Parse(settings.LANURL)
	public, publicErr := url.Parse(settings.PublicURL)
	return lanErr == nil && publicErr == nil && strings.EqualFold(lan.Host, public.Host) && lan.Scheme != public.Scheme
}

func validTime(t time.Time) bool { return !t.IsZero() && t.Year() >= 1 && t.Year() <= 9999 }

func validTimes(created, updated time.Time, revoked *time.Time) bool {
	return validTime(created) && validTime(updated) && !updated.Before(created) && (revoked == nil || validTime(*revoked) && !revoked.Before(created) && !revoked.After(updated))
}

func validTTL(ttl int) bool { return ttl >= minTTL && ttl <= maxTTL }

func ttlSeconds(ttl, fallback int) (int, error) {
	if ttl == 0 {
		ttl = fallback
	}
	if !validTTL(ttl) {
		return 0, failure("invalid_parameters")
	}
	return ttl, nil
}

func validLegacyFile(file legacyStoreFile) bool {
	legacy := storeFile{Version: 1, Clusters: file.Clusters, Groups: file.Groups, Credentials: file.Credentials,
		Organization: file.Organization, Members: file.Members, Connections: file.Connections}
	return validLegacyData(legacy)
}

func validLegacyData(file storeFile) bool {
	if file.Version != 1 || file.Clusters == nil || file.Groups == nil || file.Credentials == nil || len(file.Clusters) > maxClusters || len(file.Groups) > maxGroups || len(file.Credentials) > maxCredentials {
		return false
	}
	if (file.Organization == nil) != (file.Members == nil) {
		return false
	}
	if file.Organization != nil {
		if file.Organization.ID != "self-hosted" || !validName(file.Organization.Name) ||
			len(file.Members)+1 > maxMembers {
			return false
		}
		for id, member := range file.Members {
			if id != member.ID || !validMember(member, file.Clusters) {
				return false
			}
		}
	}
	if file.Connections != nil {
		if ambiguousConnectionSchemes(file.Connections) {
			return false
		}
		for _, origin := range []string{file.Connections.LANURL, file.Connections.PublicURL} {
			if origin == "" {
				continue
			}
			normalized, err := cloud.NormalizeHubOrigin(origin)
			if err != nil || normalized != origin {
				return false
			}
		}
	}
	digests := map[string]bool{}
	for id, c := range file.Clusters {
		if id != c.ID || !validCluster(c.ClusterInput) || !validTimes(c.CreatedAt, c.UpdatedAt, c.RevokedAt) || !validHex(c.Digest) || digests[c.Digest] {
			return false
		}
		digests[c.Digest] = true
	}
	for id, g := range file.Groups {
		ids, err := scope(g.ClusterIDs, file.Clusters, true)
		if id != g.ID || !idPattern.MatchString(id) || !validName(g.Name) || err != nil || !slices.Equal(ids, g.ClusterIDs) || !validTimes(g.CreatedAt, g.UpdatedAt, nil) {
			return false
		}
	}
	families := map[string]storedCredential{}
	active := map[string]bool{}
	for id, c := range file.Credentials {
		ids, err := scope(c.ClusterIDs, file.Clusters, false)
		if id != c.ID || !validHex(id) || !validHex(c.FamilyID) || !validOwner(c.OwnerID) || !validText(c.Label, 128, false) || err != nil || !slices.Equal(ids, c.ClusterIDs) || !validHex(c.Digest) || digests[c.Digest] || !validTTL(c.TTLSeconds) || !validTTL(c.OriginalTTLSeconds) {
			return false
		}
		if !validTime(c.CreatedAt) || !validTime(c.ExpiresAt) || !validTime(c.RefreshUntil) || !validTime(c.FamilyCreatedAt) || c.CreatedAt.Before(c.FamilyCreatedAt) || !c.RefreshUntil.Equal(c.FamilyCreatedAt.Add(familyLifetime)) {
			return false
		}
		expires := c.CreatedAt.Add(time.Duration(c.TTLSeconds) * time.Second)
		if expires.After(c.RefreshUntil) {
			expires = c.RefreshUntil
		}
		if !c.ExpiresAt.Equal(expires) || c.ExpiresAt.Sub(c.CreatedAt) < time.Duration(minTTL)*time.Second || c.RevokedAt != nil && (!validTime(*c.RevokedAt) || c.RevokedAt.Before(c.CreatedAt)) {
			return false
		}
		if c.ID == c.FamilyID && (!c.CreatedAt.Equal(c.FamilyCreatedAt) || c.TTLSeconds != c.OriginalTTLSeconds) {
			return false
		}
		if first, ok := families[c.FamilyID]; ok {
			if c.OwnerID != first.OwnerID || c.ReadOnly != first.ReadOnly || c.Label != first.Label || !slices.Equal(c.ClusterIDs, first.ClusterIDs) || !c.FamilyCreatedAt.Equal(first.FamilyCreatedAt) || c.OriginalTTLSeconds != first.OriginalTTLSeconds {
				return false
			}
		} else {
			families[c.FamilyID] = c
		}
		if c.RevokedAt == nil {
			if active[c.FamilyID] {
				return false
			}
			active[c.FamilyID] = true
		}
		digests[c.Digest] = true
	}
	return true
}

func validFile(file storeFile) bool {
	if file.Version != 2 || file.Clusters == nil || file.Groups == nil || file.Credentials == nil ||
		file.Organizations == nil || file.Accounts == nil || file.Memberships == nil || file.Connections == nil ||
		len(file.Clusters) > maxClusters || len(file.Groups) > maxGroups || len(file.Credentials) > maxCredentials ||
		len(file.Organizations) == 0 || len(file.Organizations) > maxOrganizations || len(file.Accounts) > maxAccounts {
		return false
	}
	if _, ok := file.Organizations["self-hosted"]; !ok {
		return false
	}
	for id, organization := range file.Organizations {
		if id != organization.ID || !idPattern.MatchString(id) || !validName(organization.Name) {
			return false
		}
	}
	accountEmails := map[string]bool{}
	localOwners := 0
	for id, account := range file.Accounts {
		if id != account.ID || !validAccount(account) || accountEmails[account.Email] && account.Email != "" {
			return false
		}
		if account.Email != "" {
			accountEmails[account.Email] = true
		}
		if account.PlatformOwner {
			localOwners++
		}
	}
	if localOwners > 1 {
		return false
	}
	for organizationID, entries := range file.Memberships {
		if _, ok := file.Organizations[organizationID]; !ok || entries == nil {
			return false
		}
		for accountID, membership := range entries {
			account, ok := file.Accounts[accountID]
			if !ok || !validMembership(account, organizationID, membership, file.Clusters) {
				return false
			}
		}
	}
	if ambiguousConnectionSchemes(file.Connections) {
		return false
	}
	for _, origin := range []string{file.Connections.LANURL, file.Connections.PublicURL} {
		if origin == "" {
			continue
		}
		normalized, err := cloud.NormalizeHubOrigin(origin)
		if err != nil || normalized != origin {
			return false
		}
	}
	digests := map[string]bool{}
	for id, cluster := range file.Clusters {
		if id != cluster.ID || !validCluster(cluster.ClusterInput) || !validOrg(cluster.OrganizationID, file.Organizations) ||
			!validTimes(cluster.CreatedAt, cluster.UpdatedAt, cluster.RevokedAt) || !validHex(cluster.Digest) || digests[cluster.Digest] {
			return false
		}
		digests[cluster.Digest] = true
	}
	for id, group := range file.Groups {
		ids, err := scopeOrganization(group.ClusterIDs, group.OrganizationID, file.Clusters, true)
		if id != group.ID || !idPattern.MatchString(id) || !validName(group.Name) || !validOrg(group.OrganizationID, file.Organizations) ||
			err != nil || !slices.Equal(ids, group.ClusterIDs) || !validTimes(group.CreatedAt, group.UpdatedAt, nil) {
			return false
		}
	}
	families := map[string]storedCredential{}
	active := map[string]bool{}
	for id, credential := range file.Credentials {
		if id != credential.ID || !validHex(id) || !validHex(credential.FamilyID) || !validOwner(credential.OwnerID) ||
			!validOrg(credential.OrganizationID, file.Organizations) || !validText(credential.Label, 128, false) ||
			!validCredentialClusterScope(credential, file.Clusters) || !validHex(credential.Digest) || digests[credential.Digest] ||
			!validTTL(credential.TTLSeconds) || !validTTL(credential.OriginalTTLSeconds) {
			return false
		}
		if !validTime(credential.CreatedAt) || !validTime(credential.ExpiresAt) || !validTime(credential.RefreshUntil) ||
			!validTime(credential.FamilyCreatedAt) || credential.CreatedAt.Before(credential.FamilyCreatedAt) ||
			!credential.RefreshUntil.Equal(credential.FamilyCreatedAt.Add(familyLifetime)) {
			return false
		}
		expires := credential.CreatedAt.Add(time.Duration(credential.TTLSeconds) * time.Second)
		if expires.After(credential.RefreshUntil) {
			expires = credential.RefreshUntil
		}
		if !credential.ExpiresAt.Equal(expires) || credential.ExpiresAt.Sub(credential.CreatedAt) < time.Duration(minTTL)*time.Second ||
			credential.RevokedAt != nil && (!validTime(*credential.RevokedAt) || credential.RevokedAt.Before(credential.CreatedAt)) {
			return false
		}
		if credential.ID == credential.FamilyID && (!credential.CreatedAt.Equal(credential.FamilyCreatedAt) || credential.TTLSeconds != credential.OriginalTTLSeconds) {
			return false
		}
		if first, ok := families[credential.FamilyID]; ok {
			if credential.OwnerID != first.OwnerID || credential.OrganizationID != first.OrganizationID || credential.ReadOnly != first.ReadOnly ||
				credential.Label != first.Label || !slices.Equal(credential.ClusterIDs, first.ClusterIDs) ||
				!credential.FamilyCreatedAt.Equal(first.FamilyCreatedAt) || credential.OriginalTTLSeconds != first.OriginalTTLSeconds {
				return false
			}
		} else {
			families[credential.FamilyID] = credential
		}
		if credential.RevokedAt == nil {
			if active[credential.FamilyID] {
				return false
			}
			active[credential.FamilyID] = true
		}
		digests[credential.Digest] = true
	}
	return true
}

func validCredentialClusterScope(credential storedCredential, clusters map[string]storedCluster) bool {
	ids := credential.ClusterIDs
	if credential.RevokedAt == nil {
		normalized, err := scopeOrganization(ids, credential.OrganizationID, clusters, false)
		return err == nil && slices.Equal(normalized, ids)
	}
	if len(ids) == 0 || len(ids) > maxClusters {
		return false
	}
	for index, id := range ids {
		if !idPattern.MatchString(id) || index > 0 && id <= ids[index-1] {
			return false
		}
	}
	return true
}

func validOrg(id string, organizations map[string]Organization) bool {
	_, ok := organizations[id]
	return ok && idPattern.MatchString(id)
}

func validAccount(account Account) bool {
	if !validOwner(account.ID) || !validOwner(account.UserID) || !validName(account.DisplayName) ||
		!validTimes(account.CreatedAt, account.UpdatedAt, nil) {
		return false
	}
	switch account.Status {
	case "active", "pending", "disabled":
	default:
		return false
	}
	if account.Email != "" {
		parsed, err := mail.ParseAddress(account.Email)
		if err != nil || parsed.Address != account.Email || strings.ToLower(account.Email) != account.Email || len(account.Email) > 254 {
			return false
		}
	}
	if account.PasswordHash != "" && !validPasswordHash(account.PasswordHash) {
		return false
	}
	return account.PlatformOwner == account.Local && (!account.Local || account.Status == "active" && account.Email == "" && account.PasswordHash == "")
}

func validPasswordHash(value string) bool {
	return len(value) == 60 && (strings.HasPrefix(value, "$2a$") || strings.HasPrefix(value, "$2b$") || strings.HasPrefix(value, "$2y$"))
}

func validMembership(account Account, organizationID string, membership Membership, clusters map[string]storedCluster) bool {
	switch membership.Role {
	case "owner", "member", "viewer":
	default:
		return false
	}
	if !validTimes(membership.CreatedAt, membership.UpdatedAt, nil) || membership.LegacyAllClusters && !membership.LegacyRestricted {
		return false
	}
	ids, err := scopeOrganization(membership.ClusterIDs, organizationID, clusters, true)
	if err != nil || !slices.Equal(ids, membership.ClusterIDs) || !validKubernetesGroups(account.UserID, membership.KubernetesGroups) {
		return false
	}
	return true
}

func scopeOrganization(ids []string, organizationID string, clusters map[string]storedCluster, allowEmpty bool) ([]string, error) {
	copy := append([]string{}, ids...)
	slices.Sort(copy)
	if len(copy) > maxClusters || !allowEmpty && len(copy) == 0 {
		return nil, failure("invalid_scope")
	}
	for i, id := range copy {
		cluster, ok := clusters[id]
		if !ok || cluster.OrganizationID != organizationID || i > 0 && id == copy[i-1] {
			return nil, failure("invalid_scope")
		}
	}
	return copy, nil
}
