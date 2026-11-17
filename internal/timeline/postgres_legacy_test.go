package timeline

import (
	"database/sql"
	"github.com/maomada/KubeAtlas/pkg/compat"
	"testing"
)

func TestPostgresBrandingReopensExistingPhysicalHistory(t *testing.T) {
	dsn := testPostgresDSN(t)
	store, err := NewPostgresStore(dsn)
	if err != nil {
		t.Fatal(err)
	}
	store.MarkResourceSeen("fixture", "", "Pod", "fixture", "retained")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewPostgresStore(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if !reopened.IsResourceSeen("fixture", "", "Pod", "fixture", "retained") {
		t.Fatal("retained seen-resource history disappeared")
	}
	var old, new sql.NullString
	if err := reopened.db.QueryRowContext(t.Context(), "SELECT to_regclass($1)::text,to_regclass($2)::text", compat.PhysicalTimelineSQL("kubeatlas_timeline_events"), "kubeatlas_timeline_events").Scan(&old, &new); err != nil {
		t.Fatal(err)
	}
	if !old.Valid || new.Valid {
		t.Fatal("branding created a parallel empty timeline schema")
	}
}
