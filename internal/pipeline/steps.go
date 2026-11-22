package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"
	"sync"

	"hostinger-migrator/internal/provision"
	"hostinger-migrator/internal/restore"
	"hostinger-migrator/internal/secrets"
	"hostinger-migrator/internal/sshx"
	"hostinger-migrator/internal/store"
	"hostinger-migrator/internal/verify"
)

func (r *Runner) provision(ctx context.Context, ic *ItemContext) error {
	api, err := ic.DestAPI()
	if err != nil {
		return err
	}
	p := &provision.Provisioner{Client: api, Account: ic.DestAcc, PollInterval: r.ProvisionPoll, Timeout: r.ProvisionTimeout, Sleep: r.Sleep, Now: r.Now}
	var res *provision.Result
	if t, ok := ic.ManualConfirmed(ctx, "MANUAL_CREATE_SITE"); ok {
		dummy := strings.TrimSpace(strings.ToLower(t.Value))
		if dummy == "" {
			return &ManualError{Code: "MANUAL_CREATE_SITE", Instructions: "Nama domain sementara wajib diisi dengan --nilai.\n" + provision.ManualInstructions(ic.DestAcc.ID, ic.Item.SourceDomain)}
		}
		res, err = p.Find(ctx, dummy)
		if err != nil {
			return err
		}
		if res == nil {
			return &ManualError{Code: "MANUAL_CREATE_SITE", Instructions: fmt.Sprintf("Website %s tidak ditemukan di akun %s lewat API.\n", dummy, ic.DestAcc.ID) + provision.ManualInstructions(ic.DestAcc.ID, ic.Item.SourceDomain)}
		}
	} else {
		res, err = p.Ensure(ctx, ic.Item.DummyDomain, func(d string) error {
			return ic.Save(func(it *store.PipelineItem) { it.DummyDomain = d })
		})
		switch {
		case errors.Is(err, provision.ErrManual):
			return &ManualError{Code: "MANUAL_CREATE_SITE", Instructions: err.Error() + "\n" + provision.ManualInstructions(ic.DestAcc.ID, ic.Item.SourceDomain)}
		case errors.Is(err, provision.ErrPending):
			return fmt.Errorf("%w: %v", ErrWait, err)
		case err != nil:
			return err
		}
	}
	c, err := ic.SSH(ctx, ic.Batch.DestAccount)
	if err != nil {
		return err
	}
	out, err := c.Run(ctx, "test -d "+sshx.ShellQuote(res.UnitPath)+" && echo ada", nil)
	if err != nil {
		return err
	}
	if strings.TrimSpace(out.Stdout) != "ada" {
		return fmt.Errorf("folder website dummy %s tidak ada di server tujuan", res.UnitPath)
	}
	return ic.Save(func(it *store.PipelineItem) {
		it.DummyDomain = res.DummyDomain
		it.DestWebsiteRef = res.DummyDomain
		it.DestRootPath = res.UnitPath
	})
}

func (r *Runner) restorer(ctx context.Context, ic *ItemContext) (*restore.Restorer, error) {
	c, err := ic.SSH(ctx, ic.Batch.DestAccount)
	if err != nil {
		return nil, err
	}
	info, err := r.Store.GetSSH(ctx, ic.Batch.DestAccount)
	if err != nil {
		return nil, err
	}
	if info.Home == "" {
		return nil, errors.New("folder home SSH tujuan tidak diketahui; jalankan 'migrator ssh test'")
	}
	api, err := ic.DestAPI()
	if err != nil {
		return nil, err
	}
	return &restore.Restorer{Store: r.Store, Secrets: r.Secrets, Backup: r.Backup, API: api, SSH: c, Account: ic.DestAcc, Home: info.Home, Redactor: r.Redactor, Log: r.Log}, nil
}

func (r *Runner) RunRestore(ctx context.Context, ic *ItemContext, final bool) error {
	if ic.Item.DestRootPath == "" || ic.Item.DummyDomain == "" {
		return errors.New("website dummy tujuan belum siap")
	}
	m, err := r.manifest(ctx, ic, final)
	if err != nil {
		return err
	}
	rs, err := r.restorer(ctx, ic)
	if err != nil {
		return err
	}
	if _, err := rs.Files(ctx, ic.Item, m, ic.Item.DestRootPath); err != nil {
		return err
	}
	_, manualDB := ic.ManualConfirmed(ctx, "MANUAL_CREATE_DB")
	dbs, err := rs.CreateDatabases(ctx, ic.Item, m, ic.Item.DummyDomain, manualDB)
	if len(dbs) > 0 {
		ic.Save(func(it *store.PipelineItem) { it.DestDBs = dbs })
	}
	if errors.Is(err, restore.ErrManualDB) {
		return &ManualError{Code: "MANUAL_CREATE_DB", Instructions: err.Error() + "\n" + rs.ManualDBInstructions(ic.Item, dbs)}
	}
	if err != nil {
		return err
	}
	mu, _ := r.importMu.LoadOrStore(ic.Batch.DestAccount, &sync.Mutex{})
	lock := mu.(*sync.Mutex)
	lock.Lock()
	for i := range dbs {
		dbs[i].Imported = false
	}
	dbs, err = rs.Import(ctx, ic.Item, m, dbs)
	lock.Unlock()
	ic.Save(func(it *store.PipelineItem) { it.DestDBs = dbs })
	if err != nil {
		return err
	}
	if err := rs.UpdateConfigs(ctx, ic.Item.DestRootPath, ic.Databases, dbs); err != nil {
		return err
	}
	if err := rs.AfterRestore(ctx, ic.Website, ic.Item.DestRootPath); err != nil {
		return err
	}
	if _, ok := ic.ManualConfirmed(ctx, "MANUAL_PHP"); !ok {
		if _, err := rs.MatchPHP(ctx, ic.Website.PHPVersion, ic.Item.DummyDomain); err != nil {
			if errors.Is(err, restore.ErrManualPHP) {
				return &ManualError{Code: "MANUAL_PHP", Instructions: fmt.Sprintf("%v\nUbah versi PHP website %s di hPanel akun %s → Advanced → PHP Configuration ke %s, lalu jalankan: migrator confirm MANUAL_PHP %s",
					err, ic.Item.DummyDomain, ic.DestAcc.ID, ic.Website.PHPVersion, ic.Item.SourceDomain)}
			}
			return err
		}
	}
	return nil
}

func (r *Runner) wpAvailable(ctx context.Context, account string) bool {
	caps, _ := r.Store.ListCapabilities(ctx, account)
	for _, c := range caps {
		if c.Code == "SSH_TOOLS" && c.DetailJSON != "" {
			var d map[string]any
			if json.Unmarshal([]byte(c.DetailJSON), &d) == nil {
				if v, ok := d["wp"].(bool); ok {
					return v
				}
			}
		}
	}
	return false
}

func (r *Runner) target(ctx context.Context, ic *ItemContext, rs *restore.Restorer) (verify.Target, error) {
	t := verify.Target{
		Domain:      ic.Item.SourceDomain,
		DummyDomain: ic.Item.DummyDomain,
		SiteType:    ic.Website.SiteType,
		DummyOnly:   ic.Item.ExpectedFinal == StateDummyOnly,
		DestUnit:    ic.Item.DestRootPath,
		MarkerPath:  path.Join(rs.WorkDir(ic.Item), "restore.marker"),
	}
	m, err := r.manifest(ctx, ic, ic.Item.FinalSyncDone)
	if err == nil {
		t.FileCount = m.Files.FileCount
	}
	for _, d := range ic.Item.DestDBs {
		pw, err := r.Secrets.Get(secrets.KindDBPassword, ic.DestAcc.ID+":"+d.Name)
		if err != nil {
			return t, fmt.Errorf("password database %s tidak ditemukan di Credential Manager: %w", d.Name, err)
		}
		t.DBs = append(t.DBs, verify.DBCheck{Name: d.Name, User: d.User, Password: pw, Host: d.Host})
	}
	return t, nil
}

func (r *Runner) RunVerify(ctx context.Context, ic *ItemContext, prefix string) (string, error) {
	rs, err := r.restorer(ctx, ic)
	if err != nil {
		return "", err
	}
	t, err := r.target(ctx, ic, rs)
	if err != nil {
		return "", err
	}
	rep := &verify.Report{}
	r.Verifier.Server(ctx, rs.SSH, t, rep)
	ip, err := r.Verifier.HTTP(ctx, t, rep)
	if err != nil {
		return "", err
	}
	if t.SiteType == "WordPress" && prefix == "" && httpResult(rep) == verify.Warn && r.wpAvailable(ctx, ic.Batch.DestAccount) && !ic.Item.SearchReplaceDone {
		cmd := verify.SearchReplaceCommand(t.DestUnit, "https://"+t.Domain, "https://"+t.DummyDomain)
		res, err := rs.SSH.Run(ctx, cmd, nil)
		if err == nil && res.ExitCode == 0 {
			ic.Save(func(it *store.PipelineItem) { it.SearchReplaceDone = true })
			rep = &verify.Report{}
			r.Verifier.Server(ctx, rs.SSH, t, rep)
			t2 := t
			t2.SiteType = "Statis"
			r.Verifier.HTTP(ctx, t2, rep)
			rep.Method = "wp search-replace ke " + t.DummyDomain + " lalu GET langsung (dikembalikan saat cutover)"
		}
	}
	vs := rep.Verifications(prefix + "VERIFIKASI_")
	vs = append([]store.Verification{{CheckType: prefix + "VERIFIKASI_METODE", Result: verify.Pass, Detail: rep.Method}}, vs...)
	vs = append(vs, store.Verification{CheckType: prefix + "VERIFIKASI_HASIL", Result: rep.Overall(), Detail: "hasil keseluruhan"})
	if err := r.Store.ReplaceVerifications(ctx, ic.Item.ID, prefix+"VERIFIKASI_", vs); err != nil {
		return "", err
	}
	if ip != "" {
		ic.Save(func(it *store.PipelineItem) { it.DestIP = ip })
	}
	overall := rep.Overall()
	if overall != verify.Fail && prefix == "" {
		rs.Cleanup(ctx, ic.Item)
	}
	return overall, nil
}

func httpResult(rep *verify.Report) string {
	for _, c := range rep.Checks {
		if c.Name == "HTTP" {
			return c.Result
		}
	}
	return ""
}

func ItemLabel(it *store.PipelineItem) string {
	s := it.State
	if it.SideState != "" {
		s += "/" + it.SideState
	}
	if it.RetryCount > 0 && it.SideState == "" {
		s += " (percobaan " + strconv.Itoa(it.RetryCount) + ")"
	}
	return s
}
