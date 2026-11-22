package backup

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"hostinger-migrator/internal/detector"
	"hostinger-migrator/internal/logx"
	"hostinger-migrator/internal/sshx"
	"hostinger-migrator/internal/store"
)

const (
	KindFiles    = "files"
	KindDBPrefix = "db:"
	KindManifest = "manifest"
	FinalPrefix  = "final-"
)

var ErrManualBackupIncomplete = errors.New("file backup manual belum lengkap atau tidak valid")

type Engine struct {
	Store     *store.Store
	BackupDir string
	Redactor  *logx.Redactor
	Log       *slog.Logger
	Version   string
	Timeout   time.Duration
}

type DBCredential struct {
	Name     string
	User     string
	Password string
	Host     string
	Source   string
}

type FileEntry struct {
	Path               string `json:"path"`
	Size               int64  `json:"ukuran_byte"`
	SHA256             string `json:"sha256"`
	FileCount          int64  `json:"jumlah_file"`
	Entries            int64  `json:"jumlah_entri"`
	InventoryFileCount int64  `json:"jumlah_file_inventaris"`
	Note               string `json:"catatan,omitempty"`
}

type DBEntry struct {
	Name   string           `json:"nama"`
	Path   string           `json:"path"`
	Size   int64            `json:"ukuran_byte"`
	SHA256 string           `json:"sha256"`
	Tables []string         `json:"tabel"`
	Rows   map[string]int64 `json:"baris_per_tabel"`
}

type Manifest struct {
	Domain    string    `json:"domain"`
	Folder    string    `json:"folder_sumber"`
	BatchID   string    `json:"batch"`
	Final     bool      `json:"backup_final"`
	CreatedAt string    `json:"dibuat"`
	Version   string    `json:"versi_migrator"`
	Manual    bool      `json:"backup_manual"`
	Files     FileEntry `json:"file"`
	Databases []DBEntry `json:"database"`
}

func (e *Engine) redactor() *logx.Redactor {
	if e.Redactor == nil {
		return logx.Global
	}
	return e.Redactor
}

func (e *Engine) Dir(it *store.PipelineItem, final bool) string {
	d := filepath.Join(e.BackupDir, it.BatchID, it.SourceDomain)
	if final {
		d = filepath.Join(d, "final")
	}
	return d
}

func kind(base string, final bool) string {
	if final {
		return FinalPrefix + base
	}
	return base
}

func (e *Engine) ReadCredentials(ctx context.Context, c *sshx.Client, w *store.Website, dbs []store.DatabaseRow) ([]DBCredential, error) {
	var out []DBCredential
	for _, db := range dbs {
		if db.Source == "" || db.Source == "api" {
			return nil, fmt.Errorf("sumber kredensial database %s tidak diketahui; tidak bisa membuat dump otomatis", db.Name)
		}
		remote := path.Join(w.UnitPath, db.Source)
		data, err := c.ReadFile(remote, 1<<20)
		if err != nil {
			return nil, fmt.Errorf("gagal membaca %s: %w", db.Source, err)
		}
		for _, s := range detector.ExtractSecrets(string(data)) {
			e.redactor().Add(s)
		}
		ref := detector.CredentialsFrom(db.Source, string(data))
		if ref.Name != db.Name {
			return nil, fmt.Errorf("%s sekarang merujuk database %q, inventaris mencatat %q; jalankan inventory --refresh", db.Source, ref.Name, db.Name)
		}
		host := ref.Host
		if host == "" {
			host = "localhost"
		}
		e.redactor().Add(ref.Password)
		out = append(out, DBCredential{Name: ref.Name, User: ref.User, Password: ref.Password, Host: host, Source: db.Source})
	}
	return out, nil
}

func (e *Engine) timeout() time.Duration {
	if e.Timeout > 0 {
		return e.Timeout
	}
	return 6 * time.Hour
}

func (e *Engine) BackupFiles(ctx context.Context, c *sshx.Client, it *store.PipelineItem, w *store.Website, final bool) (*FileEntry, error) {
	dir := e.Dir(it, final)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	dst := filepath.Join(dir, "files.tar.gz")
	tmp := dst + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	cctx, cancel := context.WithTimeout(ctx, e.timeout())
	defer cancel()
	res, err := c.Stream(cctx, "tar -czf - -C "+sshx.ShellQuote(w.UnitPath)+" .", nil, io.MultiWriter(f, h))
	closeErr := f.Close()
	if err != nil {
		os.Remove(tmp)
		return nil, fmt.Errorf("stream arsip file terputus: %w", err)
	}
	if closeErr != nil {
		os.Remove(tmp)
		return nil, closeErr
	}
	if res.ExitCode > 1 {
		os.Remove(tmp)
		return nil, fmt.Errorf("tar keluar dengan kode %d: %s", res.ExitCode, e.redactor().String(strings.TrimSpace(res.Stderr)))
	}
	stats, err := InspectTarGz(tmp)
	if err != nil {
		os.Remove(tmp)
		return nil, err
	}
	entry := &FileEntry{Path: dst, Size: res.Bytes, SHA256: hex.EncodeToString(h.Sum(nil)), FileCount: stats.Files, Entries: stats.Entries, InventoryFileCount: w.FileCount}
	if res.ExitCode == 1 {
		entry.Note = "tar melaporkan file berubah saat dibaca: " + e.redactor().String(strings.TrimSpace(res.Stderr))
	}
	if err := checkFileCount(entry); err != nil {
		os.Remove(tmp)
		return nil, err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return nil, err
	}
	if err := e.Store.SaveArtifact(ctx, store.Artifact{ItemID: it.ID, Kind: kind(KindFiles, final), Path: dst, Size: entry.Size, SHA256: entry.SHA256, Validated: true}); err != nil {
		return nil, err
	}
	return entry, nil
}

func checkFileCount(e *FileEntry) error {
	if e.InventoryFileCount <= 0 {
		return nil
	}
	diff := e.InventoryFileCount - e.FileCount
	if diff < 0 {
		diff = -diff
	}
	allowed := e.InventoryFileCount / 50
	if allowed < 20 {
		allowed = 20
	}
	if diff <= allowed {
		return nil
	}
	if e.FileCount < e.InventoryFileCount*9/10 {
		return fmt.Errorf("arsip berisi %d file, inventaris mencatat %d; selisih terlalu besar", e.FileCount, e.InventoryFileCount)
	}
	note := fmt.Sprintf("jumlah file arsip %d berbeda dari inventaris %d (website berubah sejak inventaris)", e.FileCount, e.InventoryFileCount)
	if e.Note != "" {
		e.Note += "; " + note
	} else {
		e.Note = note
	}
	return nil
}

func DumpCommand(cred DBCredential, events bool) string {
	opts := "--single-transaction --quick --routines --triggers --default-character-set=utf8mb4 --no-tablespaces"
	if events {
		opts += " --events"
	}
	return "set -o pipefail; mysqldump " + opts + " -h " + sshx.ShellQuote(cred.Host) + " -u " + sshx.ShellQuote(cred.User) + " " + sshx.ShellQuote(cred.Name) + " | gzip -c"
}

func (e *Engine) DumpTo(ctx context.Context, c *sshx.Client, cred DBCredential, dst string) (string, int64, DumpStats, error) {
	tmp := dst + ".part"
	var lastErr error
	for _, events := range []bool{true, false} {
		f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			return "", 0, DumpStats{}, err
		}
		h := sha256.New()
		cctx, cancel := context.WithTimeout(ctx, e.timeout())
		res, err := c.Stream(cctx, DumpCommand(cred, events), map[string]string{"MYSQL_PWD": cred.Password}, io.MultiWriter(f, h))
		cancel()
		f.Close()
		if err != nil {
			os.Remove(tmp)
			return "", 0, DumpStats{}, fmt.Errorf("stream dump %s terputus: %w", cred.Name, err)
		}
		if res.ExitCode != 0 {
			os.Remove(tmp)
			msg := e.redactor().String(strings.TrimSpace(res.Stderr))
			lastErr = fmt.Errorf("mysqldump %s gagal (kode %d): %s", cred.Name, res.ExitCode, msg)
			if events && strings.Contains(strings.ToLower(msg), "event") {
				continue
			}
			return "", 0, DumpStats{}, lastErr
		}
		stats, err := ParseDumpFile(tmp)
		if err != nil {
			os.Remove(tmp)
			return "", 0, DumpStats{}, err
		}
		if !stats.Completed {
			os.Remove(tmp)
			return "", 0, DumpStats{}, fmt.Errorf("dump %s tidak lengkap (penanda akhir mysqldump tidak ada)", cred.Name)
		}
		if err := os.Rename(tmp, dst); err != nil {
			return "", 0, DumpStats{}, err
		}
		return hex.EncodeToString(h.Sum(nil)), res.Bytes, stats, nil
	}
	return "", 0, DumpStats{}, lastErr
}

func (e *Engine) BackupDB(ctx context.Context, c *sshx.Client, it *store.PipelineItem, cred DBCredential, final bool) (*DBEntry, error) {
	dir := e.Dir(it, final)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	dst := filepath.Join(dir, "db-"+cred.Name+".sql.gz")
	sum, size, stats, err := e.DumpTo(ctx, c, cred, dst)
	if err != nil {
		return nil, err
	}
	if err := e.Store.SaveArtifact(ctx, store.Artifact{ItemID: it.ID, Kind: kind(KindDBPrefix+cred.Name, final), Path: dst, Size: size, SHA256: sum, Validated: true}); err != nil {
		return nil, err
	}
	return &DBEntry{Name: cred.Name, Path: dst, Size: size, SHA256: sum, Tables: stats.Tables, Rows: stats.Rows}, nil
}

func (e *Engine) WriteManifest(ctx context.Context, it *store.PipelineItem, m *Manifest) (string, error) {
	final := m.Final
	m.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	m.Version = e.Version
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", err
	}
	p := filepath.Join(e.Dir(it, final), "manifest.json")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		return "", err
	}
	sum, size, _ := FileSHA256(p)
	return p, e.Store.SaveArtifact(ctx, store.Artifact{ItemID: it.ID, Kind: kind(KindManifest, final), Path: p, Size: size, SHA256: sum, Validated: true})
}

func ReadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("manifest %s rusak: %w", path, err)
	}
	return &m, nil
}

func (e *Engine) Run(ctx context.Context, c *sshx.Client, it *store.PipelineItem, w *store.Website, dbs []store.DatabaseRow, final bool) (*Manifest, error) {
	m := &Manifest{Domain: it.SourceDomain, Folder: w.UnitPath, BatchID: it.BatchID, Final: final}
	if a, err := e.Store.ValidatedArtifact(ctx, it.ID, kind(KindManifest, final)); err == nil && !final {
		if existing, err := ReadManifest(a.Path); err == nil && e.artifactsIntact(existing) {
			return existing, nil
		}
	}
	creds, err := e.ReadCredentials(ctx, c, w, dbs)
	if err != nil {
		return nil, err
	}
	fe, err := e.reuseOrBackupFiles(ctx, c, it, w, final)
	if err != nil {
		return nil, err
	}
	m.Files = *fe
	for _, cred := range creds {
		entry, err := e.BackupDB(ctx, c, it, cred, final)
		if err != nil {
			return nil, err
		}
		m.Databases = append(m.Databases, *entry)
	}
	if _, err := e.WriteManifest(ctx, it, m); err != nil {
		return nil, err
	}
	return m, nil
}

func (e *Engine) reuseOrBackupFiles(ctx context.Context, c *sshx.Client, it *store.PipelineItem, w *store.Website, final bool) (*FileEntry, error) {
	if a, err := e.Store.ValidatedArtifact(ctx, it.ID, kind(KindFiles, final)); err == nil {
		if sum, size, err := FileSHA256(a.Path); err == nil && sum == a.SHA256 {
			stats, err := InspectTarGz(a.Path)
			if err == nil {
				return &FileEntry{Path: a.Path, Size: size, SHA256: sum, FileCount: stats.Files, Entries: stats.Entries, InventoryFileCount: w.FileCount}, nil
			}
		}
	}
	return e.BackupFiles(ctx, c, it, w, final)
}

func (e *Engine) artifactsIntact(m *Manifest) bool {
	if sum, _, err := FileSHA256(m.Files.Path); err != nil || sum != m.Files.SHA256 {
		return false
	}
	for _, d := range m.Databases {
		if sum, _, err := FileSHA256(d.Path); err != nil || sum != d.SHA256 {
			return false
		}
	}
	return true
}

func (e *Engine) VerifyLocal(m *Manifest) []string {
	var problems []string
	if sum, _, err := FileSHA256(m.Files.Path); err != nil {
		problems = append(problems, "arsip file tidak terbaca: "+err.Error())
	} else if sum != m.Files.SHA256 {
		problems = append(problems, "checksum arsip file berubah")
	} else if _, err := InspectTarGz(m.Files.Path); err != nil {
		problems = append(problems, err.Error())
	}
	for _, d := range m.Databases {
		if sum, _, err := FileSHA256(d.Path); err != nil {
			problems = append(problems, "dump "+d.Name+" tidak terbaca: "+err.Error())
		} else if sum != d.SHA256 {
			problems = append(problems, "checksum dump "+d.Name+" berubah")
		}
	}
	return problems
}

func ManualInstructions(dir string, w *store.Website, dbs []store.DatabaseRow) string {
	var b strings.Builder
	fmt.Fprintf(&b, "SSH sumber tidak tersedia. Unduh backup %s dari hPanel → Websites → %s → Backups:\n", w.Domain, w.Domain)
	fmt.Fprintf(&b, "1. Backup file (seluruh folder domain), simpan sebagai: %s (atau files.zip)\n", filepath.Join(dir, "files.tar.gz"))
	for i, d := range dbs {
		fmt.Fprintf(&b, "%d. Backup database %s, simpan sebagai: %s\n", i+2, d.Name, filepath.Join(dir, "db-"+d.Name+".sql.gz"))
	}
	fmt.Fprintf(&b, "Setelah semua file ada, jalankan: migrator confirm MANUAL_BACKUP %s", w.Domain)
	return b.String()
}

func (e *Engine) ImportManual(ctx context.Context, it *store.PipelineItem, w *store.Website, dbs []store.DatabaseRow) (*Manifest, error) {
	dir := e.Dir(it, false)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	filesPath := filepath.Join(dir, "files.tar.gz")
	if _, err := os.Stat(filesPath); errors.Is(err, os.ErrNotExist) {
		zipPath := filepath.Join(dir, "files.zip")
		if _, zerr := os.Stat(zipPath); zerr == nil {
			if err := ZipToTarGz(zipPath, filesPath); err != nil {
				return nil, fmt.Errorf("%w: konversi files.zip gagal: %v", ErrManualBackupIncomplete, err)
			}
		} else {
			return nil, fmt.Errorf("%w: %s tidak ditemukan", ErrManualBackupIncomplete, filesPath)
		}
	}
	stats, err := InspectTarGz(filesPath)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrManualBackupIncomplete, err)
	}
	sum, size, err := FileSHA256(filesPath)
	if err != nil {
		return nil, err
	}
	m := &Manifest{Domain: it.SourceDomain, Folder: w.UnitPath, BatchID: it.BatchID, Manual: true,
		Files: FileEntry{Path: filesPath, Size: size, SHA256: sum, FileCount: stats.Files, Entries: stats.Entries, InventoryFileCount: w.FileCount}}
	if err := e.Store.SaveArtifact(ctx, store.Artifact{ItemID: it.ID, Kind: KindFiles, Path: filesPath, Size: size, SHA256: sum, Validated: true}); err != nil {
		return nil, err
	}
	for _, d := range dbs {
		p := filepath.Join(dir, "db-"+d.Name+".sql.gz")
		st, err := ParseDumpFile(p)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrManualBackupIncomplete, err)
		}
		if len(st.Tables) == 0 {
			return nil, fmt.Errorf("%w: dump %s tidak berisi tabel", ErrManualBackupIncomplete, d.Name)
		}
		dsum, dsize, _ := FileSHA256(p)
		m.Databases = append(m.Databases, DBEntry{Name: d.Name, Path: p, Size: dsize, SHA256: dsum, Tables: st.Tables, Rows: st.Rows})
		if err := e.Store.SaveArtifact(ctx, store.Artifact{ItemID: it.ID, Kind: KindDBPrefix + d.Name, Path: p, Size: dsize, SHA256: dsum, Validated: true}); err != nil {
			return nil, err
		}
	}
	if _, err := e.WriteManifest(ctx, it, m); err != nil {
		return nil, err
	}
	return m, nil
}

func ZipToTarGz(src, dst string) error {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer zr.Close()
	tmp := dst + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, zf := range zr.File {
		name := strings.TrimPrefix(path.Clean("/"+zf.Name), "/")
		if name == "" || strings.HasPrefix(name, "..") {
			continue
		}
		hdr, err := tar.FileInfoHeader(zf.FileInfo(), "")
		if err != nil {
			f.Close()
			return err
		}
		hdr.Name = "./" + name
		if zf.FileInfo().IsDir() {
			hdr.Name += "/"
		}
		if err := tw.WriteHeader(hdr); err != nil {
			f.Close()
			return err
		}
		if !zf.FileInfo().IsDir() {
			rc, err := zf.Open()
			if err != nil {
				f.Close()
				return err
			}
			_, err = io.Copy(tw, rc)
			rc.Close()
			if err != nil {
				f.Close()
				return err
			}
		}
	}
	if err := tw.Close(); err != nil {
		f.Close()
		return err
	}
	if err := gz.Close(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}
