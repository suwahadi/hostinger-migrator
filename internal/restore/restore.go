package restore

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"hostinger-migrator/internal/backup"
	"hostinger-migrator/internal/detector"
	"hostinger-migrator/internal/hostinger"
	"hostinger-migrator/internal/logx"
	"hostinger-migrator/internal/secrets"
	"hostinger-migrator/internal/sshx"
	"hostinger-migrator/internal/store"
)

var (
	ErrManualDB  = errors.New("database tujuan perlu dibuat manual")
	ErrManualPHP = errors.New("versi PHP tujuan perlu disamakan manual")
)

type Restorer struct {
	Store    *store.Store
	Secrets  *secrets.Store
	Backup   *backup.Engine
	API      *hostinger.Client
	SSH      *sshx.Client
	Account  store.HostingAccount
	Home     string
	Redactor *logx.Redactor
	Log      *slog.Logger
	Timeout  time.Duration
}

func (r *Restorer) redactor() *logx.Redactor {
	if r.Redactor == nil {
		return logx.Global
	}
	return r.Redactor
}

func (r *Restorer) WorkDir(it *store.PipelineItem) string {
	return path.Join(r.Home, ".migrator", strconv.FormatInt(it.ID, 10))
}

func (r *Restorer) run(ctx context.Context, cmd string, env map[string]string) (string, error) {
	wait := r.Timeout
	if wait <= 0 {
		wait = 6 * time.Hour
	}
	cctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	res, err := r.SSH.Run(cctx, cmd, env)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return res.Stdout, fmt.Errorf("perintah gagal (kode %d): %s", res.ExitCode, r.redactor().String(strings.TrimSpace(res.Stderr)))
	}
	return res.Stdout, nil
}

func (r *Restorer) uploadVerified(ctx context.Context, local, remote, sha string) error {
	if got, err := r.SSH.RemoteSHA256(ctx, remote); err == nil && got == sha {
		return nil
	}
	if _, err := r.SSH.Upload(ctx, local, remote); err != nil {
		return fmt.Errorf("upload %s gagal: %w", filepath.Base(local), err)
	}
	got, err := r.SSH.RemoteSHA256(ctx, remote)
	if err != nil {
		return err
	}
	if got != sha {
		return fmt.Errorf("checksum %s di server (%s) tidak sama dengan lokal (%s)", filepath.Base(local), got, sha)
	}
	return nil
}

func (r *Restorer) Files(ctx context.Context, it *store.PipelineItem, m *backup.Manifest, destUnit string) (int64, error) {
	work := r.WorkDir(it)
	remote := path.Join(work, "files.tar.gz")
	marker := path.Join(work, "extracted-"+m.Files.SHA256[:16])
	if out, _ := r.run(ctx, "test -f "+sshx.ShellQuote(marker)+" && cat "+sshx.ShellQuote(marker), nil); strings.TrimSpace(out) != "" {
		n, _ := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
		return n, nil
	}
	if err := r.uploadVerified(ctx, m.Files.Path, remote, m.Files.SHA256); err != nil {
		return 0, err
	}
	stash := path.Join(work, "isi-bawaan-"+time.Now().UTC().Format("20060102150405"))
	q := sshx.ShellQuote
	cmd := "set -e; mkdir -p " + q(destUnit) + " " + q(stash) + "; " +
		"find " + q(destUnit) + " -mindepth 1 -maxdepth 1 -exec mv -t " + q(stash) + " {} +; " +
		"tar -xzf " + q(remote) + " -C " + q(destUnit) + "; " +
		"find " + q(destUnit) + " -type f | wc -l"
	out, err := r.run(ctx, cmd, nil)
	if err != nil {
		return 0, fmt.Errorf("ekstrak arsip gagal: %w", err)
	}
	count, _ := strconv.ParseInt(strings.TrimSpace(lastLine(out)), 10, 64)
	if count != m.Files.FileCount {
		return count, fmt.Errorf("jumlah file di tujuan %d, arsip %d", count, m.Files.FileCount)
	}
	if _, err := r.run(ctx, "printf '%s' "+q(strconv.FormatInt(count, 10))+" > "+q(marker)+"; touch "+q(path.Join(work, "restore.marker")), nil); err != nil {
		return count, err
	}
	return count, nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

func DestName(destUser, sourceName string) string {
	suffix := sourceName
	if i := strings.Index(sourceName, "_"); i >= 0 && strings.HasPrefix(sourceName, "u") {
		suffix = sourceName[i+1:]
	}
	name := destUser + "_" + suffix
	if len(name) > 64 {
		name = name[:64]
	}
	return name
}

func (r *Restorer) destDBs(it *store.PipelineItem, m *backup.Manifest) []store.DestDB {
	existing := map[string]store.DestDB{}
	for _, d := range it.DestDBs {
		existing[d.Source] = d
	}
	var out []store.DestDB
	for _, db := range m.Databases {
		if d, ok := existing[db.Name]; ok {
			out = append(out, d)
			continue
		}
		name := DestName(r.Account.ID, db.Name)
		out = append(out, store.DestDB{Source: db.Name, Name: name, User: name, Host: "localhost"})
	}
	return out
}

func (r *Restorer) secretID(name string) string {
	return r.Account.ID + ":" + name
}

func (r *Restorer) password(name string) (string, error) {
	pw, err := r.Secrets.Get(secrets.KindDBPassword, r.secretID(name))
	if err == nil {
		return pw, nil
	}
	if !errors.Is(err, secrets.ErrNotFound) {
		return "", err
	}
	pw, err = secrets.GeneratePassword(20)
	if err != nil {
		return "", err
	}
	if err := r.Secrets.Put(secrets.KindDBPassword, r.secretID(name), pw); err != nil {
		return "", err
	}
	return pw, nil
}

func (r *Restorer) PlanDatabases(it *store.PipelineItem, m *backup.Manifest) []store.DestDB {
	return r.destDBs(it, m)
}

func (r *Restorer) CreateDatabases(ctx context.Context, it *store.PipelineItem, m *backup.Manifest, dummy string, manualConfirmed bool) ([]store.DestDB, error) {
	dbs := r.destDBs(it, m)
	if len(dbs) == 0 {
		return nil, nil
	}
	existing, err := r.API.ListDatabases(ctx, r.Account.ID)
	if err != nil {
		return nil, fmt.Errorf("daftar database tujuan tidak terbaca: %w", err)
	}
	have := map[string]bool{}
	for _, d := range existing {
		have[d.Name] = true
	}
	var missing []string
	for i := range dbs {
		d := &dbs[i]
		if have[d.Name] {
			d.Created = true
			continue
		}
		if manualConfirmed {
			missing = append(missing, d.Name)
			continue
		}
		pw, err := r.password(d.Name)
		if err != nil {
			return dbs, err
		}
		_, err = r.API.Do(ctx, hostinger.Request{Endpoint: "hosting.databases.create", PathParams: map[string]string{"username": r.Account.ID},
			Body: map[string]any{"name": d.Name, "user": d.User, "password": pw, "website_domain": dummy}}, nil)
		if err != nil {
			var apiErr *hostinger.APIError
			if errors.As(err, &apiErr) && (apiErr.Kind == hostinger.KindClient || apiErr.Kind == hostinger.KindNotFound) {
				return dbs, fmt.Errorf("%w: %v", ErrManualDB, err)
			}
			if errors.Is(err, hostinger.ErrUncertain) {
				return dbs, fmt.Errorf("pembuatan database %s tidak pasti, dicek ulang pada percobaan berikutnya: %w", d.Name, err)
			}
			return dbs, err
		}
		d.Created = true
	}
	if len(missing) > 0 {
		return dbs, fmt.Errorf("%w: database %s belum ada di akun tujuan", ErrManualDB, strings.Join(missing, ", "))
	}
	return dbs, nil
}

func (r *Restorer) ManualDBInstructions(it *store.PipelineItem, dbs []store.DestDB) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Buat database berikut di hPanel akun %s → Databases → Management:\n", r.Account.ID)
	for _, d := range dbs {
		if d.Created {
			continue
		}
		fmt.Fprintf(&b, "- Nama database: %s · User: %s · Password: lihat 'migrator pending --tampilkan-rahasia'\n", d.Name, d.User)
	}
	fmt.Fprintf(&b, "Lalu jalankan: migrator confirm MANUAL_CREATE_DB %s", it.SourceDomain)
	return b.String()
}

func (r *Restorer) Import(ctx context.Context, it *store.PipelineItem, m *backup.Manifest, dbs []store.DestDB) ([]store.DestDB, error) {
	q := sshx.ShellQuote
	work := r.WorkDir(it)
	for i := range dbs {
		d := &dbs[i]
		var src *backup.DBEntry
		for j := range m.Databases {
			if m.Databases[j].Name == d.Source {
				src = &m.Databases[j]
			}
		}
		if src == nil {
			return dbs, fmt.Errorf("dump %s tidak ada di manifest", d.Source)
		}
		pw, err := r.password(d.Name)
		if err != nil {
			return dbs, err
		}
		env := map[string]string{"MYSQL_PWD": pw}
		remote := path.Join(work, "db-"+d.Source+".sql.gz")
		if err := r.uploadVerified(ctx, src.Path, remote, src.SHA256); err != nil {
			return dbs, err
		}
		conn := " -h " + q(d.Host) + " -u " + q(d.User) + " "
		drop := "set -o pipefail; mysql -N -B" + conn + "-e \"SELECT CONCAT('DROP TABLE IF EXISTS \\`', table_name, '\\`;') FROM information_schema.tables WHERE table_schema = DATABASE()\" " + q(d.Name) +
			" | { echo 'SET FOREIGN_KEY_CHECKS=0;'; cat; } | mysql" + conn + q(d.Name)
		if _, err := r.run(ctx, drop, env); err != nil {
			return dbs, fmt.Errorf("mengosongkan database %s gagal: %w", d.Name, err)
		}
		if _, err := r.run(ctx, "set -o pipefail; gunzip -c "+q(remote)+" | mysql"+conn+q(d.Name), env); err != nil {
			return dbs, fmt.Errorf("import %s gagal: %w", d.Name, err)
		}
		check := filepath.Join(r.Backup.Dir(it, false), "cek-tujuan-"+d.Name+".sql.gz")
		_, _, dstStats, err := r.Backup.DumpTo(ctx, r.SSH, backup.DBCredential{Name: d.Name, User: d.User, Password: pw, Host: d.Host}, check)
		os.Remove(check)
		if err != nil {
			return dbs, fmt.Errorf("validasi import %s gagal: %w", d.Name, err)
		}
		srcStats := backup.DumpStats{Tables: src.Tables, Rows: src.Rows}
		if diffs := backup.CompareDumps(srcStats, dstStats); len(diffs) > 0 {
			return dbs, fmt.Errorf("jumlah baris %s tidak cocok: %s", d.Name, strings.Join(diffs, "; "))
		}
		d.Imported = true
	}
	return dbs, nil
}

func (r *Restorer) UpdateConfigs(ctx context.Context, destUnit string, dbRows []store.DatabaseRow, dbs []store.DestDB) error {
	q := sshx.ShellQuote
	for _, row := range dbRows {
		if row.Source == "" || row.Source == "api" {
			continue
		}
		var d *store.DestDB
		for i := range dbs {
			if dbs[i].Source == row.Name {
				d = &dbs[i]
			}
		}
		if d == nil {
			continue
		}
		pw, err := r.password(d.Name)
		if err != nil {
			return err
		}
		file := path.Join(destUnit, row.Source)
		if _, err := r.run(ctx, "test -f "+q(file+".migrator-orig")+" || cp -p "+q(file)+" "+q(file+".migrator-orig"), nil); err != nil {
			return fmt.Errorf("salinan asli %s gagal dibuat: %w", row.Source, err)
		}
		orig, err := r.SSH.ReadFile(file+".migrator-orig", 1<<20)
		if err != nil {
			return err
		}
		updated, err := detector.UpdateCredentials(row.Source, string(orig), detector.Credential{Name: d.Name, User: d.User, Password: pw, Host: d.Host})
		if err != nil {
			return err
		}
		if err := r.SSH.WriteFile(file, []byte(updated)); err != nil {
			return fmt.Errorf("menulis %s gagal: %w", row.Source, err)
		}
	}
	return nil
}

func (r *Restorer) AfterRestore(ctx context.Context, w *store.Website, destUnit string) error {
	if w.SiteType != detector.TypeLaravel {
		return nil
	}
	q := sshx.ShellQuote
	_, err := r.run(ctx, "cd "+q(destUnit)+" && { php artisan config:clear >/dev/null 2>&1; rm -f bootstrap/cache/config.php; }", nil)
	return err
}

func (r *Restorer) MatchPHP(ctx context.Context, sourceVersion, dummy string) (string, error) {
	if sourceVersion == "" {
		return "", nil
	}
	cur, err := r.API.GetPHPDetails(ctx, r.Account.ID, dummy)
	if err == nil && cur.Version == sourceVersion {
		return cur.Version, nil
	}
	_, err = r.API.Do(ctx, hostinger.Request{Endpoint: "hosting.php.version.update", PathParams: map[string]string{"username": r.Account.ID, "domain": dummy}, Body: map[string]any{"version": sourceVersion}}, nil)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrManualPHP, err)
	}
	after, err := r.API.GetPHPDetails(ctx, r.Account.ID, dummy)
	if err != nil {
		return "", err
	}
	if after.Version != sourceVersion {
		return after.Version, fmt.Errorf("%w: versi PHP tujuan %s, sumber %s", ErrManualPHP, after.Version, sourceVersion)
	}
	return after.Version, nil
}

func (r *Restorer) Cleanup(ctx context.Context, it *store.PipelineItem) error {
	_, err := r.run(ctx, "rm -f "+sshx.ShellQuote(path.Join(r.WorkDir(it), "files.tar.gz"))+" "+sshx.ShellQuote(path.Join(r.WorkDir(it), "db-"))+"*.sql.gz", nil)
	return err
}
