package hubstate

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	pkgauth "github.com/maomada/KubeAtlas/pkg/auth"
)

func requiredFields(data []byte) bool {
	var file map[string]json.RawMessage
	if json.Unmarshal(data, &file) != nil {
		return false
	}
	extension := file["organization"] != nil || file["members"] != nil
	if (len(file) != 4 && len(file) != 6) ||
		file["version"] == nil || file["clusters"] == nil || file["groups"] == nil ||
		file["credentials"] == nil || extension && (file["organization"] == nil || file["members"] == nil) {
		return false
	}
	if extension && (bytes.Equal(bytes.TrimSpace(file["organization"]), []byte("null")) ||
		bytes.Equal(bytes.TrimSpace(file["members"]), []byte("null"))) {
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

func validFile(file storeFile) bool {
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
