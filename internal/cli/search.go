package cli

import (
	"strings"

	"hostinger-migrator/internal/store"
)

const maxResults = 30

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
