package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSaveInventoryRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	ns := true
	snap := &InventorySnapshot{AccountID: "u1", Alias: "xyz", SSHUsed: true, Warnings: []string{"peringatan"}, Websites: []Website{
		{Domain: "a.com", UnitPath: "/home/u1/domains/a.com", VhostType: "main", SiteType: "WordPress", TypeConfidence: 0.95, SizeBytes: 100, FileCount: 5, SizeSource: SizeFromSSH, DomainStatus: DomainActive, IsEnabled: true, NSExternal: &ns, Nameservers: []string{"ns1.x"}, Subdomains: []string{"blog.a.com"}, Signals: []string{"wp-config.php"}, Notes: []string{"catatan"}, MailOrderID: "mo"},
		{Domain: "blog.a.com", UnitPath: "/home/u1/domains/a.com/public_html/blog", VhostType: "subdomain", IncludedIn: "a.com", SizeBytes: -1, FileCount: -1},
		{Domain: "b.com", UnitPath: "/home/u1/domains/b.com", VhostType: "addon", DomainStatus: DomainExpired, SizeBytes: 50},
	}, Databases: []DatabaseRow{{WebsiteDomain: "a.com", Name: "u1_wp", User: "u1_wp", InAPI: true, SizeBytes: 10}, {Name: "u1_lain"}},
		Mailboxes: []MailboxRow{{Domain: "a.com", Address: "info@a.com", MessageCount: 3}},
		Cron:      []CronRow{{UID: "c1", Schedule: "* * * * *", Command: "php x"}}}
	if err := s.SaveInventory(ctx, snap); err != nil {
		t.Fatal(err)
	}
	ws, _ := s.ListWebsites(ctx, "u1")
	if len(ws) != 3 || ws[0].Domain != "a.com" || ws[0].NSExternal == nil || !*ws[0].NSExternal || ws[0].Subdomains[0] != "blog.a.com" || ws[0].MailOrderID != "mo" {
		t.Fatalf("ws = %+v", ws)
	}
	if ws[1].Domain != "b.com" || !ws[0].IsUnit() || ws[2].IsUnit() || !ws[1].DummyOnly() || ws[0].DummyOnly() {
		t.Fatal("unit/dummy salah")
	}
	dbs, _ := s.ListDatabasesByAccount(ctx, "u1")
	if len(dbs) != 2 || dbs[1].WebsiteDomain != "a.com" || dbs[0].WebsiteDomain != "" {
		t.Fatalf("dbs = %+v", dbs)
	}
	boxes, _ := s.ListMailboxes(ctx, []string{"a.com", "b.com"})
	if len(boxes) != 1 || boxes[0].MessageCount != 3 {
		t.Fatalf("boxes = %+v", boxes)
	}
	if empty, _ := s.ListMailboxes(ctx, nil); empty != nil {
		t.Fatal("tanpa domain harus kosong")
	}
	run, err := s.GetInventoryRun(ctx, "u1", "xyz")
	if err != nil || run.TotalSize != 150 || !run.SSHUsed || run.Warnings[0] != "peringatan" {
		t.Fatalf("run = %+v %v", run, err)
	}
	if _, err := s.GetInventoryRun(ctx, "u1", "lain"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	firstID := ws[0].ID
	snap.Websites = snap.Websites[:1]
	snap.Mailboxes = nil
	if err := s.SaveInventory(ctx, snap); err != nil {
		t.Fatal(err)
	}
	ws, _ = s.ListWebsites(ctx, "u1")
	if len(ws) != 1 || ws[0].ID != firstID {
		t.Fatalf("website hilang harus ditandai removed, id tetap: %+v", ws)
	}
	if boxes, _ := s.ListMailboxes(ctx, []string{"a.com"}); len(boxes) != 0 {
		t.Fatal("mailbox lama harus dihapus")
	}
	if err := s.SaveInventory(ctx, &InventorySnapshot{}); err == nil {
		t.Fatal("snapshot tanpa akun harus ditolak")
	}
}

func TestBatchIDsAndActiveItems(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	s.SetClock(func() time.Time { return time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC) })
	s.SaveInventory(ctx, &InventorySnapshot{AccountID: "u1", Alias: "xyz", Websites: []Website{{Domain: "a.com", UnitPath: "/x"}}})
	ws, _ := s.ListWebsites(ctx, "u1")
	b := Batch{SourceAlias: "xyz", SourceAccount: "u1", DestAlias: "zyx", DestAccount: "u2", PlanJSON: "{}"}
	id1, err := s.CreateBatch(ctx, b, []Item{{WebsiteID: ws[0].ID, SourceDomain: "a.com", State: ItemPlanned, ExpectedFinal: "COMPLETED"}})
	if err != nil || id1 != "MIG-20261122-001" {
		t.Fatalf("id1 = %s %v", id1, err)
	}
	id2, _ := s.CreateBatch(ctx, b, []Item{{SourceDomain: "b.com", State: ItemBlocked, LastError: "bentrok"}})
	if id2 != "MIG-20261122-002" {
		t.Fatalf("id2 = %s", id2)
	}
	active, _ := s.ActiveItemDomains(ctx, "u1")
	if active["a.com"] != id1 || active["b.com"] != "" {
		t.Fatalf("active = %v", active)
	}
	got, items, err := s.GetBatch(ctx, id2)
	if err != nil || got.DestAccount != "u2" || items[0].LastError != "bentrok" || items[0].WebsiteID != 0 {
		t.Fatalf("batch = %+v %+v %v", got, items, err)
	}
	if _, _, err := s.GetBatch(ctx, "MIG-X"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	all, _ := s.ListBatches(ctx)
	if len(all) != 2 {
		t.Fatalf("batches = %d", len(all))
	}
	entries, _ := s.ListAudit(ctx, 5)
	if entries[0].Action != "plan.create" {
		t.Fatalf("audit = %+v", entries[0])
	}
}
