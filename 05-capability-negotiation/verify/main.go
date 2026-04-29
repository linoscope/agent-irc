// verify exercises the CAP LS 302 flow against the agent-irc fork:
//
//  1. Connect.
//  2. Send `CAP LS 302`.
//  3. Read continuation-handled CAP * LS lines until we have the full set.
//  4. Assert that `agent-irc.example/hello` is in the advertised caps.
//  5. `CAP REQ :agent-irc.example/hello` → expect ACK.
//  6. `CAP END` → expect 001 RPL_WELCOME.
package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

const (
	port    = "16671"
	wantCap = "agent-irc.example/hello"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL:", err)
		os.Exit(1)
	}
	fmt.Println("PASS: agent-irc.example/hello advertised, REQ acknowledged, registration completes")
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

	// Step 1+2: hold registration open with CAP LS 302.
	if err := send("CAP LS 302"); err != nil {
		return err
	}
	if err := send("NICK alice"); err != nil {
		return err
	}
	if err := send("USER alice 0 * :Alice"); err != nil {
		return err
	}

	// Step 3: collect CAP LS continuation lines.
	var capLines []string
	for {
		line, err := readLine()
		if err != nil {
			return fmt.Errorf("read CAP LS: %w", err)
		}
		// The form is ":server CAP * LS [* :]caps..." — accumulate until
		// we see a non-`*` continuation marker.
		if !strings.Contains(line, " CAP ") || !strings.Contains(line, " LS ") {
			return fmt.Errorf("expected CAP LS line, got: %s", line)
		}
		capLines = append(capLines, line)
		// `CAP * LS *` (with the asterisk after LS) means more is coming.
		// `CAP * LS :caps` (no asterisk) is the final line.
		if !strings.Contains(line, " LS * :") {
			break
		}
	}

	// Step 4: extract and check.
	allCaps := extractCaps(capLines)
	fmt.Printf("  parsed %d capabilities\n", len(allCaps))
	if _, ok := allCaps[wantCap]; !ok {
		return fmt.Errorf("server did not advertise %q\n  caps were: %v", wantCap, sortedKeys(allCaps))
	}

	// Step 5: REQ → ACK.
	if err := send("CAP REQ :" + wantCap); err != nil {
		return err
	}
	for {
		line, err := readLine()
		if err != nil {
			return err
		}
		if strings.Contains(line, "CAP * NAK") {
			return fmt.Errorf("server NAK'd %q", wantCap)
		}
		// IRCv3 lets ACK use either the trailing-colon form (`ACK :cap1 cap2`)
		// or the plain form (`ACK cap1`) for a single cap. Match both.
		if strings.Contains(line, "CAP * ACK") && strings.Contains(line, wantCap) {
			break
		}
	}

	// Step 6: end registration.
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

// extractCaps parses CAP LS lines into a map of cap name to its (optional)
// "=value" string. Each line is ":server CAP nick LS [* :]token1 token2 ...".
func extractCaps(lines []string) map[string]string {
	out := make(map[string]string)
	for _, l := range lines {
		// Trailing param starts after " :".
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

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
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
