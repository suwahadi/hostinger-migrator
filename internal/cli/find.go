package cli

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"hostinger-migrator/internal/hostinger"
)

type foundSite struct {
	Domain  string
	Account string
	Vhost   string
	Aliases []string
}

type siteData struct {
	Sites  []foundSite
	Local  int
	API    int
	Missed int
	Warns  []string
}

func (a *App) remoteWebsites(ctx context.Context, alias string) ([]hostinger.Website, error) {
	c, err := a.tokenClient(alias)
	if err != nil {
		return nil, err
	}
	return c.ReadOnlyCopy().ListWebsites(ctx, hostinger.WebsiteFilter{})
}

func (a *App) collectSites(ctx context.Context, remote func(ctx context.Context, alias string) ([]hostinger.Website, error)) (*siteData, error) {
	accs, err := a.store.ListHostingAccounts(ctx, "")
	if err != nil {
		return nil, err
	}
	aliases := map[string][]string{}
	scanned := map[string]bool{}
	for _, acc := range accs {
		if !slices.Contains(aliases[acc.ID], acc.Alias) {
			aliases[acc.ID] = append(aliases[acc.ID], acc.Alias)
		}
		if _, err := a.store.GetInventoryRun(ctx, acc.ID, acc.Alias); err == nil {
			scanned[acc.ID] = true
		}
	}
	byKey := map[string]*foundSite{}
	add := func(domain, account, vhost string) {
		key := strings.ToLower(domain) + "|" + account
		if byKey[key] == nil {
			byKey[key] = &foundSite{Domain: domain, Account: account, Vhost: vhost}
		}
	}
	unscanned := map[string][]string{}
	for id := range aliases {
		if scanned[id] {
			ws, err := a.store.ListWebsites(ctx, id)
			if err != nil {
				return nil, err
			}
			for _, w := range ws {
				add(w.Domain, id, w.VhostType)
			}
			continue
		}
		for _, al := range aliases[id] {
			unscanned[al] = append(unscanned[al], id)
		}
	}
	tokens := make([]string, 0, len(unscanned))
	for al := range unscanned {
		tokens = append(tokens, al)
	}
	sort.Strings(tokens)
	fetched := map[string]bool{}
	failed := map[string]string{}
	for _, al := range tokens {
		a.printf("Mengambil daftar website token %s dari API...\n", al)
		list, err := remote(ctx, al)
		if err != nil {
			failed[al] = a.redactor.String(err.Error())
			continue
		}
		for _, id := range unscanned[al] {
			fetched[id] = true
		}
		for _, w := range list {
			if slices.Contains(unscanned[al], w.Username) {
				add(w.Domain, w.Username, w.VhostType)
			}
		}
	}
	d := &siteData{Local: len(scanned), API: len(fetched), Missed: len(aliases) - len(scanned) - len(fetched)}
	for _, al := range tokens {
		reason, ok := failed[al]
		if !ok {
			continue
		}
		missed := 0
		for _, id := range unscanned[al] {
			if !fetched[id] {
				missed++
			}
		}
		if missed == 0 {
			d.Warns = append(d.Warns, fmt.Sprintf("Token %s: daftar website tidak bisa diambil (%s); semua akunnya terambil lewat token lain.", al, reason))
		} else {
			d.Warns = append(d.Warns, fmt.Sprintf("Token %s: daftar website tidak bisa diambil (%s); %d akun yang belum dipindai di token ini tidak ikut dicari.", al, reason, missed))
		}
	}
	for _, s := range byKey {
		s.Aliases = append([]string(nil), aliases[s.Account]...)
		sort.Strings(s.Aliases)
		d.Sites = append(d.Sites, *s)
	}
	sort.Slice(d.Sites, func(i, j int) bool {
		if d.Sites[i].Domain != d.Sites[j].Domain {
			return d.Sites[i].Domain < d.Sites[j].Domain
		}
		return d.Sites[i].Account < d.Sites[j].Account
	})
	return d, nil
}

func matchFound(sites []foundSite, query string) []foundSite {
	q := strings.ToLower(strings.TrimSpace(query))
	var out []foundSite
	for _, s := range sites {
		if strings.Contains(strings.ToLower(s.Domain), q) {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Domain) < strings.ToLower(out[j].Domain) })
	return out
}

func foundLine(n int, s foundSite) string {
	label := ""
	if s.Vhost == "subdomain" || s.Vhost == "parked" {
		label = " (" + s.Vhost + ")"
	}
	return fmt.Sprintf("(%d). %s ada di akun: %s - %s%s", n, s.Domain, s.Account, strings.Join(s.Aliases, ", "), label)
}

func (a *App) findWebsiteMenu(ctx context.Context) error {
	return a.findWebsiteMenuWith(ctx, a.remoteWebsites)
}

func (a *App) findWebsiteMenuWith(ctx context.Context, remote func(ctx context.Context, alias string) ([]hostinger.Website, error)) error {
	a.say(toneTitle, "CARI WEBSITE")
	d, err := a.collectSites(ctx, remote)
	if err != nil {
		return err
	}
	a.println(fmt.Sprintf("Data: %d akun dari inventaris lokal, %d akun dari API (%d website).", d.Local, d.API, len(d.Sites)))
	for _, w := range d.Warns {
		a.say(toneWarn, w)
	}
	a.println()
	a.println("[0] Kembali ke menu utama")
	for {
		v, err := a.prompt.Line("\nCari (ketik sebagian domain, 0 = kembali): ")
		if err != nil {
			return err
		}
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if v == "0" {
			return nil
		}
		a.clearScreen()
		a.say(toneTitle, "CARI WEBSITE")
		hits := matchFound(d.Sites, v)
		if len(hits) == 0 {
			a.say(toneWarn, fmt.Sprintf("Tidak ada website yang cocok dengan %q.", v))
		} else {
			a.println("Ditemukan:")
			shown := hits[:min(len(hits), maxResults)]
			for i, s := range shown {
				a.println(foundLine(i+1, s))
			}
			if len(hits) > len(shown) {
				a.say(toneWarn, fmt.Sprintf("Menampilkan %d dari %d. Persempit kata kunci.", len(shown), len(hits)))
			}
		}
		if d.Missed > 0 {
			a.say(toneWarn, fmt.Sprintf("Catatan: %d akun yang belum dipindai tidak ikut dicari karena API gagal.", d.Missed))
		}
		a.println()
		a.println("[0] Kembali ke menu utama")
	}
}
