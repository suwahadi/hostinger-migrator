package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/mattn/go-isatty"
	"golang.org/x/term"
)

var ANSIDisabled bool

type tone int

const (
	toneNone tone = iota
	toneTitle
	toneOK
	toneDanger
	toneWarn
)

var toneCodes = map[tone]string{
	toneNone:   "39",
	toneTitle:  "1;94",
	toneOK:     "92",
	toneDanger: "91",
	toneWarn:   "93",
}

const (
	ansiReset   = "\x1b[0m"
	ansiClear   = "\x1b[H\x1b[2J\x1b[3J"
	pausePrompt = "Tekan ENTER untuk kembali ke menu. "
)

var statusTones = map[string]tone{
	"DITOLAK": toneDanger, "ERROR": toneDanger, "UNSUPPORTED": toneDanger, "SSH_GAGAL": toneDanger,
	"BLOCKED": toneDanger, "FAILED": toneDanger, "FAIL": toneDanger,
	"BELUM_DIUJI": toneWarn, "UNKNOWN": toneWarn, "MANUAL": toneWarn, "WAITING_MANUAL": toneWarn,
	"SSL_PENDING": toneWarn, "WARN": toneWarn, "DUMMY_ONLY": toneWarn,
	"AKTIF": toneOK, "SUPPORTED": toneOK, "SSH_READY": toneOK, "VERIFIED": toneOK, "PASS": toneOK,
	"SSL_ACTIVE": toneOK, "CUTOVER_DONE": toneOK, "EMAIL_DONE": toneOK, "NO_EMAIL": toneOK,
	"CLEANUP_READY": toneOK, "CLEANED": toneOK, "COMPLETED": toneOK,
}

var tonePriority = []tone{toneDanger, toneWarn, toneOK}

func isTerminal(v any) bool {
	f, ok := v.(*os.File)
	if !ok || f == nil || ANSIDisabled {
		return false
	}
	fd := f.Fd()
	return term.IsTerminal(int(fd)) || isatty.IsCygwinTerminal(fd)
}

func paint(on bool, t tone, s string) string {
	if !on {
		return s
	}
	return "\x1b[" + toneCodes[t] + "m" + s + ansiReset
}

func statusTone(s string) tone {
	found := map[tone]bool{}
	for _, tok := range strings.FieldsFunc(strings.TrimSpace(s), func(r rune) bool {
		return r == '/' || r == ' ' || r == '(' || r == ')'
	}) {
		if t, ok := statusTones[tok]; ok {
			found[t] = true
		}
	}
	for _, t := range tonePriority {
		if found[t] {
			return t
		}
	}
	return toneNone
}

func (a *App) color(t tone, s string) string {
	return paint(a.colorOut, t, s)
}

func (a *App) say(t tone, s string) {
	a.println(a.color(t, s))
}

func (a *App) statusCell(s string) string {
	return a.color(statusTone(s), s)
}

func (a *App) printError(msg string) {
	fmt.Fprintln(a.env.Stderr, paint(a.colorErr, toneDanger, "ERROR: "+a.redactor.String(msg)))
}

func (a *App) clearScreen() {
	if a.interactive {
		fmt.Fprint(a.env.Stdout, ansiClear)
	}
}

func (a *App) pause() error {
	if !a.interactive {
		return nil
	}
	_, err := a.prompt.Line(pausePrompt)
	return err
}

func (a *App) finish(err error, pause bool) error {
	if errors.Is(err, ErrInputClosed) {
		return err
	}
	if err != nil {
		a.printError(err.Error())
	}
	if pause || err != nil {
		return a.pause()
	}
	return nil
}
