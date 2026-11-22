package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func seedAccounts(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		if err := s.InsertCredential(ctx, tx, "xyz"); err != nil {
			return err
		}
		return s.ReplaceHostingAccounts(ctx, tx, "xyz", []HostingAccount{{ID: "u111", Plan: "hostinger_premium"}, {ID: "u222"}})
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSaveAndListSSH(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	seedAccounts(t, s)
	all, _ := s.ListSSH(ctx)
	if len(all) != 2 || all[0].Status != SSHNotConfigured || all[0].Plan != "hostinger_premium" {
		t.Fatalf("awal = %+v", all)
	}
	err := s.SaveSSH(ctx, SSHInfo{AccountID: "u111", Host: "1.2.3.4", Port: 65002, User: "u111", Fingerprint: "SHA256:abc", Status: SSHReady, Auth: SSHAuthKey, Home: "/home/u111"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSSH(ctx, "u111")
	if err != nil || got.Host != "1.2.3.4" || got.Port != 65002 || got.Status != SSHReady || got.CheckedAt == "" || got.Home != "/home/u111" {
		t.Fatalf("got = %+v err = %v", got, err)
	}
	if err := s.SaveSSH(ctx, SSHInfo{AccountID: "u999", Status: SSHReady}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestSSHInfoCopiedToNewAlias(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	seedAccounts(t, s)
	s.SaveSSH(ctx, SSHInfo{AccountID: "u111", Host: "1.2.3.4", Port: 65002, User: "u111", Fingerprint: "SHA256:abc", Status: SSHReady, Auth: SSHAuthKey})
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		if err := s.InsertCredential(ctx, tx, "abc"); err != nil {
			return err
		}
		return s.ReplaceHostingAccounts(ctx, tx, "abc", []HostingAccount{{ID: "u111"}})
	})
	if err != nil {
		t.Fatal(err)
	}
	accs, _ := s.ListHostingAccounts(ctx, "abc")
	if len(accs) != 1 || accs[0].SSHHost != "1.2.3.4" || accs[0].SSHStatus != SSHReady || accs[0].SSHFingerprint != "SHA256:abc" {
		t.Fatalf("info SSH tidak tersalin: %+v", accs)
	}
	got, _ := s.GetSSH(ctx, "u111")
	if len(got.Aliases) != 2 {
		t.Fatalf("alias = %v", got.Aliases)
	}
}

func TestCapabilitiesUpsert(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	err := s.SaveCapabilities(ctx, "u111", []Capability{{Code: "SSH", Status: "UNKNOWN", Note: "belum"}, {Code: "API_LIST_WEBSITES", Status: "SUPPORTED"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveCapabilities(ctx, "u111", []Capability{{Code: "SSH", Status: "SUPPORTED", DetailJSON: `{"a":1}`}}); err != nil {
		t.Fatal(err)
	}
	caps, _ := s.ListCapabilities(ctx, "u111")
	if len(caps) != 2 || caps[1].Code != "SSH" || caps[1].Status != "SUPPORTED" || caps[1].Note != "" || caps[1].DetailJSON != `{"a":1}` {
		t.Fatalf("caps = %+v", caps)
	}
	if err := s.SaveCapabilities(ctx, "u111", []Capability{{Code: "X", Status: "MUNGKIN"}}); err == nil {
		t.Fatal("status tidak valid harus ditolak")
	}
}
