package pipeline

import (
	"context"
	"errors"
	"fmt"
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
	if err := st.ConfirmManualTask(ctx, it.ID, code, value); err != nil {
		return nil, err
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
