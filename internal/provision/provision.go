package provision

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"

	"hostinger-migrator/internal/hostinger"
	"hostinger-migrator/internal/store"
)

var (
	ErrManual  = errors.New("pembuatan website dummy butuh langkah manual")
	ErrPending = errors.New("website dummy masih disiapkan Hostinger")
)

type Result struct {
	DummyDomain string
	UnitPath    string
	DocRoot     string
}

type Provisioner struct {
	Client       *hostinger.Client
	Account      store.HostingAccount
	PollInterval time.Duration
	Timeout      time.Duration
	Sleep        func(context.Context, time.Duration) error
	Now          func() time.Time
}

type onboarding struct {
	Domain string `json:"domain"`
	Status string `json:"status"`
}

func (p *Provisioner) sleep(ctx context.Context, d time.Duration) error {
	if p.Sleep != nil {
		return p.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func unitPath(root string) string {
	root = strings.TrimRight(root, "/")
	if strings.HasSuffix(root, "/public_html") {
		return path.Dir(root)
	}
	return root
}

func (p *Provisioner) Find(ctx context.Context, domain string) (*Result, error) {
	sites, err := p.Client.ListWebsites(ctx, hostinger.WebsiteFilter{Username: p.Account.ID, Domain: domain})
	if err != nil {
		return nil, err
	}
	for _, s := range sites {
		if strings.EqualFold(s.Domain, domain) && s.RootDirectory != "" {
			return &Result{DummyDomain: s.Domain, UnitPath: unitPath(s.RootDirectory), DocRoot: s.RootDirectory}, nil
		}
	}
	return nil, nil
}

func (p *Provisioner) setupStatus(ctx context.Context, domain string) (string, error) {
	var list []onboarding
	q := url.Values{}
	q.Set("domain", domain)
	if _, err := p.Client.Do(ctx, hostinger.Request{Endpoint: "hosting.onboardings.list", Query: q}, &list); err != nil {
		return "", err
	}
	for _, o := range list {
		if strings.EqualFold(o.Domain, domain) {
			return o.Status, nil
		}
	}
	return "", nil
}

func (p *Provisioner) GenerateDummy(ctx context.Context) (string, error) {
	var out struct {
		Domain string `json:"domain"`
	}
	if _, err := p.Client.Do(ctx, hostinger.Request{Endpoint: "hosting.free_subdomains.generate"}, &out); err != nil {
		if isUnsupported(err) {
			return "", fmt.Errorf("%w: generate subdomain gratis ditolak API (%v)", ErrManual, err)
		}
		return "", err
	}
	if out.Domain == "" {
		return "", errors.New("API tidak mengembalikan nama subdomain gratis")
	}
	return out.Domain, nil
}

func isUnsupported(err error) bool {
	var apiErr *hostinger.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Kind == hostinger.KindClient || apiErr.Kind == hostinger.KindNotFound
	}
	return false
}

func (p *Provisioner) Start(ctx context.Context, dummy string) error {
	if p.Account.OrderID == 0 {
		return fmt.Errorf("%w: order ID akun tujuan %s tidak diketahui", ErrManual, p.Account.ID)
	}
	body := map[string]any{"domain": dummy}
	_, err := p.Client.Do(ctx, hostinger.Request{Endpoint: "hosting.onboardings.start", PathParams: map[string]string{"order_id": fmt.Sprint(p.Account.OrderID)}, Body: body}, nil)
	if err == nil {
		return nil
	}
	if errors.Is(err, hostinger.ErrConflict) {
		return nil
	}
	if isUnsupported(err) {
		return fmt.Errorf("%w: onboarding ditolak API (%v)", ErrManual, err)
	}
	return err
}

func (p *Provisioner) Wait(ctx context.Context, dummy string) (*Result, error) {
	interval := p.PollInterval
	if interval <= 0 {
		interval = 12 * time.Second
	}
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	now := time.Now
	if p.Now != nil {
		now = p.Now
	}
	deadline := now().Add(timeout)
	for {
		status, err := p.setupStatus(ctx, dummy)
		if err != nil && !errors.Is(err, hostinger.ErrNotFound) {
			return nil, err
		}
		switch status {
		case "completed", "":
			res, err := p.Find(ctx, dummy)
			if err != nil {
				return nil, err
			}
			if res != nil && (status == "completed" || status == "") {
				return res, nil
			}
		case "failed":
			return nil, fmt.Errorf("Hostinger melaporkan setup website %s gagal", dummy)
		}
		if now().After(deadline) {
			return nil, fmt.Errorf("%w: %s belum selesai setelah %s", ErrPending, dummy, timeout)
		}
		if err := p.sleep(ctx, interval); err != nil {
			return nil, err
		}
	}
}

func (p *Provisioner) Ensure(ctx context.Context, current string, save func(dummy string) error) (*Result, error) {
	if current != "" {
		if res, err := p.Find(ctx, current); err != nil {
			return nil, err
		} else if res != nil {
			return res, nil
		}
		status, err := p.setupStatus(ctx, current)
		if err != nil && !errors.Is(err, hostinger.ErrNotFound) {
			return nil, err
		}
		if status == "running" {
			return p.Wait(ctx, current)
		}
		if err := p.Start(ctx, current); err != nil {
			if !isUnsupported(err) && !errors.Is(err, ErrManual) {
				return nil, err
			}
			fresh, gerr := p.GenerateDummy(ctx)
			if gerr != nil {
				return nil, err
			}
			current = fresh
			if err := save(current); err != nil {
				return nil, err
			}
			if err := p.Start(ctx, current); err != nil {
				return nil, err
			}
		}
		return p.Wait(ctx, current)
	}
	dummy, err := p.GenerateDummy(ctx)
	if err != nil {
		return nil, err
	}
	if err := save(dummy); err != nil {
		return nil, err
	}
	if err := p.Start(ctx, dummy); err != nil {
		if errors.Is(err, hostinger.ErrUncertain) {
			return p.Wait(ctx, dummy)
		}
		return nil, err
	}
	return p.Wait(ctx, dummy)
}

func ManualInstructions(destAccount, sourceDomain string) string {
	return fmt.Sprintf("API tidak bisa membuat website dummy untuk %s di akun %s.\n"+
		"1. hPanel akun %s → Websites → Add Website → pilih \"Skip, I'll create empty website\" / website kosong.\n"+
		"2. Pilih domain sementara gratis (*.hostingersite.com) yang ditawarkan hPanel.\n"+
		"3. Tunggu sampai website selesai dibuat, catat nama domain sementaranya.\n"+
		"4. Jalankan: migrator confirm MANUAL_CREATE_SITE %s --nilai <domain-sementara>",
		sourceDomain, destAccount, destAccount, sourceDomain)
}
