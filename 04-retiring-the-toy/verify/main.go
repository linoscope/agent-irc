// verify exercises chapter 04: confirm Ergo is running, accept two clients,
// register them, JOIN #room, and verify a PRIVMSG fans out — exactly the
// scenario from chapter 02, but now against the real Ergo binary.
//
// We do not start Ergo here; start-ergo.sh is run separately. This script
// just probes the running server.
package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

const port = "16670"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL:", err)
		os.Exit(1)
	}
	fmt.Println("PASS: Ergo handshake + broadcast round-trip works")
}

func run() error {
	if !portReady(port, 5*time.Second) {
		return fmt.Errorf("Ergo is not listening on :%s — run ./start-ergo.sh first", port)
	}

	alice, err := dialClient("alice")
	if err != nil {
		return err
	}
	defer alice.close()
	bob, err := dialClient("bob")
	if err != nil {
		return err
	}
	defer bob.close()

	// Confirm Ergo emits a 005 line (multiple, in fact — note the difference
	// from chapter 03's single line).
	if _, err := alice.expect("005"); err != nil {
		return err
	}

	// JOIN #room from both, then alice PRIVMSGs and bob receives.
	if err := alice.send("JOIN #room"); err != nil {
		return err
	}
	if _, err := alice.expect("366"); err != nil {
		return err
	}
	if err := bob.send("JOIN #room"); err != nil {
		return err
	}
	if _, err := bob.expect("366"); err != nil {
		return err
	}
	if err := alice.send("PRIVMSG #room :hello bob"); err != nil {
		return err
	}
	line, err := bob.expect("PRIVMSG #room :hello bob")
	if err != nil {
		return err
	}
	if !strings.HasPrefix(line, ":alice!") {
		return fmt.Errorf("expected :alice! prefix on relayed PRIVMSG, got %q", line)
	}
	return nil
}

type client struct {
	c    net.Conn
	rd   *bufio.Reader
	nick string
}

func dialClient(nick string) (*client, error) {
	c, err := net.Dial("tcp", "localhost:"+port)
	if err != nil {
		return nil, err
	}
	cl := &client{c: c, rd: bufio.NewReader(c), nick: nick}
	if err := cl.send("NICK " + nick); err != nil {
		return nil, err
	}
	if err := cl.send(fmt.Sprintf("USER %s 0 * :%s the agent", nick, nick)); err != nil {
		return nil, err
	}
	if _, err := cl.expect("001"); err != nil {
		return nil, err
	}
	return cl, nil
}

func (c *client) send(line string) error {
	_, err := c.c.Write([]byte(line + "\r\n"))
	return err
}

func (c *client) expect(substr string) (string, error) {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c.c.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		line, err := c.rd.ReadString('\n')
		if err != nil {
			if isTimeout(err) {
				continue
			}
			return "", fmt.Errorf("%s read: %w", c.nick, err)
		}
		line = strings.TrimRight(line, "\r\n")
		fmt.Printf("  %s <- %s\n", c.nick, line)
		if strings.Contains(line, substr) {
			return line, nil
		}
	}
	return "", fmt.Errorf("%s timeout waiting for %q", c.nick, substr)
}

func (c *client) close() { _ = c.c.Close() }

func isTimeout(err error) bool {
	if err == nil {
		return false
	}
	type timeouter interface{ Timeout() bool }
	t, ok := err.(timeouter)
	return ok && t.Timeout()
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
