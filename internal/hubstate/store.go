package hubstate

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	maxStoreBytes  = 16 << 20
	maxClusters    = 100
	maxGroups      = 100
	maxMembers     = 1024
	maxCredentials = 16384
	minTTL         = 15 * 60
	maxTTL         = 7 * 24 * 60 * 60
	defaultTTL     = 8 * 60 * 60
	familyLifetime = 30 * 24 * time.Hour
	retention      = 30 * 24 * time.Hour
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
	Version      int                         `json:"version"`
	Clusters     map[string]storedCluster    `json:"clusters"`
	Groups       map[string]Group            `json:"groups"`
	Credentials  map[string]storedCredential `json:"credentials"`
	Organization *Organization               `json:"organization,omitempty"`
	Members      map[string]Member           `json:"members"`
}

type Store struct {
	mu   sync.Mutex
	path string
	data storeFile
	now  func() time.Time
}

func OpenStore(path string) (*Store, error) {
	if path == "" {
		return nil, failure("invalid_parameters")
	}
	s := &Store{path: path, now: time.Now, data: storeFile{Version: 1,
		Clusters: map[string]storedCluster{}, Groups: map[string]Group{}, Credentials: map[string]storedCredential{},
		Organization: &Organization{ID: "self-hosted", Name: "KubeAtlas"}, Members: map[string]Member{}}}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
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
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var file storeFile
	if decoder.Decode(&file) != nil || decoder.Decode(new(any)) != io.EOF || !validFile(file) {
		return nil, failure("store_unavailable")
	}
	if file.Organization == nil {
		file.Organization = &Organization{ID: "self-hosted", Name: "KubeAtlas"}
		file.Members = map[string]Member{}
	}
	s.data = file
	return s, nil
}

func (s *Store) next(now time.Time) storeFile {
	next := storeFile{
		Version: 1, Clusters: make(map[string]storedCluster, len(s.data.Clusters)),
		Groups:       make(map[string]Group, len(s.data.Groups)),
		Credentials:  make(map[string]storedCredential, len(s.data.Credentials)),
		Organization: &Organization{ID: s.data.Organization.ID, Name: s.data.Organization.Name},
		Members:      make(map[string]Member, len(s.data.Members)),
	}
	for id, c := range s.data.Clusters {
		c.Cluster = cloneCluster(c.Cluster)
		next.Clusters[id] = c
	}
	for id, g := range s.data.Groups {
		next.Groups[id] = cloneGroup(g)
	}
	for id, m := range s.data.Members {
		next.Members[id] = cloneMember(m)
	}
	for id, c := range s.data.Credentials {
		last := c.ExpiresAt
		if c.RevokedAt != nil && c.RevokedAt.After(last) {
			last = *c.RevokedAt
		}
		if now.After(last.Add(retention)) {
			continue
		}
		c.Credential = cloneCredential(c.Credential)
		next.Credentials[id] = c
	}
	return next
}

func (s *Store) commit(next storeFile) error {
	if len(next.Credentials) > maxCredentials {
		return failure("capacity_exceeded")
	}
	if next.Organization == nil || next.Members == nil || !validFile(next) {
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
