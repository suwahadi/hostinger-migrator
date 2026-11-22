package imapx

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"hostinger-migrator/tests/imaptest"
)

func TestClientAgainstFakeServer(t *testing.T) {
	srv, err := imaptest.New()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	srv.AddUser("info@a.com", "Sandi-1!")
	big := bytes.Repeat([]byte("lampiran-besar-"), 50000)
	srv.Seed("info@a.com", "INBOX",
		imaptest.Message{Flags: []string{`\Seen`}, Date: "22-Nov-2026 10:00:00 +0700", Body: []byte("Subject: halo\r\n\r\nisi {5}\r\n")},
		imaptest.Message{Flags: nil, Date: "22-Nov-2026 11:00:00 +0700", Body: big},
	)
	ctx := context.Background()
	c, err := Dial(ctx, srv.Addr(), false, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Login("info@a.com", "salah"); !errors.Is(err, ErrAuth) {
		t.Fatalf("err = %v", err)
	}
	if err := c.Login("info@a.com", "Sandi-1!"); err != nil {
		t.Fatal(err)
	}
	if err := c.Create(`Arsip "2026"`); err != nil {
		t.Fatal(err)
	}
	if err := c.Create(`Arsip "2026"`); err != nil {
		t.Fatalf("CREATE folder yang sudah ada harus dianggap berhasil: %v", err)
	}
	boxes, err := c.List()
	if err != nil || len(boxes) != 2 || boxes[0].Name != `Arsip "2026"` || boxes[1].Name != "INBOX" || boxes[1].Delim != "." {
		t.Fatalf("list = %+v %v", boxes, err)
	}
	if n, err := c.Status("INBOX"); err != nil || n != 2 {
		t.Fatalf("status = %d %v", n, err)
	}
	var got []Message
	if err := c.FetchAll("INBOX", func(m Message) error { got = append(got, m); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].UID != 1 || got[0].Flags[0] != `\Seen` || got[0].InternalDate != "22-Nov-2026 10:00:00 +0700" || !bytes.Equal(got[1].Body, big) || !strings.Contains(string(got[0].Body), "isi {5}") {
		t.Fatalf("fetch salah: %d pesan", len(got))
	}
	if err := c.Append(`Arsip "2026"`, []string{`\Seen`, `\Recent`}, got[0].InternalDate, got[1].Body); err != nil {
		t.Fatal(err)
	}
	stored := srv.Folder("info@a.com", `Arsip "2026"`)
	if len(stored) != 1 || !bytes.Equal(stored[0].Body, big) || len(stored[0].Flags) != 1 || stored[0].Date != "22-Nov-2026 10:00:00 +0700" {
		t.Fatalf("append salah: %+v", len(stored))
	}
	if err := c.Append("TidakAda", nil, "", []byte("x")); err == nil {
		t.Fatal("append ke folder tidak ada harus error")
	}
	if err := c.FetchAll("Kosong", func(Message) error { return nil }); err == nil {
		t.Fatal("folder tidak ada harus error")
	}
	c.Logout()
}

func TestQuoteAndLiteral(t *testing.T) {
	if Quote(`a"b\c`) != `"a\"b\\c"` {
		t.Fatal("quote salah")
	}
	if n, ok := literalSize("* 1 FETCH (BODY[] {123}"); !ok || n != 123 {
		t.Fatal("literal salah")
	}
	if _, ok := literalSize("tanpa literal"); ok {
		t.Fatal("bukan literal")
	}
}
