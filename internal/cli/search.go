package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"hostinger-migrator/internal/store"
)

const (
	maxResults  = 30
	fullListMax = 20
)

type accountChoice struct {
	ID      string
	Domains []string
}

type websiteHit struct {
	Account string
	Site    store.Website
}

func matchWebsites(listed []listedAccount, query string) []websiteHit {
	q := strings.ToLower(strings.TrimSpace(query))
	var out []websiteHit
	for _, la := range listed {
		accountHit := strings.Contains(strings.ToLower(la.Account.ID), q)
		for _, w := range la.Units {
			if accountHit || strings.Contains(strings.ToLower(w.Domain), q) {
				out = append(out, websiteHit{Account: la.Account.ID, Site: w})
			}
		}
	}
	return out
}

func exactWebsites(listed []listedAccount, input string) ([]websiteHit, []string, bool) {
	var hits []websiteHit
	var missing []string
	names := strings.Split(input, ",")
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		found := false
		for _, la := range listed {
			for _, w := range la.Units {
				if strings.EqualFold(w.Domain, name) {
					hits = append(hits, websiteHit{Account: la.Account.ID, Site: w})
					found = true
				}
			}
		}
		if !found {
			missing = append(missing, name)
		}
	}
	exact := len(names) > 1 || len(hits) > 0
	return hits, missing, exact
}

func matchAccounts(items []accountChoice, query string) []accountChoice {
	q := strings.ToLower(strings.TrimSpace(query))
	var out []accountChoice
	for _, it := range items {
		if strings.Contains(strings.ToLower(it.ID), q) {
			out = append(out, accountChoice{ID: it.ID})
			continue
		}
		var hit []string
		for _, d := range it.Domains {
			if strings.Contains(strings.ToLower(d), q) {
				hit = append(hit, d)
			}
		}
		if len(hit) > 0 {
			out = append(out, accountChoice{ID: it.ID, Domains: hit})
		}
	}
	return out
}

func (a *App) chooseAccountFrom(items []accountChoice, prompt string, allowEmpty bool, describe func(id string) string) (string, error) {
	var shown []accountChoice
	show := func(list []accountChoice) {
		shown = list[:min(len(list), maxResults)]
		for i, it := range shown {
			line := fmt.Sprintf("%2d. %s  %s", i+1, it.ID, describe(it.ID))
			if len(it.Domains) > 0 {
				line += "  " + strings.Join(it.Domains[:min(len(it.Domains), 3)], ", ")
			}
			a.println(line)
		}
		if len(list) > len(shown) {
			a.say(toneWarn, fmt.Sprintf("Menampilkan %d dari %d. Persempit kata kunci.", len(shown), len(list)))
		}
	}
	if len(items) <= fullListMax {
		plain := make([]accountChoice, len(items))
		for i, it := range items {
			plain[i] = accountChoice{ID: it.ID}
		}
		show(plain)
	} else {
		a.println("Ketik sebagian id akun atau domain untuk mencari.")
	}
	for {
		v, err := a.prompt.Line(prompt)
		if err != nil {
			return "", err
		}
		v = strings.TrimSpace(v)
		if v == "" {
			if allowEmpty {
				return "", nil
			}
			a.say(toneWarn, "Belum ada akun dipilih.")
			continue
		}
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= len(shown) {
			return shown[n-1].ID, nil
		}
		for _, it := range items {
			if strings.EqualFold(it.ID, v) {
				return it.ID, nil
			}
		}
		hits := matchAccounts(items, v)
		if len(hits) == 0 {
			a.say(toneWarn, fmt.Sprintf("Tidak ada akun yang cocok dengan %q.", v))
			continue
		}
		a.printf("Hasil %q (%d):\n", v, len(hits))
		show(hits)
	}
}

func (a *App) accountChoices(ctx context.Context, ids []string) ([]accountChoice, error) {
	items := make([]accountChoice, 0, len(ids))
	for _, id := range ids {
		ws, err := a.store.ListWebsites(ctx, id)
		if err != nil {
			return nil, err
		}
		it := accountChoice{ID: id}
		for _, w := range ws {
			it.Domains = append(it.Domains, w.Domain)
		}
		items = append(items, it)
	}
	return items, nil
}
