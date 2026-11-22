package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"hostinger-migrator/internal/pipeline"
	"hostinger-migrator/internal/store"
	"hostinger-migrator/tests/sshtest"
	"hostinger-migrator/tests/testenv"
)

func TestScaleBatchWithDisruptions(t *testing.T) {
	if testing.Short() {
		t.Skip("uji skala dilewati dengan -short")
	}
	started := time.Now()
	e := testenv.New(t)
	domains := e.AddSites(15)
	batch := e.Prepare(domains...)
	ctx := context.Background()

	e.API.FailTimes("onboarding", 500, 3)
	e.API.FailTimes("free-subdomain", 502, 2)
	os.WriteFile(filepath.Join(e.FakeDB, "noevents"), nil, 0o600)
	srcExec := e.SrcSSH.Exec
	var mu sync.Mutex
	tarFails := map[string]bool{"web04.com": true, "web11.com": true}
	e.SrcSSH.Exec = func(cmd string, env map[string]string) sshtest.ExecResult {
		if strings.HasPrefix(cmd, "tar -czf") {
			mu.Lock()
			for d, pending := range tarFails {
				if pending && strings.Contains(cmd, "/"+d+"'") {
					tarFails[d] = false
					mu.Unlock()
					return sshtest.ExecResult{Stderr: "tar: koneksi terputus", Exit: 2}
				}
			}
			mu.Unlock()
		}
		return srcExec(cmd, env)
	}

	killCtx, kill := context.WithCancel(ctx)
	deps := e.Deps()
	deps.Parallel = 2
	r1 := pipeline.NewRunner(deps)
	go func() {
		for {
			time.Sleep(20 * time.Millisecond)
			items, _ := e.Store.ListItems(ctx, batch)
			done := 0
			for _, it := range items {
				if it.State == pipeline.StateVerified {
					done++
				}
			}
			if done >= 3 {
				kill()
				return
			}
		}
	}()
	for i := 0; i < 50 && killCtx.Err() == nil; i++ {
		r1.RunOnce(killCtx)
	}
	kill()
	if n, _ := e.Store.ResetRunningSteps(ctx); n < 0 {
		t.Fatal("reset gagal")
	}

	e.API.Revoke(testenv.DstToken)
	r2 := pipeline.NewRunner(e.Deps())
	e.RunUntilIdle(r2, 10)
	blocked := 0
	items, _ := e.Store.ListItems(ctx, batch)
	for _, it := range items {
		if it.SideState == store.SideBlocked {
			blocked++
		}
	}
	if blocked == 0 {
		t.Fatal("token tujuan dicabut harus membuat item BLOCKED")
	}
	e.API.Allow(testenv.DstToken)
	retried, err := pipeline.Retry(ctx, e.Store, batch, "", true)
	if err != nil || len(retried) != blocked {
		t.Fatalf("retry = %v %v (blocked %d)", retried, err, blocked)
	}

	r3 := pipeline.NewRunner(e.Deps())
	for round := 0; round < 40; round++ {
		e.RunUntilIdle(r3, 30)
		items, _ = e.Store.ListItems(ctx, batch)
		pending := false
		for _, it := range items {
			if it.State != pipeline.StateVerified && it.SideState == "" {
				pending = true
				e.ExpireBackoff(it.SourceDomain)
			}
		}
		if !pending {
			break
		}
	}

	items, _ = e.Store.ListItems(ctx, batch)
	dummies := map[string]string{}
	for _, it := range items {
		if it.State != pipeline.StateVerified {
			t.Errorf("%s = %s/%s %s", it.SourceDomain, it.State, it.SideState, it.LastError)
			continue
		}
		if other, dup := dummies[it.DummyDomain]; dup {
			t.Errorf("dummy %s dipakai %s dan %s", it.DummyDomain, other, it.SourceDomain)
		}
		dummies[it.DummyDomain] = it.SourceDomain
		if n := e.Count(`SELECT COUNT(1) FROM artifacts WHERE item_id = ? AND kind = 'files'`, it.ID); n != 1 {
			t.Errorf("%s artefak file = %d", it.SourceDomain, n)
		}
		if map[string]bool{"web03.com": true, "web06.com": true, "web09.com": true, "web12.com": true, "web15.com": true}[it.SourceDomain] {
			if len(it.DestDBs) != 1 || !it.DestDBs[0].Imported {
				t.Errorf("%s database = %+v", it.SourceDomain, it.DestDBs)
			}
		}
	}
	created := 0
	for _, w := range e.API.WebsitesOf(testenv.DstToken) {
		if strings.HasSuffix(w.Domain, ".hostingersite.com") {
			created++
		}
	}
	if created != len(domains) {
		t.Errorf("website dummy dibuat %d, harus %d (tanpa duplikat)", created, len(domains))
	}
	steps := e.Count(`SELECT COUNT(1) FROM steps WHERE attempts > 1`)
	if steps == 0 {
		t.Error("gangguan harus menyebabkan langkah diulang")
	}
	b, _, _ := e.Store.GetBatch(ctx, batch)
	t.Logf("batch %s: %d website VERIFIED, status %s, %d langkah diulang, %s", batch, len(dummies), b.Status, steps, time.Since(started).Round(time.Millisecond))
	var secretsList []string
	for i := 3; i <= 15; i += 3 {
		secretsList = append(secretsList, "FAKE-skala-"+twoDigits(i)+"-Sandi")
	}
	e.AssertNoSecretsOnDisk(append(secretsList, testenv.SrcToken, testenv.DstToken)...)
}

func twoDigits(i int) string {
	if i < 10 {
		return "0" + string(rune('0'+i))
	}
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}
