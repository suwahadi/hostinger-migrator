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

type Database struct {
	Name        string `json:"name"`
	User        string `json:"user"`
	Domain      string `json:"domain"`
	DiskUsageMB int64  `json:"disk_usage_mb"`
	MaxSizeMB   int64  `json:"max_size_mb"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
}

func (c *Client) ListDatabases(ctx context.Context, username string) ([]Database, error) {
	return ListAll[Database](ctx, c, Request{Endpoint: "hosting.databases.list", PathParams: map[string]string{"username": username}}, 100)
}

type Subdomain struct {
	Username      string `json:"username"`
	Domain        string `json:"domain"`
	ParentDomain  string `json:"parent_domain"`
	RootDirectory string `json:"root_directory"`
	Subdomain     string `json:"subdomain"`
}

func (c *Client) ListSubdomains(ctx context.Context, username, domain string) ([]Subdomain, error) {
	var out []Subdomain
	_, err := c.Do(ctx, Request{Endpoint: "hosting.subdomains.list", PathParams: map[string]string{"username": username, "domain": domain}}, &out)
	return out, err
}

type ParkedDomain struct {
	Domain        string `json:"domain"`
	ParentDomain  string `json:"parent_domain"`
	RootDirectory string `json:"root_directory"`
	Type          string `json:"type"`
}

func (c *Client) ListParkedDomains(ctx context.Context, username, domain string) ([]ParkedDomain, error) {
	var out []ParkedDomain
	_, err := c.Do(ctx, Request{Endpoint: "hosting.parked_domains.list", PathParams: map[string]string{"username": username, "domain": domain}}, &out)
	return out, err
}

type PortfolioDomain struct {
	ID        int64  `json:"id"`
	Domain    string `json:"domain"`
	Type      string `json:"type"`
	Status    string `json:"status"`
	ExpiresAt string `json:"expires_at"`
}

func (c *Client) ListPortfolio(ctx context.Context) ([]PortfolioDomain, error) {
	var out []PortfolioDomain
	_, err := c.Do(ctx, Request{Endpoint: "domains.portfolio.list"}, &out)
	return out, err
}

type DNSRecord struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	TTL     int    `json:"ttl"`
	Records []struct {
		Content string `json:"content"`
	} `json:"records"`
}

func (c *Client) GetZone(ctx context.Context, domain string) ([]DNSRecord, error) {
	var out []DNSRecord
	_, err := c.Do(ctx, Request{Endpoint: "dns.zones.get", PathParams: map[string]string{"domain": domain}}, &out)
	return out, err
}

type MailOrder struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Seats  int    `json:"seats"`
	Domain *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"domain"`
	Plan *struct {
		Name  string `json:"name"`
		Title string `json:"title"`
	} `json:"plan"`
}

func (o MailOrder) DomainName() string {
	if o.Domain == nil {
		return ""
	}
	return o.Domain.Name
}

func (o MailOrder) PlanName() string {
	if o.Plan == nil {
		return ""
	}
	return o.Plan.Name
}

func (c *Client) ListMailOrders(ctx context.Context) ([]MailOrder, error) {
	return ListAll[MailOrder](ctx, c, Request{Endpoint: "mail.orders.list"}, 100)
}

type Mailbox struct {
	ID         string `json:"id"`
	Address    string `json:"address"`
	Status     string `json:"status"`
	IsCatchall bool   `json:"is_catchall"`
	Usage      struct {
		StorageUsed   int64 `json:"storage_used"`
		StorageQuota  int64 `json:"storage_quota"`
		MessagesUsed  int64 `json:"messages_used"`
		MessagesQuota int64 `json:"messages_quota"`
	} `json:"usage"`
}

func (c *Client) ListMailboxes(ctx context.Context, orderID string) ([]Mailbox, error) {
	return ListAll[Mailbox](ctx, c, Request{Endpoint: "mail.mailboxes.list", PathParams: map[string]string{"orderId": orderID}}, 100)
}

type CronJob struct {
	UID      string `json:"uid"`
	Username string `json:"username"`
	Time     string `json:"time"`
	Command  string `json:"command"`
}

func (c *Client) ListCronJobs(ctx context.Context, username string) ([]CronJob, error) {
	var out []CronJob
	_, err := c.Do(ctx, Request{Endpoint: "hosting.cron_jobs.list", PathParams: map[string]string{"username": username}}, &out)
	return out, err
}

type PHPDetails struct {
	Version     string `json:"php_version"`
	VersionFull string `json:"php_version_full"`
}

func (c *Client) GetPHPDetails(ctx context.Context, username, domain string) (*PHPDetails, error) {
	var out PHPDetails
	_, err := c.Do(ctx, Request{Endpoint: "hosting.php.details", PathParams: map[string]string{"username": username, "domain": domain}}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

type FileEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Type string `json:"type"`
}

func (c *Client) ListFiles(ctx context.Context, username, domain string) ([]FileEntry, error) {
	var out struct {
		Items []FileEntry `json:"items"`
	}
	_, err := c.Do(ctx, Request{Endpoint: "hosting.files.list", PathParams: map[string]string{"username": username, "domain": domain}}, &out)
	return out.Items, err
}
