package hubmcp

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
)

type pager struct {
	key [32]byte
	now func() time.Time
}
type continuation struct {
	Query, Version string
	Offset         int
	ExpiresAt      time.Time
}
type page struct {
	Limit          int    `json:"limit"`
	Returned       int    `json:"returned"`
	MatchingAtRead int    `json:"matchingAtRead"`
	Truncated      bool   `json:"truncated"`
	NextCursor     string `json:"nextCursor,omitempty"`
	Consistency    string `json:"consistency"`
}

func newPager() *pager {
	p := &pager{now: time.Now}
	if _, err := rand.Read(p.key[:]); err != nil {
		panic("Hub cursor key unavailable")
	}
	return p
}
func fingerprint(value any) string {
	body, _ := json.Marshal(value)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
func (p *pager) sign(c continuation) string {
	body, _ := json.Marshal(c)
	mac := hmac.New(sha256.New, p.key[:])
	mac.Write(body)
	return base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func pageRows[T any](p *pager, rows []T, limit int, token, query, version string) ([]T, page, error) {
	if limit < 0 || limit > 100 {
		return nil, page{}, invalid("limit must be between 1 and 100")
	}
	if limit == 0 {
		limit = 20
	}
	c := continuation{Query: query, Version: version, ExpiresAt: p.now().UTC().Add(10 * time.Minute)}
	bad := &fault{code: "invalid_cursor", message: "Cursor is invalid for this request and credential.", next: "Restart without cursor, keeping the intended selection and limit."}
	if token != "" {
		if len(token) > 4096 {
			return nil, page{}, bad
		}
		body, signature, ok := strings.Cut(token, ".")
		data, err := base64.RawURLEncoding.Strict().DecodeString(body)
		sig, sigErr := base64.RawURLEncoding.Strict().DecodeString(signature)
		mac := hmac.New(sha256.New, p.key[:])
		mac.Write(data)
		if !ok || err != nil || sigErr != nil || !hmac.Equal(sig, mac.Sum(nil)) || json.Unmarshal(data, &c) != nil {
			return nil, page{}, bad
		}
		canonical, _ := json.Marshal(c)
		if !bytes.Equal(canonical, data) || c.Offset < 0 || c.Query != query || c.ExpiresAt.IsZero() {
			return nil, page{}, bad
		}
		if !p.now().Before(c.ExpiresAt) {
			return nil, page{}, &fault{code: "cursor_expired", message: "Cursor expired.", next: "Restart the query without cursor."}
		}
		if c.Version != version {
			return nil, page{}, &fault{code: "source_changed", message: "Directory, connection or catalog changed during traversal.", next: "Restart the query without cursor."}
		}
		if c.Offset > len(rows) {
			return nil, page{}, bad
		}
	}
	end := min(c.Offset+limit, len(rows))
	meta := page{Limit: limit, Returned: end - c.Offset, MatchingAtRead: len(rows), Truncated: end < len(rows), Consistency: "live_non_atomic"}
	start := c.Offset
	if meta.Truncated {
		c.Offset = end
		meta.NextCursor = p.sign(c)
	}
	return rows[start:end], meta, nil
}
