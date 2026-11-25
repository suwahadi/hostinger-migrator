package cleanup

import (
	"reflect"
	"testing"

	"hostinger-migrator/internal/store"
)

func TestDeletionTargetsExcludeSkipped(t *testing.T) {
	got := deletionTargets([]store.DatabaseRow{{Name: "u1_ok"}, {Name: "u1_x"}}, []string{"u1_x"})
	if !reflect.DeepEqual(got, map[string]bool{"u1_ok": true}) {
		t.Fatalf("target hapus = %v", got)
	}
}
