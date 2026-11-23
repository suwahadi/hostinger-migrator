package cli

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"hostinger-migrator/internal/store"
)

func testListed() []listedAccount {
	site := func(d string) store.Website { return store.Website{Domain: d, SizeBytes: -1} }
	return []listedAccount{
		{Account: store.HostingAccount{ID: "u111"}, Units: []store.Website{site("toko.id"), site("domainanda.com")}},
		{Account: store.HostingAccount{ID: "u222"}, Units: []store.Website{site("tokoku.net"), site("contoh.com")}},
	}
}

func hitDomains(hits []websiteHit) []string {
	var out []string
	for _, h := range hits {
		out = append(out, h.Site.Domain)
	}
	return out
}

func TestMatchWebsites(t *testing.T) {
	listed := testListed()
	cases := map[string][]string{
		"TOKO": {"toko.id", "tokoku.net"},
		"u222": {"tokoku.net", "contoh.com"},
		"zzz":  nil,
	}
	for q, want := range cases {
		if got := hitDomains(matchWebsites(listed, q)); !reflect.DeepEqual(got, want) {
			t.Errorf("%q: %v", q, got)
		}
	}
}

func TestExactWebsites(t *testing.T) {
	listed := testListed()
	hits, missing, exact := exactWebsites(listed, "Toko.ID")
	if !exact || !reflect.DeepEqual(hitDomains(hits), []string{"toko.id"}) || len(missing) != 0 {
		t.Fatalf("Toko.ID: %v %v %v", exact, hitDomains(hits), missing)
	}
	hits, missing, exact = exactWebsites(listed, "toko.id, kedai.id")
	if !exact || !reflect.DeepEqual(hitDomains(hits), []string{"toko.id"}) || !reflect.DeepEqual(missing, []string{"kedai.id"}) {
		t.Fatalf("berkoma: %v %v %v", exact, hitDomains(hits), missing)
	}
	if _, _, exact = exactWebsites(listed, "toko"); exact {
		t.Fatal("kata kunci sebagian bukan exact")
	}
}

func TestSelectWebsitesSearchAndPick(t *testing.T) {
	out := &bytes.Buffer{}
	in := strings.Join([]string{"zzz", "toko", "9", "1", "tokoku.net", "u111", "A", ""}, "\n") + "\n"
	a := newApp(Env{Stdin: strings.NewReader(in), Stdout: out, Stderr: &bytes.Buffer{}})
	acc, domains, err := a.selectWebsites(testListed(), "")
	if err != nil {
		t.Fatal(err)
	}
	if acc != "u111" || !reflect.DeepEqual(domains, []string{"toko.id", "domainanda.com"}) {
		t.Fatalf("%s %v\n%s", acc, domains, out.String())
	}
	for _, want := range []string{
		`Tidak ada website yang cocok dengan "zzz".`,
		`Hasil "toko" (2):`,
		`Pilihan "9" tidak valid.`,
		"Satu batch hanya untuk satu akun sumber (u111)",
		"Terpilih (u111): toko.id, domainanda.com",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("tidak memuat %q\n%s", want, out.String())
		}
	}
}

func TestSelectWebsitesCapsResults(t *testing.T) {
	var sites []store.Website
	for i := 1; i <= 45; i++ {
		sites = append(sites, store.Website{Domain: fmt.Sprintf("web%02d.com", i), SizeBytes: -1})
	}
	listed := []listedAccount{{Account: store.HostingAccount{ID: "u111"}, Units: sites}}
	out := &bytes.Buffer{}
	a := newApp(Env{Stdin: strings.NewReader("web\nA\n\n"), Stdout: out, Stderr: &bytes.Buffer{}})
	_, domains, err := a.selectWebsites(listed, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Menampilkan 30 dari 45.") || len(domains) != 30 {
		t.Fatalf("%d domain\n%s", len(domains), out.String())
	}
}

func manyAccounts(n int) []accountChoice {
	var items []accountChoice
	for i := 0; i < n; i++ {
		items = append(items, accountChoice{ID: fmt.Sprintf("u%d", 100+i), Domains: []string{fmt.Sprintf("site%d.com", 100+i)}})
	}
	return items
}

func TestMatchAccounts(t *testing.T) {
	items := []accountChoice{{ID: "u111", Domains: []string{"toko.id"}}, {ID: "u222", Domains: []string{"kedai.id"}}}
	if got := matchAccounts(items, "TOKO"); len(got) != 1 || got[0].ID != "u111" {
		t.Errorf("TOKO: %v", got)
	}
	if got := matchAccounts(items, "u22"); len(got) != 1 || got[0].ID != "u222" {
		t.Errorf("u22: %v", got)
	}
}

func TestChooseAccountSmallListByNumberAndId(t *testing.T) {
	items := []accountChoice{{ID: "u111"}, {ID: "u222"}, {ID: "u333"}}
	describe := func(id string) string { return "(Premium)" }
	for in, want := range map[string]string{"2\n": "u222", "u333\n": "u333"} {
		out := &bytes.Buffer{}
		a := newApp(Env{Stdin: strings.NewReader(in), Stdout: out, Stderr: &bytes.Buffer{}})
		got, err := a.chooseAccountFrom(items, "Pilih akun: ", false, describe)
		if err != nil || got != want {
			t.Fatalf("%q: %q %v", in, got, err)
		}
		for _, row := range []string{" 1. u111  (Premium)", " 2. u222  (Premium)", " 3. u333  (Premium)"} {
			if !strings.Contains(out.String(), row) {
				t.Errorf("tidak memuat %q\n%s", row, out.String())
			}
		}
	}
}

func TestChooseAccountDescribesOnlyShown(t *testing.T) {
	calls := 0
	describe := func(id string) string { calls++; return "(Business)" }
	out := &bytes.Buffer{}
	a := newApp(Env{Stdin: strings.NewReader("site107\n1\n"), Stdout: out, Stderr: &bytes.Buffer{}})
	got, err := a.chooseAccountFrom(manyAccounts(25), "Pilih: ", false, describe)
	if err != nil || got != "u107" {
		t.Fatalf("%q %v\n%s", got, err, out.String())
	}
	if calls != 1 || strings.Contains(out.String(), "u100") {
		t.Fatalf("describe %d kali\n%s", calls, out.String())
	}
}

func TestChooseAccountEmptyMeansAll(t *testing.T) {
	a := newApp(Env{Stdin: strings.NewReader("\n"), Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}})
	got, err := a.chooseAccountFrom(manyAccounts(25), "ENTER = semua: ", true, func(string) string { return "" })
	if err != nil || got != "" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestChooseAccountNumberWithoutListIsKeyword(t *testing.T) {
	out := &bytes.Buffer{}
	a := newApp(Env{Stdin: strings.NewReader("5\n\n"), Stdout: out, Stderr: &bytes.Buffer{}})
	got, err := a.chooseAccountFrom(manyAccounts(25), "Pilih: ", true, func(string) string { return "" })
	if err != nil || got != "" {
		t.Fatalf("%q %v", got, err)
	}
	if !strings.Contains(out.String(), `Hasil "5" (2):`) {
		t.Fatal(out.String())
	}
}
