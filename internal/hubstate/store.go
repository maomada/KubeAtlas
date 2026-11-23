package hubstate

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/maomada/KubeAtlas/internal/cloud"
)

const (
	maxStoreBytes    = 16 << 20
	maxClusters      = 100
	maxGroups        = 100
	maxMembers       = 1024
	maxAccounts      = 4096
	maxOrganizations = 100
	maxCredentials   = 16384
	minTTL           = 15 * 60
	maxTTL           = 7 * 24 * 60 * 60
	defaultTTL       = 8 * 60 * 60
	familyLifetime   = 30 * 24 * time.Hour
	retention        = 30 * 24 * time.Hour
)

type storedCluster struct {
	Cluster
	Digest string `json:"digest"`
}

type storedCredential struct {
	Credential
	Digest             string    `json:"digest"`
	FamilyID           string    `json:"familyId"`
	FamilyCreatedAt    time.Time `json:"familyCreatedAt"`
	OriginalTTLSeconds int       `json:"originalTtlSeconds"`
	TTLSeconds         int       `json:"ttlSeconds"`
}

type storeFile struct {
	Version       int                              `json:"version"`
	Clusters      map[string]storedCluster         `json:"clusters"`
	Groups        map[string]Group                 `json:"groups"`
	Credentials   map[string]storedCredential      `json:"credentials"`
	Organizations map[string]Organization          `json:"organizations"`
	Accounts      map[string]Account               `json:"accounts"`
	Memberships   map[string]map[string]Membership `json:"memberships"`
	Connections   *ConnectionSettings              `json:"connectionSettings"`
	Organization  *Organization                    `json:"-"`
	Members       map[string]Member                `json:"-"`
}

type legacyStoreFile struct {
	Version      int                         `json:"version"`
	Clusters     map[string]storedCluster    `json:"clusters"`
	Groups       map[string]Group            `json:"groups"`
	Credentials  map[string]storedCredential `json:"credentials"`
	Organization *Organization               `json:"organization,omitempty"`
	Members      map[string]Member           `json:"members,omitempty"`
	Connections  *ConnectionSettings         `json:"connectionSettings,omitempty"`
}

type Store struct {
	mu   sync.Mutex
	path string
	data storeFile
	now  func() time.Time
}

func OpenStore(path string) (*Store, error) {
	return OpenStoreWithInitialOrigin(path, "")
}

// OpenStoreWithInitialOrigin seeds the public HTTPS address for a new Hub.
// Existing stores can manage their addresses independently of deployment configuration.
func OpenStoreWithInitialOrigin(path, origin string) (*Store, error) {
	if path == "" {
		return nil, failure("invalid_parameters")
	}
	if origin != "" {
		normalized, err := cloud.NormalizeHubOrigin(origin)
		if err != nil || !strings.HasPrefix(normalized, "https://") {
			return nil, errors.New("initial Hub origin must be an HTTPS root origin")
		}
		origin = normalized
	}
	s := &Store{path: path, now: time.Now, data: newStoreFile()}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		s.data.Connections.PublicURL = origin
		return s, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return nil, failure("store_unavailable")
	}
	dir, err := os.Stat(filepath.Dir(path))
	if err != nil || !dir.IsDir() || dir.Mode().Perm() != 0o700 {
		return nil, failure("store_unavailable")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, failure("store_unavailable")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxStoreBytes+1))
	if err != nil {
		return nil, failure("store_unavailable")
	}
	if len(data) > maxStoreBytes {
		return nil, failure("capacity_exceeded")
	}
	if !utf8.Valid(data) || !uniqueJSON(data) || !requiredFields(data) {
		return nil, failure("store_unavailable")
	}
	var version struct {
		Version int `json:"version"`
	}
	if json.Unmarshal(data, &version) != nil {
		return nil, failure("store_unavailable")
	}
	if version.Version == 1 {
		var legacy legacyStoreFile
		if decodeStrict(data, &legacy) != nil || !validLegacyFile(legacy) {
			return nil, failure("store_unavailable")
		}
		s.data = migrateLegacy(legacy)
		if err := s.commit(s.data); err != nil {
			return nil, err
		}
	} else if version.Version == 2 {
		var file storeFile
		if decodeStrict(data, &file) != nil || !validFile(file) {
			return nil, failure("store_unavailable")
		}
		s.data = file
		syncAliasesFromPrimary(&s.data)
	} else {
		return nil, failure("store_unavailable")
	}
	return s, nil
}

func newStoreFile() storeFile {
	organization := Organization{ID: "self-hosted", Name: "KubeAtlas"}
	return storeFile{Version: 2, Clusters: map[string]storedCluster{}, Groups: map[string]Group{},
		Credentials: map[string]storedCredential{}, Organizations: map[string]Organization{organization.ID: organization},
		Accounts: map[string]Account{}, Memberships: map[string]map[string]Membership{organization.ID: {}},
		Connections: &ConnectionSettings{}, Organization: &organization, Members: map[string]Member{}}
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || decoder.Decode(new(any)) != io.EOF {
		return failure("store_unavailable")
	}
	return nil
}

func migrateLegacy(legacy legacyStoreFile) storeFile {
	file := newStoreFile()
	if legacy.Organization != nil {
		file.Organizations[legacy.Organization.ID] = *legacy.Organization
		file.Memberships[legacy.Organization.ID] = map[string]Membership{}
	}
	file.Clusters = legacy.Clusters
	file.Groups = legacy.Groups
	file.Credentials = legacy.Credentials
	file.Connections = cloneConnectionSettings(legacy.Connections)
	for id, cluster := range file.Clusters {
		cluster.OrganizationID = "self-hosted"
		file.Clusters[id] = cluster
	}
	for id, group := range file.Groups {
		group.OrganizationID = "self-hosted"
		file.Groups[id] = group
	}
	for id, credential := range file.Credentials {
		credential.OrganizationID = "self-hosted"
		file.Credentials[id] = credential
	}
	if legacy.Members != nil {
		for id, member := range legacy.Members {
			role := member.Role
			legacyRestricted, legacyAllClusters := true, role == "admin" && len(member.ClusterIDs) == 0
			if role == "admin" {
				role = "owner"
			}
			file.Accounts[id] = Account{ID: id, UserID: member.UserID, DisplayName: member.DisplayName,
				Status: member.Status, CreatedAt: member.CreatedAt, UpdatedAt: member.UpdatedAt}
			file.Memberships["self-hosted"][id] = Membership{Role: role, ClusterIDs: append([]string{}, member.ClusterIDs...),
				KubernetesGroups: append([]string{}, member.KubernetesGroups...), LegacyRestricted: legacyRestricted,
				LegacyAllClusters: legacyAllClusters, CreatedAt: member.CreatedAt, UpdatedAt: member.UpdatedAt}
		}
	}
	syncAliasesFromPrimary(&file)
	return file
}

func syncAliasesFromPrimary(file *storeFile) {
	if file.Organizations == nil {
		file.Organizations = map[string]Organization{}
	}
	if file.Memberships == nil {
		file.Memberships = map[string]map[string]Membership{}
	}
	organization, ok := file.Organizations["self-hosted"]
	if ok {
		copy := organization
		file.Organization = &copy
	}
	file.Members = make(map[string]Member)
	for id, membership := range file.Memberships["self-hosted"] {
		account, exists := file.Accounts[id]
		if !exists {
			continue
		}
		file.Members[id] = Member{ID: id, UserID: account.UserID, DisplayName: account.DisplayName, Role: membership.Role,
			Status: account.Status, AuthorizationRevision: account.AuthorizationRevision, ClusterIDs: append([]string{}, membership.ClusterIDs...),
			KubernetesGroups: append([]string{}, membership.KubernetesGroups...), LegacyRestricted: membership.LegacyRestricted,
			LegacyAllClusters: membership.LegacyAllClusters, CreatedAt: membership.CreatedAt, UpdatedAt: membership.UpdatedAt}
	}
}

func syncPrimaryFromAliases(file *storeFile) {
	if file.Organizations == nil {
		file.Organizations = map[string]Organization{}
	}
	if file.Memberships == nil {
		file.Memberships = map[string]map[string]Membership{}
	}
	if file.Organization != nil {
		file.Organizations[file.Organization.ID] = *file.Organization
	}
	if file.Members == nil {
		return
	}
	entries := make(map[string]Membership, len(file.Members))
	for id, member := range file.Members {
		previous := file.Memberships["self-hosted"][id]
		accountID := id
		account, ok := file.Accounts[accountID]
		if !ok {
			for candidate, current := range file.Accounts {
				if current.UserID == member.UserID {
					accountID, account, ok = candidate, current, true
					break
				}
			}
		}
		if !ok {
			account = Account{ID: accountID, UserID: member.UserID, CreatedAt: member.CreatedAt, UpdatedAt: member.UpdatedAt}
		}
		account.DisplayName, account.Status, account.UpdatedAt = member.DisplayName, member.Status, member.UpdatedAt
		file.Accounts[accountID] = account
		entries[id] = Membership{Role: member.Role, ClusterIDs: append([]string{}, member.ClusterIDs...),
			KubernetesGroups: append([]string{}, member.KubernetesGroups...), LegacyRestricted: previous.LegacyRestricted,
			LegacyAllClusters: previous.LegacyAllClusters, CreatedAt: member.CreatedAt, UpdatedAt: member.UpdatedAt}
	}
	file.Memberships["self-hosted"] = entries
}

func (s *Store) next(now time.Time) storeFile {
	next := storeFile{Version: 2, Clusters: make(map[string]storedCluster, len(s.data.Clusters)),
		Groups: make(map[string]Group, len(s.data.Groups)), Credentials: make(map[string]storedCredential, len(s.data.Credentials)),
		Organizations: make(map[string]Organization, len(s.data.Organizations)), Accounts: make(map[string]Account, len(s.data.Accounts)),
		Memberships: make(map[string]map[string]Membership, len(s.data.Memberships)), Connections: cloneConnectionSettings(s.data.Connections)}
	for id, organization := range s.data.Organizations {
		next.Organizations[id] = organization
	}
	for id, account := range s.data.Accounts {
		next.Accounts[id] = cloneAccount(account)
	}
	for organizationID, entries := range s.data.Memberships {
		copyEntries := make(map[string]Membership, len(entries))
		for memberID, membership := range entries {
			copyEntries[memberID] = cloneMembership(membership)
		}
		next.Memberships[organizationID] = copyEntries
	}
	for id, c := range s.data.Clusters {
		c.Cluster = cloneCluster(c.Cluster)
		next.Clusters[id] = c
	}
	for id, g := range s.data.Groups {
		next.Groups[id] = cloneGroup(g)
	}
	for id, c := range s.data.Credentials {
		last := c.ExpiresAt
		if c.RevokedAt != nil && c.RevokedAt.After(last) {
			last = *c.RevokedAt
		}
		if !last.IsZero() && now.After(last.Add(retention)) {
			continue
		}
		c.Credential = cloneCredential(c.Credential)
		next.Credentials[id] = c
	}
	syncAliasesFromPrimary(&next)
	return next
}

func (s *Store) commit(next storeFile) error {
	if next.Accounts == nil {
		return failure("store_unavailable")
	}
	accounts := make(map[string]Account, len(next.Accounts))
	for id, account := range next.Accounts {
		accounts[id] = account
	}
	next.Accounts = accounts
	if err := applyAuthorizationRevisions(s.data, &next); err != nil {
		return err
	}
	syncAliasesFromPrimary(&next)
	if len(next.Credentials) > maxCredentials {
		return failure("capacity_exceeded")
	}
	if !validFile(next) {
		return failure("store_unavailable")
	}
	data, err := json.Marshal(next)
	if err != nil {
		return failure("store_unavailable")
	}
	if len(data) > maxStoreBytes {
		return failure("capacity_exceeded")
	}
	dir := filepath.Dir(s.path)
	if os.MkdirAll(dir, 0o700) != nil {
		return failure("store_unavailable")
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return failure("store_unavailable")
	}
	temp, err := os.CreateTemp(dir, ".hub-state-*.tmp")
	if err != nil {
		return failure("store_unavailable")
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	if temp.Chmod(0o600) != nil {
		return failure("store_unavailable")
	}
	if _, err := temp.Write(data); err != nil {
		return failure("store_unavailable")
	}
	if temp.Sync() != nil || temp.Close() != nil {
		return failure("store_unavailable")
	}
	if os.Rename(temp.Name(), s.path) != nil {
		return failure("store_unavailable")
	}
	// Rename is the commit point. A subsequent directory-sync error cannot
	// be reported as rollback: the new credentials are already on disk.
	s.data = next
	if directory, err := os.Open(dir); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return nil
}

func uniqueJSON(data []byte) bool {
	d := json.NewDecoder(bytes.NewReader(data))
	var value func(int) bool
	value = func(depth int) bool {
		if depth > 32 {
			return false
		}
		token, err := d.Token()
		if err != nil || token == nil {
			return false
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return true
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				name, ok := key.(string)
				if err != nil || !ok || seen[name] {
					return false
				}
				seen[name] = true
				if !value(depth + 1) {
					return false
				}
			}
		case '[':
			for d.More() {
				if !value(depth + 1) {
					return false
				}
			}
		default:
			return false
		}
		_, err = d.Token()
		return err == nil
	}
	if !value(0) {
		return false
	}
	_, err := d.Token()
	return err == io.EOF
}
