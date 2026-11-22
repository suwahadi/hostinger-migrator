package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"hostinger-migrator/configs"
)

type Config struct {
	Storage    StorageConfig    `yaml:"storage"`
	API        APIConfig        `yaml:"api"`
	Worker     WorkerConfig     `yaml:"worker"`
	Backup     BackupConfig     `yaml:"backup"`
	Verifikasi VerifikasiConfig `yaml:"verifikasi"`
	SSL        SSLConfig        `yaml:"ssl"`
	Cleanup    CleanupConfig    `yaml:"cleanup"`
	KuotaPaket map[string]Kuota `yaml:"kuota_paket"`
	Email      EmailConfig      `yaml:"email"`
}

type EmailConfig struct {
	IMAPHost string `yaml:"imap_host"`
	IMAPTLS  bool   `yaml:"imap_tls"`
}

type Kuota struct {
	Website  int `yaml:"website"`
	Database int `yaml:"database"`
}

type StorageConfig struct {
	DataDir string `yaml:"data_dir"`
}

type APIConfig struct {
	BaseURL      string `yaml:"base_url"`
	TimeoutDetik int    `yaml:"timeout_detik"`
	RatePerMenit int    `yaml:"rate_per_menit"`
	RetryMaks    int    `yaml:"retry_maks"`
}

type WorkerConfig struct {
	WebsiteParalel         int `yaml:"website_paralel"`
	ImportDBParalelPerAkun int `yaml:"import_db_paralel_per_akun"`
}

type BackupConfig struct {
	RetensiHari             int  `yaml:"retensi_hari"`
	FinalSyncSebelumCutover bool `yaml:"final_sync_sebelum_cutover"`
}

type VerifikasiConfig struct {
	TimeoutHTTPDetik int `yaml:"timeout_http_detik"`
	MinAssetDicek    int `yaml:"min_asset_dicek"`
}

type SSLConfig struct {
	BatasTungguMenit int `yaml:"batas_tunggu_menit"`
}

type CleanupConfig struct {
	WajibSSLAktif bool `yaml:"wajib_ssl_aktif"`
}

func Default() (*Config, error) {
	return parse(configs.Default)
}

func DefaultYAML() []byte {
	return append([]byte(nil), configs.Default...)
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Default()
	}
	if err != nil {
		return nil, fmt.Errorf("gagal membaca config %s: %w", path, err)
	}
	base, err := Default()
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(base); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("config %s tidak valid: %w", path, err)
	}
	if err := base.Validate(); err != nil {
		return nil, fmt.Errorf("config %s tidak valid: %w", path, err)
	}
	return base, nil
}

func parse(data []byte) (*Config, error) {
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("config bawaan tidak valid: %w", err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) Validate() error {
	var problems []string
	if strings.TrimSpace(c.Storage.DataDir) == "" {
		problems = append(problems, "storage.data_dir wajib diisi")
	}
	local := strings.HasPrefix(c.API.BaseURL, "http://127.0.0.1") || strings.HasPrefix(c.API.BaseURL, "http://localhost")
	if !strings.HasPrefix(c.API.BaseURL, "https://") && !local {
		problems = append(problems, "api.base_url harus diawali https://")
	}
	if c.API.TimeoutDetik < 1 {
		problems = append(problems, "api.timeout_detik minimal 1")
	}
	if c.API.RatePerMenit < 1 {
		problems = append(problems, "api.rate_per_menit minimal 1")
	}
	if c.API.RetryMaks < 0 || c.API.RetryMaks > 20 {
		problems = append(problems, "api.retry_maks harus 0 sampai 20")
	}
	if c.Worker.WebsiteParalel < 1 {
		problems = append(problems, "worker.website_paralel minimal 1")
	}
	if c.Worker.ImportDBParalelPerAkun < 1 {
		problems = append(problems, "worker.import_db_paralel_per_akun minimal 1")
	}
	if c.Backup.RetensiHari < 1 {
		problems = append(problems, "backup.retensi_hari minimal 1")
	}
	if c.Verifikasi.TimeoutHTTPDetik < 1 {
		problems = append(problems, "verifikasi.timeout_http_detik minimal 1")
	}
	if c.Verifikasi.MinAssetDicek < 0 {
		problems = append(problems, "verifikasi.min_asset_dicek tidak boleh negatif")
	}
	if c.SSL.BatasTungguMenit < 1 {
		problems = append(problems, "ssl.batas_tunggu_menit minimal 1")
	}
	if !strings.Contains(c.Email.IMAPHost, ":") {
		problems = append(problems, "email.imap_host harus berformat host:port")
	}
	for name, k := range c.KuotaPaket {
		if k.Website < 0 || k.Database < 0 {
			problems = append(problems, "kuota_paket."+name+" tidak boleh negatif")
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func (c *Config) DataDir(configPath string) string {
	dir := c.Storage.DataDir
	if filepath.IsAbs(dir) {
		return filepath.Clean(dir)
	}
	base := filepath.Dir(configPath)
	if base == "" {
		base = "."
	}
	return filepath.Clean(filepath.Join(base, dir))
}

var cloudSyncMarkers = []string{"onedrive", "google drive", "googledrive", "my drive", "dropbox", "icloud"}

func CloudSyncWarning(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	lower := strings.ToLower(filepath.ToSlash(abs))
	for _, m := range cloudSyncMarkers {
		if strings.Contains(lower, m) {
			return fmt.Sprintf("PERINGATAN: folder data %s tampaknya berada di folder sinkronisasi cloud (%s). Backup tidak dienkripsi; pindahkan folder data ke luar OneDrive/Google Drive/Dropbox.", abs, m)
		}
	}
	return ""
}
