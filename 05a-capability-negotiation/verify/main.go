// verify exercises the CAP LS 302 flow against unmodified Ergo:
//
//  1. Connect.
//  2. Send `CAP LS 302`.
//  3. Read continuation-handled CAP * LS lines until the full cap set arrived.
//  4. Assert several standard IRCv3 caps are advertised (sanity check that
//     this is a working IRCv3 server, not a 1990s ircd).
//  5. `CAP REQ :account-tag server-time` → expect ACK.
//  6. `CAP END` → expect 001 RPL_WELCOME.
//
// Chapter 05b's verify checks for our agent-irc.example/hello vendor cap;
// 05a only checks that the standard machinery is in place.
package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

const port = "16671"

// Standard IRCv3 caps we expect any modern Ergo to advertise.
var requiredCaps = []string{
	"account-tag",
	"server-time",
	"message-tags",
	"sasl",
	"batch",
	"echo-message",
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL:", err)
		os.Exit(1)
	}
	fmt.Println("PASS: CAP LS 302 → REQ → ACK → 001 against unmodified Ergo")
}

func run() error {
	if !portReady(port, 5*time.Second) {
		return fmt.Errorf("Ergo not listening on :%s — run ./start-ergo.sh first", port)
	}
	c, err := net.Dial("tcp", "localhost:"+port)
	if err != nil {
		return err
	}
	defer c.Close()
	rd := bufio.NewReader(c)

	send := func(line string) error {
		_, err := c.Write([]byte(line + "\r\n"))
		fmt.Printf("  -> %s\n", line)
		return err
	}
	readLine := func() (string, error) {
		c.SetReadDeadline(time.Now().Add(3 * time.Second))
		line, err := rd.ReadString('\n')
		if err != nil {
			return "", err
		}
		line = strings.TrimRight(line, "\r\n")
		fmt.Printf("  <- %s\n", line)
		return line, nil
	}

	if err := send("CAP LS 302"); err != nil {
		return err
	}
	if err := send("NICK alice"); err != nil {
		return err
	}
	if err := send("USER alice 0 * :Alice"); err != nil {
		return err
	}

	// Collect CAP * LS continuation lines.
	var capLines []string
	for {
		line, err := readLine()
		if err != nil {
			return fmt.Errorf("read CAP LS: %w", err)
		}
		if !strings.Contains(line, " CAP ") || !strings.Contains(line, " LS ") {
			return fmt.Errorf("expected CAP LS line, got: %s", line)
		}
		capLines = append(capLines, line)
		if !strings.Contains(line, " LS * :") {
			break // final batch
		}
	}

	allCaps := extractCaps(capLines)
	fmt.Printf("  parsed %d capabilities\n", len(allCaps))
	for _, want := range requiredCaps {
		if _, ok := allCaps[want]; !ok {
			return fmt.Errorf("expected standard cap %q in CAP LS", want)
		}
	}
	fmt.Printf("  ✓ all %d required standard caps present\n", len(requiredCaps))

	if err := send("CAP REQ :account-tag server-time"); err != nil {
		return err
	}
	for {
		line, err := readLine()
		if err != nil {
			return err
		}
		if strings.Contains(line, "CAP * NAK") {
			return fmt.Errorf("server NAK'd standard caps")
		}
		if strings.Contains(line, "CAP * ACK") &&
			strings.Contains(line, "account-tag") &&
			strings.Contains(line, "server-time") {
			break
		}
	}

	if err := send("CAP END"); err != nil {
		return err
	}
	for {
		line, err := readLine()
		if err != nil {
			return err
		}
		if strings.Contains(line, " 001 ") {
			return nil
		}
	}
}

func extractCaps(lines []string) map[string]string {
	out := make(map[string]string)
	for _, l := range lines {
		idx := strings.Index(l, " :")
		if idx < 0 {
			continue
		}
		body := l[idx+2:]
		for _, tok := range strings.Fields(body) {
			name, value, _ := strings.Cut(tok, "=")
			out[name] = value
		}
	}
	return out
}

func portReady(port string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c, err := net.Dial("tcp", "localhost:"+port)
		if err == nil {
			c.Close()
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}
