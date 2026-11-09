package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/maomada/KubeAtlas/internal/k8s"
	pkgauth "github.com/maomada/KubeAtlas/pkg/auth"
	"github.com/maomada/KubeAtlas/pkg/readreason"
	"github.com/maomada/KubeAtlas/pkg/resourceapi"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func paginationList(t *testing.T, ctx context.Context, input listResourcesInput) (listEnvelope, string) {
	t.Helper()
	res, _, err := handleListResources(ctx, nil, input)
	if err != nil {
		t.Fatal(err)
	}
	text := extractText(t, res)
	var body listEnvelope
	if err := json.Unmarshal([]byte(text), &body); err != nil {
		t.Fatal(err)
	}
	return body, text
}

func paginationCode(t *testing.T, err error, code readreason.Code) {
	t.Helper()
	var re *readError
	if !errors.As(err, &re) || re.code != code {
		t.Fatalf("got %v; want %s", err, code)
	}
	res, ok := readErrorResult(err)
	if !ok || !res.IsError {
		t.Fatal("pagination error not rendered as structured tool error")
	}
	var body mcpReadError
	if err := json.Unmarshal([]byte(extractText(t, res)), &body); err != nil {
		t.Fatal(err)
	}
	if body.ErrorCode != code || body.Next == "" {
		t.Fatal("pagination error missing code or actionable next step")
	}
}

func paginationKeys(t *testing.T, items []any) []string {
	t.Helper()
	keys := []string{}
	for _, item := range items {
		row := item.(map[string]any)
		if metadata, ok := row["metadata"].(map[string]any); ok {
			row = metadata
		}
		keys = append(keys, row["namespace"].(string)+"/"+row["name"].(string))
	}
	return keys
}

func TestResourcePaginationTypedAndUnpaged(t *testing.T) {
	setupFakeCacheForFilterTests(t)
	ctx := context.Background()
	input := listResourcesInput{Kind: "pods", Context: "none"}
	unpaged, text := paginationList(t, ctx, input)
	if unpaged.Page != nil || len(unpaged.Items) != 3 {
		t.Fatal("omitted pagination truncated or changed shape")
	}
	legacy, err := json.Marshal(struct {
		Summary  string               `json:"summary"`
		Next     string               `json:"next,omitempty"`
		Coverage resourceapi.Coverage `json:"coverage"`
		Items    []any                `json:"items"`
	}{listSummary(ctx, "pods", len(unpaged.Items), onlyRowKind(unpaged.Items), unpaged.Coverage), listNext(unpaged.Coverage), unpaged.Coverage, unpaged.Items})
	if err != nil || string(legacy) != text {
		t.Fatal("unpaged envelope differs from legacy rendering")
	}
	input.Limit = 1
	var keys []string
	for i := 0; i < 3; i++ {
		body, _ := paginationList(t, ctx, input)
		if body.Page == nil || body.Page.Limit != 1 || body.Page.Returned != 1 || body.Page.MatchingAtRead != 3 || body.Page.Consistency != "live_non_atomic" || !strings.Contains(body.Summary, "PAGE") || !strings.Contains(body.Summary, "not a complete inventory") {
			t.Fatalf("bad page: %+v", body.Page)
		}
		if !reflect.DeepEqual(unpaged.Coverage, body.Coverage) {
			t.Fatal("pagination changed collection coverage")
		}
		keys = append(keys, paginationKeys(t, body.Items)...)
		if body.Page.Truncated != (i < 2) || (body.Page.NextCursor != "") != (i < 2) {
			t.Fatal("incorrect final-page continuation")
		}
		input.Cursor = body.Page.NextCursor
	}
	if !slices.Equal(keys, []string{"alpha/alpha-pod", "beta/beta-pod", "gamma/gamma-pod"}) {
		t.Fatalf("pages repeat or skip rows: %v", keys)
	}
	for _, invalid := range []listResourcesInput{{Kind: "pods", Limit: -1}, {Kind: "pods", Limit: 201}, {Kind: "pods", Cursor: "opaque"}} {
		_, _, err := handleListResources(ctx, nil, invalid)
		paginationCode(t, err, readreason.InvalidParameters)
	}
}

func TestResourcePaginationDynamicAuthorization(t *testing.T) {
	for _, class := range []coverageClass{coverageCRD, coverageDirect} {
		t.Run(string(class), func(t *testing.T) {
			kind := setupListCoverageFixture(t, class, false, "gamma", "beta", "alpha")
			ctx := withRestrictedUser(t, "pagination-reader", []string{"alpha", "gamma"})
			t.Cleanup(func() { getPermCache().Invalidate() })
			input := listResourcesInput{Kind: kind, Context: "none", Limit: 1}
			first, text := paginationList(t, ctx, input)
			if first.Page == nil || first.Page.Returned != 1 || first.Page.MatchingAtRead != 2 || !first.Page.Truncated || strings.Contains(text, "beta") || !slices.Equal(paginationKeys(t, first.Items), []string{"alpha/alpha-object"}) {
				t.Fatal("dynamic first page leaked or failed to bound authorized rows")
			}
			input.Cursor = first.Page.NextCursor
			last, text := paginationList(t, ctx, input)
			if last.Page.Truncated || last.Page.NextCursor != "" || last.Page.Returned != 1 || strings.Contains(text, "beta") || !slices.Equal(paginationKeys(t, last.Items), []string{"gamma/gamma-object"}) {
				t.Fatal("dynamic final page incorrect")
			}
			ctx = withRestrictedUser(t, "pagination-reader", []string{"alpha"})
			narrowed, text := paginationList(t, ctx, input)
			if len(narrowed.Items) != 0 || narrowed.Page.MatchingAtRead != 1 || narrowed.Page.Truncated || strings.Contains(text, "gamma-object") {
				t.Fatal("cursor bypassed current authorization")
			}
		})
	}
}

func TestResourcePaginationOwnedOrderingAndLiveChanges(t *testing.T) {
	pager := newResourcePager()
	input := listResourcesInput{Kind: "pods", Limit: 2}
	request, err := pager.prepare(context.Background(), input, 1)
	if err != nil {
		t.Fatal(err)
	}
	objects := []*corev1.Pod{
		{ObjectMeta: metav1.ObjectMeta{Namespace: "beta", Name: "a"}},
		{ObjectMeta: metav1.ObjectMeta{Namespace: "alpha", Name: "z"}},
		{ObjectMeta: metav1.ObjectMeta{Namespace: "alpha", Name: "a"}},
		{ObjectMeta: metav1.ObjectMeta{Namespace: "gamma", Name: "a"}},
	}
	original := slices.Clone(objects)
	first, page, err := paginateResources(objects, request)
	if err != nil || len(first) != 2 || first[0] != objects[2] || first[1] != objects[1] || !page.Truncated || !slices.Equal(objects, original) {
		t.Fatal("page order incorrect or source slice mutated")
	}
	input.Cursor = page.NextCursor
	request, err = pager.prepare(context.Background(), input, 1)
	if err != nil {
		t.Fatal(err)
	}
	last, page, err := paginateResources(objects, request)
	if err != nil || len(last) != 2 || last[0] != objects[0] || last[1] != objects[3] || page.Truncated {
		t.Fatal("resume was not strictly namespace/name ordered")
	}
	remaining := []*corev1.Pod{objects[2], objects[1]}
	last, page, err = paginateResources(remaining, request)
	if err != nil || len(last) != 0 || page.MatchingAtRead != 2 || page.Truncated {
		t.Fatal("live deletion did not produce empty terminal page")
	}
	unchanged, page, err := paginateResources(objects, nil)
	if err != nil || page != nil || &unchanged[0] != &objects[0] {
		t.Fatal("unpaged helper altered its input")
	}
}

func TestResourcePaginationCursorBindingAndExpiry(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	pager := newResourcePager()
	pager.now = func() time.Time { return now }
	user := &pkgauth.User{Username: "reader", Groups: []string{"z", "a"}, UID: "uid", System: true, HubGroups: []string{"view", "admin"}, Extra: map[string][]string{"key": {"b", "a"}}, Token: "test-only-bearer"}
	ctx := pkgauth.ContextWithUser(context.Background(), user)
	input := listResourcesInput{Kind: "pods", Limit: 1, Context: "none"}
	request, err := pager.prepare(ctx, input, 7)
	if err != nil {
		t.Fatal(err)
	}
	objects := []*corev1.Pod{{ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "alpha"}}, {ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: "alpha"}}, {ObjectMeta: metav1.ObjectMeta{Name: "c", Namespace: "alpha"}}}
	_, page, err := paginateResources(objects, request)
	if err != nil {
		t.Fatal(err)
	}
	input.Cursor = page.NextCursor
	parts := strings.Split(input.Cursor, ".")
	payload, _ := base64.RawURLEncoding.DecodeString(parts[0])
	if strings.Contains(string(payload), user.Token) || strings.Contains(string(payload), user.Username) {
		t.Fatal("cursor exposes principal identity or bearer")
	}
	if !slices.Equal(user.Groups, []string{"z", "a"}) || !slices.Equal(user.Extra["key"], []string{"b", "a"}) {
		t.Fatal("fingerprinting mutated identity")
	}
	canonical := *user
	canonical.Groups = []string{"a", "z"}
	canonical.HubGroups = []string{"admin", "view"}
	canonical.Extra = map[string][]string{"key": {"a", "b"}}
	if _, err = pager.prepare(pkgauth.ContextWithUser(context.Background(), &canonical), input, 7); err != nil {
		t.Fatal("canonical group order invalidated cursor")
	}
	for _, mutate := range []func(*listResourcesInput){
		func(i *listResourcesInput) { i.Kind = "services" }, func(i *listResourcesInput) { i.Group = "apps" }, func(i *listResourcesInput) { i.Namespace = "alpha" }, func(i *listResourcesInput) { i.Context = "basic" }, func(i *listResourcesInput) { i.Limit = 2 },
	} {
		changed := input
		mutate(&changed)
		_, err = pager.prepare(ctx, changed, 7)
		paginationCode(t, err, readreason.InvalidCursor)
	}
	for _, mutate := range []func(*pkgauth.User){
		func(u *pkgauth.User) { u.Username = "other" }, func(u *pkgauth.User) { u.Groups = []string{"other"} }, func(u *pkgauth.User) { u.UID = "other" }, func(u *pkgauth.User) { u.System = false }, func(u *pkgauth.User) { u.HubGroups = []string{"other"} }, func(u *pkgauth.User) { u.Extra = map[string][]string{"key": {"other"}} }, func(u *pkgauth.User) { u.Token = "other" },
	} {
		changed := *user
		mutate(&changed)
		_, err = pager.prepare(pkgauth.ContextWithUser(context.Background(), &changed), input, 7)
		paginationCode(t, err, readreason.InvalidCursor)
	}
	_, err = pager.prepare(ctx, input, 8)
	paginationCode(t, err, readreason.ConnectionChanged)
	_, err = newResourcePager().prepare(ctx, input, 7)
	paginationCode(t, err, readreason.InvalidCursor)
	for _, token := range []string{"!", "A" + input.Cursor[1:], input.Cursor + ".", strings.Repeat("x", resourceCursorMaxBytes+1)} {
		changed := input
		changed.Cursor = token
		_, err = pager.prepare(ctx, changed, 7)
		paginationCode(t, err, readreason.InvalidCursor)
		if strings.Contains(err.Error(), token) {
			t.Fatal("error echoed cursor")
		}
	}
	now = now.Add(5 * time.Minute)
	request, err = pager.prepare(ctx, input, 7)
	if err != nil {
		t.Fatal(err)
	}
	_, page, err = paginateResources(objects, request)
	if err != nil {
		t.Fatal(err)
	}
	input.Cursor = page.NextCursor
	now = now.Add(5 * time.Minute)
	_, err = pager.prepare(ctx, input, 7)
	paginationCode(t, err, readreason.CursorExpired)
}

func TestResourcePaginationCacheGeneration(t *testing.T) {
	setupFakeCacheForFilterTests(t)
	input := listResourcesInput{Kind: "pods", Limit: 1, Context: "none"}
	first, _ := paginationList(t, context.Background(), input)
	input.Cursor = first.Page.NextCursor
	old := k8s.GetResourceCache().Generation()
	k8s.ResetTestState()
	setupFakeCacheForFilterTests(t)
	if k8s.GetResourceCache().Generation() == old {
		t.Fatal("test cache generation did not change")
	}
	_, _, err := handleListResources(context.Background(), nil, input)
	paginationCode(t, err, readreason.ConnectionChanged)
}
