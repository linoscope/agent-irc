// Chapter 01 — Hello, IRC.
//
// A minimal IRC server that handles exactly enough to complete the
// registration handshake. No channels, no PING/PONG, no error replies —
// those come in chapters 02 and 03. The point is to feel the wire format.
//
// Try it:
//   go run . &
//   nc -C localhost 6667
//   NICK alice
//   USER alice 0 * :Alice
//
// You should see the 001 RPL_WELCOME numeric come back.
package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
)

const serverName = "irc.example"

func listenAddr() string {
	if a := os.Getenv("LISTEN"); a != "" {
		return a
	}
	return ":6667"
}

// session holds per-connection state during registration.
type session struct {
	conn     net.Conn
	rd       *bufio.Reader
	nick     string // set by NICK
	user     string // set by USER
	realname string // set by USER (trailing param)
	welcomed bool   // set after we emit 001
}

func main() {
	addr := listenAddr()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	log.Printf("listening on %s", addr)
	for {
		c, err := ln.Accept()
		if err != nil {
			log.Printf("accept: %v", err)
			continue
		}
		go handle(c)
	}
}

func handle(c net.Conn) {
	defer c.Close()
	s := &session{conn: c, rd: bufio.NewReader(c)}
	log.Printf("[%s] connected", c.RemoteAddr())

	for {
		// IRC framing: lines terminated by CR LF, max 512 bytes including CRLF.
		// bufio.Reader.ReadString('\n') gives us the line including the trailing
		// LF; we strip CR LF below. (A production parser would also enforce the
		// 512-byte cap and reject overlong lines.)
		line, err := s.rd.ReadString('\n')
		if err != nil {
			log.Printf("[%s] disconnect: %v", c.RemoteAddr(), err)
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			continue
		}

		verb, params := parseLine(line)
		log.Printf("[%s] <- %s %v", c.RemoteAddr(), verb, params)

		switch strings.ToUpper(verb) {
		case "NICK":
			if len(params) < 1 {
				continue // chapter 02 will return ERR_NONICKNAMEGIVEN (431)
			}
			s.nick = params[0]
		case "USER":
			// USER <user> <mode> <unused> :<realname>
			if len(params) < 4 {
				continue // chapter 02 will return ERR_NEEDMOREPARAMS (461)
			}
			s.user = params[0]
			s.realname = params[3]
		case "QUIT":
			return
		}

		// Emit 001 RPL_WELCOME once both NICK and USER have arrived.
		if !s.welcomed && s.nick != "" && s.user != "" {
			s.send("001", s.nick, "Welcome to the chapter-01 IRC server, "+s.nick)
			s.welcomed = true
		}
	}
}

// send writes a server-prefixed numeric/command. The final argument always
// becomes the trailing parameter (introduced by ":"), so it may contain spaces.
func (s *session) send(verb string, target string, trailing string) {
	line := fmt.Sprintf(":%s %s %s :%s\r\n", serverName, verb, target, trailing)
	log.Printf("[%s] -> %s", s.conn.RemoteAddr(), strings.TrimRight(line, "\r\n"))
	if _, err := s.conn.Write([]byte(line)); err != nil {
		log.Printf("[%s] write: %v", s.conn.RemoteAddr(), err)
	}
}

// parseLine implements a deliberately-naive IRC message parser.
//
// Wire format (no tags, no source, just verb + params for chapter 01):
//
//	verb SP param1 SP param2 SP ... SP :trailing\r\n
//
// The leading `:` on the trailing parameter means "the rest of the line,
// including spaces, is one parameter." Without it, parameters split on SP.
//
// Known limitations on purpose (chapter 02 fixes them):
//   - does not handle a leading `:source` prefix
//   - collapses runs of spaces
//   - silently drops empty trailing params (the `KICK #c nick :` edge case)
func parseLine(line string) (verb string, params []string) {
	// Split off the trailing param if present.
	var trailing string
	hasTrailing := false
	if i := strings.Index(line, " :"); i >= 0 {
		trailing = line[i+2:]
		line = line[:i]
		hasTrailing = true
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "", nil
	}
	verb = fields[0]
	params = fields[1:]
	if hasTrailing {
		params = append(params, trailing)
	}
	return verb, params
}
