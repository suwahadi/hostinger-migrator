package email

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"hostinger-migrator/internal/gate"
	"hostinger-migrator/internal/hostinger"
	"hostinger-migrator/internal/imapx"
	"hostinger-migrator/internal/pipeline"
	"hostinger-migrator/internal/secrets"
	"hostinger-migrator/internal/store"
	"hostinger-migrator/internal/verify"
)

const (
	StepApproval     = ""
	StepPassword     = "PASSWORD"
	StepBackup       = "BACKUP"
	StepDeleteSource = "HAPUS_SUMBER"
	StepSourceRemove = "LAYANAN_SUMBER"
	StepDestSetup    = "SETUP_TUJUAN"
	StepCreate       = "BUAT"
	StepRestore      = "RESTORE"
	StepExtras       = "ALIAS"
	StepDNS          = "DNS"
	StepVerify       = "VERIFIKASI"
	StepDone         = "SELESAI"
)

type Stage struct {
	Runner  *pipeline.Runner
	IMAP    string
	TLS     bool
	Timeout time.Duration
}

func (s *Stage) Handlers() map[string]pipeline.Handler {
	return map[string]pipeline.Handler{pipeline.StateEmailRunning: s.handle}
}

type MsgMeta struct {
	File   string   `json:"file"`
	UID    uint32   `json:"uid"`
	Flags  []string `json:"flags"`
	Date   string   `json:"tanggal"`
	SHA256 string   `json:"sha256"`
	Size   int      `json:"ukuran"`
}

type FolderManifest struct {
	Name        string    `json:"nama"`
	ServerCount int       `json:"jumlah_di_server"`
	Count       int       `json:"jumlah_dibackup"`
	Bytes       int64     `json:"ukuran_byte"`
	Messages    []MsgMeta `json:"pesan"`
}

type MailboxManifest struct {
	Address   string           `json:"alamat"`
	CreatedAt string           `json:"dibuat"`
	Folders   []FolderManifest `json:"folder"`
}

func (m *MailboxManifest) Total() int {
	n := 0
	for _, f := range m.Folders {
		n += f.Count
	}
	return n
}

type Extras struct {
	Aliases    []map[string]any `json:"alias"`
	Forwarders []map[string]any `json:"forwarder"`
	Catchalls  []map[string]any `json:"catchall"`
	Autoreplys []map[string]any `json:"autoreply"`
}

func (s *Stage) dir(ic *pipeline.ItemContext) string {
	return filepath.Join(s.Runner.Backup.Dir(ic.Item, false), "email")
}

func (s *Stage) mailboxDir(ic *pipeline.ItemContext, address string) string {
	return filepath.Join(s.dir(ic), url.PathEscape(address))
}

func (s *Stage) setState(ic *pipeline.ItemContext, st string) error {
	return ic.Save(func(it *store.PipelineItem) { it.EmailState = st })
}

func (s *Stage) mailboxes(ctx context.Context, ic *pipeline.ItemContext) ([]store.MailboxRow, error) {
	return s.Runner.Store.ListMailboxes(ctx, []string{ic.Item.SourceDomain})
}

func (s *Stage) password(address string) (string, error) {
	pw, err := s.Runner.Secrets.Get(secrets.KindMailboxPassword, address)
	if err == nil {
		return pw, nil
	}
	if !errors.Is(err, secrets.ErrNotFound) {
		return "", err
	}
	pw, err = secrets.GeneratePassword(16)
	if err != nil {
		return "", err
	}
	return pw, s.Runner.Secrets.Put(secrets.KindMailboxPassword, address, pw)
}

func (s *Stage) dial(ctx context.Context, address string) (*imapx.Client, error) {
	pw, err := s.password(address)
	if err != nil {
		return nil, err
	}
	c, err := imapx.Dial(ctx, s.IMAP, s.TLS, s.Timeout)
	if err != nil {
		return nil, fmt.Errorf("koneksi IMAP %s gagal: %w", s.IMAP, err)
	}
	if err := c.Login(address, pw); err != nil {
		c.Close()
		return nil, fmt.Errorf("login IMAP %s: %w", address, err)
	}
	return c, nil
}

func isClientErr(err error) bool {
	var apiErr *hostinger.APIError
	return errors.As(err, &apiErr) && (apiErr.Kind == hostinger.KindClient || apiErr.Kind == hostinger.KindNotFound)
}

func (s *Stage) handle(ctx context.Context, ic *pipeline.ItemContext) (string, error) {
	r := s.Runner
	approval, err := gate.For(ctx, r.Store, ic.Batch.ID, gate.KindDeleteEmail, ic.Item.SourceDomain)
	if err != nil {
		return "", pipeline.ErrIdle
	}
	boxes, err := s.mailboxes(ctx, ic)
	if err != nil {
		return "", err
	}
	for {
		var next string
		switch ic.Item.EmailState {
		case StepApproval:
			next = StepPassword
		case StepPassword:
			next, err = s.changePasswords(ctx, ic, approval, boxes)
		case StepBackup:
			next, err = s.backup(ctx, ic, boxes)
		case StepDeleteSource:
			next, err = s.deleteSource(ctx, ic, approval, boxes)
		case StepSourceRemove:
			next, err = s.manualStep(ctx, ic, "MANUAL_EMAIL_SOURCE_REMOVE", StepDestSetup, fmt.Sprintf(
				"Mailbox sumber %s sudah dihapus dan backup tervalidasi.\n1. hPanel akun sumber %s → Emails → %s → hapus/lepaskan layanan email domain ini agar bisa diaktifkan di akun tujuan.\n2. Jalankan: migrator confirm MANUAL_EMAIL_SOURCE_REMOVE %s",
				ic.Item.SourceDomain, ic.Batch.SourceAccount, ic.Item.SourceDomain, ic.Item.SourceDomain))
		case StepDestSetup:
			next, err = s.destSetup(ctx, ic)
		case StepCreate:
			next, err = s.create(ctx, ic, boxes)
		case StepRestore:
			next, err = s.restore(ctx, ic, boxes)
		case StepExtras:
			next, err = s.extras(ctx, ic)
		case StepDNS:
			next, err = s.manualStep(ctx, ic, "MANUAL_EMAIL_DNS", StepVerify, fmt.Sprintf(
				"Periksa DNS email %s:\n1. hPanel akun tujuan %s → Emails → %s → DNS settings: catat MX, SPF, DKIM, DMARC yang diminta.\n2. Samakan di zone DNS domain (MX/SPF Hostinger biasanya tetap sama; DKIM bisa berbeda per akun). Record lain jangan diubah.\n3. Jalankan: migrator confirm MANUAL_EMAIL_DNS %s",
				ic.Item.SourceDomain, ic.Batch.DestAccount, ic.Item.SourceDomain, ic.Item.SourceDomain))
		case StepVerify:
			next, err = s.verify(ctx, ic, boxes)
		case StepDone:
			return pipeline.StateEmailDone, nil
		default:
			return "", fmt.Errorf("status email tidak dikenal: %s", ic.Item.EmailState)
		}
		if err != nil {
			return "", err
		}
		if err := s.setState(ic, next); err != nil {
			return "", err
		}
		r.Store.AppendAudit(ctx, nil, "worker", "email.step", ic.Item.SourceDomain, next, "")
	}
}

func (s *Stage) manualStep(ctx context.Context, ic *pipeline.ItemContext, code, next, instructions string) (string, error) {
	if _, ok := ic.ManualConfirmed(ctx, code); ok {
		return next, nil
	}
	return "", &pipeline.ManualError{Code: code, Instructions: instructions}
}

func (s *Stage) changePasswords(ctx context.Context, ic *pipeline.ItemContext, approval *gate.Approval, boxes []store.MailboxRow) (string, error) {
	if _, ok := ic.ManualConfirmed(ctx, "MANUAL_EMAIL_PASSWORD"); ok {
		return StepBackup, nil
	}
	api, err := ic.SourceAPI()
	if err != nil {
		return "", err
	}
	for _, b := range boxes {
		pw, err := s.password(b.Address)
		if err != nil {
			return "", err
		}
		if err := approval.Require(gate.KindDeleteEmail, ic.Item.SourceDomain); err != nil {
			return "", err
		}
		_, err = api.Do(ctx, hostinger.Request{Endpoint: "mail.mailboxes.password.change", PathParams: map[string]string{"mailboxId": b.APIID}, Body: map[string]any{"password": pw}}, nil)
		if err != nil {
			if isClientErr(err) {
				return "", &pipeline.ManualError{Code: "MANUAL_EMAIL_PASSWORD", Instructions: fmt.Sprintf("API menolak mengganti password mailbox (%v).\nGanti password setiap mailbox %s di hPanel sumber dengan password baru dari 'migrator email passwords --domain %s', lalu jalankan: migrator confirm MANUAL_EMAIL_PASSWORD %s", err, ic.Item.SourceDomain, ic.Item.SourceDomain, ic.Item.SourceDomain)}
			}
			return "", err
		}
		gate.Record(ctx, s.Runner.Store, approval, "email.password_change", b.Address, "OK", "password mailbox sumber diganti untuk backup IMAP")
	}
	return StepBackup, nil
}

func (s *Stage) backup(ctx context.Context, ic *pipeline.ItemContext, boxes []store.MailboxRow) (string, error) {
	for _, b := range boxes {
		if m, err := readManifest(filepath.Join(s.mailboxDir(ic, b.Address), "manifest.json")); err == nil && manifestIntact(s.mailboxDir(ic, b.Address), m) {
			continue
		}
		if err := s.backupMailbox(ctx, ic, b.Address); err != nil {
			return "", err
		}
	}
	if err := s.backupExtras(ctx, ic); err != nil {
		return "", err
	}
	return StepDeleteSource, nil
}

func folderDir(base, name string) string {
	return filepath.Join(base, url.PathEscape(name))
}

func (s *Stage) backupMailbox(ctx context.Context, ic *pipeline.ItemContext, address string) error {
	c, err := s.dial(ctx, address)
	if err != nil {
		return err
	}
	defer c.Logout()
	folders, err := c.List()
	if err != nil {
		return err
	}
	base := s.mailboxDir(ic, address)
	os.RemoveAll(base)
	m := &MailboxManifest{Address: address, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	for _, f := range folders {
		if !f.Selectable() {
			continue
		}
		count, err := c.Status(f.Name)
		if err != nil {
			return err
		}
		fd := folderDir(base, f.Name)
		if err := os.MkdirAll(fd, 0o700); err != nil {
			return err
		}
		fm := FolderManifest{Name: f.Name, ServerCount: count}
		i := 0
		err = c.FetchAll(f.Name, func(msg imapx.Message) error {
			i++
			name := fmt.Sprintf("%06d-%d.eml", i, msg.UID)
			if err := os.WriteFile(filepath.Join(fd, name), msg.Body, 0o600); err != nil {
				return err
			}
			sum := sha256.Sum256(msg.Body)
			fm.Messages = append(fm.Messages, MsgMeta{File: name, UID: msg.UID, Flags: msg.Flags, Date: msg.InternalDate, SHA256: hex.EncodeToString(sum[:]), Size: len(msg.Body)})
			fm.Bytes += int64(len(msg.Body))
			return nil
		})
		if err != nil {
			return fmt.Errorf("backup folder %s/%s gagal: %w", address, f.Name, err)
		}
		fm.Count = len(fm.Messages)
		if fm.Count < fm.ServerCount {
			return fmt.Errorf("backup %s/%s hanya %d dari %d pesan", address, f.Name, fm.Count, fm.ServerCount)
		}
		m.Folders = append(m.Folders, fm)
	}
	data, _ := json.MarshalIndent(m, "", "  ")
	return os.WriteFile(filepath.Join(base, "manifest.json"), data, 0o600)
}

func readManifest(p string) (*MailboxManifest, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var m MailboxManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func manifestIntact(base string, m *MailboxManifest) bool {
	for _, f := range m.Folders {
		for _, msg := range f.Messages {
			data, err := os.ReadFile(filepath.Join(folderDir(base, f.Name), msg.File))
			if err != nil {
				return false
			}
			sum := sha256.Sum256(data)
			if hex.EncodeToString(sum[:]) != msg.SHA256 {
				return false
			}
		}
	}
	return true
}

func (s *Stage) sourceOrder(ctx context.Context, ic *pipeline.ItemContext) string {
	boxes, _ := s.mailboxes(ctx, ic)
	for _, b := range boxes {
		if b.OrderID != "" {
			return b.OrderID
		}
	}
	return ic.Website.MailOrderID
}

func (s *Stage) backupExtras(ctx context.Context, ic *pipeline.ItemContext) error {
	p := filepath.Join(s.dir(ic), "konfigurasi-email.json")
	if _, err := os.Stat(p); err == nil {
		return nil
	}
	order := s.sourceOrder(ctx, ic)
	var ex Extras
	if order != "" {
		api, err := ic.SourceAPI()
		if err != nil {
			return err
		}
		for _, k := range []struct {
			id  string
			dst *[]map[string]any
		}{{"mail.aliases.list", &ex.Aliases}, {"mail.forwarders.list", &ex.Forwarders}, {"mail.catchalls.list", &ex.Catchalls}, {"mail.autoreplies.list", &ex.Autoreplys}} {
			list, err := hostinger.ListAll[map[string]any](ctx, api, hostinger.Request{Endpoint: k.id, PathParams: map[string]string{"orderId": order}}, 100)
			if err != nil {
				return fmt.Errorf("%s gagal: %w", k.id, err)
			}
			*k.dst = list
		}
	}
	if err := os.MkdirAll(s.dir(ic), 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(ex, "", "  ")
	return os.WriteFile(p, data, 0o600)
}

func (s *Stage) deleteSource(ctx context.Context, ic *pipeline.ItemContext, approval *gate.Approval, boxes []store.MailboxRow) (string, error) {
	for _, b := range boxes {
		m, err := readManifest(filepath.Join(s.mailboxDir(ic, b.Address), "manifest.json"))
		if err != nil || !manifestIntact(s.mailboxDir(ic, b.Address), m) {
			return "", fmt.Errorf("backup %s tidak valid; mailbox sumber tidak dihapus", b.Address)
		}
	}
	api, err := ic.SourceAPI()
	if err != nil {
		return "", err
	}
	for _, b := range boxes {
		if err := approval.Require(gate.KindDeleteEmail, ic.Item.SourceDomain); err != nil {
			return "", err
		}
		_, err := api.Do(ctx, hostinger.Request{Endpoint: "mail.mailboxes.delete", PathParams: map[string]string{"mailboxId": b.APIID}}, nil)
		if err != nil && !errors.Is(err, hostinger.ErrNotFound) {
			if isClientErr(err) {
				return "", &pipeline.ManualError{Code: "MANUAL_EMAIL_DELETE", Instructions: fmt.Sprintf("API menolak menghapus mailbox (%v). Hapus semua mailbox %s di hPanel sumber, lalu jalankan: migrator confirm MANUAL_EMAIL_DELETE %s", err, ic.Item.SourceDomain, ic.Item.SourceDomain)}
			}
			return "", err
		}
		gate.Record(ctx, s.Runner.Store, approval, "email.delete_source", b.Address, "OK", "mailbox sumber dihapus setelah backup tervalidasi")
	}
	return StepSourceRemove, nil
}

func (s *Stage) destOrder(ctx context.Context, ic *pipeline.ItemContext) (string, error) {
	api, err := ic.DestAPI()
	if err != nil {
		return "", err
	}
	orders, err := api.ListMailOrders(ctx)
	if err != nil {
		return "", err
	}
	for _, o := range orders {
		if strings.EqualFold(o.DomainName(), ic.Item.SourceDomain) {
			return o.ID, nil
		}
	}
	return "", nil
}

func (s *Stage) destSetup(ctx context.Context, ic *pipeline.ItemContext) (string, error) {
	instr := fmt.Sprintf("Aktifkan email untuk %s di akun tujuan %s:\n1. hPanel akun %s → Emails → tambahkan domain %s (Free Business Email bawaan paket atau paket email).\n2. Jangan buat mailbox; tool akan membuatnya.\n3. Jalankan: migrator confirm MANUAL_EMAIL_SETUP %s",
		ic.Item.SourceDomain, ic.Batch.DestAccount, ic.Batch.DestAccount, ic.Item.SourceDomain, ic.Item.SourceDomain)
	if _, ok := ic.ManualConfirmed(ctx, "MANUAL_EMAIL_SETUP"); !ok {
		return "", &pipeline.ManualError{Code: "MANUAL_EMAIL_SETUP", Instructions: instr}
	}
	order, err := s.destOrder(ctx, ic)
	if err != nil {
		return "", err
	}
	if order == "" {
		return "", &pipeline.ManualError{Code: "MANUAL_EMAIL_SETUP", Instructions: "Layanan email " + ic.Item.SourceDomain + " belum terlihat di akun tujuan lewat API.\n" + instr}
	}
	return StepCreate, nil
}

func (s *Stage) create(ctx context.Context, ic *pipeline.ItemContext, boxes []store.MailboxRow) (string, error) {
	if _, ok := ic.ManualConfirmed(ctx, "MANUAL_EMAIL_CREATE"); ok {
		return StepRestore, nil
	}
	order, err := s.destOrder(ctx, ic)
	if err != nil {
		return "", err
	}
	api, err := ic.DestAPI()
	if err != nil {
		return "", err
	}
	for _, b := range boxes {
		pw, err := s.password(b.Address)
		if err != nil {
			return "", err
		}
		local := strings.SplitN(b.Address, "@", 2)[0]
		_, err = api.Do(ctx, hostinger.Request{Endpoint: "mail.mailboxes.create", PathParams: map[string]string{"orderId": order}, Body: map[string]any{"local_part": local, "password": pw}}, nil)
		if err != nil && !errors.Is(err, hostinger.ErrConflict) {
			if isClientErr(err) {
				var list []string
				for _, x := range boxes {
					list = append(list, x.Address)
				}
				return "", &pipeline.ManualError{Code: "MANUAL_EMAIL_CREATE", Instructions: fmt.Sprintf("API menolak membuat mailbox (%v).\nBuat mailbox berikut di hPanel akun %s → Emails → %s, dengan password dari 'migrator email passwords --domain %s':\n- %s\nLalu jalankan: migrator confirm MANUAL_EMAIL_CREATE %s",
					err, ic.Batch.DestAccount, ic.Item.SourceDomain, ic.Item.SourceDomain, strings.Join(list, "\n- "), ic.Item.SourceDomain)}
			}
			return "", err
		}
	}
	return StepRestore, nil
}

type progress map[string]int

func (s *Stage) restore(ctx context.Context, ic *pipeline.ItemContext, boxes []store.MailboxRow) (string, error) {
	for _, b := range boxes {
		base := s.mailboxDir(ic, b.Address)
		m, err := readManifest(filepath.Join(base, "manifest.json"))
		if err != nil {
			return "", fmt.Errorf("manifest email %s tidak ada: %w", b.Address, err)
		}
		progPath := filepath.Join(base, "restore-progress.json")
		prog := progress{}
		if data, err := os.ReadFile(progPath); err == nil {
			json.Unmarshal(data, &prog)
		}
		c, err := s.dial(ctx, b.Address)
		if err != nil {
			return "", err
		}
		for _, f := range m.Folders {
			if !strings.EqualFold(f.Name, "INBOX") {
				if err := c.Create(f.Name); err != nil {
					c.Logout()
					return "", fmt.Errorf("membuat folder %s gagal: %w", f.Name, err)
				}
			}
			done := prog[f.Name]
			if !strings.EqualFold(f.Name, "INBOX") {
				if existing, err := c.Status(f.Name); err == nil && existing > done {
					done = existing
				}
			}
			if done >= len(f.Messages) {
				prog[f.Name] = len(f.Messages)
				continue
			}
			for i := done; i < len(f.Messages); i++ {
				msg := f.Messages[i]
				body, err := os.ReadFile(filepath.Join(folderDir(base, f.Name), msg.File))
				if err != nil {
					c.Logout()
					return "", err
				}
				if err := c.Append(f.Name, msg.Flags, msg.Date, body); err != nil {
					c.Close()
					data, _ := json.Marshal(prog)
					os.WriteFile(progPath, data, 0o600)
					return "", fmt.Errorf("APPEND %s/%s pesan ke-%d gagal: %w", b.Address, f.Name, i+1, err)
				}
				prog[f.Name] = i + 1
				if (i+1)%50 == 0 {
					data, _ := json.Marshal(prog)
					os.WriteFile(progPath, data, 0o600)
				}
			}
			data, _ := json.Marshal(prog)
			os.WriteFile(progPath, data, 0o600)
		}
		c.Logout()
	}
	return StepExtras, nil
}

func (s *Stage) extras(ctx context.Context, ic *pipeline.ItemContext) (string, error) {
	data, err := os.ReadFile(filepath.Join(s.dir(ic), "konfigurasi-email.json"))
	if err != nil {
		return StepDNS, nil
	}
	var ex Extras
	json.Unmarshal(data, &ex)
	order, err := s.destOrder(ctx, ic)
	if err != nil {
		return "", err
	}
	api, err := ic.DestAPI()
	if err != nil {
		return "", err
	}
	dest, err := api.ListMailboxes(ctx, order)
	if err != nil {
		return "", err
	}
	idByAddr := map[string]string{}
	for _, d := range dest {
		idByAddr[strings.ToLower(d.Address)] = d.ID
	}
	owner := func(e map[string]any) string {
		mb, _ := e["mailbox"].(map[string]any)
		addr, _ := mb["address"].(string)
		return idByAddr[strings.ToLower(addr)]
	}
	var problems []string
	post := func(endpoint, mailbox string, body map[string]any) {
		if mailbox == "" {
			problems = append(problems, endpoint+": mailbox pemilik tidak ditemukan di tujuan")
			return
		}
		_, err := api.Do(ctx, hostinger.Request{Endpoint: endpoint, PathParams: map[string]string{"mailboxId": mailbox}, Body: body}, nil)
		if err != nil && !errors.Is(err, hostinger.ErrConflict) {
			problems = append(problems, endpoint+": "+err.Error())
		}
	}
	for _, a := range ex.Aliases {
		addr, _ := a["address"].(string)
		post("mail.aliases.create", owner(a), map[string]any{"local_part": strings.SplitN(addr, "@", 2)[0]})
	}
	for _, f := range ex.Forwarders {
		post("mail.forwarders.create", owner(f), map[string]any{"destination": f["destination"], "is_keep_copy_enabled": f["is_keep_copy_enabled"]})
	}
	for _, c := range ex.Catchalls {
		post("mail.catchalls.create", owner(c), nil)
	}
	for _, a := range ex.Autoreplys {
		body := map[string]any{"subject": a["subject"], "body": a["body"]}
		for _, k := range []string{"display_name", "starts_at", "ends_at"} {
			if v, ok := a[k]; ok && v != nil {
				body[k] = v
			}
		}
		post("mail.autoreplies.create", owner(a), body)
	}
	if len(problems) > 0 {
		s.Runner.Store.ReplaceVerifications(ctx, ic.Item.ID, "EMAIL_ALIAS", []store.Verification{{CheckType: "EMAIL_ALIAS", Result: verify.Warn, Detail: strings.Join(problems, "; ")}})
	} else {
		s.Runner.Store.ReplaceVerifications(ctx, ic.Item.ID, "EMAIL_ALIAS", []store.Verification{{CheckType: "EMAIL_ALIAS", Result: verify.Pass, Detail: fmt.Sprintf("%d alias, %d forwarder, %d catch-all, %d auto-reply dibuat ulang", len(ex.Aliases), len(ex.Forwarders), len(ex.Catchalls), len(ex.Autoreplys))}})
	}
	return StepDNS, nil
}

func (s *Stage) verify(ctx context.Context, ic *pipeline.ItemContext, boxes []store.MailboxRow) (string, error) {
	var vs []store.Verification
	failed := false
	for _, b := range boxes {
		m, err := readManifest(filepath.Join(s.mailboxDir(ic, b.Address), "manifest.json"))
		if err != nil {
			return "", err
		}
		c, err := s.dial(ctx, b.Address)
		if err != nil {
			vs = append(vs, store.Verification{CheckType: "EMAIL_LOGIN_" + b.Address, Result: verify.Fail, Detail: err.Error()})
			failed = true
			continue
		}
		vs = append(vs, store.Verification{CheckType: "EMAIL_LOGIN_" + b.Address, Result: verify.Pass, Detail: "login IMAP dengan password baru berhasil"})
		for _, f := range m.Folders {
			n, err := c.Status(f.Name)
			res, detail := verify.Pass, fmt.Sprintf("%s: sumber %d, tujuan %d pesan", f.Name, f.Count, n)
			if err != nil || n < f.Count {
				res, failed = verify.Fail, true
				if err != nil {
					detail = f.Name + ": " + err.Error()
				}
			}
			vs = append(vs, store.Verification{CheckType: "EMAIL_FOLDER_" + b.Address + "/" + f.Name, Result: res, Detail: detail})
		}
		c.Logout()
	}
	var logins, folders []store.Verification
	for _, v := range vs {
		if strings.HasPrefix(v.CheckType, "EMAIL_LOGIN_") {
			logins = append(logins, v)
		} else {
			folders = append(folders, v)
		}
	}
	if err := s.Runner.Store.ReplaceVerifications(ctx, ic.Item.ID, "EMAIL_LOGIN_", logins); err != nil {
		return "", err
	}
	if err := s.Runner.Store.ReplaceVerifications(ctx, ic.Item.ID, "EMAIL_FOLDER_", folders); err != nil {
		return "", err
	}
	if failed {
		return "", errors.New("verifikasi email gagal: jumlah pesan atau login tidak sesuai")
	}
	return StepDone, nil
}

func Manifests(backupDir string, it *store.PipelineItem) ([]MailboxManifest, error) {
	base := filepath.Join(backupDir, it.BatchID, it.SourceDomain, "email")
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil, err
	}
	var out []MailboxManifest
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if m, err := readManifest(filepath.Join(base, e.Name(), "manifest.json")); err == nil {
			out = append(out, *m)
		}
	}
	return out, nil
}
