// Package bridge wraps an ergochat/irc-go Connection and exposes its inbound
// events as our protocol.Event type. The daemon owns one Bridge per nick.
//
// Responsibilities:
//   - Configure ircevent.Connection with TLS, caps, SASL.
//   - Translate inbound ircmsg.Message → protocol.Event for: PRIVMSG, JOIN,
//     PART, QUIT, NICK. Unknown verbs become Type="raw".
//   - Sanitize outbound text (strip CR/LF) so an LLM emitting a multi-line
//     reply produces one IRC line, not several.
//   - Track our own nick so events can be tagged IsSelf=true.
//   - Track per-channel member sets (from JOIN/PART/QUIT/353 RPL_NAMREPLY)
//     so the daemon can serve `nicks` queries without re-asking the server.
package bridge

import (
	"crypto/ecdsa"
	"crypto/tls"
	"encoding/base64"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/ergochat/irc-go/ircevent"
	"github.com/ergochat/irc-go/ircmsg"

	"github.com/lin/agent-irc/cli/internal/erc8004"
	"github.com/lin/agent-irc/cli/internal/protocol"
)

// Caps we always request. account-tag is what surfaces verified identity on
// every relayed message; server-time + message-tags + batch are needed for
// chathistory; multi-prefix lets us see the full "@+" status prefix list.
var DefaultCaps = []string{
	"account-tag",
	"server-time",
	"message-tags",
	"batch",
	"multi-prefix",
	"echo-message",
}

// Config configures a Bridge.
type Config struct {
	Server   string // host:port
	Nick     string
	TLS      bool
	Password string // optional SASL PLAIN password
	OnEvent  func(protocol.Event)

	// ERC8004 fields. If ERC8004Key is non-nil the bridge runs the canonical
	// ERC-8004 SASL mechanism in place of PLAIN, and Password is ignored.
	//
	// AgentID is the ERC-721 token id under which this key is registered in
	// the ERC-8004 Identity Registry. The server uses it to look up the
	// expected signing wallet via getAgentWallet(agentId); the client sends
	// it as the first SASL payload. nil falls back to chapter-07 behavior
	// (no registry, signature-only auth).
	ERC8004Key *ecdsa.PrivateKey
	AgentID    *big.Int // ERC-8004 token id; nil = pre-canonical (ch07) mode
	ChainID    uint64   // bound into the signed body (chapter 10)
	ServerName string   // bound into the signed body (chapter 10)
}

// Bridge owns one IRC connection.
type Bridge struct {
	cfg  Config
	irc  *ircevent.Connection
	done chan struct{}

	// saslStep tracks our position in the ERC8004 3-step exchange:
	//   0 = waiting for `AUTHENTICATE +` (kickoff)
	//   1 = waiting for the nonce challenge
	//   2 = done (signature sent)
	saslStep int

	mu       sync.Mutex
	channels map[string]map[string]struct{} // channel → set of nicks (lowercased)
}

// New returns a Bridge ready to Run.
func New(cfg Config) *Bridge {
	b := &Bridge{
		cfg:      cfg,
		done:     make(chan struct{}),
		channels: map[string]map[string]struct{}{},
	}
	conn := &ircevent.Connection{
		Server:      cfg.Server,
		Nick:        cfg.Nick,
		User:        cfg.Nick,
		RealName:    cfg.Nick + " (agent-irc)",
		UseTLS:      cfg.TLS,
		RequestCaps: DefaultCaps,
	}
	if cfg.TLS {
		// Tutorial-grade default: skip cert verification so localhost self-signed
		// Ergo deployments work. Production users should override TLSConfig
		// before passing the bridge through to a real server.
		conn.TLSConfig = &tls.Config{InsecureSkipVerify: true}
	}
	switch {
	case cfg.ERC8004Key != nil:
		// We can't set SASLMech="ERC8004" up front: ircevent v0.6.0 validates
		// the mechanism against a hardcoded {PLAIN,EXTERNAL} set in Connect()
		// before any wire traffic. So we configure for PLAIN to pass validation,
		// then swap SASLMech to "ERC8004" from a CAP-ACK callback that fires
		// (in registration order, ahead of ircevent's handleCAP) just before
		// ircevent reads the field to send `AUTHENTICATE <mech>`. The
		// AUTHENTICATE callback that responds with the 3 ERC8004 rounds is
		// also installed below. The SASLLogin/Password values are required
		// here only so ircevent flips UseSASL=true; their content is unused.
		conn.SASLMech = "PLAIN"
		conn.SASLLogin = cfg.Nick
		conn.SASLPassword = "ignored-by-erc8004"
		conn.UseSASL = true
	case cfg.Password != "":
		conn.SASLLogin = cfg.Nick
		conn.SASLPassword = cfg.Password
		conn.UseSASL = true
	}
	b.irc = conn
	b.installCallbacks()
	return b
}

// Connect dials and starts the event loop in a background goroutine.
// Returns once registration completes (RPL_WELCOME received) or the
// timeout elapses.
func (b *Bridge) Connect(timeout time.Duration) error {
	registered := make(chan struct{}, 1)
	id := b.irc.AddConnectCallback(func(ircmsg.Message) {
		select {
		case registered <- struct{}{}:
		default:
		}
	})
	defer b.irc.RemoveCallback(id)

	if err := b.irc.Connect(); err != nil {
		return err
	}
	go func() {
		b.irc.Loop()
		close(b.done)
	}()

	select {
	case <-registered:
		return nil
	case <-time.After(timeout):
		return errTimeout
	}
}

// Done returns a channel closed when the IRC loop exits.
func (b *Bridge) Done() <-chan struct{} { return b.done }

// CurrentNick returns the nick the server actually assigned us (may differ
// from configured Nick if there was a collision).
func (b *Bridge) CurrentNick() string { return b.irc.CurrentNick() }

// Join asks the server to add us to a channel.
func (b *Bridge) Join(channel string) error  { return b.irc.Join(channel) }
func (b *Bridge) Part(channel string) error  { return b.irc.Part(channel) }

// Send is PRIVMSG. text is sanitized to strip CR/LF so a multi-line reply
// from an LLM never injects additional commands.
func (b *Bridge) Send(target, text string) error {
	return b.irc.Privmsg(target, sanitize(text))
}

// SendRaw lets the daemon issue arbitrary IRC commands (e.g., CHATHISTORY).
func (b *Bridge) SendRaw(line string) error { return b.irc.SendRaw(line) }

// Nicks returns the cached membership of channel. May return empty if we
// haven't received a NAMES list for it yet.
func (b *Bridge) Nicks(channel string) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	set := b.channels[strings.ToLower(channel)]
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	return out
}

// Quit cleanly disconnects.
func (b *Bridge) Quit() { b.irc.Quit() }

// ---- callback wiring -----------------------------------------------------

func (b *Bridge) installCallbacks() {
	if b.cfg.ERC8004Key != nil {
		// Both must be added BEFORE Connect() (which calls ircevent.setupCallbacks
		// internally) so they fire ahead of ircevent's own handleCAP and the
		// PLAIN/EXTERNAL AUTHENTICATE responder.
		b.irc.AddCallback("CAP", b.onCAPForERC8004)
		b.irc.AddCallback("AUTHENTICATE", b.onAUTHForERC8004)
	}
	b.irc.AddCallback("PRIVMSG", b.onPrivmsg)
	b.irc.AddCallback("JOIN", b.onJoin)
	b.irc.AddCallback("PART", b.onPart)
	b.irc.AddCallback("QUIT", b.onQuit)
	b.irc.AddCallback("NICK", b.onNick)
	b.irc.AddCallback("353", b.on353) // RPL_NAMREPLY
}

// onCAPForERC8004 runs before ircevent's handleCAP. When CAP ACK includes
// "sasl" we flip SASLMech to "ERC8004". By the time ircevent's connect
// goroutine wakes from capsChan and sends `AUTHENTICATE <mech>`, the field
// has been updated. See the comment in New() for why this dance is needed.
func (b *Bridge) onCAPForERC8004(e ircmsg.Message) {
	if len(e.Params) < 3 || e.Params[1] != "ACK" {
		return
	}
	for _, token := range strings.Fields(e.Params[2]) {
		name := token
		if i := strings.IndexByte(token, '='); i >= 0 {
			name = token[:i]
		}
		if name == "sasl" {
			b.irc.SASLMech = "ERC8004"
			return
		}
	}
}

// onAUTHForERC8004 drives the 3-step exchange:
//   - step 0: server sent `AUTHENTICATE +` → reply with base64(20-byte address).
//   - step 1: server sent `AUTHENTICATE <b64(nonce)>` → reply with base64(sig).
//
// ircevent's own AUTHENTICATE callback runs after this one and hits its
// `default` (no-op) branch because SASLMech is "ERC8004".
func (b *Bridge) onAUTHForERC8004(e ircmsg.Message) {
	if len(e.Params) < 1 {
		return
	}
	data := e.Params[0]
	switch b.saslStep {
	case 0:
		if data != "+" {
			return
		}
		// Step 1: the spec requires the client to declare which agentId it's
		// authenticating as (the canonical registry has no reverse
		// address→agentId lookup). If AgentID is nil we're in pre-canonical
		// ch07 mode and the lower 20 bytes of the 32-byte payload encode the
		// claimed address — that's what the chapter 07 fork branch expects.
		var first [erc8004.AgentIDSize]byte
		if b.cfg.AgentID != nil {
			b.cfg.AgentID.FillBytes(first[:])
		} else {
			copy(first[erc8004.AgentIDSize-20:], erc8004.Address(b.cfg.ERC8004Key).Bytes())
		}
		_ = b.irc.Send("AUTHENTICATE", base64.StdEncoding.EncodeToString(first[:]))
		b.saslStep = 1
	case 1:
		nonce, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			return
		}
		sig, err := erc8004.SignChallenge(b.cfg.ERC8004Key, b.cfg.ChainID, b.cfg.ServerName, b.cfg.AgentID, nonce)
		if err != nil {
			return
		}
		_ = b.irc.Send("AUTHENTICATE", base64.StdEncoding.EncodeToString(sig))
		b.saslStep = 2
	}
}

func (b *Bridge) onPrivmsg(e ircmsg.Message) {
	if len(e.Params) < 2 {
		return
	}
	from := nickFromSource(e.Source)
	target := e.Params[0]
	text := e.Params[1]
	tags := e.AllTags()
	b.cfg.OnEvent(protocol.Event{
		Type:    "message",
		T:       time.Now().Unix(),
		Channel: target,
		From:    from,
		Text:    text,
		Account: tags["account"],
		IsSelf:  from == b.irc.CurrentNick(),
	})
}

func (b *Bridge) onJoin(e ircmsg.Message) {
	if len(e.Params) < 1 {
		return
	}
	from := nickFromSource(e.Source)
	channel := e.Params[0]
	b.addMember(channel, from)
	b.cfg.OnEvent(protocol.Event{
		Type:    "join",
		T:       time.Now().Unix(),
		Channel: channel,
		From:    from,
		IsSelf:  from == b.irc.CurrentNick(),
	})
}

func (b *Bridge) onPart(e ircmsg.Message) {
	if len(e.Params) < 1 {
		return
	}
	from := nickFromSource(e.Source)
	channel := e.Params[0]
	reason := ""
	if len(e.Params) >= 2 {
		reason = e.Params[1]
	}
	b.removeMember(channel, from)
	b.cfg.OnEvent(protocol.Event{
		Type: "part", T: time.Now().Unix(), Channel: channel,
		From: from, Reason: reason, IsSelf: from == b.irc.CurrentNick(),
	})
}

func (b *Bridge) onQuit(e ircmsg.Message) {
	from := nickFromSource(e.Source)
	reason := ""
	if len(e.Params) >= 1 {
		reason = e.Params[0]
	}
	// QUIT applies to all channels we know this nick is in.
	b.mu.Lock()
	for ch, set := range b.channels {
		_, _ = ch, set
		delete(set, strings.ToLower(from))
	}
	b.mu.Unlock()
	b.cfg.OnEvent(protocol.Event{
		Type: "quit", T: time.Now().Unix(),
		From: from, Reason: reason, IsSelf: from == b.irc.CurrentNick(),
	})
}

func (b *Bridge) onNick(e ircmsg.Message) {
	if len(e.Params) < 1 {
		return
	}
	old := nickFromSource(e.Source)
	new_ := e.Params[0]
	b.mu.Lock()
	for _, set := range b.channels {
		if _, ok := set[strings.ToLower(old)]; ok {
			delete(set, strings.ToLower(old))
			set[strings.ToLower(new_)] = struct{}{}
		}
	}
	b.mu.Unlock()
	b.cfg.OnEvent(protocol.Event{
		Type: "nick", T: time.Now().Unix(), OldNick: old, NewNick: new_,
		IsSelf: old == b.irc.CurrentNick(),
	})
}

// 353 RPL_NAMREPLY: ":server 353 nick = #chan :@alice +bob carol"
func (b *Bridge) on353(e ircmsg.Message) {
	if len(e.Params) < 4 {
		return
	}
	channel := e.Params[2]
	for _, n := range strings.Fields(e.Params[3]) {
		// Strip op/voice prefixes like @, +, %, ~, &
		n = strings.TrimLeft(n, "@+%~&")
		if n != "" {
			b.addMember(channel, n)
		}
	}
}

// ---- member-set helpers --------------------------------------------------

func (b *Bridge) addMember(channel, nick string) {
	ch := strings.ToLower(channel)
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.channels[ch]; !ok {
		b.channels[ch] = map[string]struct{}{}
	}
	b.channels[ch][strings.ToLower(nick)] = struct{}{}
}

func (b *Bridge) removeMember(channel, nick string) {
	ch := strings.ToLower(channel)
	b.mu.Lock()
	defer b.mu.Unlock()
	if set, ok := b.channels[ch]; ok {
		delete(set, strings.ToLower(nick))
	}
}

// ---- helpers --------------------------------------------------------------

func nickFromSource(src string) string {
	// Source format: nick!user@host or just server.name
	if i := strings.IndexByte(src, '!'); i >= 0 {
		return src[:i]
	}
	return src
}

// sanitize strips CR/LF/NUL from outbound text. Closes the line-injection
// bug class — an agent that emits "alice\nQUIT :pwn" would otherwise produce
// two IRC lines, the second of which is whatever the agent (or its prompt
// injection) felt like sending.
func sanitize(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\x00", "")
	return s
}

// ---- errors --------------------------------------------------------------

type bridgeError string

func (e bridgeError) Error() string { return string(e) }

const errTimeout = bridgeError("registration timeout")
