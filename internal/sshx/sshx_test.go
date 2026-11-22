package sshx

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"hostinger-migrator/tests/sshtest"
)

func TestEnsureKeyPairCreatesAndReuses(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ssh")
	kp, err := EnsureKeyPair(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !kp.Created || !strings.HasPrefix(kp.AuthorizedKey, "ssh-ed25519 AAAA") || !strings.HasSuffix(kp.AuthorizedKey, " hostinger-migrator") {
		t.Fatalf("key = %+v", kp)
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(kp.PrivatePath)
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("izin private key = %v", info.Mode().Perm())
		}
	}
	pub, _ := os.ReadFile(kp.PublicPath)
	if strings.TrimSpace(string(pub)) != kp.AuthorizedKey {
		t.Fatal("file public key tidak cocok")
	}
	again, err := EnsureKeyPair(dir)
	if err != nil {
		t.Fatal(err)
	}
	if again.Created || again.AuthorizedKey != kp.AuthorizedKey {
		t.Fatal("key yang sudah ada harus dipakai ulang, bukan dibuat baru")
	}
	if strings.Contains(kp.AuthorizedKey, "PRIVATE") {
		t.Fatal("baris public key tidak boleh memuat private key")
	}
}

func TestEnsureKeyPairRejectsCorruptKey(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "id_ed25519"), []byte("rusak"), 0o600)
	if _, err := EnsureKeyPair(dir); err == nil {
		t.Fatal("private key rusak harus ditolak")
	}
}

func newServer(t *testing.T) *sshtest.Server {
	t.Helper()
	srv, err := sshtest.New("u494983133")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	return srv
}

func TestDialWithKeyRunAndSFTP(t *testing.T) {
	srv := newServer(t)
	kp, _ := EnsureKeyPair(t.TempDir())
	srv.Authorize(kp.Signer.PublicKey())
	srv.Exec = func(cmd string, env map[string]string) sshtest.ExecResult {
		if cmd == "whoami" {
			return sshtest.ExecResult{Stdout: "u494983133\n"}
		}
		if cmd == "cek-env" {
			return sshtest.ExecResult{Stdout: env["MYSQL_PWD"]}
		}
		return sshtest.ExecResult{Stderr: "tidak dikenal", Exit: 127}
	}
	ctx := context.Background()
	c, err := Dial(ctx, DialOptions{Target: Target{Host: srv.Host(), Port: srv.Port(), User: "u494983133"}, Auth: Auth{Signer: kp.Signer}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Fingerprint != Fingerprint(srv.HostSigner.PublicKey()) {
		t.Fatal("fingerprint host tidak tercatat")
	}
	res, err := c.Run(ctx, "whoami", nil)
	if err != nil || strings.TrimSpace(res.Stdout) != "u494983133" || res.ExitCode != 0 {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	res, _ = c.Run(ctx, "salah", nil)
	if res.ExitCode != 127 || res.Stderr != "tidak dikenal" {
		t.Fatalf("exit code salah: %+v", res)
	}
	res, _ = c.Run(ctx, "cek-env", map[string]string{"MYSQL_PWD": "rahasia"})
	if res.Stdout != "rahasia" {
		t.Fatalf("env tidak terkirim: %+v", res)
	}
	if _, err := c.CheckSFTP(); err != nil {
		t.Fatalf("sftp: %v", err)
	}
}

func TestDialWithPassword(t *testing.T) {
	srv := newServer(t)
	srv.SetPassword("Sandi-Uji-123")
	c, err := Dial(context.Background(), DialOptions{Target: Target{Host: srv.Host(), Port: srv.Port(), User: "u494983133"}, Auth: Auth{Password: "Sandi-Uji-123"}})
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
}

func TestDialWrongKeyIsAuthFailure(t *testing.T) {
	srv := newServer(t)
	kp, _ := EnsureKeyPair(t.TempDir())
	_, err := Dial(context.Background(), DialOptions{Target: Target{Host: srv.Host(), Port: srv.Port(), User: "u494983133"}, Auth: Auth{Signer: kp.Signer}})
	if cause, _ := Classify(err); cause != CauseAuth {
		t.Fatalf("cause = %s err = %v", cause, err)
	}
}

func TestDialHostKeyChanged(t *testing.T) {
	srv := newServer(t)
	kp, _ := EnsureKeyPair(t.TempDir())
	srv.Authorize(kp.Signer.PublicKey())
	target := Target{Host: srv.Host(), Port: srv.Port(), User: "u494983133"}
	c, err := Dial(context.Background(), DialOptions{Target: target, Auth: Auth{Signer: kp.Signer}})
	if err != nil {
		t.Fatal(err)
	}
	first := c.Fingerprint
	c.Close()
	c, err = Dial(context.Background(), DialOptions{Target: target, Auth: Auth{Signer: kp.Signer}, ExpectedFingerprint: first})
	if err != nil {
		t.Fatalf("fingerprint sama harus diterima: %v", err)
	}
	c.Close()
	_, err = Dial(context.Background(), DialOptions{Target: target, Auth: Auth{Signer: kp.Signer}, ExpectedFingerprint: "SHA256:lain"})
	var hk *HostKeyChangedError
	if !errors.As(err, &hk) {
		t.Fatalf("err = %v", err)
	}
	if cause, _ := Classify(err); cause != CauseHostKeyChanged {
		t.Fatalf("cause = %s", cause)
	}
}

func TestClassifyRefusedAndTimeout(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	kp, _ := EnsureKeyPair(t.TempDir())
	_, err := Dial(context.Background(), DialOptions{Target: Target{Host: "127.0.0.1", Port: port, User: "u1"}, Auth: Auth{Signer: kp.Signer}, Timeout: 2 * time.Second})
	if cause, _ := Classify(err); cause != CauseRefused {
		t.Fatalf("cause = %s err = %v", cause, err)
	}
	silent, _ := net.Listen("tcp", "127.0.0.1:0")
	defer silent.Close()
	go func() {
		for {
			c, err := silent.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	_, err = Dial(context.Background(), DialOptions{Target: Target{Host: "127.0.0.1", Port: silent.Addr().(*net.TCPAddr).Port, User: "u1"}, Auth: Auth{Signer: kp.Signer}, Timeout: 500 * time.Millisecond})
	if cause, _ := Classify(err); cause != CauseTimeout {
		t.Fatalf("cause = %s err = %v", cause, err)
	}
}

func TestTargetValidate(t *testing.T) {
	for _, tg := range []Target{{Port: 22, User: "u"}, {Host: "h", Port: 0, User: "u"}, {Host: "h", Port: 70000, User: "u"}, {Host: "h", Port: 22}} {
		if tg.Validate() == nil {
			t.Errorf("%+v harus ditolak", tg)
		}
	}
}

func TestShellQuote(t *testing.T) {
	if got := ShellQuote("a'b c"); got != `'a'"'"'b c'` {
		t.Fatalf("quote = %s", got)
	}
}
