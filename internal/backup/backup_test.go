package backup

import (
	"context"
	"errors"
	"strings"
	"testing"

	"hostinger-migrator/internal/store"
)

func TestReadCredentialsUnknownSource(t *testing.T) {
	for _, src := range []string{"api", ""} {
		_, err := (&Engine{}).ReadCredentials(context.Background(), nil, &store.Website{UnitPath: "/x"}, []store.DatabaseRow{{Name: "u1_db", Source: src}})
		if !errors.Is(err, ErrUnknownDBSource) || !strings.Contains(err.Error(), "u1_db") {
			t.Fatalf("sumber %q: %v", src, err)
		}
	}
}
