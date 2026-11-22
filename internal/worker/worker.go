package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"hostinger-migrator/internal/pipeline"
	"hostinger-migrator/internal/store"
)

var ErrAlreadyRunning = errors.New("worker lain sedang berjalan")

type Worker struct {
	Store    *store.Store
	Runner   *pipeline.Runner
	PID      int
	Lease    time.Duration
	Renew    time.Duration
	Interval time.Duration
	Log      *slog.Logger
	Sleep    func(context.Context, time.Duration) error
}

func (w *Worker) defaults() {
	if w.PID == 0 {
		w.PID = os.Getpid()
	}
	if w.Lease <= 0 {
		w.Lease = 2 * time.Minute
	}
	if w.Renew <= 0 {
		w.Renew = 30 * time.Second
	}
	if w.Interval <= 0 {
		w.Interval = 15 * time.Second
	}
	if w.Sleep == nil {
		w.Sleep = func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		}
	}
}

func Alive(ctx context.Context, st *store.Store) (*store.LockInfo, bool) {
	l, err := st.WorkerLock(ctx)
	if err != nil {
		return nil, false
	}
	return l, l.LeaseUntil.After(st.Now().UTC())
}

func (w *Worker) Run(ctx context.Context) error {
	w.defaults()
	ok, holder, err := w.Store.AcquireWorkerLock(ctx, w.PID, w.Lease)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w (PID %d, lease sampai %s)", ErrAlreadyRunning, holder.PID, holder.LeaseUntil.Format(time.RFC3339))
	}
	defer w.Store.ReleaseWorkerLock(context.Background(), w.PID)
	if n, _ := w.Store.ResetRunningSteps(ctx); n > 0 && w.Log != nil {
		w.Log.Info("langkah yang terputus ditandai untuk diulang", "jumlah", n)
	}
	w.Store.AppendAudit(ctx, nil, "worker", "worker.start", fmt.Sprint(w.PID), "OK", "")
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		t := time.NewTicker(w.Renew)
		defer t.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-t.C:
				held, err := w.Store.RenewWorkerLock(context.Background(), w.PID, w.Lease)
				if err == nil && !held {
					cancel()
					return
				}
				if l, err := w.Store.WorkerLock(context.Background()); err == nil && l.StopRequested {
					cancel()
					return
				}
			}
		}
	}()
	reason := "tidak ada pekerjaan otomatis tersisa"
	for {
		if l, err := w.Store.WorkerLock(ctx); err == nil && l.StopRequested {
			reason = "dihentikan operator"
			break
		}
		sum, err := w.Runner.RunOnce(runCtx)
		if runCtx.Err() != nil {
			reason = "dihentikan operator"
			break
		}
		if err != nil && w.Log != nil {
			w.Log.Error("siklus worker gagal", "error", err)
		}
		if sum.Processed == 0 && sum.Pending == 0 {
			break
		}
		if sum.Processed == 0 {
			if err := w.Sleep(runCtx, w.Interval); err != nil {
				reason = "dihentikan operator"
				break
			}
		}
	}
	w.Store.AppendAudit(context.Background(), nil, "worker", "worker.stop", fmt.Sprint(w.PID), "OK", reason)
	if w.Log != nil {
		w.Log.Info("worker berhenti", "alasan", reason)
	}
	return nil
}

func Spawn(exe string, args []string, logPath string) (int, error) {
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return 0, err
	}
	out, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	defer out.Close()
	cmd := exec.Command(exe, args...)
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.Stdin = nil
	cmd.SysProcAttr = detachAttr()
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	cmd.Process.Release()
	return pid, nil
}

const TaskName = "hostinger-migrator"

func AutostartCommand(on bool, exe, configPath string) ([]string, error) {
	if runtime.GOOS != "windows" {
		return nil, errors.New("autostart hanya tersedia di Windows (Task Scheduler)")
	}
	if !on {
		return []string{"schtasks.exe", "/Delete", "/F", "/TN", TaskName}, nil
	}
	tr := `"` + exe + `" resume --config "` + configPath + `"`
	if strings.ContainsAny(exe+configPath, `"`) {
		return nil, errors.New("path tidak boleh mengandung tanda kutip")
	}
	return []string{"schtasks.exe", "/Create", "/F", "/SC", "ONLOGON", "/RL", "LIMITED", "/TN", TaskName, "/TR", tr}, nil
}
