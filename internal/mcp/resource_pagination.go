package mcp

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"time"

	pkgauth "github.com/maomada/KubeAtlas/pkg/auth"
	"github.com/maomada/KubeAtlas/pkg/readreason"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
)

const resourcePageMaxLimit = 200
const resourceCursorMaxBytes = 4096
const resourceCursorLifetime = 10 * time.Minute

type resourcePage struct {
	Limit          int    `json:"limit"`
	Returned       int    `json:"returned"`
	MatchingAtRead int    `json:"matchingAtRead"`
	Truncated      bool   `json:"truncated"`
	NextCursor     string `json:"nextCursor,omitempty"`
	Consistency    string `json:"consistency"`
}

type resourceCursor struct {
	Version    int       `json:"version"`
	Generation uint64    `json:"generation"`
	Scope      string    `json:"scope"`
	ExpiresAt  time.Time `json:"expiresAt"`
	Namespace  string    `json:"namespace"`
	Name       string    `json:"name"`
}

type resourcePager struct {
	key [32]byte
	now func() time.Time
}

func newResourcePager() *resourcePager {
	p := &resourcePager{now: time.Now}
	if _, err := rand.Read(p.key[:]); err != nil {
		panic("MCP pagination key unavailable")
	}
	return p
}

var sourceResourcePager = newResourcePager()

type resourcePageRequest struct {
	limit  int
	cursor resourceCursor
	pager  *resourcePager
}

func paginationError(code readreason.Code) error {
	text, next := "The resource cursor is invalid for this request or identity.", "Restart list_resources without cursor, keeping the intended query and a limit from 1 to 200."
	switch code {
	case readreason.InvalidParameters:
		text, next = "Resource pagination requires a limit from 1 to 200; cursor requires a positive limit.", "Set limit to 1..200, or omit both limit and cursor for an unpaged list."
	case readreason.CursorExpired:
		text = "The resource cursor's ten-minute lifetime has ended."
	}
	return &readError{code: code, err: errors.New(text), next: next}
}

func validateResourcePageInput(input listResourcesInput) error {
	if input.Limit < 0 || input.Limit > resourcePageMaxLimit || input.Limit == 0 && input.Cursor != "" {
		return paginationError(readreason.InvalidParameters)
	}
	if len(input.Cursor) > resourceCursorMaxBytes {
		return paginationError(readreason.InvalidCursor)
	}
	return nil
}

func resourceScopeFingerprint(ctx context.Context, input listResourcesInput) string {
	type principal struct {
		Username  string
		Groups    []string
		UID       string
		System    bool
		HubGroups []string
		Extra     map[string][]string
		TokenHash string
	}
	canonical := func(values []string) []string {
		copy := append([]string{}, values...)
		slices.Sort(copy)
		return slices.Compact(copy)
	}
	var identity *principal
	if user := pkgauth.UserFromContext(ctx); user != nil {
		identity = &principal{Username: user.Username, Groups: canonical(user.Groups), UID: user.UID, System: user.System, HubGroups: canonical(user.HubGroups), Extra: map[string][]string{}}
		for name, values := range user.Extra {
			identity.Extra[name] = canonical(values)
		}
		if user.Token != "" {
			sum := sha256.Sum256([]byte(user.Token))
			identity.TokenHash = hex.EncodeToString(sum[:])
		}
	}
	input.Cursor = ""
	payload, _ := json.Marshal(struct {
		Query    listResourcesInput
		Identity *principal
	}{input, identity})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func (p *resourcePager) prepare(ctx context.Context, input listResourcesInput, generation uint64) (*resourcePageRequest, error) {
	if err := validateResourcePageInput(input); err != nil {
		return nil, err
	}
	if input.Limit == 0 {
		return nil, nil
	}
	scope := resourceScopeFingerprint(ctx, input)
	cursor := resourceCursor{Version: 1, Generation: generation, Scope: scope, ExpiresAt: p.now().UTC().Add(resourceCursorLifetime)}
	if input.Cursor != "" {
		var err error
		cursor, err = p.decode(input.Cursor)
		if err != nil {
			return nil, err
		}
		if cursor.Scope != scope {
			return nil, paginationError(readreason.InvalidCursor)
		}
		if cursor.Generation != generation {
			return nil, errConnectionChanged()
		}
		if !p.now().Before(cursor.ExpiresAt) {
			return nil, paginationError(readreason.CursorExpired)
		}
	}
	return &resourcePageRequest{limit: input.Limit, cursor: cursor, pager: p}, nil
}

func (p *resourcePager) encode(cursor resourceCursor) string {
	payload, _ := json.Marshal(cursor)
	mac := hmac.New(sha256.New, p.key[:])
	mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (p *resourcePager) decode(token string) (resourceCursor, error) {
	invalid := func() (resourceCursor, error) { return resourceCursor{}, paginationError(readreason.InvalidCursor) }
	if len(token) > resourceCursorMaxBytes {
		return invalid()
	}
	body, signature, ok := strings.Cut(token, ".")
	if !ok || len(signature) != 43 {
		return invalid()
	}
	payload, err := base64.RawURLEncoding.Strict().DecodeString(body)
	if err != nil {
		return invalid()
	}
	sig, err := base64.RawURLEncoding.Strict().DecodeString(signature)
	if err != nil {
		return invalid()
	}
	mac := hmac.New(sha256.New, p.key[:])
	mac.Write(payload)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return invalid()
	}
	var cursor resourceCursor
	d := json.NewDecoder(bytes.NewReader(payload))
	d.DisallowUnknownFields()
	if d.Decode(&cursor) != nil || d.Decode(new(any)) != io.EOF || cursor.Version != 1 || len(cursor.Scope) != 64 || cursor.ExpiresAt.IsZero() || cursor.Name == "" || len(cursor.Name) > 253 || len(cursor.Namespace) > 253 {
		return invalid()
	}
	canonical, err := json.Marshal(cursor)
	if err != nil || !bytes.Equal(payload, canonical) {
		return invalid()
	}
	return cursor, nil
}

func paginateResources[T runtime.Object](items []T, request *resourcePageRequest) ([]T, *resourcePage, error) {
	if request == nil {
		return items, nil, nil
	}
	type row struct {
		object          T
		namespace, name string
	}
	ordered := make([]row, 0, len(items))
	for _, item := range items {
		accessor, err := meta.Accessor(item)
		if err != nil {
			return nil, nil, readErrorf(readreason.ReadError, "Resource metadata is unavailable for pagination.")
		}
		ordered = append(ordered, row{item, accessor.GetNamespace(), accessor.GetName()})
	}
	slices.SortFunc(ordered, func(a, b row) int {
		if order := strings.Compare(a.namespace, b.namespace); order != 0 {
			return order
		}
		return strings.Compare(a.name, b.name)
	})
	start := 0
	if request.cursor.Name != "" {
		for start < len(ordered) && (ordered[start].namespace < request.cursor.Namespace || ordered[start].namespace == request.cursor.Namespace && ordered[start].name <= request.cursor.Name) {
			start++
		}
	}
	end := min(start+request.limit, len(ordered))
	result := make([]T, 0, end-start)
	for _, row := range ordered[start:end] {
		result = append(result, row.object)
	}
	page := &resourcePage{Limit: request.limit, Returned: len(result), MatchingAtRead: len(items), Truncated: end < len(ordered), Consistency: "live_non_atomic"}
	if page.Truncated {
		cursor := request.cursor
		cursor.Namespace, cursor.Name = ordered[end-1].namespace, ordered[end-1].name
		page.NextCursor = request.pager.encode(cursor)
	}
	return result, page, nil
}
