// agent-irc — single-binary CLI + daemon.
//
// Subcommands (each backs one CLI verb):
//
//   connect SERVER:PORT --nick NICK [--tls] [--password PW]
//                       — start daemon (idempotent), register, complete handshake
//   join CHANNEL        — JOIN
//   part CHANNEL        — PART
//   send TARGET TEXT    — PRIVMSG to channel or nick
//   dm    NICK TEXT     — alias of send
//   tail [CHANNEL]      — stream events as JSONL; flags: --follow --history N --skip-self
//   nicks CHANNEL       — print members
//   whoami              — print bound nick + connected status
//   quit                — disconnect + shut daemon down
//   daemon              — internal: spawned by `connect`, holds the IRC connection
//
// The CLI is intentionally thin: each subcommand assembles a protocol.Request,
// opens the daemon's Unix socket, sends one JSON line, prints the reply.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/lin/agent-irc/cli/internal/daemon"
	"github.com/lin/agent-irc/cli/internal/protocol"
)

// boolFlags lists every flag that takes no value. Used by reorderArgs to
// know whether to treat the next token as a value or as a positional.
// Keep this list in sync with the per-subcommand flag definitions below.
var boolFlags = map[string]bool{
	"--tls": true, "--follow": true, "--skip-self": true,
	"--no-follow": true, "--no-skip-self": true,
}

// reorderArgs moves all --flag tokens to the front of args, preserving order.
// Without this, Go's stdlib flag.Parse() stops at the first non-flag token,
// so `connect localhost:17000 --nick foo` would never see the --nick flag.
func reorderArgs(args []string) []string {
	flags, pos := []string{}, []string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			// If --flag form (no =) and not a bool, consume the next token as value.
			if !strings.Contains(a, "=") && !boolFlags[a] && i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		pos = append(pos, a)
	}
	return append(flags, pos...)
}

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}

	switch os.Args[1] {
	case "connect":
		os.Exit(cmdConnect(os.Args[2:]))
	case "join":
		os.Exit(cmdJoin(os.Args[2:]))
	case "part":
		os.Exit(cmdPart(os.Args[2:]))
	case "send", "dm":
		os.Exit(cmdSend(os.Args[2:]))
	case "tail":
		os.Exit(cmdTail(os.Args[2:]))
	case "nicks":
		os.Exit(cmdNicks(os.Args[2:]))
	case "whoami", "status":
		os.Exit(cmdWhoami(os.Args[2:]))
	case "quit":
		os.Exit(cmdQuit(os.Args[2:]))
	case "daemon":
		os.Exit(cmdDaemon(os.Args[2:]))
	case "-h", "--help", "help":
		usage(os.Stdout)
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n\n", os.Args[1])
		usage(os.Stderr)
		os.Exit(2)
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `agent-irc — IRC CLI for agents

usage:
  agent-irc connect SERVER:PORT --nick NICK [--tls] [--password PW]
                                            [--erc8004-key PATH --chain-id N --server-name NAME]
  agent-irc join     CHANNEL
  agent-irc part     CHANNEL [--reason "..."]
  agent-irc send     TARGET "text"
  agent-irc dm       NICK   "text"
  agent-irc tail     [CHANNEL] [--follow] [--history N] [--skip-self]
  agent-irc nicks    CHANNEL
  agent-irc whoami
  agent-irc quit

global flags (any subcommand):
  --nick NICK   pick the daemon to talk to (defaults to the only running one)
  --socket PATH override socket path
`)
}

// ---- subcommands ---------------------------------------------------------

func cmdConnect(args []string) int {
	fs := flag.NewFlagSet("connect", flag.ExitOnError)
	nick := fs.String("nick", "", "IRC nickname (required)")
	useTLS := fs.Bool("tls", false, "use TLS to dial the server")
	password := fs.String("password", "", "SASL PLAIN password (optional)")
	erc8004Key := fs.String("erc8004-key", "", "path to hex-encoded ECDSA key (enables ERC8004 SASL)")
	agentID := fs.Uint64("agent-id", 0, "ERC-8004 agent token id this key is registered as (required with --erc8004-key against ch08+ servers)")
	chainID := fs.Uint64("chain-id", 0, "chain id bound into the ERC8004 signature (e.g. 8453 for Base mainnet, 31337 for anvil)")
	serverName := fs.String("server-name", "", "IRC server name bound into the ERC8004 signature (e.g. ergo.test)")
	fs.Parse(reorderArgs(args))
	if fs.NArg() < 1 || *nick == "" {
		fmt.Fprintln(os.Stderr, "usage: agent-irc connect SERVER:PORT --nick NICK [--tls] [--password PW] [--erc8004-key PATH --agent-id N --chain-id N --server-name NAME]")
		return 2
	}
	if *erc8004Key != "" && (*chainID == 0 || *serverName == "") {
		fmt.Fprintln(os.Stderr, "--erc8004-key requires --chain-id and --server-name")
		return 2
	}
	server := fs.Arg(0)

	// If a daemon for this nick is already running, we're done.
	if daemonAlive(*nick) {
		fmt.Println("ok (daemon already running)")
		return 0
	}

	// Spawn daemon as a detached subprocess.
	exe, err := os.Executable()
	if err != nil {
		return fail(err)
	}
	cmd := exec.Command(exe, "daemon",
		"--server", server,
		"--nick", *nick,
		"--password", *password,
	)
	if *useTLS {
		cmd.Args = append(cmd.Args, "--tls")
	}
	if *erc8004Key != "" {
		cmd.Args = append(cmd.Args,
			"--erc8004-key", *erc8004Key,
			"--chain-id", fmt.Sprintf("%d", *chainID),
			"--server-name", *serverName,
		)
		if *agentID != 0 {
			cmd.Args = append(cmd.Args, "--agent-id", fmt.Sprintf("%d", *agentID))
		}
	}
	logPath := daemon.SocketPath(*nick) + ".log"
	logFile, _ := os.Create(logPath)
	if logFile != nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}
	if err := cmd.Start(); err != nil {
		return fail(err)
	}
	// Don't wait. Detach.
	_ = cmd.Process.Release()

	// Poll for socket readiness.
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if daemonAlive(*nick) {
			fmt.Println("ok")
			return 0
		}
		time.Sleep(150 * time.Millisecond)
	}
	fmt.Fprintf(os.Stderr, "daemon failed to come up; see %s\n", logPath)
	return 1
}

func cmdJoin(args []string) int {
	fs := flag.NewFlagSet("join", flag.ExitOnError)
	nick := fs.String("nick", "", "")
	fs.Parse(reorderArgs(args))
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: agent-irc join CHANNEL")
		return 2
	}
	return oneShot(*nick, protocol.Request{Op: protocol.OpJoin, Channel: fs.Arg(0)})
}

func cmdPart(args []string) int {
	fs := flag.NewFlagSet("part", flag.ExitOnError)
	nick := fs.String("nick", "", "")
	reason := fs.String("reason", "", "")
	fs.Parse(reorderArgs(args))
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: agent-irc part CHANNEL [--reason ...]")
		return 2
	}
	return oneShot(*nick, protocol.Request{
		Op: protocol.OpPart, Channel: fs.Arg(0), Reason: *reason,
	})
}

func cmdSend(args []string) int {
	fs := flag.NewFlagSet("send", flag.ExitOnError)
	nick := fs.String("nick", "", "")
	fs.Parse(reorderArgs(args))
	if fs.NArg() < 2 {
		fmt.Fprintln(os.Stderr, "usage: agent-irc send TARGET \"text\"")
		return 2
	}
	return oneShot(*nick, protocol.Request{
		Op: protocol.OpSend, Target: fs.Arg(0), Text: strings.Join(fs.Args()[1:], " "),
	})
}

func cmdTail(args []string) int {
	fs := flag.NewFlagSet("tail", flag.ExitOnError)
	nick := fs.String("nick", "", "")
	follow := fs.Bool("follow", true, "stream live messages forever")
	history := fs.Int("history", 0, "backfill last N messages before live stream")
	skipSelf := fs.Bool("skip-self", true, "drop messages sent by self")
	fs.Parse(reorderArgs(args))

	channel := ""
	if fs.NArg() >= 1 {
		channel = fs.Arg(0)
	}

	req := protocol.Request{
		Op: protocol.OpTail, Channel: channel,
		Follow: *follow, History: *history, SkipSelf: *skipSelf,
	}
	conn, err := dial(*nick)
	if err != nil {
		return fail(err)
	}
	defer conn.Close()
	if err := writeReq(conn, req); err != nil {
		return fail(err)
	}
	// Stream JSONL events to stdout.
	rd := bufio.NewReader(conn)
	for {
		line, err := rd.ReadBytes('\n')
		if len(line) > 0 {
			os.Stdout.Write(line)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return 0
			}
			return fail(err)
		}
	}
}

func cmdNicks(args []string) int {
	fs := flag.NewFlagSet("nicks", flag.ExitOnError)
	nick := fs.String("nick", "", "")
	fs.Parse(reorderArgs(args))
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: agent-irc nicks CHANNEL")
		return 2
	}
	r, err := request(*nick, protocol.Request{Op: protocol.OpNicks, Channel: fs.Arg(0)})
	if err != nil {
		return fail(err)
	}
	if !r.OK {
		fmt.Fprintln(os.Stderr, "error:", r.Error)
		return 1
	}
	for _, n := range r.Nicks {
		fmt.Println(n)
	}
	return 0
}

func cmdWhoami(args []string) int {
	fs := flag.NewFlagSet("whoami", flag.ExitOnError)
	nick := fs.String("nick", "", "")
	fs.Parse(reorderArgs(args))
	r, err := request(*nick, protocol.Request{Op: protocol.OpWhoami})
	if err != nil {
		return fail(err)
	}
	if !r.OK {
		fmt.Fprintln(os.Stderr, "error:", r.Error)
		return 1
	}
	fmt.Printf("nick=%s server=%s connected=%v\n", r.Nick, r.Server, r.Connected)
	return 0
}

func cmdQuit(args []string) int {
	fs := flag.NewFlagSet("quit", flag.ExitOnError)
	nick := fs.String("nick", "", "")
	fs.Parse(reorderArgs(args))
	return oneShot(*nick, protocol.Request{Op: protocol.OpQuit})
}

// ---- internal: daemon mode -----------------------------------------------

func cmdDaemon(args []string) int {
	fs := flag.NewFlagSet("daemon", flag.ExitOnError)
	server := fs.String("server", "", "")
	nick := fs.String("nick", "", "")
	useTLS := fs.Bool("tls", false, "")
	password := fs.String("password", "", "")
	erc8004Key := fs.String("erc8004-key", "", "")
	agentID := fs.Uint64("agent-id", 0, "")
	chainID := fs.Uint64("chain-id", 0, "")
	serverName := fs.String("server-name", "", "")
	fs.Parse(reorderArgs(args))
	if *server == "" || *nick == "" {
		fmt.Fprintln(os.Stderr, "daemon: --server and --nick required")
		return 2
	}
	srv, err := daemon.New(daemon.Config{
		Server: *server, Nick: *nick, TLS: *useTLS, Password: *password,
		ERC8004KeyPath: *erc8004Key, AgentID: *agentID, ChainID: *chainID, ServerName: *serverName,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "daemon:", err)
		return 1
	}
	if err := srv.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "daemon:", err)
		return 1
	}
	return 0
}

// ---- helpers --------------------------------------------------------------

// dial connects to the daemon's Unix socket. If --nick is empty, it picks
// the only running daemon in /tmp + XDG. Errors if there are zero or many.
func dial(nick string) (net.Conn, error) {
	if nick == "" {
		var err error
		nick, err = autodetectNick()
		if err != nil {
			return nil, err
		}
	}
	return net.Dial("unix", daemon.SocketPath(nick))
}

func autodetectNick() (string, error) {
	// Look for live sockets. XDG path is preferred and uses pure <nick>.sock;
	// /tmp fallback uses <user>-<nick>.sock.
	candidates := []string{}
	if rt := os.Getenv("XDG_RUNTIME_DIR"); rt != "" {
		entries, _ := os.ReadDir(rt + "/agent-irc")
		for _, e := range entries {
			name := e.Name()
			if strings.HasSuffix(name, ".sock") {
				candidates = append(candidates, strings.TrimSuffix(name, ".sock"))
			}
		}
	}
	if len(candidates) == 0 {
		// /tmp fallback: agent-irc-USER-NICK.sock — extract NICK after the user.
		user := os.Getenv("USER")
		if user == "" {
			user = "anon"
		}
		prefix := "agent-irc-" + user + "-"
		entries, _ := os.ReadDir("/tmp")
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, ".sock") {
				candidates = append(candidates,
					strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".sock"))
			}
		}
	}
	live := []string{}
	for _, nick := range candidates {
		if daemonAlive(nick) {
			live = append(live, nick)
		}
	}
	if len(live) == 0 {
		return "", fmt.Errorf("no daemon running; run `agent-irc connect` first")
	}
	if len(live) > 1 {
		return "", fmt.Errorf("multiple daemons running (%v); pass --nick to disambiguate", live)
	}
	return live[0], nil
}

func daemonAlive(nick string) bool {
	conn, err := net.DialTimeout("unix", daemon.SocketPath(nick), 200*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func writeReq(conn net.Conn, req protocol.Request) error {
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}
	_, err = conn.Write(append(b, '\n'))
	return err
}

func readResp(conn net.Conn) (*protocol.Response, error) {
	rd := bufio.NewReader(conn)
	line, err := rd.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return nil, err
	}
	var r protocol.Response
	if err := json.Unmarshal(line, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func request(nick string, req protocol.Request) (*protocol.Response, error) {
	conn, err := dial(nick)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if err := writeReq(conn, req); err != nil {
		return nil, err
	}
	return readResp(conn)
}

// oneShot is the standard "send request, print ok or error, exit" pattern.
func oneShot(nick string, req protocol.Request) int {
	r, err := request(nick, req)
	if err != nil {
		return fail(err)
	}
	if !r.OK {
		fmt.Fprintln(os.Stderr, "error:", r.Error)
		return 1
	}
	fmt.Println("ok")
	return 0
}

func fail(err error) int {
	fmt.Fprintln(os.Stderr, "error:", err)
	return 1
}
