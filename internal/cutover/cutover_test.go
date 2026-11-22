package cutover_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hostinger-migrator/internal/cutover"
	"hostinger-migrator/internal/gate"
	"hostinger-migrator/internal/pipeline"
	"hostinger-migrator/internal/store"
	"hostinger-migrator/tests/testenv"
)

func setup(t *testing.T, domains ...string) (*testenv.Env, *pipeline.Runner, string) {
	t.Helper()
	e := testenv.New(t)
	batch := e.Prepare(domains...)
	var r *pipeline.Runner
	st := &cutover.Stage{FinalSync: true, SSLWait: time.Hour, HTTPSScheme: "http"}
	r = pipeline.NewRunner(e.Deps(st))
	st.Runner = r
	e.RunUntilIdle(r, 30)
	return e, r, batch
}

func zoneRR(e *testenv.Env, domain, name, typ string) []string {
	var out []string
	for _, rr := range e.API.Zone(domain) {
		if rr["name"] == name && rr["type"] == typ {
			b, _ := json.Marshal(rr["records"])
			var recs []map[string]any
			json.Unmarshal(b, &recs)
			for _, r := range recs {
				out = append(out, r["content"].(string))
			}
		}
	}
	return out
}

func renameSource(e *testenv.Env, from, to string) {
	e.API.Lock()
	defer e.API.Unlock()
	for i, w := range e.API.WebsitesFor[testenv.SrcToken] {
		if w.Domain == from {
			e.API.WebsitesFor[testenv.SrcToken][i].Domain = to
		}
	}
}

func TestCutoverFullAndRollback(t *testing.T) {
	e, r, batch := setup(t, "domainanda.com", "mati.com")
	ctx := context.Background()
	it := e.Item("domainanda.com")
	if it.State != pipeline.StateVerified {
		t.Fatalf("awal = %s %s", it.State, it.LastError)
	}
	e.RunUntilIdle(r, 5)
	if it = e.Item("domainanda.com"); it.State != pipeline.StateVerified {
		t.Fatal("tanpa persetujuan tidak boleh cutover")
	}
	e.API.Lock()
	e.API.Zones["domainanda.com"] = append(e.API.Zones["domainanda.com"], map[string]any{"name": "@", "type": "AAAA", "ttl": 3600, "records": []map[string]any{{"content": "2001:db8::1"}}})
	e.API.Unlock()

	if err := gate.Request(ctx, e.Store, batch, gate.KindCutover, []string{"domainanda.com"}, "CUTOVER"); err != nil {
		t.Fatal(err)
	}
	e.RunUntilIdle(r, 30)
	it = e.Item("domainanda.com")
	if it.ManualCode != "MANUAL_SOURCE_RENAME" || !it.FinalSyncDone {
		t.Fatalf("harus menunggu ganti domain sumber setelah backup final: %+v", it)
	}
	if _, err := os.Stat(filepath.Join(e.DataDir, "backups", batch, "domainanda.com", "final", "files.tar.gz")); err != nil {
		t.Fatal("backup final harus ada")
	}
	snaps, _ := e.Store.ListDNSSnapshots(ctx, "domainanda.com")
	if len(snaps) != 1 || !strings.Contains(snaps[0].RecordsJSON, testenv.SourceIP) {
		t.Fatalf("snapshot DNS = %+v", snaps)
	}
	if got := zoneRR(e, "domainanda.com", "@", "A"); len(got) != 1 || got[0] != testenv.SourceIP {
		t.Fatalf("DNS tidak boleh berubah sebelum domain sumber dilepas: %v", got)
	}
	if _, err := pipeline.Confirm(ctx, e.Store, "MANUAL_SOURCE_RENAME", "domainanda.com", "", "lama-1.hostingersite.com"); err != nil {
		t.Fatal(err)
	}
	e.RunUntilIdle(r, 10)
	it = e.Item("domainanda.com")
	if it.ManualCode != "MANUAL_SOURCE_RENAME" || !strings.Contains(e.Count2(it.ID), "Verifikasi gagal") {
		t.Fatalf("domain sumber belum diganti, harus kembali menunggu: %+v", it)
	}
	renameSource(e, "domainanda.com", "lama-1.hostingersite.com")
	pipeline.Confirm(ctx, e.Store, "MANUAL_SOURCE_RENAME", "domainanda.com", "", "lama-1.hostingersite.com")
	e.RunUntilIdle(r, 30)
	it = e.Item("domainanda.com")
	if it.State != pipeline.StateCleanupReady {
		t.Fatalf("setelah cutover = %s/%s %s", it.State, it.SideState, it.LastError)
	}
	if got := zoneRR(e, "domainanda.com", "@", "A"); len(got) != 1 || got[0] != testenv.DestIP {
		t.Fatalf("A @ = %v", got)
	}
	if got := zoneRR(e, "domainanda.com", "@", "AAAA"); len(got) != 0 {
		t.Fatalf("AAAA lama harus dihapus: %v", got)
	}
	for _, keep := range []struct{ name, typ, want string }{
		{"@", "MX", "5 mx1.hostinger.com."},
		{"@", "TXT", "\"google-site-verification=abc\""},
		{"shop", "CNAME", "shops.myshopify.com."},
		{"www", "CNAME", "domainanda.com."},
	} {
		got := zoneRR(e, "domainanda.com", keep.name, keep.typ)
		if len(got) == 0 || got[0] != keep.want {
			t.Errorf("record %s %s pihak ketiga harus dipertahankan: %v", keep.typ, keep.name, got)
		}
	}
	siteurl, _ := os.ReadFile(filepath.Join(it.DestRootPath, "public_html", ".wp-siteurl"))
	if string(siteurl) != "https://domainanda.com" || it.SearchReplaceDone {
		t.Fatalf("URL WordPress harus dikembalikan: %s", siteurl)
	}
	vs, _ := e.Store.ListVerifications(ctx, it.ID)
	got := map[string]string{}
	for _, v := range vs {
		got[v.CheckType] = v.Result
	}
	if got["SSL_STATUS"] != "PASS" || got["AKHIR_HTTPS"] != "PASS" || got["AKHIR_HASIL"] == "FAIL" || got["FINAL_VERIFIKASI_HASIL"] == "FAIL" {
		t.Fatalf("verifikasi akhir = %v", got)
	}
	entries, _ := e.Store.ListAudit(ctx, 200)
	dnsAudit := false
	for _, en := range entries {
		if en.Action == "dns.update" && strings.Contains(en.Detail, "persetujuan CUTOVER") && strings.Contains(en.Detail, "A @ → "+testenv.DestIP) {
			dnsAudit = true
		}
	}
	if !dnsAudit {
		t.Fatal("perubahan DNS harus tercatat di audit_log dengan persetujuan")
	}
	if dead := e.Item("mati.com"); dead.State != pipeline.StateDummyOnly {
		t.Fatalf("DUMMY_ONLY tidak boleh di-cutover: %s", dead.State)
	}

	res, err := cutover.Rollback(ctx, r, it)
	if err != nil {
		t.Fatal(err)
	}
	if !res.ParkedRemoved || len(res.DNSRestored) == 0 || len(res.Manual) != 1 || !strings.Contains(res.Manual[0], "lama-1.hostingersite.com") {
		t.Fatalf("rollback = %+v", res)
	}
	if got := zoneRR(e, "domainanda.com", "@", "A"); len(got) != 1 || got[0] != testenv.SourceIP {
		t.Fatalf("A @ setelah rollback = %v", got)
	}
	if got := zoneRR(e, "domainanda.com", "@", "AAAA"); len(got) != 1 {
		t.Fatalf("AAAA harus kembali: %v", got)
	}
	if got := zoneRR(e, "domainanda.com", "@", "MX"); len(got) != 2 {
		t.Fatalf("MX harus utuh: %v", got)
	}
	e.RunUntilIdle(r, 5)
	if it = e.Item("domainanda.com"); it.State != pipeline.StateVerified {
		t.Fatalf("setelah rollback harus VERIFIED dan tidak cutover otomatis: %s", it.State)
	}
	if _, err := cutover.Rollback(ctx, r, it); err == nil {
		t.Fatal("rollback item VERIFIED harus ditolak")
	}
}

func TestCutoverExternalNSAndSSLPending(t *testing.T) {
	e := testenv.New(t)
	batch := e.Prepare("toko.id")
	ctx := context.Background()
	e.Store.DB().Exec(`UPDATE websites SET ns_external = 1, nameservers = '["ana.ns.cloudflare.com"]' WHERE domain = 'toko.id'`)
	e.API.SSLPolls = 1 << 20
	st := &cutover.Stage{FinalSync: false, SSLWait: time.Nanosecond, HTTPSScheme: "http"}
	r := pipeline.NewRunner(e.Deps(st))
	st.Runner = r
	e.RunUntilIdle(r, 30)
	gate.Request(ctx, e.Store, batch, gate.KindCutover, []string{"toko.id"}, "CUTOVER")
	renameSource(e, "toko.id", "lama-2.hostingersite.com")
	e.RunUntilIdle(r, 10)
	pipeline.Confirm(ctx, e.Store, "MANUAL_SOURCE_RENAME", "toko.id", "", "lama-2.hostingersite.com")
	e.RunUntilIdle(r, 10)
	it := e.Item("toko.id")
	if it.ManualCode != "MANUAL_DNS" {
		t.Fatalf("NS eksternal harus checkpoint manual: %+v", it)
	}
	task, _ := e.Store.GetManualTask(ctx, it.ID, "MANUAL_DNS")
	if !strings.Contains(task.Instructions, "A  @    → "+testenv.DestIP) || !strings.Contains(task.Instructions, "ana.ns.cloudflare.com") {
		t.Fatalf("instruksi DNS = %s", task.Instructions)
	}
	if got := zoneRR(e, "toko.id", "@", "A"); got[0] != testenv.SourceIP {
		t.Fatal("zone tidak boleh diubah untuk NS eksternal")
	}
	pipeline.Confirm(ctx, e.Store, "MANUAL_DNS", "toko.id", "", "")
	e.RunUntilIdle(r, 10)
	it = e.Item("toko.id")
	if it.State != pipeline.StateCleanupReady {
		t.Fatalf("status = %s/%s %s", it.State, it.SideState, it.LastError)
	}
	vs, _ := e.Store.ListVerifications(ctx, it.ID)
	pending := false
	for _, v := range vs {
		if v.CheckType == "SSL_STATUS" && v.Result == "WARN" {
			pending = true
		}
	}
	steps, _ := e.Store.ListSteps(ctx, it.ID)
	_ = steps
	if !pending {
		t.Fatalf("SSL harus tercatat belum aktif: %+v", vs)
	}
	var sslState string
	e.Store.DB().QueryRow(`SELECT action FROM audit_log WHERE target = 'toko.id' AND detail LIKE '%SSL_PENDING%' LIMIT 1`).Scan(&sslState)
	if sslState == "" {
		t.Fatal("item harus melewati SSL_PENDING")
	}
	_ = store.SideBlocked
}
