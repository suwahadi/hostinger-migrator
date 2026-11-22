package cutover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"hostinger-migrator/internal/gate"
	"hostinger-migrator/internal/hostinger"
	"hostinger-migrator/internal/pipeline"
	"hostinger-migrator/internal/store"
	"hostinger-migrator/internal/verify"
)

type Stage struct {
	Runner      *pipeline.Runner
	FinalSync   bool
	SSLWait     time.Duration
	HTTPSScheme string
	Now         func() time.Time
}

func (s *Stage) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Stage) Handlers() map[string]pipeline.Handler {
	return map[string]pipeline.Handler{
		pipeline.StateVerified:       s.hVerified,
		pipeline.StateCutoverRunning: s.hCutover,
		pipeline.StateCutoverDone:    s.hSSL,
		pipeline.StateSSLActive:      s.hAfterSSL,
		pipeline.StateSSLPending:     s.hAfterSSL,
		pipeline.StateNoEmail: func(ctx context.Context, ic *pipeline.ItemContext) (string, error) {
			return pipeline.StateCleanupReady, nil
		},
		pipeline.StateEmailDone: func(ctx context.Context, ic *pipeline.ItemContext) (string, error) {
			return pipeline.StateCleanupReady, nil
		},
	}
}

func (s *Stage) hVerified(ctx context.Context, ic *pipeline.ItemContext) (string, error) {
	if _, err := gate.For(ctx, ic.Runner().Store, ic.Batch.ID, gate.KindCutover, ic.Item.SourceDomain); err != nil {
		return "", pipeline.ErrIdle
	}
	return pipeline.StateCutoverRunning, nil
}

type checkpoint struct {
	SnapshotID   int64    `json:"snapshot_lokal"`
	APISnapshot  int64    `json:"snapshot_api"`
	Parked       bool     `json:"parked_dipasang"`
	DNSChanged   []string `json:"dns_diubah"`
	SSLStarted   string   `json:"ssl_dimulai"`
	SSLInstalled bool     `json:"ssl_diminta"`
}

func (s *Stage) loadCP(ctx context.Context, ic *pipeline.ItemContext) checkpoint {
	var cp checkpoint
	steps, _ := ic.Runner().Store.ListSteps(ctx, ic.Item.ID)
	for _, st := range steps {
		if st.Name == "cutover" && st.Checkpoint != "" {
			json.Unmarshal([]byte(st.Checkpoint), &cp)
		}
	}
	return cp
}

func (s *Stage) saveCP(ctx context.Context, ic *pipeline.ItemContext, cp checkpoint, status, errMsg string) {
	b, _ := json.Marshal(cp)
	st := ic.Runner().Store
	steps, _ := st.ListSteps(ctx, ic.Item.ID)
	exists := false
	for _, x := range steps {
		if x.Name == "cutover" {
			exists = true
		}
	}
	if !exists {
		st.StartStep(ctx, ic.Item.ID, "cutover")
	}
	st.FinishStep(context.Background(), ic.Item.ID, "cutover", status, string(b), errMsg)
}

func (s *Stage) hCutover(ctx context.Context, ic *pipeline.ItemContext) (string, error) {
	r := ic.Runner()
	approval, err := gate.For(ctx, r.Store, ic.Batch.ID, gate.KindCutover, ic.Item.SourceDomain)
	if err != nil {
		return "", &pipeline.BlockedError{Reason: err.Error()}
	}
	cp := s.loadCP(ctx, ic)
	fail := func(err error) (string, error) {
		s.saveCP(ctx, ic, cp, store.StepFailed, err.Error())
		return "", err
	}
	if s.FinalSync && !ic.Item.FinalSyncDone {
		if _, err := r.RunBackup(ctx, ic, true); err != nil {
			return fail(err)
		}
		if err := r.RunRestore(ctx, ic, true); err != nil {
			return fail(err)
		}
		ic.Save(func(it *store.PipelineItem) { it.FinalSyncDone = true; it.SearchReplaceDone = false })
		overall, err := r.RunVerify(ctx, ic, "FINAL_")
		if err != nil {
			return fail(err)
		}
		if overall == verify.Fail {
			return fail(errors.New("verifikasi singkat setelah backup final FAIL"))
		}
	}
	srcAPI, err := ic.SourceAPI()
	if err != nil {
		return fail(err)
	}
	dstAPI, err := ic.DestAPI()
	if err != nil {
		return fail(err)
	}
	domain := ic.Item.SourceDomain
	if cp.SnapshotID == 0 && ic.Website.DNSInToken {
		zone, err := srcAPI.GetZone(ctx, domain)
		if err != nil {
			return fail(fmt.Errorf("snapshot DNS %s gagal: %w", domain, err))
		}
		raw, _ := json.Marshal(zone)
		var apiID int64
		var snaps []struct {
			ID int64 `json:"id"`
		}
		if _, err := srcAPI.Do(ctx, hostinger.Request{Endpoint: "dns.snapshots.list", PathParams: map[string]string{"domain": domain}}, &snaps); err == nil {
			for _, sn := range snaps {
				if sn.ID > apiID {
					apiID = sn.ID
				}
			}
		}
		id, err := r.Store.SaveDNSSnapshot(ctx, store.DNSSnapshot{Domain: domain, ItemID: ic.Item.ID, RecordsJSON: string(raw), APISnapshotID: apiID, Reason: "sebelum cutover"})
		if err != nil {
			return fail(err)
		}
		cp.SnapshotID, cp.APISnapshot = id, apiID
		s.saveCP(ctx, ic, cp, store.StepRunning, "")
	}

	if ic.Item.SourceTempDomain == "" {
		task, confirmed := ic.ManualConfirmed(ctx, "MANUAL_SOURCE_RENAME")
		if !confirmed {
			return fail(&pipeline.ManualError{Code: "MANUAL_SOURCE_RENAME", Instructions: renameInstructions(ic)})
		}
		temp := strings.ToLower(strings.TrimSpace(task.Value))
		sites, err := srcAPI.ListWebsites(ctx, hostinger.WebsiteFilter{Username: ic.Batch.SourceAccount})
		if err != nil {
			return fail(err)
		}
		hasTemp, hasOld := false, false
		for _, w := range sites {
			if strings.EqualFold(w.Domain, temp) {
				hasTemp = true
			}
			if strings.EqualFold(w.Domain, domain) {
				hasOld = true
			}
		}
		if temp == "" || !hasTemp || hasOld {
			why := fmt.Sprintf("Verifikasi gagal: domain sementara %q harus ada dan %s tidak boleh lagi terpasang di akun sumber.\n", temp, domain)
			return fail(&pipeline.ManualError{Code: "MANUAL_SOURCE_RENAME", Instructions: why + renameInstructions(ic)})
		}
		gate.Record(ctx, r.Store, approval, "cutover.source_rename", domain, "OK", "domain sumber diganti ke "+temp+" oleh operator")
		ic.Save(func(it *store.PipelineItem) { it.SourceTempDomain = temp })
	}

	if !cp.Parked {
		if _, ok := ic.ManualConfirmed(ctx, "MANUAL_DEST_DOMAIN"); ok {
			cp.Parked = true
		} else {
			_, err := dstAPI.Do(ctx, hostinger.Request{Endpoint: "hosting.parked_domains.create", PathParams: map[string]string{"username": ic.Batch.DestAccount, "domain": ic.Item.DummyDomain}, Body: map[string]any{"parked_domain": domain}}, nil)
			if err != nil && !errors.Is(err, hostinger.ErrConflict) {
				var apiErr *hostinger.APIError
				if errors.As(err, &apiErr) && (apiErr.Kind == hostinger.KindClient || apiErr.Kind == hostinger.KindNotFound) {
					return fail(&pipeline.ManualError{Code: "MANUAL_DEST_DOMAIN", Instructions: fmt.Sprintf("API menolak memasang %s sebagai parked domain (%v).\n%s", domain, err, destDomainInstructions(ic))})
				}
				return fail(err)
			}
			parked, err := dstAPI.ListParkedDomains(ctx, ic.Batch.DestAccount, ic.Item.DummyDomain)
			if err != nil {
				return fail(err)
			}
			found := false
			for _, p := range parked {
				if strings.EqualFold(p.Domain, domain) {
					found = true
				}
			}
			if !found {
				return fail(&pipeline.ManualError{Code: "MANUAL_DEST_DOMAIN", Instructions: "Parked domain tidak terlihat setelah dipasang.\n" + destDomainInstructions(ic)})
			}
			cp.Parked = true
			gate.Record(ctx, r.Store, approval, "cutover.dest_domain", domain, "OK", "parked di "+ic.Item.DummyDomain)
		}
		s.saveCP(ctx, ic, cp, store.StepRunning, "")
	}

	if len(cp.DNSChanged) == 0 {
		changed, err := s.updateDNS(ctx, ic, approval, srcAPI)
		if err != nil {
			return fail(err)
		}
		cp.DNSChanged = changed
		s.saveCP(ctx, ic, cp, store.StepRunning, "")
	}

	if ic.Item.SearchReplaceDone {
		rs, err := ic.SSH(ctx, ic.Batch.DestAccount)
		if err != nil {
			return fail(err)
		}
		res, err := rs.Run(ctx, verify.SearchReplaceCommand(ic.Item.DestRootPath, "https://"+ic.Item.DummyDomain, "https://"+domain), nil)
		if err != nil || res.ExitCode != 0 {
			return fail(fmt.Errorf("mengembalikan URL WordPress ke %s gagal", domain))
		}
		ic.Save(func(it *store.PipelineItem) { it.SearchReplaceDone = false })
	}
	s.saveCP(ctx, ic, cp, store.StepDone, "")
	return pipeline.StateCutoverDone, nil
}

func renameInstructions(ic *pipeline.ItemContext) string {
	return fmt.Sprintf("Lepas domain %s dari akun sumber %s (situs lama tetap hidup untuk rollback):\n"+
		"1. hPanel akun %s → Websites → %s → Dashboard → ganti domain (Change domain) ke domain sementara gratis yang ditawarkan hPanel.\n"+
		"2. Pastikan situs lama tetap bisa dibuka di domain sementara tersebut.\n"+
		"3. Jalankan: migrator confirm MANUAL_SOURCE_RENAME %s --nilai <domain-sementara>",
		ic.Item.SourceDomain, ic.Batch.SourceAccount, ic.Batch.SourceAccount, ic.Item.SourceDomain, ic.Item.SourceDomain)
}

func destDomainInstructions(ic *pipeline.ItemContext) string {
	return fmt.Sprintf("Pasang domain %s di akun tujuan %s:\n"+
		"1. hPanel akun %s → Websites → %s → Domains → tambahkan %s sebagai parked/alias domain (atau ganti domain website dummy ke %s).\n"+
		"2. Pastikan folder website tetap sama.\n"+
		"3. Jalankan: migrator confirm MANUAL_DEST_DOMAIN %s",
		ic.Item.SourceDomain, ic.Batch.DestAccount, ic.Batch.DestAccount, ic.Item.DummyDomain, ic.Item.SourceDomain, ic.Item.SourceDomain, ic.Item.SourceDomain)
}

func (s *Stage) updateDNS(ctx context.Context, ic *pipeline.ItemContext, approval *gate.Approval, api *hostinger.Client) ([]string, error) {
	domain := ic.Item.SourceDomain
	ip := ic.Item.DestIP
	if ip == "" {
		return nil, errors.New("IP server tujuan belum diketahui; jalankan verifikasi ulang")
	}
	external := ic.Website.NSExternal != nil && *ic.Website.NSExternal
	if external || !ic.Website.DNSInToken {
		if _, ok := ic.ManualConfirmed(ctx, "MANUAL_DNS"); ok {
			return []string{"manual"}, nil
		}
		why := "Zone DNS tidak dikelola token sumber"
		if external {
			why = "Domain memakai nameserver eksternal (" + strings.Join(ic.Website.Nameservers, ", ") + ")"
		}
		return nil, &pipeline.ManualError{Code: "MANUAL_DNS", Instructions: fmt.Sprintf("%s. Ubah record berikut di penyedia DNS:\n- A  @    → %s\n- A  www  → %s (atau CNAME www → %s)\n- Hapus AAAA @ dan www bila mengarah ke server lama\nRecord lain (MX, TXT, CNAME pihak ketiga) jangan diubah.\nLalu jalankan: migrator confirm MANUAL_DNS %s", why, ip, ip, domain, domain)}
	}
	if err := approval.Require(gate.KindCutover, domain); err != nil {
		return nil, err
	}
	zone, err := api.GetZone(ctx, domain)
	if err != nil {
		return nil, err
	}
	update := []map[string]any{{"name": "@", "type": "A", "ttl": 300, "records": []map[string]any{{"content": ip}}}}
	changed := []string{"A @ → " + ip}
	var deletes []map[string]string
	wwwIsCNAME := false
	for _, rr := range zone {
		switch {
		case rr.Name == "www" && rr.Type == "A":
			update = append(update, map[string]any{"name": "www", "type": "A", "ttl": 300, "records": []map[string]any{{"content": ip}}})
			changed = append(changed, "A www → "+ip)
		case rr.Name == "www" && rr.Type == "CNAME":
			wwwIsCNAME = true
		case (rr.Name == "@" || rr.Name == "www") && rr.Type == "AAAA":
			deletes = append(deletes, map[string]string{"name": rr.Name, "type": "AAAA"})
			changed = append(changed, "hapus AAAA "+rr.Name)
		}
	}
	hasWWW := wwwIsCNAME
	for _, u := range update {
		if u["name"] == "www" {
			hasWWW = true
		}
	}
	if !hasWWW {
		update = append(update, map[string]any{"name": "www", "type": "CNAME", "ttl": 300, "records": []map[string]any{{"content": domain + "."}}})
		changed = append(changed, "CNAME www → "+domain)
	}
	if _, err := api.Do(ctx, hostinger.Request{Endpoint: "dns.zones.update", PathParams: map[string]string{"domain": domain}, Body: map[string]any{"overwrite": true, "zone": update}}, nil); err != nil {
		return nil, fmt.Errorf("perubahan DNS %s gagal: %w", domain, err)
	}
	if len(deletes) > 0 {
		if _, err := api.Do(ctx, hostinger.Request{Endpoint: "dns.zones.delete_records", PathParams: map[string]string{"domain": domain}, Body: map[string]any{"filters": deletes}}, nil); err != nil {
			return nil, fmt.Errorf("hapus AAAA lama %s gagal: %w", domain, err)
		}
	}
	gate.Record(ctx, ic.Runner().Store, approval, "dns.update", domain, "OK", strings.Join(changed, "; "))
	return changed, nil
}

func (s *Stage) hSSL(ctx context.Context, ic *pipeline.ItemContext) (string, error) {
	r := ic.Runner()
	cp := s.loadCP(ctx, ic)
	api, err := ic.DestAPI()
	if err != nil {
		return "", err
	}
	domain := ic.Item.SourceDomain
	if !cp.SSLInstalled {
		_, err := api.Do(ctx, hostinger.Request{Endpoint: "hosting.ssl.setup", PathParams: map[string]string{"username": ic.Batch.DestAccount, "domain": domain}}, nil)
		cp.SSLStarted = s.now().UTC().Format(time.RFC3339)
		cp.SSLInstalled = true
		if err != nil {
			var apiErr *hostinger.APIError
			if errors.As(err, &apiErr) && apiErr.Kind == hostinger.KindClient {
				s.saveCP(ctx, ic, cp, store.StepDone, "SSL ditolak API: "+err.Error())
				r.Store.ReplaceVerifications(ctx, ic.Item.ID, "SSL_", []store.Verification{{CheckType: "SSL_STATUS", Result: verify.Warn, Detail: "Pemasangan SSL ditolak API: " + err.Error()}})
				return s.finalVerify(ctx, ic, false)
			}
			return "", err
		}
		s.saveCP(ctx, ic, cp, store.StepDone, "")
	}
	var st struct {
		Status string `json:"status"`
	}
	if _, err := api.Do(ctx, hostinger.Request{Endpoint: "hosting.ssl.status", PathParams: map[string]string{"username": ic.Batch.DestAccount, "domain": domain}}, &st); err != nil {
		return "", err
	}
	if st.Status == "active" {
		r.Store.ReplaceVerifications(ctx, ic.Item.ID, "SSL_", []store.Verification{{CheckType: "SSL_STATUS", Result: verify.Pass, Detail: "SSL aktif"}})
		return s.finalVerify(ctx, ic, true)
	}
	started, _ := time.Parse(time.RFC3339, cp.SSLStarted)
	wait := s.SSLWait
	if wait <= 0 {
		wait = time.Hour
	}
	if st.Status == "failed" || s.now().Sub(started) >= wait {
		r.Store.ReplaceVerifications(ctx, ic.Item.ID, "SSL_", []store.Verification{{CheckType: "SSL_STATUS", Result: verify.Warn, Detail: "SSL belum aktif (status " + st.Status + ")"}})
		return s.finalVerify(ctx, ic, false)
	}
	return "", fmt.Errorf("%w: SSL %s masih %s", pipeline.ErrWait, domain, st.Status)
}

func (s *Stage) finalVerify(ctx context.Context, ic *pipeline.ItemContext, sslActive bool) (string, error) {
	r := ic.Runner()
	v := r.Verifier
	rep := &verify.Report{}
	ip := ic.Item.DestIP
	v.FinalCheck(ctx, ic.Item.SourceDomain, ip, sslActive, s.HTTPSScheme, rep)
	vs := rep.Verifications("AKHIR_")
	vs = append(vs, store.Verification{CheckType: "AKHIR_HASIL", Result: rep.Overall(), Detail: "verifikasi akhir via domain asli"})
	r.Store.ReplaceVerifications(ctx, ic.Item.ID, "AKHIR_", vs)
	if sslActive {
		return pipeline.StateSSLActive, nil
	}
	return pipeline.StateSSLPending, nil
}

func (s *Stage) hAfterSSL(ctx context.Context, ic *pipeline.ItemContext) (string, error) {
	boxes, err := ic.Runner().Store.ListMailboxes(ctx, []string{ic.Item.SourceDomain})
	if err != nil {
		return "", err
	}
	if len(boxes) == 0 {
		return pipeline.StateNoEmail, nil
	}
	return pipeline.StateEmailRunning, nil
}
