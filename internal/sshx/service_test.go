package sshx

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hostinger-migrator/internal/logx"
	"hostinger-migrator/internal/secrets"
	"hostinger-migrator/internal/store"
	"hostinger-migrator/tests/sshtest"
)

func newService(t *testing.T) (*Service, *sshtest.Server, *store.Store) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "migrator.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	err = st.WithTx(ctx, func(tx *sql.Tx) error {
		if err := st.InsertCredential(ctx, tx, "xyz"); err != nil {
			return err
		}
		return st.ReplaceHostingAccounts(ctx, tx, "xyz", []store.HostingAccount{{ID: "u494983133"}})
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := newServer(t)
	srv.Exec = func(cmd string, env map[string]string) sshtest.ExecResult {
		if cmd == "echo ok; whoami; pwd" {
			return sshtest.ExecResult{Stdout: "ok\nu494983133\n/home/u494983133\n"}
		}
		return sshtest.ExecResult{Exit: 127}
	}
	svc := &Service{Store: st, Secrets: secrets.New(secrets.NewMemoryBackend(), logx.NewRedactor()), KeyDir: filepath.Join(t.TempDir(), "ssh"), Timeout: 5 * time.Second}
	return svc, srv, st
}

func (s *Service) info(srv *sshtest.Server, user string) store.SSHInfo {
	return store.SSHInfo{AccountID: "u494983133", Host: srv.Host(), Port: srv.Port(), User: user}
}

func TestServiceCheckKeyThenConnect(t *testing.T) {
	svc, srv, st := newService(t)
	ctx := context.Background()
	if _, err := svc.Connect(ctx, "u494983133"); err == nil || !strings.Contains(err.Error(), "belum siap") {
		t.Fatalf("akun belum siap harus ditolak: %v", err)
	}
	kp, _ := svc.Keys()
	srv.Authorize(kp.Signer.PublicKey())
	res, err := svc.Check(ctx, svc.info(srv, "u494983133"), false)
	if err != nil || !res.Ready() || res.Info.Auth != store.SSHAuthKey || res.Home != "/home/u494983133" || !res.NewHostKey {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	saved, _ := st.GetSSH(ctx, "u494983133")
	if saved.Status != store.SSHReady || saved.Fingerprint == "" {
		t.Fatalf("tersimpan = %+v", saved)
	}
	c, err := svc.Connect(ctx, "u494983133")
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	entries, _ := st.ListAudit(ctx, 5)
	if len(entries) == 0 || entries[0].Action != "ssh.test" {
		t.Fatalf("audit = %+v", entries)
	}
}

func TestServiceCheckPasswordFallback(t *testing.T) {
	svc, srv, _ := newService(t)
	srv.SetPassword("Sandi-Uji-777")
	svc.Secrets.SetSSHPassword("u494983133", "Sandi-Uji-777")
	res, err := svc.Check(context.Background(), svc.info(srv, "u494983133"), false)
	if err != nil || !res.Ready() || res.Info.Auth != store.SSHAuthPassword {
		t.Fatalf("res = %+v err = %v", res, err)
	}
}

func TestServiceCheckWhoamiMismatch(t *testing.T) {
	svc, srv, _ := newService(t)
	srv.User = "u111"
	kp, _ := svc.Keys()
	srv.Authorize(kp.Signer.PublicKey())
	res, err := svc.Check(context.Background(), svc.info(srv, "u111"), false)
	if err != nil || res.Ready() || !strings.Contains(res.Message, "whoami") {
		t.Fatalf("res = %+v err = %v", res, err)
	}
}

func TestServiceConnectBlocksOnHostKeyChange(t *testing.T) {
	svc, srv, st := newService(t)
	ctx := context.Background()
	kp, _ := svc.Keys()
	srv.Authorize(kp.Signer.PublicKey())
	if res, _ := svc.Check(ctx, svc.info(srv, "u494983133"), false); !res.Ready() {
		t.Fatal("harus siap")
	}
	srv.RotateHostKey()
	if _, err := svc.Connect(ctx, "u494983133"); !errors.Is(err, ErrBlocked) {
		t.Fatalf("err = %v", err)
	}
	info, _ := st.GetSSH(ctx, "u494983133")
	if info.Status != store.SSHBlocked {
		t.Fatalf("status = %s", info.Status)
	}
	if _, err := svc.Connect(ctx, "u494983133"); !errors.Is(err, ErrBlocked) {
		t.Fatalf("akun BLOCKED harus tetap ditolak: %v", err)
	}
	res, err := svc.Check(ctx, *info, true)
	if err != nil || !res.Ready() {
		t.Fatalf("terima host baru: %+v %v", res, err)
	}
}
