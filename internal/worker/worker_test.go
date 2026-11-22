package worker_test

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"hostinger-migrator/internal/pipeline"
	"hostinger-migrator/internal/worker"
	"hostinger-migrator/tests/testenv"
)

func TestWorkerRunsBatchAndHoldsLock(t *testing.T) {
	e := testenv.New(t)
	e.Prepare("domainanda.com", "mati.com")
	ctx := context.Background()
	if ok, _, _ := e.Store.AcquireWorkerLock(ctx, 999999, time.Minute); !ok {
		t.Fatal("lock awal harus didapat")
	}
	w := &worker.Worker{Store: e.Store, Runner: pipeline.NewRunner(e.Deps()), PID: 1234, Interval: time.Millisecond}
	if err := w.Run(ctx); !errors.Is(err, worker.ErrAlreadyRunning) {
		t.Fatalf("dua worker tidak boleh berjalan: %v", err)
	}
	if _, alive := worker.Alive(ctx, e.Store); !alive {
		t.Fatal("lock harus terlihat hidup")
	}
	e.Store.ReleaseWorkerLock(ctx, 999999)
	if err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if it := e.Item("domainanda.com"); it.State != pipeline.StateVerified {
		t.Fatalf("item = %s %s", it.State, it.LastError)
	}
	if it := e.Item("mati.com"); it.State != pipeline.StateDummyOnly {
		t.Fatalf("item = %s %s", it.State, it.LastError)
	}
	if _, alive := worker.Alive(ctx, e.Store); alive {
		t.Fatal("lock harus dilepas setelah worker selesai")
	}
	entries, _ := e.Store.ListAudit(ctx, 100)
	found := false
	for _, en := range entries {
		if en.Action == "worker.stop" && en.Detail == "tidak ada pekerjaan otomatis tersisa" {
			found = true
		}
	}
	if !found {
		t.Fatal("worker harus mencatat alasan berhenti")
	}
}

func TestWorkerStopRequest(t *testing.T) {
	e := testenv.New(t)
	e.Prepare("domainanda.com")
	e.API.OnboardingPolls = 1 << 30
	ctx := context.Background()
	deps := e.Deps()
	deps.ProvisionTimeout = time.Millisecond
	w := &worker.Worker{Store: e.Store, Runner: pipeline.NewRunner(deps), PID: 4321, Interval: 5 * time.Millisecond, Renew: 5 * time.Millisecond}
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if it := e.Item("domainanda.com"); it.State == pipeline.StateDestProvisioning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker tidak sampai ke provisioning")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if ok, _ := e.Store.RequestWorkerStop(ctx); !ok {
		t.Fatal("permintaan stop harus tercatat")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("worker tidak berhenti setelah diminta")
	}
	entries, _ := e.Store.ListAudit(ctx, 200)
	found := false
	for _, en := range entries {
		if en.Action == "worker.stop" && en.Detail == "dihentikan operator" {
			found = true
		}
	}
	if !found {
		t.Fatal("alasan berhenti harus 'dihentikan operator'")
	}
	if it := e.Item("domainanda.com"); it.State != pipeline.StateDestProvisioning || it.SideState != "" {
		t.Fatalf("item harus bisa dilanjutkan: %s/%s", it.State, it.SideState)
	}
}

func TestAutostartCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		args, err := worker.AutostartCommand(true, `C:\m\migrator.exe`, `C:\m\config.yaml`)
		if err != nil || args[0] != "schtasks.exe" {
			t.Fatalf("args = %v %v", args, err)
		}
		return
	}
	if _, err := worker.AutostartCommand(true, "/x/migrator", "/x/config.yaml"); err == nil {
		t.Fatal("di luar Windows autostart harus ditolak dengan pesan jelas")
	}
}
