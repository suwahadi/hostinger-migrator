package cli

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"hostinger-migrator/internal/hostinger"
	"hostinger-migrator/internal/store"
)

func addAccounts(t *testing.T, st *store.Store, alias string, ids ...string) {
	t.Helper()
	ctx := context.Background()
	tx, err := st.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.InsertCredential(ctx, tx, alias); err != nil {
		t.Fatal(err)
	}
	var accs []store.HostingAccount
	for _, id := range ids {
		accs = append(accs, store.HostingAccount{ID: id, Alias: alias})
	}
	if err := st.ReplaceHostingAccounts(ctx, tx, alias, accs); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func addInventory(t *testing.T, st *store.Store, alias, account string, sites ...store.Website) {
	t.Helper()
	for i := range sites {
		sites[i].IsEnabled = true
		sites[i].SizeBytes, sites[i].FileCount = -1, -1
	}
	if err := st.SaveInventory(context.Background(), &store.InventorySnapshot{AccountID: account, Alias: alias, Websites: sites}); err != nil {
		t.Fatal(err)
	}
}

func noRemote(t *testing.T) func(context.Context, string) ([]hostinger.Website, error) {
	return func(_ context.Context, alias string) ([]hostinger.Website, error) {
		t.Fatalf("API tidak boleh dipanggil (alias %s)", alias)
		return nil, nil
	}
}

func TestCollectSitesMergesAliases(t *testing.T) {
	st := openTestStore(t)
	addAccounts(t, st, "a", "u1")
	addAccounts(t, st, "b", "u1")
	addInventory(t, st, "a", "u1", store.Website{Domain: "x.com", VhostType: "main"})
	a := newApp(Env{Stdout: &bytes.Buffer{}})
	a.store = st
	sites, local, api, warns, err := a.collectSites(context.Background(), noRemote(t))
	if err != nil || local != 1 || api != 0 || len(warns) != 0 {
		t.Fatalf("local=%d api=%d warns=%v err=%v", local, api, warns, err)
	}
	want := []foundSite{{Domain: "x.com", Account: "u1", Vhost: "main", Aliases: []string{"a", "b"}}}
	if !reflect.DeepEqual(sites, want) {
		t.Fatalf("sites = %+v", sites)
	}
}

func TestCollectSitesUsesAPIOnlyForUnscanned(t *testing.T) {
	st := openTestStore(t)
	addAccounts(t, st, "a", "u1", "u2")
	addInventory(t, st, "a", "u1", store.Website{Domain: "x.com", VhostType: "main"})
	calls := map[string]int{}
	remote := func(_ context.Context, alias string) ([]hostinger.Website, error) {
		calls[alias]++
		return []hostinger.Website{{Domain: "satu.com", Username: "u1", VhostType: "main"}, {Domain: "dua.com", Username: "u2", VhostType: "addon"}}, nil
	}
	a := newApp(Env{Stdout: &bytes.Buffer{}})
	a.store = st
	sites, local, api, _, err := a.collectSites(context.Background(), remote)
	if err != nil || local != 1 || api != 1 || calls["a"] != 1 || len(calls) != 1 {
		t.Fatalf("local=%d api=%d calls=%v err=%v", local, api, calls, err)
	}
	var got []string
	for _, s := range sites {
		got = append(got, s.Domain+"@"+s.Account)
	}
	if !reflect.DeepEqual(got, []string{"dua.com@u2", "x.com@u1"}) {
		t.Fatalf("sites = %v", got)
	}
}

func TestCollectSitesAPIFailureWarns(t *testing.T) {
	st := openTestStore(t)
	addAccounts(t, st, "a", "u1")
	addAccounts(t, st, "b", "u3")
	addInventory(t, st, "a", "u1", store.Website{Domain: "x.com", VhostType: "main"})
	remote := func(_ context.Context, alias string) ([]hostinger.Website, error) {
		return nil, errors.New("koneksi putus")
	}
	a := newApp(Env{Stdout: &bytes.Buffer{}})
	a.store = st
	sites, _, _, warns, err := a.collectSites(context.Background(), remote)
	if err != nil || len(sites) != 1 || len(warns) != 1 || !strings.Contains(warns[0], "Token b: daftar website tidak bisa diambil (koneksi putus)") {
		t.Fatalf("sites=%v warns=%v err=%v", sites, warns, err)
	}
}

func TestMatchFoundLabels(t *testing.T) {
	cases := map[string]string{
		"main":      "(1). x.com ada di akun: u1 - a, b",
		"addon":     "(1). x.com ada di akun: u1 - a, b",
		"subdomain": "(1). x.com ada di akun: u1 - a, b (subdomain)",
		"parked":    "(1). x.com ada di akun: u1 - a, b (parked)",
	}
	for vhost, want := range cases {
		if got := foundLine(1, foundSite{Domain: "x.com", Account: "u1", Vhost: vhost, Aliases: []string{"a", "b"}}); got != want {
			t.Errorf("%s: %q", vhost, got)
		}
	}
	sites := []foundSite{{Domain: "b.TOKO.id"}, {Domain: "a.toko.id"}, {Domain: "lain.com"}}
	var got []string
	for _, s := range matchFound(sites, "toko") {
		got = append(got, s.Domain)
	}
	if !reflect.DeepEqual(got, []string{"a.toko.id", "b.TOKO.id"}) {
		t.Fatalf("match = %v", got)
	}
}

func TestFindWebsiteMenu(t *testing.T) {
	st := openTestStore(t)
	addAccounts(t, st, "hostinger-ilhamiahnure", "u920879458")
	addInventory(t, st, "hostinger-ilhamiahnure", "u920879458", store.Website{Domain: "serene-papayawhip-goldfish-35.hostingersite.com", VhostType: "main"})
	out := &bytes.Buffer{}
	a := newApp(Env{Stdin: strings.NewReader("\nzzz\ngoldfish\n0\n"), Stdout: out, Stderr: &bytes.Buffer{}})
	a.store = st
	if err := a.findWebsiteMenu(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"CARI WEBSITE",
		"Data: 1 akun dari inventaris lokal, 0 akun dari API (1 website).",
		`Tidak ada website yang cocok dengan "zzz".`,
		"Ditemukan:",
		"(1). serene-papayawhip-goldfish-35.hostingersite.com ada di akun: u920879458 - hostinger-ilhamiahnure",
		"[0] Kembali ke menu utama",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("tidak memuat %q:\n%s", want, out.String())
		}
	}
}
