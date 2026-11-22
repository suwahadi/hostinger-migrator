package imaptest

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type Message struct {
	UID   uint32
	Flags []string
	Date  string
	Body  []byte
}

type account struct {
	password string
	folders  map[string][]Message
	nextUID  uint32
}

type Server struct {
	listener         net.Listener
	mu               sync.Mutex
	accounts         map[string]*account
	Appends          int
	DropAfterAppends int
	wg               sync.WaitGroup
}

func New() (*Server, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &Server{listener: l, accounts: map[string]*account{}}
	go s.serve()
	return s, nil
}

func (s *Server) Addr() string { return s.listener.Addr().String() }

func (s *Server) Close() error {
	err := s.listener.Close()
	s.wg.Wait()
	return err
}

func (s *Server) AddUser(addr, password string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a, ok := s.accounts[addr]; ok {
		a.password = password
		return
	}
	s.accounts[addr] = &account{password: password, folders: map[string][]Message{"INBOX": nil}, nextUID: 1}
}

func (s *Server) SetPassword(addr, password string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.accounts[addr]
	if ok {
		a.password = password
	}
	return ok
}

func (s *Server) DeleteUser(addr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.accounts, addr)
}

func (s *Server) Seed(addr, folder string, msgs ...Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.accounts[addr]
	if _, ok := a.folders[folder]; !ok {
		a.folders[folder] = nil
	}
	for _, m := range msgs {
		m.UID = a.nextUID
		a.nextUID++
		a.folders[folder] = append(a.folders[folder], m)
	}
}

func (s *Server) Folder(addr, folder string) []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.accounts[addr]
	if !ok {
		return nil
	}
	return append([]Message(nil), a.folders[folder]...)
}

func (s *Server) Folders(addr string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	if a, ok := s.accounts[addr]; ok {
		for f := range a.folders {
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

func (s *Server) serve() {
	for {
		c, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handle(c)
		}()
	}
}

var (
	cmdRe    = regexp.MustCompile(`^(\S+) (.*)$`)
	quotedRe = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`)
	appendRe = regexp.MustCompile(`^APPEND "((?:[^"\\]|\\.)*)" \(([^)]*)\)(?: "([^"]*)")? \{(\d+)\}$`)
)

func unq(s string) string {
	return strings.NewReplacer(`\"`, `"`, `\\`, `\`).Replace(s)
}

func q(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func (s *Server) handle(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	w := bufio.NewWriter(c)
	send := func(format string, args ...any) {
		fmt.Fprintf(w, format+"\r\n", args...)
		w.Flush()
	}
	send("* OK IMAP tiruan siap")
	var user string
	selected := ""
	for {
		raw, err := r.ReadString('\n')
		if err != nil {
			return
		}
		raw = strings.TrimRight(raw, "\r\n")
		m := cmdRe.FindStringSubmatch(raw)
		if m == nil {
			continue
		}
		tag, rest := m[1], m[2]
		upper := strings.ToUpper(rest)
		switch {
		case strings.HasPrefix(upper, "LOGIN "):
			args := quotedRe.FindAllStringSubmatch(rest, -1)
			s.mu.Lock()
			a, ok := s.accounts[unq(args[0][1])]
			good := ok && len(args) == 2 && a.password == unq(args[1][1])
			s.mu.Unlock()
			if !good {
				send("%s NO [AUTHENTICATIONFAILED] login gagal", tag)
				continue
			}
			user = unq(args[0][1])
			send("%s OK login", tag)
		case user == "" && !strings.HasPrefix(upper, "LOGOUT"):
			send("%s NO belum login", tag)
		case strings.HasPrefix(upper, "LIST "):
			for _, f := range s.Folders(user) {
				send(`* LIST (\HasNoChildren) "." %s`, q(f))
			}
			send("%s OK list", tag)
		case strings.HasPrefix(upper, "STATUS "):
			name := unq(quotedRe.FindStringSubmatch(rest)[1])
			s.mu.Lock()
			a := s.accounts[user]
			msgs, ok := a.folders[name]
			s.mu.Unlock()
			if !ok {
				send("%s NO tidak ada", tag)
				continue
			}
			send("* STATUS %s (MESSAGES %d)", q(name), len(msgs))
			send("%s OK status", tag)
		case strings.HasPrefix(upper, "EXAMINE "), strings.HasPrefix(upper, "SELECT "):
			name := unq(quotedRe.FindStringSubmatch(rest)[1])
			s.mu.Lock()
			msgs, ok := s.accounts[user].folders[name]
			s.mu.Unlock()
			if !ok {
				send("%s NO tidak ada", tag)
				continue
			}
			selected = name
			send("* %d EXISTS", len(msgs))
			send("%s OK [READ-ONLY] pilih", tag)
		case strings.HasPrefix(upper, "UID FETCH "):
			for i, msg := range s.Folder(user, selected) {
				fmt.Fprintf(w, "* %d FETCH (UID %d FLAGS (%s) INTERNALDATE %s BODY[] {%d}\r\n", i+1, msg.UID, strings.Join(msg.Flags, " "), q(msg.Date), len(msg.Body))
				w.Write(msg.Body)
				w.WriteString(")\r\n")
			}
			w.Flush()
			send("%s OK fetch", tag)
		case strings.HasPrefix(upper, "CREATE "):
			name := unq(quotedRe.FindStringSubmatch(rest)[1])
			s.mu.Lock()
			a := s.accounts[user]
			_, exists := a.folders[name]
			if !exists {
				a.folders[name] = nil
			}
			s.mu.Unlock()
			if exists {
				send("%s NO [ALREADYEXISTS] sudah ada", tag)
				continue
			}
			send("%s OK dibuat", tag)
		case strings.HasPrefix(upper, "APPEND "):
			am := appendRe.FindStringSubmatch(rest)
			if am == nil {
				send("%s BAD format APPEND", tag)
				continue
			}
			n, _ := strconv.Atoi(am[4])
			send("+ lanjut")
			body := make([]byte, n)
			if _, err := io.ReadFull(r, body); err != nil {
				return
			}
			r.ReadString('\n')
			s.mu.Lock()
			s.Appends++
			drop := s.DropAfterAppends > 0 && s.Appends == s.DropAfterAppends
			a := s.accounts[user]
			name := unq(am[1])
			if _, ok := a.folders[name]; !ok {
				s.mu.Unlock()
				send("%s NO [TRYCREATE] folder tidak ada", tag)
				continue
			}
			a.folders[name] = append(a.folders[name], Message{UID: a.nextUID, Flags: strings.Fields(am[2]), Date: am[3], Body: body})
			a.nextUID++
			s.mu.Unlock()
			if drop {
				return
			}
			send("%s OK append", tag)
		case strings.HasPrefix(upper, "LOGOUT"):
			send("* BYE")
			send("%s OK logout", tag)
			return
		default:
			send("%s OK", tag)
		}
	}
}
