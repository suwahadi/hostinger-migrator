package email_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hostinger-migrator/internal/cutover"
	"hostinger-migrator/internal/email"
	"hostinger-migrator/internal/gate"
	"hostinger-migrator/internal/imapx"
	"hostinger-migrator/internal/pipeline"
	"hostinger-migrator/internal/secrets"
	"hostinger-migrator/tests/testenv"
)

func login(t *testing.T, e *testenv.Env, addr, pw string) error {
	t.Helper()
	c, err := imapx.Dial(context.Background(), e.IMAP.Addr(), false, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	return c.Login(addr, pw)
}

func TestEmailMigrationFull(t *testing.T) {
	e := testenv.New(t)
	e.SetupMail()
	batch := e.Prepare("domainanda.com")
	ctx := context.Background()
	cut := &cutover.Stage{SSLWait: time.Hour, HTTPSScheme: "http"}
	mail := &email.Stage{IMAP: e.IMAP.Addr(), TLS: false, Timeout: 5 * time.Second}
	r := pipeline.NewRunner(e.Deps(cut, mail))
	cut.Runner, mail.Runner = r, r
	e.RunUntilIdle(r, 30)
	gate.Request(ctx, e.Store, batch, gate.KindCutover, []string{"domainanda.com"}, "CUTOVER")
	e.API.Lock()
	for i, w := range e.API.WebsitesFor[testenv.SrcToken] {
		if w.Domain == "domainanda.com" {
			e.API.WebsitesFor[testenv.SrcToken][i].Domain = "sementara.hostingersite.com"
		}
	}
	e.API.Unlock()
	e.RunUntilIdle(r, 10)
	pipeline.Confirm(ctx, e.Store, "MANUAL_SOURCE_RENAME", "domainanda.com", "", "sementara.hostingersite.com")
	e.RunUntilIdle(r, 30)
	it := e.Item("domainanda.com")
	if it.State != pipeline.StateEmailRunning || it.EmailState != "" {
		t.Fatalf("harus menunggu persetujuan email: %s %q %s", it.State, it.EmailState, it.LastError)
	}
	if login(t, e, "info@domainanda.com", testenv.MailOldInfo) != nil {
		t.Fatal("tanpa persetujuan, password lama tidak boleh berubah")
	}

	if err := gate.Request(ctx, e.Store, batch, gate.KindDeleteEmail, []string{"domainanda.com"}, "HAPUS EMAIL"); err != nil {
		t.Fatal(err)
	}
	e.RunUntilIdle(r, 10)
	it = e.Item("domainanda.com")
	if it.ManualCode != "MANUAL_EMAIL_SOURCE_REMOVE" || it.EmailState != email.StepSourceRemove {
		t.Fatalf("setelah backup = %+v", it)
	}
	newInfo, _ := e.Secrets.Get(secrets.KindMailboxPassword, "info@domainanda.com")
	if newInfo == "" || newInfo == testenv.MailOldInfo {
		t.Fatal("password baru harus dibuat")
	}
	if len(e.API.MailboxesOf("mo-src")) != 0 || len(e.IMAP.Folders("info@domainanda.com")) != 0 {
		t.Fatal("mailbox sumber harus dihapus setelah backup")
	}
	man, err := email.Manifests(filepath.Join(e.DataDir, "backups"), it)
	if err != nil || len(man) != 2 {
		t.Fatalf("manifest = %+v %v", man, err)
	}
	totals := map[string]int{}
	for _, m := range man {
		totals[m.Address] = m.Total()
	}
	if totals["info@domainanda.com"] != 3 || totals["admin@domainanda.com"] != 2 {
		t.Fatalf("jumlah pesan backup = %v", totals)
	}
	eml, _ := filepath.Glob(filepath.Join(e.DataDir, "backups", batch, "domainanda.com", "email", "*", "INBOX", "*.eml"))
	foundBig := false
	for _, f := range eml {
		data, _ := os.ReadFile(f)
		if bytes.Contains(data, testenv.BigAttachment) {
			foundBig = true
		}
	}
	if !foundBig {
		t.Fatal("lampiran besar harus tersimpan utuh di .eml")
	}

	pipeline.Confirm(ctx, e.Store, "MANUAL_EMAIL_SOURCE_REMOVE", "domainanda.com", "", "")
	e.RunUntilIdle(r, 10)
	if it = e.Item("domainanda.com"); it.ManualCode != "MANUAL_EMAIL_SETUP" {
		t.Fatalf("harus menunggu aktivasi email tujuan: %+v", it)
	}
	pipeline.Confirm(ctx, e.Store, "MANUAL_EMAIL_SETUP", "domainanda.com", "", "")
	e.RunUntilIdle(r, 10)
	if it = e.Item("domainanda.com"); it.ManualCode != "MANUAL_EMAIL_SETUP" || !strings.Contains(e.Count2(it.ID), "belum terlihat") {
		t.Fatalf("layanan tujuan belum ada, harus kembali menunggu: %+v", it)
	}
	e.ActivateDestMail()
	e.IMAP.DropAfterAppends = 2
	pipeline.Confirm(ctx, e.Store, "MANUAL_EMAIL_SETUP", "domainanda.com", "", "")
	e.RunUntilIdle(r, 10)
	it = e.Item("domainanda.com")
	if it.EmailState != email.StepRestore || it.LastError == "" {
		t.Fatalf("restore harus terputus di tengah: %+v", it)
	}
	e.ExpireBackoff("domainanda.com")
	e.RunUntilIdle(r, 10)
	if it = e.Item("domainanda.com"); it.ManualCode != "MANUAL_EMAIL_DNS" {
		t.Fatalf("setelah restore = %+v", it)
	}
	pipeline.Confirm(ctx, e.Store, "MANUAL_EMAIL_DNS", "domainanda.com", "", "")
	e.RunUntilIdle(r, 10)
	it = e.Item("domainanda.com")
	if it.State != pipeline.StateCleanupReady || it.EmailState != email.StepDone {
		t.Fatalf("akhir = %s %s %s", it.State, it.EmailState, it.LastError)
	}

	if login(t, e, "info@domainanda.com", newInfo) != nil {
		t.Fatal("password baru harus berlaku di tujuan")
	}
	if login(t, e, "info@domainanda.com", testenv.MailOldInfo) == nil {
		t.Fatal("password lama tidak berlaku lagi (keputusan D-009)")
	}
	inbox := e.IMAP.Folder("info@domainanda.com", "INBOX")
	if len(inbox) < 2 || !bytes.Contains(inbox[1].Body, testenv.BigAttachment) || inbox[0].Flags[0] != `\Seen` || inbox[0].Date != "01-Nov-2026 08:00:00 +0700" {
		t.Fatalf("INBOX tujuan salah: %d pesan", len(inbox))
	}
	if len(inbox) != 2 {
		t.Fatalf("INBOX tujuan %d pesan, harus 2", len(inbox))
	}
	if n := len(e.IMAP.Folder("info@domainanda.com", "Arsip Lama")); n != 1 {
		t.Fatalf("folder Arsip Lama = %d", n)
	}
	if n := len(e.IMAP.Folder("admin@domainanda.com", "Sent")); n != 1 {
		t.Fatalf("folder Sent admin = %d (tidak boleh ganda)", n)
	}
	extras := e.API.MailExtras["mo-dst"]
	if len(extras["aliases"]) != 1 || extras["aliases"][0]["address"] != "sales@domainanda.com" || len(extras["forwarders"]) != 1 || len(extras["catchalls"]) != 1 || len(extras["autoreplies"]) != 1 {
		t.Fatalf("alias/forwarder tujuan = %+v", extras)
	}
	vs, _ := e.Store.ListVerifications(ctx, it.ID)
	fail := 0
	folders := 0
	for _, v := range vs {
		if strings.HasPrefix(v.CheckType, "EMAIL_FOLDER_") {
			folders++
		}
		if v.Result == "FAIL" {
			fail++
		}
	}
	if fail != 0 || folders != 4 {
		t.Fatalf("verifikasi email: %d gagal, %d folder: %+v", fail, folders, vs)
	}
	e.AssertNoSecretsOnDisk(testenv.MailOldInfo, testenv.MailOldAdmin, newInfo)
}
