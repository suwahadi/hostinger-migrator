package pipeline

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"hostinger-migrator/internal/store"
)

func Confirm(ctx context.Context, st *store.Store, code, domain, batchID, value string) (*store.PipelineItem, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	it, err := st.FindItem(ctx, domain, batchID)
	if err != nil {
		return nil, fmt.Errorf("website %s tidak ada di batch mana pun", domain)
	}
	t, err := st.GetManualTask(ctx, it.ID, code)
	if errors.Is(err, store.ErrNotFound) {
		return nil, fmt.Errorf("tidak ada tindakan manual %s untuk %s", code, domain)
	}
	if err != nil {
		return nil, err
	}
	if t.Status != store.ManualOpen {
		return nil, fmt.Errorf("tindakan manual %s untuk %s sudah dikonfirmasi pada %s", code, domain, t.ConfirmedAt)
	}
	var skipped []string
	if code == codeDBSource {
		value = strings.TrimSpace(value)
		switch {
		case value == "":
		case strings.EqualFold(value, skipValue):
			if skipped, err = unknownDatabases(ctx, st, it); err != nil {
				return nil, err
			}
			if len(skipped) == 0 {
				return nil, fmt.Errorf("tidak ada database bersumber tidak diketahui untuk %s; '%s' tidak diperlukan", domain, skipValue)
			}
			value = skipValue + ":" + strings.Join(skipped, ",")
		default:
			return nil, fmt.Errorf("nilai %s hanya boleh kosong atau '%s'", codeDBSource, skipValue)
		}
	}
	if err := st.ConfirmManualTask(ctx, it.ID, code, value); err != nil {
		return nil, err
	}
	if len(skipped) > 0 {
		if err := st.AppendAudit(ctx, nil, "operator", "item.db_skipped", it.SourceDomain, "OK", strings.Join(skipped, ", ")); err != nil {
			return nil, err
		}
	}
	return st.UpdateItem(ctx, it.ID, "operator", "item.resume", func(x *store.PipelineItem) error {
		if x.SideState == store.SideWaitingManual && x.ManualCode == code {
			x.SideState = ""
			x.ManualCode = ""
			x.LastError = ""
			x.RetryCount = 0
		}
		return nil
	})
}

func Retry(ctx context.Context, st *store.Store, batchID, domain string, includeBlocked bool) ([]string, error) {
	items, err := st.ListItems(ctx, batchID)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, it := range items {
		if domain != "" && it.SourceDomain != domain {
			continue
		}
		eligible := it.SideState == store.SideFailed || (includeBlocked && it.SideState == store.SideBlocked) || (domain != "" && it.SideState == "" && it.LastError != "")
		if !eligible {
			continue
		}
		if _, err := st.UpdateItem(ctx, it.ID, "operator", "item.retry", func(x *store.PipelineItem) error {
			x.SideState = ""
			x.RetryCount = 0
			x.LastError = ""
			return nil
		}); err != nil {
			return out, err
		}
		out = append(out, it.SourceDomain)
	}
	return out, nil
}

const (
	codeDBSource = "MANUAL_DB_SOURCE"
	skipValue    = "lewati"
)

func unknownDatabases(ctx context.Context, st *store.Store, it *store.PipelineItem) ([]string, error) {
	b, _, err := st.GetBatch(ctx, it.BatchID)
	if err != nil {
		return nil, err
	}
	dbs, err := st.ListDatabasesByAccount(ctx, b.SourceAccount)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, d := range dbs {
		if d.WebsiteDomain == it.SourceDomain && unknownSource(d) {
			out = append(out, d.Name)
		}
	}
	return out, nil
}

func SkipSnapshot(ctx context.Context, st *store.Store, it *store.PipelineItem) ([]string, error) {
	t, err := st.GetManualTask(ctx, it.ID, codeDBSource)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	names, ok := strings.CutPrefix(t.Value, skipValue+":")
	if t.Status != store.ManualConfirmed || !ok || names == "" {
		return nil, nil
	}
	return strings.Split(names, ","), nil
}

func SkippedDatabases(ctx context.Context, st *store.Store, it *store.PipelineItem) ([]string, error) {
	snapshot, err := SkipSnapshot(ctx, st, it)
	if err != nil || len(snapshot) == 0 {
		return nil, err
	}
	unknown, err := unknownDatabases(ctx, st, it)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, n := range snapshot {
		if slices.Contains(unknown, n) {
			out = append(out, n)
		}
	}
	return out, nil
}
