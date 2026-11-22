package logx

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fakeToken = "FAKE-tok3n-Abc123456789xyz"

func TestRedactorRegisteredSecret(t *testing.T) {
	r := NewRedactor()
	r.Add(fakeToken)
	got := r.String("gagal memakai " + fakeToken + " di permintaan")
	if strings.Contains(got, fakeToken) {
		t.Fatalf("token bocor: %s", got)
	}
	if !strings.Contains(got, Mask) {
		t.Fatalf("mask tidak muncul: %s", got)
	}
}

func TestRedactorIgnoresTooShortSecret(t *testing.T) {
	r := NewRedactor()
	r.Add("ab")
	if got := r.String("abc ab"); got != "abc ab" {
		t.Fatalf("secret pendek tidak boleh dipakai: %s", got)
	}
}

func TestRedactorPatterns(t *testing.T) {
	r := NewRedactor()
	cases := []struct {
		in     string
		secret string
	}{
		{"Authorization: Bearer abcdefghijklmnop", "abcdefghijklmnop"},
		{"MYSQL_PWD=s3cretPass mysqldump db", "s3cretPass"},
		{"mysqldump -u user -pRahasia123 db", "Rahasia123"},
		{"DB_PASSWORD=envSecret99", "envSecret99"},
		{`{"password":"jsonSecret1"}`, "jsonSecret1"},
		{"define( 'DB_PASSWORD', 'wpSecret77' );", "wpSecret77"},
		{"api_token: tokVal123456", "tokVal123456"},
		{"ssh://user:sshPass55@host", "sshPass55"},
	}
	for _, c := range cases {
		got := r.String(c.in)
		if strings.Contains(got, c.secret) {
			t.Errorf("pola gagal menyaring %q -> %q", c.in, got)
		}
	}
}

func TestRedactorKeepsNormalText(t *testing.T) {
	r := NewRedactor()
	in := "website domainanda.com berhasil di-backup ke /home/u494983133/domains"
	if got := r.String(in); got != in {
		t.Fatalf("teks normal berubah: %s", got)
	}
}

func TestHandlerRedactsEverything(t *testing.T) {
	r := NewRedactor()
	r.Add(fakeToken)
	var buf bytes.Buffer
	log := slog.New(NewRedactingHandler(slog.NewJSONHandler(&buf, nil), r))
	log = log.With("alias", "xyz", "token", fakeToken)
	log.Info("memanggil API dengan "+fakeToken,
		"error", errors.New("401 untuk token "+fakeToken),
		"password", "apa-saja",
		slog.Group("req", "authorization", "Bearer "+fakeToken, "url", "https://x/?t="+fakeToken),
		"data", []byte(fakeToken),
		"jumlah", 3,
	)
	out := buf.String()
	if strings.Contains(out, fakeToken) || strings.Contains(out, "apa-saja") {
		t.Fatalf("log bocor: %s", out)
	}
	if !strings.Contains(out, `"jumlah":3`) {
		t.Fatalf("atribut biasa hilang: %s", out)
	}
}

func TestNewWritesRedactedFile(t *testing.T) {
	dir := t.TempDir()
	r := NewRedactor()
	r.Add(fakeToken)
	var term bytes.Buffer
	fixed := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	log, closeFn, err := New(Options{Dir: dir, Terminal: &term, TerminalLevel: slog.LevelWarn, FileLevel: slog.LevelDebug, Redactor: r, Now: func() time.Time { return fixed }})
	if err != nil {
		t.Fatal(err)
	}
	log.Debug("debug " + fakeToken)
	log.Warn("peringatan", "detail", fakeToken)
	if err := closeFn(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "migrator-20261122.log"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), fakeToken) || strings.Contains(term.String(), fakeToken) {
		t.Fatal("token bocor ke file log atau terminal")
	}
	if !strings.Contains(string(data), "debug") {
		t.Fatal("log debug tidak tertulis ke file")
	}
	if strings.Contains(term.String(), "debug") {
		t.Fatal("log debug tidak boleh tampil di terminal")
	}
	if !strings.Contains(term.String(), "peringatan") {
		t.Fatal("peringatan harus tampil di terminal")
	}
}
