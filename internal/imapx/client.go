package imapx

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var ErrAuth = errors.New("login IMAP ditolak")

type Client struct {
	conn    net.Conn
	r       *bufio.Reader
	tag     int
	timeout time.Duration
}

type Mailbox struct {
	Name  string
	Delim string
	Attrs []string
}

func (m Mailbox) Selectable() bool {
	for _, a := range m.Attrs {
		if strings.EqualFold(a, `\Noselect`) || strings.EqualFold(a, `\NonExistent`) {
			return false
		}
	}
	return true
}

type Message struct {
	UID          uint32
	Flags        []string
	InternalDate string
	Body         []byte
}

type line struct {
	text     string
	literals [][]byte
}

func Dial(ctx context.Context, addr string, useTLS bool, timeout time.Duration) (*Client, error) {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	d := &net.Dialer{Timeout: timeout}
	var conn net.Conn
	var err error
	if useTLS {
		host, _, _ := net.SplitHostPort(addr)
		conn, err = (&tls.Dialer{NetDialer: d, Config: &tls.Config{ServerName: host}}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return nil, err
	}
	c := &Client{conn: conn, r: bufio.NewReaderSize(conn, 64<<10), timeout: timeout}
	c.deadline()
	greet, err := c.readLine()
	if err != nil {
		conn.Close()
		return nil, err
	}
	if !strings.HasPrefix(greet.text, "* OK") && !strings.HasPrefix(greet.text, "* PREAUTH") {
		conn.Close()
		return nil, fmt.Errorf("salam IMAP tidak dikenal: %s", greet.text)
	}
	return c, nil
}

func (c *Client) deadline() {
	c.conn.SetDeadline(time.Now().Add(c.timeout))
}

func (c *Client) Close() error {
	return c.conn.Close()
}

func (c *Client) readLine() (line, error) {
	var l line
	var b strings.Builder
	for {
		s, err := c.r.ReadString('\n')
		if err != nil {
			return l, err
		}
		s = strings.TrimRight(s, "\r\n")
		if n, ok := literalSize(s); ok {
			b.WriteString(s)
			buf := make([]byte, n)
			if _, err := io.ReadFull(c.r, buf); err != nil {
				return l, err
			}
			l.literals = append(l.literals, buf)
			c.deadline()
			continue
		}
		b.WriteString(s)
		l.text = b.String()
		return l, nil
	}
}

var literalRe = regexp.MustCompile(`\{(\d+)\+?\}$`)

func literalSize(s string) (int, bool) {
	m := literalRe.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	return n, err == nil
}

func Quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func (c *Client) cmd(format string, args ...any) (string, error) {
	c.tag++
	tag := fmt.Sprintf("M%04d", c.tag)
	c.deadline()
	if _, err := fmt.Fprintf(c.conn, tag+" "+format+"\r\n", args...); err != nil {
		return "", err
	}
	return tag, nil
}

func (c *Client) collect(tag string, onUntagged func(line) error) error {
	for {
		l, err := c.readLine()
		if err != nil {
			return err
		}
		if strings.HasPrefix(l.text, tag+" ") {
			rest := strings.TrimPrefix(l.text, tag+" ")
			switch {
			case strings.HasPrefix(rest, "OK"):
				return nil
			case strings.HasPrefix(rest, "NO"):
				return fmt.Errorf("server IMAP menolak: %s", rest)
			default:
				return fmt.Errorf("server IMAP error: %s", rest)
			}
		}
		if onUntagged != nil && strings.HasPrefix(l.text, "* ") {
			if err := onUntagged(l); err != nil {
				return err
			}
		}
	}
}

func (c *Client) Login(user, pass string) error {
	tag, err := c.cmd("LOGIN %s %s", Quote(user), Quote(pass))
	if err != nil {
		return err
	}
	if err := c.collect(tag, nil); err != nil {
		return fmt.Errorf("%w: %v", ErrAuth, err)
	}
	return nil
}

var listRe = regexp.MustCompile(`^\* LIST \(([^)]*)\) (NIL|"(?:[^"\\]|\\.)*") (.*)$`)

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return strings.NewReplacer(`\"`, `"`, `\\`, `\`).Replace(s[1 : len(s)-1])
	}
	return s
}

func (c *Client) List() ([]Mailbox, error) {
	tag, err := c.cmd(`LIST "" "*"`)
	if err != nil {
		return nil, err
	}
	var out []Mailbox
	err = c.collect(tag, func(l line) error {
		m := listRe.FindStringSubmatch(l.text)
		if m == nil {
			return nil
		}
		name := m[3]
		if len(l.literals) > 0 {
			name = string(l.literals[0])
		}
		mb := Mailbox{Name: unquote(name), Delim: unquote(m[2]), Attrs: strings.Fields(m[1])}
		if m[2] == "NIL" {
			mb.Delim = ""
		}
		out = append(out, mb)
		return nil
	})
	return out, err
}

var statusRe = regexp.MustCompile(`MESSAGES (\d+)`)

func (c *Client) Status(name string) (int, error) {
	tag, err := c.cmd("STATUS %s (MESSAGES)", Quote(name))
	if err != nil {
		return 0, err
	}
	n := -1
	err = c.collect(tag, func(l line) error {
		if strings.HasPrefix(l.text, "* STATUS") {
			if m := statusRe.FindStringSubmatch(l.text); m != nil {
				n, _ = strconv.Atoi(m[1])
			}
		}
		return nil
	})
	if err == nil && n < 0 {
		return 0, errors.New("respons STATUS tanpa jumlah pesan")
	}
	return n, err
}

var existsRe = regexp.MustCompile(`^\* (\d+) EXISTS`)

func (c *Client) Examine(name string) (int, error) {
	tag, err := c.cmd("EXAMINE %s", Quote(name))
	if err != nil {
		return 0, err
	}
	n := 0
	err = c.collect(tag, func(l line) error {
		if m := existsRe.FindStringSubmatch(l.text); m != nil {
			n, _ = strconv.Atoi(m[1])
		}
		return nil
	})
	return n, err
}

var (
	uidRe   = regexp.MustCompile(`UID (\d+)`)
	flagsRe = regexp.MustCompile(`FLAGS \(([^)]*)\)`)
	dateRe  = regexp.MustCompile(`INTERNALDATE "([^"]*)"`)
)

func (c *Client) FetchAll(name string, fn func(Message) error) error {
	n, err := c.Examine(name)
	if err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	tag, err := c.cmd("UID FETCH 1:* (UID FLAGS INTERNALDATE BODY.PEEK[])")
	if err != nil {
		return err
	}
	return c.collect(tag, func(l line) error {
		if !strings.Contains(l.text, " FETCH ") {
			return nil
		}
		var msg Message
		if m := uidRe.FindStringSubmatch(l.text); m != nil {
			v, _ := strconv.ParseUint(m[1], 10, 32)
			msg.UID = uint32(v)
		}
		if m := flagsRe.FindStringSubmatch(l.text); m != nil {
			msg.Flags = strings.Fields(m[1])
		}
		if m := dateRe.FindStringSubmatch(l.text); m != nil {
			msg.InternalDate = m[1]
		}
		if len(l.literals) == 0 {
			return fmt.Errorf("pesan UID %d tanpa isi", msg.UID)
		}
		msg.Body = l.literals[len(l.literals)-1]
		return fn(msg)
	})
}

func (c *Client) Create(name string) error {
	tag, err := c.cmd("CREATE %s", Quote(name))
	if err != nil {
		return err
	}
	err = c.collect(tag, nil)
	if err != nil && (strings.Contains(strings.ToUpper(err.Error()), "ALREADYEXISTS") || strings.Contains(strings.ToLower(err.Error()), "exist")) {
		return nil
	}
	return err
}

func (c *Client) Append(name string, flags []string, date string, body []byte) error {
	var keep []string
	for _, f := range flags {
		if !strings.EqualFold(f, `\Recent`) {
			keep = append(keep, f)
		}
	}
	c.tag++
	tag := fmt.Sprintf("M%04d", c.tag)
	c.deadline()
	head := fmt.Sprintf("%s APPEND %s (%s)", tag, Quote(name), strings.Join(keep, " "))
	if date != "" {
		head += " " + Quote(date)
	}
	head += fmt.Sprintf(" {%d}\r\n", len(body))
	if _, err := io.WriteString(c.conn, head); err != nil {
		return err
	}
	cont, err := c.readLine()
	if err != nil {
		return err
	}
	if !strings.HasPrefix(cont.text, "+") {
		return fmt.Errorf("server IMAP menolak APPEND: %s", cont.text)
	}
	if _, err := c.conn.Write(body); err != nil {
		return err
	}
	if _, err := io.WriteString(c.conn, "\r\n"); err != nil {
		return err
	}
	return c.collect(tag, nil)
}

func (c *Client) Logout() {
	if tag, err := c.cmd("LOGOUT"); err == nil {
		c.collect(tag, nil)
	}
	c.Close()
}
