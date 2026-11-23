package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"text/tabwriter"
)

func TestPaintOffForNonTerminal(t *testing.T) {
	a := newApp(Env{Stdin: strings.NewReader(""), Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}})
	if a.colorOut || a.colorErr || a.interactive {
		t.Fatal("buffer bukan terminal")
	}
	if a.color(toneOK, "x") != "x" || a.statusCell("AKTIF") != "AKTIF" {
		t.Fatal("tidak boleh ada kode ANSI")
	}
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if isTerminal(f) {
		t.Fatal("file biasa bukan terminal")
	}
}

func TestStatusCell(t *testing.T) {
	a := newApp(Env{Stdout: &bytes.Buffer{}})
	a.colorOut = true
	cases := map[string]string{
		"AKTIF": "92", "DITOLAK": "91", "BELUM_DIUJI": "93", "SUPPORTED": "92", "UNKNOWN": "93",
		"MANUAL": "93", "UNSUPPORTED": "91", "SSH_READY": "92", "SSH_GAGAL": "91", "BACKUP_RUNNING": "39",
		"VERIFIED/WAITING_MANUAL": "93", "RESTORE_RUNNING/FAILED": "91", "COMPLETED": "92",
		"BACKUP_RUNNING (percobaan 2)": "39",
	}
	for in, code := range cases {
		if got := a.statusCell(in); got != "\x1b["+code+"m"+in+"\x1b[0m" {
			t.Errorf("%s: %q", in, got)
		}
	}
}

var ansiCode = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func TestStatusColumnAligned(t *testing.T) {
	render := func(color bool) string {
		out := &bytes.Buffer{}
		a := newApp(Env{Stdout: out})
		a.colorOut = color
		tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		for _, r := range [][2]string{{"a", "AKTIF"}, {"bbbb", "DITOLAK"}, {"cc", "BACKUP_RUNNING"}} {
			fmt.Fprintf(tw, "%s\t%s\tx\n", r[0], a.statusCell(r[1]))
		}
		tw.Flush()
		return out.String()
	}
	plain, colored := render(false), render(true)
	if colored == plain {
		t.Fatal("versi berwarna harus memuat kode ANSI")
	}
	if got := ansiCode.ReplaceAllString(colored, ""); got != plain {
		t.Fatalf("kolom bergeser:\n%s\nvs\n%s", got, plain)
	}
}

func TestFinishPausesOnSubmenuError(t *testing.T) {
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	a := newApp(Env{Stdin: strings.NewReader("\n"), Stdout: out, Stderr: errb})
	a.interactive = true
	if err := a.finish(errors.New("rusak"), false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errb.String(), "ERROR: rusak") || !strings.Contains(out.String(), "Tekan ENTER untuk kembali ke menu.") {
		t.Fatal(out.String(), errb.String())
	}
	if err := a.finish(nil, false); err != nil {
		t.Fatal("tanpa error dan tanpa jeda tidak boleh membaca input")
	}
}
