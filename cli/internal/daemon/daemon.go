// Package daemon is the long-lived process that holds the IRC connection
// and serves CLI requests over a Unix socket.
//
// One daemon per (host, nick) pair. Socket path:
//
//	$XDG_RUNTIME_DIR/agent-irc/<nick>.sock     (preferred)
//	/tmp/agent-irc-<user>-<nick>.sock          (fallback)
//
// Lifecycle:
//   - First `agent-irc connect` spawns the daemon as a subprocess.
//   - Subsequent CLI calls reuse the daemon via the socket.
//   - Daemon exits when the IRC connection drops.
package daemon

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lin/agent-irc/cli/internal/bridge"
	"github.com/lin/agent-irc/cli/internal/protocol"
)

// SocketPath returns the canonical socket path for a given nick.
func SocketPath(nick string) string {
	if rt := os.Getenv("XDG_RUNTIME_DIR"); rt != "" {
		dir := filepath.Join(rt, "agent-irc")
		_ = os.MkdirAll(dir, 0o700)
		return filepath.Join(dir, nick+".sock")
	}
	user := os.Getenv("USER")
	if user == "" {
		user = "anon"
	}
	return filepath.Join("/tmp", "agent-irc-"+user+"-"+nick+".sock")
}

// Server is the daemon. Single instance per process.
type Server struct {
	cfg      Config
	listener net.Listener
	br       *bridge.Bridge

	mu          sync.Mutex
	subscribers []*subscription // tail subscribers
	channels    map[string]*channelBuf
}

type Config struct {
	Server   string // IRC host:port
	Nick     string
	TLS      bool
	Password string
}

type subscription struct {
	channel  string // "" = all channels
	skipSelf bool
	queue    chan protocol.Event
}

type channelBuf struct {
	mu   sync.Mutex
	ring []protocol.Event // bounded recent buffer (last N)
	cap  int
}

// New creates a Server. Call Run() to start.
func New(cfg Config) *Server {
	s := &Server{
		cfg:      cfg,
		channels: map[string]*channelBuf{},
	}
	s.br = bridge.New(bridge.Config{
		Server:   cfg.Server,
		Nick:     cfg.Nick,
		TLS:      cfg.TLS,
		Password: cfg.Password,
		OnEvent:  s.onEvent,
	})
	return s
}

// Run connects to IRC, opens the socket, and serves requests until the
// IRC loop exits or the socket is closed.
func (s *Server) Run() error {
	sock := SocketPath(s.cfg.Nick)
	_ = os.Remove(sock) // remove a stale socket if any

	ln, err := net.Listen("unix", sock)
	if err != nil {
		return fmt.Errorf("listen %s: %w", sock, err)
	}
	s.listener = ln
	defer func() {
		_ = ln.Close()
		_ = os.Remove(sock)
	}()

	if err := s.br.Connect(10 * time.Second); err != nil {
		return fmt.Errorf("irc connect: %w", err)
	}

	go s.acceptLoop()

	<-s.br.Done()
	return nil
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *Server) handle(conn net.Conn) {
	defer conn.Close()
	rd := bufio.NewReader(conn)

	// Each client connection sends exactly one Request as its first line.
	line, err := rd.ReadBytes('\n')
	if err != nil {
		return
	}
	var req protocol.Request
	if err := json.Unmarshal(line, &req); err != nil {
		writeResp(conn, protocol.Response{OK: false, Error: "bad json: " + err.Error()})
		return
	}

	switch req.Op {
	case protocol.OpJoin:
		s.handleJoin(conn, req)
	case protocol.OpPart:
		s.handlePart(conn, req)
	case protocol.OpSend, protocol.OpDM:
		s.handleSend(conn, req)
	case protocol.OpTail:
		s.handleTail(conn, req)
	case protocol.OpNicks:
		s.handleNicks(conn, req)
	case protocol.OpWhoami, protocol.OpStatus:
		s.handleWhoami(conn, req)
	case protocol.OpQuit:
		s.handleQuit(conn, req)
	default:
		writeResp(conn, protocol.Response{OK: false, Error: "unknown op: " + req.Op})
	}
}

// ---- handlers ------------------------------------------------------------

func (s *Server) handleJoin(conn net.Conn, req protocol.Request) {
	if req.Channel == "" {
		writeResp(conn, protocol.Response{OK: false, Error: "channel required"})
		return
	}
	if err := s.br.Join(req.Channel); err != nil {
		writeResp(conn, protocol.Response{OK: false, Error: err.Error()})
		return
	}
	writeResp(conn, protocol.Response{OK: true})
}

func (s *Server) handlePart(conn net.Conn, req protocol.Request) {
	if req.Channel == "" {
		writeResp(conn, protocol.Response{OK: false, Error: "channel required"})
		return
	}
	if err := s.br.Part(req.Channel); err != nil {
		writeResp(conn, protocol.Response{OK: false, Error: err.Error()})
		return
	}
	writeResp(conn, protocol.Response{OK: true})
}

func (s *Server) handleSend(conn net.Conn, req protocol.Request) {
	if req.Target == "" || req.Text == "" {
		writeResp(conn, protocol.Response{OK: false, Error: "target and text required"})
		return
	}
	if err := s.br.Send(req.Target, req.Text); err != nil {
		writeResp(conn, protocol.Response{OK: false, Error: err.Error()})
		return
	}
	writeResp(conn, protocol.Response{OK: true})
}

func (s *Server) handleTail(conn net.Conn, req protocol.Request) {
	sub := &subscription{
		channel:  strings.ToLower(req.Channel), // "" matches all
		skipSelf: req.SkipSelf,
		queue:    make(chan protocol.Event, 256),
	}
	s.mu.Lock()
	s.subscribers = append(s.subscribers, sub)
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		for i, x := range s.subscribers {
			if x == sub {
				s.subscribers = append(s.subscribers[:i], s.subscribers[i+1:]...)
				break
			}
		}
		s.mu.Unlock()
	}()

	enc := json.NewEncoder(conn)

	// Optional history backfill before live stream.
	if req.History > 0 && req.Channel != "" {
		s.mu.Lock()
		buf := s.channels[strings.ToLower(req.Channel)]
		s.mu.Unlock()
		if buf != nil {
			buf.mu.Lock()
			n := len(buf.ring)
			if req.History < n {
				n = req.History
			}
			start := len(buf.ring) - n
			past := append([]protocol.Event(nil), buf.ring[start:]...)
			buf.mu.Unlock()
			for _, ev := range past {
				if err := enc.Encode(ev); err != nil {
					return
				}
			}
		}
	}

	// One-shot read: history was already streamed above; nothing else to do.
	if !req.Follow {
		return
	}

	// Stream live events. Stops when the client closes its socket.
	notify := connClosed(conn)
	for {
		select {
		case ev := <-sub.queue:
			if err := enc.Encode(ev); err != nil {
				return
			}
		case <-notify:
			return
		case <-s.br.Done():
			return
		}
	}
}

func (s *Server) handleNicks(conn net.Conn, req protocol.Request) {
	if req.Channel == "" {
		writeResp(conn, protocol.Response{OK: false, Error: "channel required"})
		return
	}
	writeResp(conn, protocol.Response{OK: true, Nicks: s.br.Nicks(req.Channel)})
}

func (s *Server) handleWhoami(conn net.Conn, req protocol.Request) {
	writeResp(conn, protocol.Response{
		OK:        true,
		Nick:      s.br.CurrentNick(),
		Server:    s.cfg.Server,
		Connected: true,
	})
}

func (s *Server) handleQuit(conn net.Conn, req protocol.Request) {
	writeResp(conn, protocol.Response{OK: true})
	go func() {
		// Give the response a chance to flush.
		time.Sleep(50 * time.Millisecond)
		s.br.Quit()
	}()
}

// ---- event fan-out -------------------------------------------------------

func (s *Server) onEvent(ev protocol.Event) {
	if ev.Channel != "" {
		s.bufferEvent(ev)
	}

	s.mu.Lock()
	subs := append([]*subscription(nil), s.subscribers...)
	s.mu.Unlock()
	for _, sub := range subs {
		if sub.skipSelf && ev.IsSelf {
			continue
		}
		if sub.channel != "" && strings.ToLower(ev.Channel) != sub.channel {
			continue
		}
		select {
		case sub.queue <- ev:
		default:
			// Slow subscriber: drop this event for them. They get partial,
			// not blocked.
		}
	}
}

func (s *Server) bufferEvent(ev protocol.Event) {
	if ev.Type != "message" {
		return
	}
	key := strings.ToLower(ev.Channel)
	s.mu.Lock()
	buf, ok := s.channels[key]
	if !ok {
		buf = &channelBuf{cap: 200}
		s.channels[key] = buf
	}
	s.mu.Unlock()

	buf.mu.Lock()
	buf.ring = append(buf.ring, ev)
	if len(buf.ring) > buf.cap {
		buf.ring = buf.ring[len(buf.ring)-buf.cap:]
	}
	buf.mu.Unlock()
}

// ---- helpers --------------------------------------------------------------

func writeResp(conn net.Conn, r protocol.Response) {
	b, _ := json.Marshal(r)
	conn.Write(append(b, '\n'))
}

// connClosed returns a channel that closes when the connection's peer hangs up.
func connClosed(conn net.Conn) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 1)
		for {
			conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			_, err := conn.Read(buf)
			if err == nil {
				continue
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return // EOF or other terminal error
		}
	}()
	return done
}
