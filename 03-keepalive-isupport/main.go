// Chapter 03 — Keepalive, ISUPPORT, casemapping.
//
// What's new vs. chapter 02:
//   - PING/PONG keepalive driven by SetReadDeadline. Idle clients get a PING
//     after IdleTimeout; if they don't reply within another IdleTimeout, the
//     server closes the connection ("Ping timeout").
//   - ISUPPORT (numeric 005) emitted at registration. CASEMAPPING, NETWORK,
//     CHANTYPES, PREFIX, NICKLEN, CHANNELLEN — the keys clients actually use.
//   - RFC 1459 casemapping (was: ascii). Channel `#FOO` and `#foo` are the
//     same channel; nick `Alice[bot]` and `alice{bot}` are the same nick.
//
// What's still missing (chapter 04+):
//   - IRCv3 capabilities, SASL, account-tag, message-tags, server-time.
//   - Account / identity persistence.
//   - Real history (CHATHISTORY).
package main

import (
	"bufio"
	"errors"
	"log"
	"net"
	"os"
	"strconv"
	"time"
)

// IdleTimeout is the amount of time a session may be silent before the server
// emits a PING. After 2 * IdleTimeout of total silence, the connection is
// closed with "Ping timeout". Override with IDLE_TIMEOUT env (seconds).
var IdleTimeout = 120 * time.Second

func init() {
	if s := os.Getenv("IDLE_TIMEOUT"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			IdleTimeout = time.Duration(n) * time.Second
		}
	}
}

func listenAddr() string {
	if a := os.Getenv("LISTEN"); a != "" {
		return a
	}
	return ":6667"
}

func main() {
	srv := NewServer()
	addr := listenAddr()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	log.Printf("chapter-03 server listening on %s (idle=%s)", addr, IdleTimeout)
	for {
		c, err := ln.Accept()
		if err != nil {
			log.Printf("accept: %v", err)
			continue
		}
		s := newSession(srv, c)
		go s.writeLoop()
		go s.readLoop()
	}
}

func (s *session) readLoop() {
	defer func() {
		s.srv.removeClient(s, "Connection closed")
		close(s.out)
		s.conn.Close()
	}()

	rd := bufio.NewReaderSize(s.conn, 1024)
	pinged := false // unanswered PING outstanding?

	for {
		// Idle detection via socket read deadline. If the client says nothing
		// for IdleTimeout, ReadString returns a timeout error — at which
		// point we either send a PING (first timeout) or drop them (second
		// consecutive timeout = "Ping timeout").
		_ = s.conn.SetReadDeadline(time.Now().Add(IdleTimeout))
		raw, err := rd.ReadString('\n')

		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				if pinged {
					log.Printf("[%s] ping timeout", s.conn.RemoteAddr())
					return
				}
				token := strconv.FormatInt(time.Now().UnixNano(), 36)
				s.sendRaw(":" + serverName + " PING :" + token + "\r\n")
				pinged = true
				continue
			}
			return // EOF, RST, or other unrecoverable error
		}

		// Any inbound traffic counts as life — PONG is just one way to
		// reset the idle timer.
		pinged = false

		if len(raw) == 0 {
			continue
		}
		msg := Parse(raw)
		if msg.Verb == "" {
			continue
		}
		log.Printf("[%s] <- %s %v", s.conn.RemoteAddr(), msg.Verb, msg.Params)
		s.dispatch(msg)
	}
}
