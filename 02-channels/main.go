// Chapter 02 — Channels and broadcast.
//
// What's new vs. chapter 01:
//   - Real parser handling tags, source, empty-trailing, multi-space
//     (verified against ircdocs/parser-tests; see parser_test.go).
//   - Multi-client server with shared channel state.
//   - JOIN, PRIVMSG, PART, QUIT, NAMES (353/366), and basic error replies.
//   - Per-connection writer goroutine so broadcasts don't block readers.
//
// What's still missing (chapter 03):
//   - PING/PONG keepalive.
//   - ISUPPORT (numeric 005) advertising server limits.
//   - Real RFC 1459 casemapping (we use ascii here for clarity).
package main

import (
	"bufio"
	"log"
	"net"
	"os"
)

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
	log.Printf("chapter-02 server listening on %s", addr)
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
	for {
		// Cap line length per IRC convention. With IRCv3 message tags this
		// would be ~512 (body) + ~4096 (tags) = ~4608; chapter 02 has no
		// tags, so 512 suffices. ReadSlice gives us up to the buffer size,
		// which we capped at 1024 above — anything longer truncates.
		raw, err := rd.ReadString('\n')
		if err != nil {
			return
		}
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
