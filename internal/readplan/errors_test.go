package readplan

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/maomada/KubeAtlas/internal/k8s"
	"github.com/maomada/KubeAtlas/pkg/k8score"
	"github.com/maomada/KubeAtlas/pkg/readreason"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestCollectorErrorCode(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code readreason.Code
	}{
		{apierrors.NewUnauthorized("rejected"), readreason.KubeAtlasCredentialsRejected},
		{apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "", errors.New("denied")), readreason.KubeAtlasForbidden},
		{k8score.ErrResourceListTooLarge, readreason.ListTooLarge},
		{k8score.ErrCollectionNotSupported, readreason.KindNotWatchable},
		{k8s.ErrUnknownDynamicKind, readreason.KindUnknown},
		{k8s.ErrDynamicNotReady, readreason.KindSyncPending},
		{context.DeadlineExceeded, ""},
		{context.Canceled, ""},
		{apierrors.NewNotFound(schema.GroupResource{Resource: "pods"}, "missing"), ""},
		{errors.New("other"), ""},
		{nil, ""},
	} {
		for _, err := range []error{tc.err, fmt.Errorf("wrapped: %w", tc.err)} {
			code, ok := CollectorErrorCode(err)
			if code != tc.code || ok != (tc.code != "") {
				t.Errorf("%v = %s / %t, want %s", err, code, ok, tc.code)
			}
		}
	}
}
