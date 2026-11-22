package hostinger

import (
	"context"
	"net/url"
	"sort"
	"strconv"
)

type OrderPlan struct {
	Name string `json:"name"`
}

type Order struct {
	ID             int64     `json:"id"`
	ClientID       int64     `json:"client_id"`
	SubscriptionID string    `json:"subscription_id"`
	Status         string    `json:"status"`
	Plan           OrderPlan `json:"plan"`
	CreatedAt      string    `json:"created_at"`
}

type Website struct {
	Domain        string `json:"domain"`
	VhostType     string `json:"vhost_type"`
	IsEnabled     bool   `json:"is_enabled"`
	Username      string `json:"username"`
	ClientID      int64  `json:"client_id"`
	OrderID       int64  `json:"order_id"`
	CreatedAt     string `json:"created_at"`
	RootDirectory string `json:"root_directory"`
	ParentDomain  string `json:"parent_domain"`
	WebsiteType   string `json:"website_type"`
}

type WebsiteFilter struct {
	Username  string
	OrderID   int64
	Domain    string
	IsEnabled *bool
}

func (c *Client) ListOrders(ctx context.Context) ([]Order, error) {
	return ListAll[Order](ctx, c, Request{Endpoint: "hosting.orders.list"}, 100)
}

func (c *Client) ListWebsites(ctx context.Context, f WebsiteFilter) ([]Website, error) {
	q := url.Values{}
	if f.Username != "" {
		q.Set("username", f.Username)
	}
	if f.OrderID > 0 {
		q.Set("order_id", strconv.FormatInt(f.OrderID, 10))
	}
	if f.Domain != "" {
		q.Set("domain", f.Domain)
	}
	if f.IsEnabled != nil {
		q.Set("is_enabled", strconv.FormatBool(*f.IsEnabled))
	}
	return ListAll[Website](ctx, c, Request{Endpoint: "hosting.websites.list", Query: q}, 100)
}

type DiscoveredAccount struct {
	Username     string
	OrderID      int64
	Plan         string
	OrderStatus  string
	WebsiteCount int
	Domains      []string
}

type Discovery struct {
	Accounts          []DiscoveredAccount
	OrdersWithoutSite []Order
}

func Discover(ctx context.Context, c *Client) (*Discovery, error) {
	orders, err := c.ListOrders(ctx)
	if err != nil {
		return nil, err
	}
	sites, err := c.ListWebsites(ctx, WebsiteFilter{})
	if err != nil {
		return nil, err
	}
	return BuildDiscovery(orders, sites), nil
}

func BuildDiscovery(orders []Order, sites []Website) *Discovery {
	orderByID := make(map[int64]Order, len(orders))
	for _, o := range orders {
		orderByID[o.ID] = o
	}
	byUser := map[string]*DiscoveredAccount{}
	usedOrders := map[int64]bool{}
	for _, s := range sites {
		if s.Username == "" {
			continue
		}
		acc, ok := byUser[s.Username]
		if !ok {
			acc = &DiscoveredAccount{Username: s.Username, OrderID: s.OrderID}
			if o, ok := orderByID[s.OrderID]; ok {
				acc.Plan = o.Plan.Name
				acc.OrderStatus = o.Status
			}
			byUser[s.Username] = acc
		}
		usedOrders[s.OrderID] = true
		acc.WebsiteCount++
		if s.Domain != "" {
			acc.Domains = append(acc.Domains, s.Domain)
		}
	}
	d := &Discovery{}
	for _, acc := range byUser {
		sort.Strings(acc.Domains)
		d.Accounts = append(d.Accounts, *acc)
	}
	sort.Slice(d.Accounts, func(i, j int) bool { return d.Accounts[i].Username < d.Accounts[j].Username })
	for _, o := range orders {
		if !usedOrders[o.ID] {
			d.OrdersWithoutSite = append(d.OrdersWithoutSite, o)
		}
	}
	return d
}
