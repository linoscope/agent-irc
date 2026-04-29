package main

import (
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
)

const networkName = "AgentIRC"

const serverName = "irc.example"

// Server holds the shared state of the IRC server. Every mutation is guarded
// by mu. Per-connection write paths funnel through session.out, so one
// session's writer cannot block another's read loop.
type Server struct {
	mu       sync.RWMutex
	clients  map[string]*session // key: lowerNick(nick)
	channels map[string]*channel // key: lowerChan(name)
}

func NewServer() *Server {
	return &Server{
		clients:  make(map[string]*session),
		channels: make(map[string]*channel),
	}
}

type channel struct {
	name    string                 // canonical (case as first JOIN'd)
	members map[*session]struct{}  // set
	topic   string
}

// session is one connected client. Multiple goroutines may write to s.out;
// exactly one writer goroutine drains it to the TCP socket.
type session struct {
	conn     net.Conn
	out      chan string
	srv      *Server

	mu       sync.Mutex // guards the following
	nick     string
	user     string
	realname string
	channels map[string]*channel // channels this session has joined
	welcomed bool
}

func newSession(srv *Server, conn net.Conn) *session {
	return &session{
		conn:     conn,
		out:      make(chan string, 32),
		srv:      srv,
		channels: make(map[string]*channel),
	}
}

// IRC casemapping per RFC 1459 §2.2. Because IRC was originally Finnish,
// {}|^ are the lowercase forms of []\~ — when comparing nicks/channel names,
// `Foo[bar]` and `foo{bar}` must be treated as the same string. Servers
// advertise this via `CASEMAPPING=rfc1459` in the 005 numeric so clients
// know which mapping to apply locally.
//
// Variants:
//   - ascii            : only A-Z lowercase
//   - rfc1459-strict   : ascii + []\ ↔ {}|
//   - rfc1459 (default): rfc1459-strict + ~ ↔ ^
//
// We implement rfc1459, the most common public-network default.
func casefold(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z':
			c += 'a' - 'A'
		case c == '[':
			c = '{'
		case c == ']':
			c = '}'
		case c == '\\':
			c = '|'
		case c == '~':
			c = '^'
		}
		b[i] = c
	}
	return string(b)
}

func lowerNick(s string) string { return casefold(s) }
func lowerChan(s string) string { return casefold(s) }

// prefix is the source string we put on relayed messages: "nick!user@host".
func (s *session) prefix() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	host := "host"
	if a, ok := s.conn.RemoteAddr().(*net.TCPAddr); ok {
		host = a.IP.String()
	}
	return fmt.Sprintf("%s!%s@%s", s.nick, s.user, host)
}

// send queues a server-prefixed line. The caller passes the part after the
// server source. CR LF is added.
func (s *session) send(line string) {
	full := fmt.Sprintf(":%s %s\r\n", serverName, line)
	s.enqueue(full)
}

// sendRaw queues an already-prefixed line (used for relayed PRIVMSG/JOIN/...
// where the prefix is some other client's nick!user@host, not the server).
func (s *session) sendRaw(line string) {
	if !strings.HasSuffix(line, "\r\n") {
		line += "\r\n"
	}
	s.enqueue(line)
}

func (s *session) enqueue(line string) {
	select {
	case s.out <- line:
	default:
		// Backpressure: a client whose write buffer is full gets disconnected.
		// Real ircds would flag this as "send-q exceeded" and KILL.
		log.Printf("[%s] send-q full, dropping connection", s.conn.RemoteAddr())
		s.conn.Close()
	}
}

// writeLoop drains s.out to the socket. Exits when out is closed or write
// fails.
func (s *session) writeLoop() {
	for line := range s.out {
		if _, err := s.conn.Write([]byte(line)); err != nil {
			log.Printf("[%s] write: %v", s.conn.RemoteAddr(), err)
			return
		}
	}
}
