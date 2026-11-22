package secrets

import (
	"errors"
	"strings"
	"testing"

	"hostinger-migrator/internal/logx"
)

const tok = "FAKE-secret-token-abcdef123456"

func TestValidateAlias(t *testing.T) {
	for _, ok := range []string{"xyz", "zyx", "klien-a", "akun_2", "a"} {
		if err := ValidateAlias(ok); err != nil {
			t.Errorf("%q harus valid: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "XYZ", "spasi ada", "-awal", "titik.dua", strings.Repeat("a", 33), "ssh:u1"} {
		if err := ValidateAlias(bad); err == nil {
			t.Errorf("%q harus ditolak", bad)
		}
	}
}

func TestValidateToken(t *testing.T) {
	if err := ValidateToken(tok); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "pendek", " " + tok, tok + "\n", "abc def ghijklmnopqrs"} {
		if err := ValidateToken(bad); err == nil {
			t.Errorf("%q harus ditolak", bad)
		}
	}
}

func TestStoreRoundTripRegistersRedaction(t *testing.T) {
	r := logx.NewRedactor()
	s := New(NewMemoryBackend(), r)
	if err := s.SetToken("xyz", tok); err != nil {
		t.Fatal(err)
	}
	got, err := s.Token("xyz")
	if err != nil || got != tok {
		t.Fatalf("token = %q err = %v", got, err)
	}
	if strings.Contains(r.String("x "+tok), tok) {
		t.Fatal("token harus otomatis terdaftar di redactor")
	}
	if ok, _ := s.HasToken("lain"); ok {
		t.Fatal("alias lain tidak boleh ada")
	}
	if err := s.DeleteToken("xyz"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Token("xyz"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if err := s.DeleteToken("xyz"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestBackendErrorIsRedacted(t *testing.T) {
	r := logx.NewRedactor()
	b := NewMemoryBackend()
	b.Fail = errors.New("akses ditolak untuk " + tok)
	s := New(b, r)
	err := s.SetToken("xyz", tok)
	if err == nil || strings.Contains(err.Error(), tok) {
		t.Fatalf("err = %v", err)
	}
}

func TestSSHPasswordSeparateFromToken(t *testing.T) {
	r := logx.NewRedactor()
	b := NewMemoryBackend()
	s := New(b, r)
	if err := s.SetToken("u111", tok); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSSHPassword("u111", "Sandi-SSH-9876"); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.Token("u111"); v != tok {
		t.Fatal("token tertimpa password SSH")
	}
	if v, _ := s.SSHPassword("u111"); v != "Sandi-SSH-9876" {
		t.Fatal("password SSH salah")
	}
	if strings.Contains(r.String("x Sandi-SSH-9876"), "Sandi-SSH-9876") {
		t.Fatal("password SSH harus disaring dari log")
	}
	if err := s.DeleteSSHPassword("u111"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SSHPassword("u111"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if err := s.SetSSHPassword("U 1", "x"); err == nil {
		t.Fatal("id akun tidak valid harus ditolak")
	}
}

func TestGenericSecretsAndPassword(t *testing.T) {
	r := logx.NewRedactor()
	s := New(NewMemoryBackend(), r)
	if err := s.Put(KindDBPassword, "u2:u2_wp", "Rahasia-DB-1"); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.Get(KindDBPassword, "u2:u2_wp"); v != "Rahasia-DB-1" {
		t.Fatal("rahasia salah")
	}
	if err := s.Put("", "x", "y"); err == nil {
		t.Fatal("rahasia tidak lengkap harus ditolak")
	}
	if s.Delete(KindDBPassword, "u2:u2_wp") != nil {
		t.Fatal("hapus gagal")
	}
	if _, err := s.Get(KindDBPassword, "u2:u2_wp"); !errors.Is(err, ErrNotFound) {
		t.Fatal("harus hilang")
	}
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		p, err := GeneratePassword(16)
		if err != nil || len(p) != 16 {
			t.Fatalf("p = %q %v", p, err)
		}
		if !strings.ContainsAny(p, lowerChars) || !strings.ContainsAny(p, upperChars) || !strings.ContainsAny(p, digitChars) || !strings.ContainsAny(p, symbolChars) {
			t.Fatalf("kelas karakter kurang: %s", p)
		}
		if strings.ContainsAny(p, " '\"\\$`#") {
			t.Fatalf("karakter berbahaya: %s", p)
		}
		seen[p] = true
	}
	if len(seen) < 50 {
		t.Fatal("password tidak acak")
	}
}
