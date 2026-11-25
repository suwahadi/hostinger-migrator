package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"hostinger-migrator/internal/backup"
	"hostinger-migrator/internal/hostinger"
	"hostinger-migrator/internal/logx"
	"hostinger-migrator/internal/secrets"
	"hostinger-migrator/internal/sshx"
	"hostinger-migrator/internal/store"
	"hostinger-migrator/internal/verify"
)

const (
	StatePlanned          = "PLANNED"
	StateBackupRunning    = "BACKUP_RUNNING"
	StateBackupDone       = "BACKUP_DONE"
	StateDestProvisioning = "DEST_PROVISIONING"
	StateDestReady        = "DEST_READY"
	StateRestoreRunning   = "RESTORE_RUNNING"
	StateRestoreDone      = "RESTORE_DONE"
	StateVerifying        = "VERIFYING"
	StateVerified         = "VERIFIED"
	StateDummyOnly        = "DUMMY_ONLY"
	StateCutoverRunning   = "CUTOVER_RUNNING"
	StateCutoverDone      = "CUTOVER_DONE"
	StateSSLActive        = "SSL_ACTIVE"
	StateSSLPending       = "SSL_PENDING"
	StateEmailRunning     = "EMAIL_RUNNING"
	StateEmailDone        = "EMAIL_DONE"
	StateNoEmail          = "NO_EMAIL"
	StateCleanupReady     = "CLEANUP_READY"
	StateCleaned          = "CLEANED"
	StateCompleted        = "COMPLETED"
	StateBlocked          = "BLOCKED"

	BatchRunning  = "BERJALAN"
	BatchWaiting  = "MENUNGGU_OPERATOR"
	BatchDone     = "SELESAI"
	BatchDoneNote = "SELESAI_DENGAN_CATATAN"
)

var Order = []string{StatePlanned, StateBackupRunning, StateBackupDone, StateDestProvisioning, StateDestReady, StateRestoreRunning, StateRestoreDone,
	StateVerifying, StateVerified, StateCutoverRunning, StateCutoverDone, StateSSLPending, StateSSLActive, StateEmailRunning, StateEmailDone, StateNoEmail,
	StateCleanupReady, StateCleaned, StateCompleted}

func IsTerminal(state string) bool {
	return state == StateCompleted || state == StateDummyOnly || state == StateBlocked
}

type ManualError struct {
	Code         string
	Instructions string
}

func (e *ManualError) Error() string { return "menunggu tindakan manual " + e.Code }

type BlockedError struct{ Reason string }

func (e *BlockedError) Error() string { return e.Reason }

var (
	ErrWait = errors.New("menunggu proses eksternal")
	ErrIdle = errors.New("menunggu keputusan operator")
)

type Handler func(ctx context.Context, ic *ItemContext) (string, error)

type Stage interface {
	Handlers() map[string]Handler
}

type Deps struct {
	Store            *store.Store
	Secrets          *secrets.Store
	SSH              *sshx.Service
	NewClient        func(alias string) (*hostinger.Client, error)
	Backup           *backup.Engine
	Verifier         *verify.Verifier
	Redactor         *logx.Redactor
	Log              *slog.Logger
	RetryMax         int
	Parallel         int
	ProvisionPoll    time.Duration
	ProvisionTimeout time.Duration
	Sleep            func(context.Context, time.Duration) error
	Now              func() time.Time
	Stages           []Stage
	OnBatchFinished  func(ctx context.Context, batchID string)
}

type Runner struct {
	Deps
	handlers map[string]Handler
	importMu sync.Map
	clientMu sync.Mutex
	clients  map[string]*hostinger.Client
}

func NewRunner(d Deps) *Runner {
	r := &Runner{Deps: d, clients: map[string]*hostinger.Client{}}
	if r.Parallel <= 0 {
		r.Parallel = 2
	}
	if r.RetryMax <= 0 {
		r.RetryMax = 8
	}
	if r.Redactor == nil {
		r.Redactor = logx.Global
	}
	r.handlers = map[string]Handler{
		StatePlanned:          r.hPlanned,
		StateBackupRunning:    r.hBackup,
		StateBackupDone:       next(StateDestProvisioning),
		StateDestProvisioning: r.hProvision,
		StateDestReady:        next(StateRestoreRunning),
		StateRestoreRunning:   r.hRestore,
		StateRestoreDone:      next(StateVerifying),
		StateVerifying:        r.hVerify,
	}
	for _, s := range d.Stages {
		for k, h := range s.Handlers() {
			r.handlers[k] = h
		}
	}
	return r
}

func next(s string) Handler {
	return func(ctx context.Context, ic *ItemContext) (string, error) { return s, nil }
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Runner) Client(alias string) (*hostinger.Client, error) {
	r.clientMu.Lock()
	defer r.clientMu.Unlock()
	if c, ok := r.clients[alias]; ok {
		return c, nil
	}
	c, err := r.NewClient(alias)
	if err != nil {
		return nil, err
	}
	r.clients[alias] = c
	return c, nil
}

type ItemContext struct {
	Item       *store.PipelineItem
	Batch      *store.Batch
	Website    *store.Website
	Databases  []store.DatabaseRow
	SourceAcc  store.HostingAccount
	DestAcc    store.HostingAccount
	r          *Runner
	sshClients map[string]*sshx.Client
}

func (ic *ItemContext) Runner() *Runner { return ic.r }

func (ic *ItemContext) SourceAPI() (*hostinger.Client, error) {
	return ic.r.Client(ic.Batch.SourceAlias)
}
func (ic *ItemContext) DestAPI() (*hostinger.Client, error) { return ic.r.Client(ic.Batch.DestAlias) }

func (ic *ItemContext) SSH(ctx context.Context, account string) (*sshx.Client, error) {
	if c, ok := ic.sshClients[account]; ok {
		return c, nil
	}
	c, err := ic.r.SSH.Connect(ctx, account)
	if err != nil {
		if errors.Is(err, sshx.ErrBlocked) {
			return nil, &BlockedError{Reason: err.Error()}
		}
		return nil, err
	}
	ic.sshClients[account] = c
	return c, nil
}

func (ic *ItemContext) close() {
	for _, c := range ic.sshClients {
		c.Close()
	}
}

func (ic *ItemContext) Save(fn func(it *store.PipelineItem)) error {
	it, err := ic.r.Store.UpdateItem(context.Background(), ic.Item.ID, "worker", "", func(it *store.PipelineItem) error {
		fn(it)
		return nil
	})
	if err == nil {
		ic.Item = it
	}
	return err
}

func (ic *ItemContext) Manual(ctx context.Context, code string) (*store.ManualTask, error) {
	t, err := ic.r.Store.GetManualTask(ctx, ic.Item.ID, code)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	return t, err
}

func (ic *ItemContext) ManualConfirmed(ctx context.Context, code string) (*store.ManualTask, bool) {
	t, err := ic.Manual(ctx, code)
	if err != nil || t == nil {
		return nil, false
	}
	return t, t.Status == store.ManualConfirmed
}

func (r *Runner) load(ctx context.Context, it *store.PipelineItem) (*ItemContext, error) {
	b, _, err := r.Store.GetBatch(ctx, it.BatchID)
	if err != nil {
		return nil, err
	}
	ic := &ItemContext{Item: it, Batch: b, r: r, sshClients: map[string]*sshx.Client{}}
	if it.WebsiteID > 0 {
		w, err := r.Store.GetWebsiteByID(ctx, it.WebsiteID)
		if err != nil {
			return nil, err
		}
		ic.Website = w
		dbs, err := r.Store.ListDatabasesByAccount(ctx, b.SourceAccount)
		if err != nil {
			return nil, err
		}
		skipped := map[string]bool{}
		if names, err := SkippedDatabases(ctx, r.Store, it); err == nil {
			for _, n := range names {
				skipped[n] = true
			}
		}
		for _, d := range dbs {
			if d.WebsiteDomain == w.Domain && !skipped[d.Name] {
				ic.Databases = append(ic.Databases, d)
			}
		}
	}
	src, err := r.Store.GetHostingAccount(ctx, b.SourceAlias, b.SourceAccount)
	if err != nil {
		return nil, fmt.Errorf("akun sumber %s: %w", b.SourceAccount, err)
	}
	dst, err := r.Store.GetHostingAccount(ctx, b.DestAlias, b.DestAccount)
	if err != nil {
		return nil, fmt.Errorf("akun tujuan %s: %w", b.DestAccount, err)
	}
	ic.SourceAcc, ic.DestAcc = *src, *dst
	return ic, nil
}

func (r *Runner) backoff(retry int) time.Duration {
	d := 10 * time.Second
	for i := 1; i < retry && d < 10*time.Minute; i++ {
		d *= 2
	}
	if d > 10*time.Minute {
		d = 10 * time.Minute
	}
	return d
}

func (r *Runner) runnable(it *store.PipelineItem) bool {
	if IsTerminal(it.State) || it.SideState != "" {
		return false
	}
	if _, ok := r.handlers[it.State]; !ok {
		return false
	}
	if it.LastError != "" && it.RetryCount > 0 {
		t, err := time.Parse(time.RFC3339, it.UpdatedAt)
		if err == nil && r.now().Before(t.Add(r.backoff(it.RetryCount))) {
			return false
		}
	}
	return true
}

type Summary struct {
	Processed int
	Waiting   int
	Pending   int
}

func (r *Runner) RunOnce(ctx context.Context) (Summary, error) {
	var sum Summary
	items, err := r.Store.ListItems(ctx, "")
	if err != nil {
		return sum, err
	}
	var todo []*store.PipelineItem
	for _, it := range items {
		if r.runnable(it) {
			todo = append(todo, it)
		} else if !IsTerminal(it.State) && it.SideState == "" && it.LastError != "" {
			sum.Pending++
		}
	}
	sem := make(chan struct{}, r.Parallel)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, it := range todo {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(it *store.PipelineItem) {
			defer wg.Done()
			defer func() { <-sem }()
			res := r.process(ctx, it)
			mu.Lock()
			defer mu.Unlock()
			switch res {
			case resultProgress:
				sum.Processed++
			case resultPending:
				sum.Pending++
			case resultIdle:
				sum.Waiting++
			}
		}(it)
	}
	wg.Wait()
	r.updateBatches(ctx)
	return sum, ctx.Err()
}

type result int

const (
	resultNone result = iota
	resultProgress
	resultPending
	resultIdle
)

func (r *Runner) process(ctx context.Context, it *store.PipelineItem) result {
	ic, err := r.load(ctx, it)
	if err != nil {
		r.fail(ctx, it, err)
		return resultPending
	}
	defer ic.close()
	progressed := false
	for {
		if ctx.Err() != nil {
			return resultNone
		}
		h, ok := r.handlers[ic.Item.State]
		if !ok || IsTerminal(ic.Item.State) {
			if progressed {
				return resultProgress
			}
			return resultIdle
		}
		from := ic.Item.State
		to, err := h(ctx, ic)
		if err != nil {
			if ctx.Err() != nil {
				return resultNone
			}
			switch {
			case errors.Is(err, ErrWait):
				r.note(ctx, ic.Item, err)
				return resultPending
			case errors.Is(err, ErrIdle):
				if progressed {
					return resultProgress
				}
				return resultIdle
			}
			r.fail(ctx, ic.Item, err)
			if progressed {
				return resultProgress
			}
			return resultPending
		}
		if to == "" || to == from {
			if progressed {
				return resultProgress
			}
			return resultIdle
		}
		updated, err := r.Store.Transition(ctx, ic.Item.ID, from, to, func(it *store.PipelineItem) error {
			it.RetryCount = 0
			if from == StatePlanned {
				it.StartedAt = r.now().UTC().Format(time.RFC3339)
			}
			if IsTerminal(to) {
				it.FinishedAt = r.now().UTC().Format(time.RFC3339)
			}
			return nil
		})
		if err != nil {
			r.fail(ctx, ic.Item, err)
			return resultPending
		}
		if r.Log != nil {
			r.Log.Info("transisi item", "domain", updated.SourceDomain, "dari", from, "ke", to)
		}
		ic.Item = updated
		progressed = true
	}
}

func (r *Runner) note(ctx context.Context, it *store.PipelineItem, err error) {
	r.Store.UpdateItem(ctx, it.ID, "worker", "", func(x *store.PipelineItem) error {
		x.LastError = r.Redactor.String(err.Error())
		return nil
	})
}

func (r *Runner) fail(ctx context.Context, it *store.PipelineItem, err error) {
	msg := r.Redactor.String(err.Error())
	var manual *ManualError
	var blocked *BlockedError
	switch {
	case errors.As(err, &manual):
		instructions := r.Redactor.String(manual.Instructions)
		if t, _ := r.Store.GetManualTask(ctx, it.ID, manual.Code); t != nil && t.Status == store.ManualConfirmed {
			r.Store.ReopenManualTask(ctx, it.ID, manual.Code, instructions)
		} else {
			r.Store.OpenManualTask(ctx, it.ID, manual.Code, instructions)
		}
		r.Store.UpdateItem(ctx, it.ID, "worker", "item.manual", func(x *store.PipelineItem) error {
			x.SideState = store.SideWaitingManual
			x.ManualCode = manual.Code
			x.LastError = "menunggu " + manual.Code
			return nil
		})
	case errors.As(err, &blocked), hostinger.IsBlocking(err), errors.Is(err, sshx.ErrBlocked):
		r.Store.UpdateItem(ctx, it.ID, "worker", "item.blocked", func(x *store.PipelineItem) error {
			x.SideState = store.SideBlocked
			x.LastError = msg
			return nil
		})
	default:
		r.Store.UpdateItem(ctx, it.ID, "worker", "item.error", func(x *store.PipelineItem) error {
			x.RetryCount++
			x.LastError = msg
			if x.RetryCount >= r.RetryMax {
				x.SideState = store.SideFailed
			}
			return nil
		})
	}
	if r.Log != nil {
		r.Log.Warn("item tertahan", "domain", it.SourceDomain, "status", it.State, "error", msg)
	}
}

func (r *Runner) updateBatches(ctx context.Context) {
	batches, err := r.Store.ListBatches(ctx)
	if err != nil {
		return
	}
	for _, b := range batches {
		if b.Status == BatchDone || b.Status == BatchDoneNote {
			continue
		}
		items, err := r.Store.ListItems(ctx, b.ID)
		if err != nil || len(items) == 0 {
			continue
		}
		status := ComputeBatchStatus(items)
		if status != b.Status {
			r.Store.SetBatchStatus(ctx, b.ID, status)
			if (status == BatchDone || status == BatchDoneNote) && r.OnBatchFinished != nil {
				r.OnBatchFinished(ctx, b.ID)
			}
		}
	}
}

func ComputeBatchStatus(items []*store.PipelineItem) string {
	allTerminal := true
	notes := false
	waiting := false
	started := false
	for _, it := range items {
		if it.State != StatePlanned {
			started = true
		}
		if it.SideState != "" {
			waiting = true
		}
		if !IsTerminal(it.State) {
			allTerminal = false
		}
		if it.State == StateDummyOnly || it.State == StateBlocked {
			notes = true
		}
	}
	switch {
	case allTerminal && notes:
		return BatchDoneNote
	case allTerminal:
		return BatchDone
	case waiting:
		return BatchWaiting
	case started:
		return BatchRunning
	}
	return store.BatchPlanned
}

func (r *Runner) hPlanned(ctx context.Context, ic *ItemContext) (string, error) {
	if ic.Website == nil {
		return "", &BlockedError{Reason: "website sumber tidak ditemukan di inventaris"}
	}
	return StateBackupRunning, nil
}

func (r *Runner) step(ctx context.Context, ic *ItemContext, name string, fn func() error) error {
	r.Store.StartStep(ctx, ic.Item.ID, name)
	err := fn()
	status, msg := store.StepDone, ""
	if err != nil {
		status, msg = store.StepFailed, r.Redactor.String(err.Error())
		if ctx.Err() != nil {
			msg = "dihentikan sebelum selesai; akan diulang"
		}
		var manual *ManualError
		if errors.As(err, &manual) {
			status = "MANUAL"
		}
	}
	r.Store.FinishStep(context.Background(), ic.Item.ID, name, status, "", msg)
	return err
}

func (r *Runner) manifest(ctx context.Context, ic *ItemContext, final bool) (*backup.Manifest, error) {
	kind := backup.KindManifest
	if final {
		kind = backup.FinalPrefix + backup.KindManifest
	}
	a, err := r.Store.ValidatedArtifact(ctx, ic.Item.ID, kind)
	if err != nil {
		return nil, fmt.Errorf("manifest backup tidak ditemukan: %w", err)
	}
	m, err := backup.ReadManifest(a.Path)
	if err != nil {
		return nil, err
	}
	if problems := r.Backup.VerifyLocal(m); len(problems) > 0 {
		r.Store.DeleteArtifacts(ctx, ic.Item.ID, kind)
		return nil, fmt.Errorf("backup lokal tidak valid lagi: %s", strings.Join(problems, "; "))
	}
	return m, nil
}

func (r *Runner) hBackup(ctx context.Context, ic *ItemContext) (string, error) {
	err := r.step(ctx, ic, "backup", func() error {
		_, err := r.RunBackup(ctx, ic, false)
		return err
	})
	if err != nil {
		return "", err
	}
	return StateBackupDone, nil
}

func (r *Runner) RunBackup(ctx context.Context, ic *ItemContext, final bool) (*backup.Manifest, error) {
	if !final {
		if _, ok := ic.ManualConfirmed(ctx, "MANUAL_BACKUP"); ok {
			m, err := r.Backup.ImportManual(ctx, ic.Item, ic.Website, ic.Databases)
			if err != nil {
				return nil, &ManualError{Code: "MANUAL_BACKUP", Instructions: err.Error() + "\n" + backup.ManualInstructions(r.Backup.Dir(ic.Item, false), ic.Website, ic.Databases)}
			}
			return m, nil
		}
	}
	info, _ := r.Store.GetSSH(ctx, ic.Batch.SourceAccount)
	if info == nil || info.Status != store.SSHReady {
		if final {
			return nil, &ManualError{Code: "MANUAL_BACKUP_FINAL", Instructions: "SSH sumber tidak siap untuk backup final. " + backup.ManualInstructions(r.Backup.Dir(ic.Item, true), ic.Website, ic.Databases)}
		}
		return nil, &ManualError{Code: "MANUAL_BACKUP", Instructions: backup.ManualInstructions(r.Backup.Dir(ic.Item, false), ic.Website, ic.Databases)}
	}
	c, err := ic.SSH(ctx, ic.Batch.SourceAccount)
	if err != nil {
		return nil, err
	}
	m, err := r.Backup.Run(ctx, c, ic.Item, ic.Website, ic.Databases, final)
	usedSSH := false
	if run, rerr := r.Store.GetInventoryRun(ctx, ic.Batch.SourceAccount, ic.Batch.SourceAlias); rerr == nil {
		usedSSH = run.SSHUsed
	}
	return m, backupManual(err, ic, usedSSH)
}

func unknownSource(d store.DatabaseRow) bool {
	return d.Source == "" || d.Source == "api"
}

func backupManual(err error, ic *ItemContext, inventoryUsedSSH bool) error {
	if !errors.Is(err, backup.ErrUnknownDBSource) {
		return err
	}
	var unknown []string
	for _, d := range ic.Databases {
		if unknownSource(d) {
			unknown = append(unknown, d.Name)
		}
	}
	db, domain, acc := strings.Join(unknown, ", "), ic.Item.SourceDomain, ic.Batch.SourceAccount
	reason := fmt.Sprintf("Penyebab paling umum: inventaris akun %s dibuat sebelum SSH siap, sehingga file konfigurasi website tidak dibaca.", acc)
	if inventoryUsedSSH {
		reason = fmt.Sprintf("Inventaris akun %s sudah memakai SSH, tetapi tidak ada file konfigurasi website yang merujuk database ini. Kemungkinan database ini tidak dipakai (mis. sisa instalasi lama).", acc)
	}
	return &ManualError{Code: "MANUAL_DB_SOURCE", Instructions: fmt.Sprintf("Password database %s untuk %s tidak bisa dibaca karena file konfigurasinya belum diketahui.\n%s\n"+
		"1. Pindai ulang akun: migrator inventory --akun %s --refresh (atau menu 3, pindai ulang, pilih akun ini)\n"+
		"2. Lanjutkan: migrator confirm MANUAL_DB_SOURCE %s\n"+
		"3. Bila database ini tidak dipakai website, lewati: migrator confirm MANUAL_DB_SOURCE %s --nilai lewati. Database yang dilewati tidak dipindah dan kemungkinan ikut terhapus saat cleanup website sumber.\n"+
		"Bila website memakai database ini tetapi tindakan ini muncul lagi setelah pindai ulang, file konfigurasinya tidak dikenali tool; catat nama filenya dan laporkan ke pengembang.",
		db, domain, reason, acc, domain, domain)}
}

func (r *Runner) hProvision(ctx context.Context, ic *ItemContext) (string, error) {
	err := r.step(ctx, ic, "provisioning", func() error { return r.provision(ctx, ic) })
	if err != nil {
		return "", err
	}
	return StateDestReady, nil
}

func (r *Runner) hRestore(ctx context.Context, ic *ItemContext) (string, error) {
	err := r.step(ctx, ic, "restore", func() error { return r.RunRestore(ctx, ic, false) })
	if err != nil {
		return "", err
	}
	return StateRestoreDone, nil
}

func (r *Runner) hVerify(ctx context.Context, ic *ItemContext) (string, error) {
	var overall string
	err := r.step(ctx, ic, "verifikasi", func() error {
		var err error
		overall, err = r.RunVerify(ctx, ic, "")
		return err
	})
	if err != nil {
		return "", err
	}
	if overall == verify.Fail {
		return "", errors.New("verifikasi FAIL; lihat 'migrator status --item " + ic.Item.SourceDomain + "'")
	}
	if ic.Item.ExpectedFinal == StateDummyOnly {
		return StateDummyOnly, nil
	}
	return StateVerified, nil
}

func SortedStates(m map[string]int) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return indexOf(out[i]) < indexOf(out[j]) })
	return out
}

func indexOf(s string) int {
	for i, x := range Order {
		if x == s {
			return i
		}
	}
	return len(Order) + 1
}

func (r *Runner) Load(ctx context.Context, it *store.PipelineItem) (*ItemContext, error) {
	return r.load(ctx, it)
}

func (ic *ItemContext) Close() {
	ic.close()
}
