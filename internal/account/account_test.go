package account

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"hostinger-migrator/internal/hostinger"
	"hostinger-migrator/internal/logx"
	"hostinger-migrator/internal/secrets"
	"hostinger-migrator/internal/store"
	"hostinger-migrator/tests/fakehostinger"
)

const (
	goodToken = "FAKE-good-token-1234567890abcdef"
	badToken  = "FAKE-bad-token-0987654321fedcba"
)

type fixture struct {
	m       *Manager
	backend *secrets.MemoryBackend
	srv     *fakehostinger.Server
	st      *store.Store
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	srv := fakehostinger.New(false, goodToken)
	t.Cleanup(srv.Close)
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "migrator.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	r := logx.NewRedactor()
	backend := secrets.NewMemoryBackend()
	m := &Manager{
		Store:    st,
		Secrets:  secrets.New(backend, r),
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Redactor: r,
		NewClient: func(alias, token string) (*hostinger.Client, error) {
			return hostinger.NewClient(hostinger.Options{Token: token, BaseURL: srv.URL, RetryMax: 0, Limiter: hostinger.NewLimiter(60000)})
		},
	}
	return &fixture{m: m, backend: backend, srv: srv, st: st}
}

func TestAddValidTokenDiscoversAccounts(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	res, err := f.m.Add(ctx, "xyz", goodToken)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Discovery.Accounts) != 2 || res.Discovery.Accounts[0].Username != "u494983133" || res.Discovery.Accounts[0].WebsiteCount != 2 {
		t.Fatalf("discovery = %+v", res.Discovery)
	}
	if tok, _ := f.m.Secrets.Token("xyz"); tok != goodToken {
		t.Fatal("token harus tersimpan di Credential Manager")
	}
	accs, _ := f.st.ListHostingAccounts(ctx, "xyz")
	if len(accs) != 2 || accs[1].ID != "u597522494" || accs[1].Plan != "hostinger_single" {
		t.Fatalf("akun = %+v", accs)
	}
	c, _ := f.st.GetCredential(ctx, "xyz")
	if c.LastStatus != store.CredentialActive {
		t.Fatalf("status = %s", c.LastStatus)
	}
}

func TestAddInvalidTokenStoresNothing(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, err := f.m.Add(ctx, "salah", badToken)
	if !errors.Is(err, ErrTokenRejected) {
		t.Fatalf("err = %v", err)
	}
	if f.backend.Len() != 0 {
		t.Fatal("token ditolak tidak boleh disimpan")
	}
	if ok, _ := f.st.CredentialExists(ctx, "salah"); ok {
		t.Fatal("alias tidak boleh tersimpan")
	}
}

func TestAddRejectsDuplicateAndInvalidAlias(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.m.Add(ctx, "xyz", goodToken); err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.Add(ctx, "xyz", goodToken); !errors.Is(err, ErrAliasExists) {
		t.Fatalf("err = %v", err)
	}
	if _, err := f.m.Add(ctx, "Alias Spasi", goodToken); err == nil {
		t.Fatal("alias tidak valid harus ditolak")
	}
	if _, err := f.m.Add(ctx, "pendek", "abc"); err == nil {
		t.Fatal("token terlalu pendek harus ditolak")
	}
}

func TestAddRollsBackKeyringWhenKeyringOnlyHasAlias(t *testing.T) {
	f := newFixture(t)
	f.m.Secrets.SetToken("yatim", goodToken)
	if _, err := f.m.Add(context.Background(), "yatim", goodToken); !errors.Is(err, ErrAliasExists) {
		t.Fatalf("alias yang sudah ada di Credential Manager harus ditolak: %v", err)
	}
}

func TestTestDetectsRevokedToken(t *testing.T) {
	f := newFixture(t)
	f.srv.EchoTokenInError = true
	ctx := context.Background()
	if _, err := f.m.Add(ctx, "xyz", goodToken); err != nil {
		t.Fatal(err)
	}
	f.srv.Revoke(goodToken)
	res, err := f.m.Test(ctx, "xyz")
	if !errors.Is(err, ErrTokenRejected) || res.Status != store.CredentialRejected {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	c, _ := f.st.GetCredential(ctx, "xyz")
	if c.LastStatus != store.CredentialRejected {
		t.Fatalf("status = %s", c.LastStatus)
	}
	if strings.Contains(c.LastError, goodToken) {
		t.Fatal("token bocor ke last_error")
	}
	entries, _ := f.st.ListAudit(ctx, 10)
	for _, e := range entries {
		if strings.Contains(e.Detail, goodToken) {
			t.Fatal("token bocor ke audit_log")
		}
	}
}

func TestTestUnknownAliasAndMissingToken(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.m.Test(ctx, "tidakada"); !errors.Is(err, ErrAliasNotFound) {
		t.Fatalf("err = %v", err)
	}
	if _, err := f.m.Add(ctx, "xyz", goodToken); err != nil {
		t.Fatal(err)
	}
	f.backend.Delete(secrets.Service, "xyz")
	if _, err := f.m.Test(ctx, "xyz"); !errors.Is(err, ErrTokenMissing) {
		t.Fatalf("err = %v", err)
	}
}

func TestRemoveOnlyLocal(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.m.Add(ctx, "xyz", goodToken); err != nil {
		t.Fatal(err)
	}
	before := f.srv.Hits("/api/hosting/v1/orders")
	if err := f.m.Remove(ctx, "xyz"); err != nil {
		t.Fatal(err)
	}
	if f.srv.Hits("/api/hosting/v1/orders") != before {
		t.Fatal("remove tidak boleh memanggil Hostinger")
	}
	if f.backend.Len() != 0 {
		t.Fatal("token harus terhapus dari Credential Manager")
	}
	if ok, _ := f.st.CredentialExists(ctx, "xyz"); ok {
		t.Fatal("alias harus terhapus dari database")
	}
	if err := f.m.Remove(ctx, "xyz"); !errors.Is(err, ErrAliasNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestListShowsAccounts(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.m.Add(ctx, "xyz", goodToken)
	items, err := f.m.List(ctx)
	if err != nil || len(items) != 1 || len(items[0].Accounts) != 2 {
		t.Fatalf("items = %+v err = %v", items, err)
	}
}
