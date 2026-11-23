package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

var ErrInputClosed = errors.New("input berakhir")

type Prompter struct {
	in         *bufio.Reader
	out        io.Writer
	errOut     io.Writer
	readSecret func() (string, error)
	warned     bool
	errColor   bool
}

func NewPrompter(in io.Reader, out, errOut io.Writer, readSecret func() (string, error)) *Prompter {
	return &Prompter{in: bufio.NewReader(in), out: out, errOut: errOut, readSecret: readSecret}
}

func (p *Prompter) Line(prompt string) (string, error) {
	fmt.Fprint(p.out, prompt)
	s, err := p.in.ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && s != "") {
		if errors.Is(err, io.EOF) {
			fmt.Fprintln(p.out)
			return "", ErrInputClosed
		}
		return "", err
	}
	return strings.TrimRight(s, "\r\n"), nil
}

func (p *Prompter) Secret(prompt string) (string, error) {
	fmt.Fprint(p.out, prompt)
	if p.readSecret != nil {
		v, err := p.readSecret()
		fmt.Fprintln(p.out)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(v), nil
	}
	if !p.warned {
		fmt.Fprintln(p.errOut)
		fmt.Fprintln(p.errOut, paint(p.errColor, toneWarn, "PERINGATAN: terminal ini tidak mendukung input tersembunyi, token akan terlihat saat diketik."))
		fmt.Fprintln(p.errOut, paint(p.errColor, toneWarn, "Untuk menyembunyikannya di Git Bash, jalankan: winpty ./migrator.exe"))
		fmt.Fprint(p.out, prompt)
		p.warned = true
	}
	s, err := p.in.ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && s != "") {
		if errors.Is(err, io.EOF) {
			return "", ErrInputClosed
		}
		return "", err
	}
	return strings.TrimSpace(s), nil
}

func (p *Prompter) Confirm(prompt, word string) (bool, error) {
	v, err := p.Line(prompt)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(v) == word, nil
}

func TerminalSecretReader(f *os.File) func() (string, error) {
	fd := int(f.Fd())
	if !term.IsTerminal(fd) {
		return nil
	}
	return func() (string, error) {
		b, err := term.ReadPassword(fd)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
}
