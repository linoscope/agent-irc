// Package protocol defines the JSONL request/response shapes spoken between
// the agent-irc CLI and its daemon over a Unix socket.
//
// Wire conventions:
//   - Each line is one JSON document (newline-delimited JSON).
//   - The CLI sends exactly one Request as the first line of a connection.
//   - The daemon replies with one Response (one-shot ops) or a stream of
//     Events terminated when the daemon closes the connection (tail).
package protocol

// Op constants — every CLI subcommand maps to exactly one op.
const (
	OpConnect = "connect"
	OpJoin    = "join"
	OpPart    = "part"
	OpSend    = "send"
	OpDM      = "dm"
	OpTail    = "tail"
	OpHistory = "history"
	OpNicks   = "nicks"
	OpWhoami  = "whoami"
	OpQuit    = "quit"
	OpStatus  = "status"
)

// Request is the envelope. Fields are op-specific; only the relevant ones
// are populated by the CLI. We use a flat struct rather than a json.RawMessage
// payload because the field set is small and stable.
type Request struct {
	Op string `json:"op"`

	// connect
	Server   string `json:"server,omitempty"`
	Nick     string `json:"nick,omitempty"`
	TLS      bool   `json:"tls,omitempty"`
	Password string `json:"password,omitempty"` // SASL PLAIN password; empty = no SASL

	// join, part, tail, history, nicks
	Channel string `json:"channel,omitempty"`
	Reason  string `json:"reason,omitempty"`

	// send, dm
	Target string `json:"target,omitempty"`
	Text   string `json:"text,omitempty"`

	// tail
	Follow   bool `json:"follow,omitempty"`
	SkipSelf bool `json:"skip_self,omitempty"`
	History  int  `json:"history,omitempty"` // backfill before stream

	// history
	N int `json:"n,omitempty"`
}

// Response is the one-shot reply. For tail, the daemon emits Events instead.
type Response struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`

	// whoami / status
	Nick      string `json:"nick,omitempty"`
	Server    string `json:"server,omitempty"`
	Connected bool   `json:"connected,omitempty"`
	Account   string `json:"account,omitempty"`

	// nicks
	Nicks []string `json:"nicks,omitempty"`

	// history
	Messages []Event `json:"messages,omitempty"`
}

// Event is one inbound IRC event surfaced to a tail subscriber.
//
// The same shape covers PRIVMSG (Type=message), JOIN, PART, QUIT, NICK,
// and a generic catch-all for things we don't have richer semantics for.
type Event struct {
	Type    string `json:"event"` // "message" | "join" | "part" | "quit" | "nick" | "raw"
	T       int64  `json:"t"`     // unix seconds
	Channel string `json:"channel,omitempty"`
	From    string `json:"from,omitempty"`
	Text    string `json:"text,omitempty"`
	Account string `json:"account,omitempty"` // IRCv3 account-tag if present
	Reason  string `json:"reason,omitempty"`
	OldNick string `json:"old,omitempty"` // for nick changes
	NewNick string `json:"new,omitempty"`
	IsSelf  bool   `json:"is_self,omitempty"` // true if this event is from us

	// Catch-all for verbs we don't have first-class handling for.
	Raw string `json:"raw,omitempty"`
}
