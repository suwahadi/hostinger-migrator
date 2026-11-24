package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultMatchesPRD(t *testing.T) {
	c, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if c.Storage.DataDir != "./data" || c.API.BaseURL != "https://developers.hostinger.com" {
		t.Fatalf("default salah: %+v", c)
	}
	if c.API.TimeoutDetik != 60 || c.API.RatePerMenit != 80 || c.API.RateBurst != 5 || c.API.RetryMaks != 8 {
		t.Fatalf("default api salah: %+v", c.API)
	}
	if c.Worker.WebsiteParalel != 2 || c.Worker.ImportDBParalelPerAkun != 1 {
		t.Fatalf("default worker salah: %+v", c.Worker)
	}
	if c.Backup.RetensiHari != 30 || !c.Backup.FinalSyncSebelumCutover {
		t.Fatalf("default backup salah: %+v", c.Backup)
	}
	if c.Verifikasi.TimeoutHTTPDetik != 30 || c.Verifikasi.MinAssetDicek != 3 || c.SSL.BatasTungguMenit != 60 || !c.Cleanup.WajibSSLAktif {
		t.Fatalf("default lain salah: %+v", c)
	}
}

func TestLoadMissingFileUsesDefault(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "tidak-ada.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.API.RatePerMenit != 80 {
		t.Fatal("harus memakai default")
	}
}

func TestLoadPartialOverride(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(p, []byte("api:\n  rate_per_menit: 30\n"), 0o600)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.API.RatePerMenit != 30 || c.API.RetryMaks != 8 {
		t.Fatalf("override parsial salah: %+v", c.API)
	}
}

func TestLoadEmptyFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(p, nil, 0o600)
	if _, err := Load(p); err != nil {
		t.Fatal(err)
	}
}

func TestLoadRejectsUnknownKey(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(p, []byte("api:\n  token: rahasia\n"), 0o600)
	if _, err := Load(p); err == nil {
		t.Fatal("key tidak dikenal harus ditolak")
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(p, []byte("api:\n  base_url: http://contoh\n  rate_per_menit: 0\n"), 0o600)
	_, err := Load(p)
	if err == nil || !strings.Contains(err.Error(), "https://") || !strings.Contains(err.Error(), "rate_per_menit") {
		t.Fatalf("validasi salah: %v", err)
	}
}

func TestDataDirRelativeToConfig(t *testing.T) {
	c, _ := Default()
	got := c.DataDir(filepath.Join("kerja", "config.yaml"))
	if got != filepath.Join("kerja", "data") {
		t.Fatalf("data dir salah: %s", got)
	}
}

func TestCloudSyncWarning(t *testing.T) {
	if CloudSyncWarning("/Users/budi/OneDrive/migrator/data") == "" {
		t.Fatal("OneDrive harus diperingatkan")
	}
	if CloudSyncWarning("/home/budi/Dropbox/data") == "" {
		t.Fatal("Dropbox harus diperingatkan")
	}
	if CloudSyncWarning("/home/budi/migrator/data") != "" {
		t.Fatal("folder biasa tidak boleh diperingatkan")
	}
}

func TestKuotaPaketDefaultAndOverride(t *testing.T) {
	c, _ := Default()
	if len(c.KuotaPaket) != 0 {
		t.Fatalf("config bawaan tidak boleh memuat kuota_paket: %+v", c.KuotaPaket)
	}
	p := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(p, []byte("kuota_paket:\n  hostinger_premium:\n    website: 3\n  hostinger_cloud:\n    website: 300\n"), 0o600)
	c, err := Load(p)
	if err != nil {
		t.Fatal("config lama dengan kuota_paket harus tetap terbaca:", err)
	}
	if c.KuotaPaket["hostinger_premium"].Website != 3 || c.KuotaPaket["hostinger_cloud"].Website != 300 {
		t.Fatalf("override kuota = %+v", c.KuotaPaket)
	}
	os.WriteFile(p, []byte("kuota_paket:\n  x:\n    website: -1\n"), 0o600)
	if _, err := Load(p); err == nil {
		t.Fatal("kuota negatif harus ditolak")
	}
}
