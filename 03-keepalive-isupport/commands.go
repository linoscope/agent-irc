package main

import (
	"fmt"
	"net"
	"strings"
)

func (s *session) dispatch(m Message) {
	switch strings.ToUpper(m.Verb) {
	case "NICK":
		s.handleNick(m)
	case "USER":
		s.handleUser(m)
	case "JOIN":
		s.handleJoin(m)
	case "PART":
		s.handlePart(m)
	case "PRIVMSG":
		s.handlePrivmsg(m)
	case "QUIT":
		s.handleQuit(m)
	case "PING":
		// Client-initiated PING (rare from real clients, but defensive):
		// echo the token back as PONG so the client knows we're alive.
		if len(m.Params) > 0 {
			s.send(fmt.Sprintf("PONG %s :%s", serverName, m.Params[0]))
		}
	case "PONG":
		// Reply to our keepalive PING. The read loop already reset the
		// idle state on this line, so no further action is needed here.
	default:
		s.send(fmt.Sprintf("421 %s %s :Unknown command", s.currentNick(), m.Verb))
	}
}

func (s *session) currentNick() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.nick == "" {
		return "*"
	}
	return s.nick
}

// ---------- registration ----------

func (s *session) handleNick(m Message) {
	if len(m.Params) < 1 || m.Params[0] == "" {
		s.send(fmt.Sprintf("431 %s :No nickname given", s.currentNick()))
		return
	}
	desired := m.Params[0]

	s.srv.mu.Lock()
	defer s.srv.mu.Unlock()
	if _, taken := s.srv.clients[lowerNick(desired)]; taken {
		s.send(fmt.Sprintf("433 %s %s :Nickname is already in use", s.currentNick(), desired))
		return
	}
	s.mu.Lock()
	old := s.nick
	s.nick = desired
	s.mu.Unlock()
	if old != "" {
		delete(s.srv.clients, lowerNick(old))
	}
	s.srv.clients[lowerNick(desired)] = s

	s.maybeWelcome()
}

func (s *session) handleUser(m Message) {
	if len(m.Params) < 4 {
		s.send(fmt.Sprintf("461 %s USER :Not enough parameters", s.currentNick()))
		return
	}
	s.mu.Lock()
	if s.welcomed {
		s.mu.Unlock()
		s.send(fmt.Sprintf("462 %s :You may not reregister", s.nick))
		return
	}
	s.user = m.Params[0]
	s.realname = m.Params[3]
	s.mu.Unlock()
	s.maybeWelcome()
}

func (s *session) maybeWelcome() {
	s.mu.Lock()
	if s.welcomed || s.nick == "" || s.user == "" {
		s.mu.Unlock()
		return
	}
	s.welcomed = true
	nick := s.nick
	s.mu.Unlock()
	s.send(fmt.Sprintf("001 %s :Welcome to %s, %s", nick, networkName, nick))
	s.send(fmt.Sprintf("002 %s :Your host is %s, running ch03", nick, serverName))
	s.send(fmt.Sprintf("003 %s :This server has no MOTD", nick))
	s.send(fmt.Sprintf("004 %s %s ch03 i nt", nick, serverName))
	s.sendISupport(nick)
}

// sendISupport emits the 005 RPL_ISUPPORT line. Real servers split into
// multiple lines when tokens exceed ~13 per line / ~400 bytes; we pack
// everything into one for simplicity.
//
// Tokens we advertise:
//
//	NETWORK       Display name shown in clients ("Connected to AgentIRC").
//	CASEMAPPING   We implement rfc1459 (see state.go).
//	CHANTYPES     # only — & (server-local) and + (modeless) are unimplemented.
//	PREFIX        Empty for chapter 03 — we have no channel-op modes yet.
//	NICKLEN       Soft cap on nicknames.
//	CHANNELLEN    Soft cap on channel names.
//	TOPICLEN      Topic-byte cap (we ignore topics anyway).
//
// A real chat network would also advertise CHANMODES, MAXCHANNELS, MODES,
// TARGMAX, STATUSMSG, EXTBAN, ELIST — see https://defs.ircdocs.horse/.
func (s *session) sendISupport(nick string) {
	tokens := []string{
		"NETWORK=" + networkName,
		"CASEMAPPING=rfc1459",
		"CHANTYPES=#",
		"PREFIX=",
		"NICKLEN=30",
		"CHANNELLEN=64",
		"TOPICLEN=390",
	}
	s.send(fmt.Sprintf("005 %s %s :are supported by this server",
		nick, strings.Join(tokens, " ")))
}

// ---------- channels ----------

func (s *session) handleJoin(m Message) {
	if !s.isWelcomed() {
		return
	}
	if len(m.Params) < 1 {
		s.send(fmt.Sprintf("461 %s JOIN :Not enough parameters", s.currentNick()))
		return
	}
	for _, name := range strings.Split(m.Params[0], ",") {
		s.joinOne(name)
	}
}

func (s *session) joinOne(name string) {
	if !strings.HasPrefix(name, "#") {
		s.send(fmt.Sprintf("403 %s %s :No such channel", s.currentNick(), name))
		return
	}
	key := lowerChan(name)

	s.srv.mu.Lock()
	ch, ok := s.srv.channels[key]
	if !ok {
		ch = &channel{
			name:    name,
			members: make(map[*session]struct{}),
		}
		s.srv.channels[key] = ch
	}
	if _, already := ch.members[s]; already {
		s.srv.mu.Unlock()
		return
	}
	ch.members[s] = struct{}{}

	s.mu.Lock()
	s.channels[key] = ch
	s.mu.Unlock()

	// Take a snapshot of names while the lock is held; release before doing IO.
	names := make([]string, 0, len(ch.members))
	for m := range ch.members {
		m.mu.Lock()
		names = append(names, m.nick)
		m.mu.Unlock()
	}
	members := make([]*session, 0, len(ch.members))
	for m := range ch.members {
		members = append(members, m)
	}
	s.srv.mu.Unlock()

	// Broadcast JOIN with the joiner's prefix to every member (including self).
	joinLine := fmt.Sprintf(":%s JOIN %s\r\n", s.prefix(), ch.name)
	for _, mem := range members {
		mem.sendRaw(joinLine)
	}
	// Send NAMES list to the joiner.
	s.send(fmt.Sprintf("353 %s = %s :%s",
		s.currentNick(), ch.name, strings.Join(names, " ")))
	s.send(fmt.Sprintf("366 %s %s :End of /NAMES list", s.currentNick(), ch.name))
}

func (s *session) handlePart(m Message) {
	if !s.isWelcomed() {
		return
	}
	if len(m.Params) < 1 {
		s.send(fmt.Sprintf("461 %s PART :Not enough parameters", s.currentNick()))
		return
	}
	reason := ""
	if len(m.Params) > 1 {
		reason = m.Params[1]
	}
	for _, name := range strings.Split(m.Params[0], ",") {
		s.partOne(name, reason)
	}
}

func (s *session) partOne(name, reason string) {
	key := lowerChan(name)
	s.srv.mu.Lock()
	ch, ok := s.srv.channels[key]
	if !ok {
		s.srv.mu.Unlock()
		s.send(fmt.Sprintf("403 %s %s :No such channel", s.currentNick(), name))
		return
	}
	if _, in := ch.members[s]; !in {
		s.srv.mu.Unlock()
		s.send(fmt.Sprintf("442 %s %s :You're not on that channel", s.currentNick(), name))
		return
	}
	members := make([]*session, 0, len(ch.members))
	for m := range ch.members {
		members = append(members, m)
	}
	delete(ch.members, s)
	if len(ch.members) == 0 {
		delete(s.srv.channels, key)
	}
	s.mu.Lock()
	delete(s.channels, key)
	s.mu.Unlock()
	s.srv.mu.Unlock()

	line := fmt.Sprintf(":%s PART %s :%s\r\n", s.prefix(), ch.name, reason)
	for _, mem := range members {
		mem.sendRaw(line)
	}
}

func (s *session) handlePrivmsg(m Message) {
	if !s.isWelcomed() {
		return
	}
	if len(m.Params) < 2 {
		s.send(fmt.Sprintf("411 %s :No recipient given", s.currentNick()))
		return
	}
	target := m.Params[0]
	body := m.Params[1]

	if strings.HasPrefix(target, "#") {
		s.deliverChannel(target, body)
	} else {
		s.deliverUser(target, body)
	}
}

func (s *session) deliverChannel(name, body string) {
	key := lowerChan(name)
	s.srv.mu.RLock()
	ch, ok := s.srv.channels[key]
	if !ok {
		s.srv.mu.RUnlock()
		s.send(fmt.Sprintf("403 %s %s :No such channel", s.currentNick(), name))
		return
	}
	members := make([]*session, 0, len(ch.members))
	for m := range ch.members {
		if m != s {
			members = append(members, m)
		}
	}
	chanName := ch.name
	s.srv.mu.RUnlock()

	line := fmt.Sprintf(":%s PRIVMSG %s :%s\r\n", s.prefix(), chanName, body)
	for _, mem := range members {
		mem.sendRaw(line)
	}
}

func (s *session) deliverUser(nick, body string) {
	s.srv.mu.RLock()
	target, ok := s.srv.clients[lowerNick(nick)]
	s.srv.mu.RUnlock()
	if !ok {
		s.send(fmt.Sprintf("401 %s %s :No such nick", s.currentNick(), nick))
		return
	}
	target.sendRaw(fmt.Sprintf(":%s PRIVMSG %s :%s\r\n", s.prefix(), nick, body))
}

func (s *session) handleQuit(m Message) {
	reason := "Client Quit"
	if len(m.Params) > 0 {
		reason = m.Params[0]
	}
	s.srv.removeClient(s, reason)
	// readLoop's defer closes the connection.
}

func (s *session) isWelcomed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.welcomed {
		s.send(fmt.Sprintf("451 %s :You have not registered", s.currentNickLocked()))
		return false
	}
	return true
}

func (s *session) currentNickLocked() string {
	if s.nick == "" {
		return "*"
	}
	return s.nick
}

// ---------- server-level removal ----------

// removeClient is called on QUIT and on disconnect. It broadcasts a single
// QUIT line to every channel the client was a member of (deduplicated across
// users by collecting into a set), then removes them from all server state.
func (srv *Server) removeClient(s *session, reason string) {
	srv.mu.Lock()
	s.mu.Lock()
	if s.nick == "" {
		s.mu.Unlock()
		srv.mu.Unlock()
		return
	}
	prefix := fmt.Sprintf("%s!%s@%s", s.nick, s.user, "host")
	if a, ok := s.conn.RemoteAddr().(*net.TCPAddr); ok {
		prefix = fmt.Sprintf("%s!%s@%s", s.nick, s.user, a.IP.String())
	}
	witness := make(map[*session]struct{})
	for key, ch := range s.channels {
		for m := range ch.members {
			if m != s {
				witness[m] = struct{}{}
			}
		}
		delete(ch.members, s)
		if len(ch.members) == 0 {
			delete(srv.channels, key)
		}
	}
	delete(srv.clients, lowerNick(s.nick))
	s.channels = nil
	nick := s.nick
	s.nick = ""
	s.mu.Unlock()
	srv.mu.Unlock()

	line := fmt.Sprintf(":%s QUIT :%s\r\n", prefix, reason)
	for w := range witness {
		w.sendRaw(line)
	}
	_ = nick
}
