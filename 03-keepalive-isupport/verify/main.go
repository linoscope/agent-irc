// verify exercises chapter 03 end-to-end:
//  1. ISUPPORT (numeric 005) is emitted with the expected tokens at registration.
//  2. RFC 1459 casemapping treats `Foo[bar]` and `foo{bar}` as the same nick
//     (collision detected on second connect).
//  3. PING/PONG keepalive: an idle client receives a PING, must reply, and
//     a non-replying client gets dropped with "Ping timeout".
//  4. Two-client PRIVMSG still works (chapter-02 regression check).
package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	port    = "16669"
	idleSec = "1" // 1-second idle so the test runs in seconds not minutes
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL:", err)
		os.Exit(1)
	}
	fmt.Println("PASS: chapter 03 — ISUPPORT, casemapping, PING/PONG, broadcast")
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	bin, err := buildServer(ctx)
	if err != nil {
		return err
	}
	defer os.Remove(bin)

	cmd := exec.CommandContext(ctx, bin)
	cmd.Env = append(os.Environ(),
		"LISTEN=:"+port,
		"IDLE_TIMEOUT="+idleSec,
	)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start server: %w", err)
	}
	defer func() {
		cancel()
		_ = cmd.Wait()
	}()
	if err := waitForPort(ctx, port); err != nil {
		return err
	}

	if err := step1ISupport(); err != nil {
		return fmt.Errorf("step 1 ISUPPORT: %w", err)
	}
	if err := step2Casemapping(); err != nil {
		return fmt.Errorf("step 2 casemapping: %w", err)
	}
	if err := step3PingTimeout(); err != nil {
		return fmt.Errorf("step 3 ping timeout: %w", err)
	}
	if err := step4PingReply(); err != nil {
		return fmt.Errorf("step 4 ping reply: %w", err)
	}
	if err := step5Broadcast(); err != nil {
		return fmt.Errorf("step 5 broadcast: %w", err)
	}
	return nil
}

// step1ISupport: connect, complete registration, scan for the 005 line and
// confirm it advertises rfc1459 casemapping and the expected tokens.
func step1ISupport() error {
	fmt.Println("--- step 1: ISUPPORT (005) ---")
	c, err := dialClient("ann")
	if err != nil {
		return err
	}
	defer c.close()
	line, err := c.expect("005 ann")
	if err != nil {
		return err
	}
	for _, token := range []string{"NETWORK=AgentIRC", "CASEMAPPING=rfc1459", "CHANTYPES=#", "NICKLEN=30"} {
		if !strings.Contains(line, token) {
			return fmt.Errorf("missing %q in 005: %s", token, line)
		}
	}
	return nil
}

// step2Casemapping: connect as `Foo[bar]`. Try to connect again as `foo{bar}`.
// Server should reject the second NICK with 433 because rfc1459 casemapping
// treats them as the same.
func step2Casemapping() error {
	fmt.Println("--- step 2: rfc1459 casemapping ---")
	a, err := dialClient("Foo[bar]")
	if err != nil {
		return err
	}
	defer a.close()

	// Open a raw connection and try to NICK with the case-folded variant.
	b, err := net.Dial("tcp", "localhost:"+port)
	if err != nil {
		return err
	}
	defer b.Close()
	if _, err := b.Write([]byte("NICK foo{bar}\r\nUSER foo 0 * :foo\r\n")); err != nil {
		return err
	}
	rd := bufio.NewReader(b)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_ = b.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		line, err := rd.ReadString('\n')
		if err != nil {
			if isTimeout(err) {
				continue
			}
			return fmt.Errorf("read: %w", err)
		}
		line = strings.TrimRight(line, "\r\n")
		fmt.Printf("  raw <- %s\n", line)
		if strings.Contains(line, "433") {
			return nil // expected: nickname collision
		}
		if strings.Contains(line, "001") {
			return fmt.Errorf("server should have rejected foo{bar} as taken; instead saw 001")
		}
	}
	return fmt.Errorf("no 433 collision reply within deadline")
}

// step3PingTimeout: connect, register, then go silent. Server should send
// PING after IDLE_TIMEOUT, and after another IDLE_TIMEOUT close the socket.
// We assert the connection closes within 4*IDLE_TIMEOUT (3s here, generous
// margin for CI jitter).
func step3PingTimeout() error {
	fmt.Println("--- step 3: ping timeout ---")
	c, err := dialClient("idler")
	if err != nil {
		return err
	}
	defer c.close()

	// Wait for the PING to arrive.
	if _, err := c.expect("PING"); err != nil {
		return err
	}
	// Don't reply. Server should drop us within ~1 more second.
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		_ = c.c.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		_, err := c.rd.ReadString('\n')
		if err != nil {
			if isTimeout(err) {
				continue
			}
			// EOF / closed connection — server dropped us, success.
			return nil
		}
	}
	return fmt.Errorf("server did not drop the unresponsive client")
}

// step4PingReply: connect, register, wait for PING, reply with PONG. Server
// should keep the connection alive past the timeout.
func step4PingReply() error {
	fmt.Println("--- step 4: ping reply keeps connection alive ---")
	c, err := dialClient("respond")
	if err != nil {
		return err
	}
	defer c.close()

	pingLine, err := c.expect("PING")
	if err != nil {
		return err
	}
	// Extract the token from "PING :token".
	idx := strings.LastIndex(pingLine, ":")
	if idx < 0 {
		return fmt.Errorf("malformed PING: %s", pingLine)
	}
	token := pingLine[idx+1:]
	if err := c.send("PONG :" + token); err != nil {
		return err
	}
	// We should *not* be dropped over the next idle window. Issue a NOOP-ish
	// command (PING) so we can confirm the connection is alive.
	time.Sleep(1500 * time.Millisecond)
	if err := c.send("PING :are-you-there"); err != nil {
		return fmt.Errorf("send after pong failed (connection dropped?): %w", err)
	}
	if _, err := c.expect("PONG"); err != nil {
		return err
	}
	return nil
}

// step5Broadcast: regression check that PRIVMSG fan-out from chapter 02 still works.
func step5Broadcast() error {
	fmt.Println("--- step 5: PRIVMSG broadcast (regression) ---")
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
		return fmt.Errorf("expected :alice! prefix, got %q", line)
	}
	return nil
}

// ---------- client helper ----------

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

// expect reads lines until one contains substr, with a per-line timeout.
func (c *client) expect(substr string) (string, error) {
	deadline := time.Now().Add(4 * time.Second)
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

func buildServer(ctx context.Context) (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	parent := wd
	if filepath.Base(wd) == "verify" {
		parent = filepath.Dir(wd)
	}
	out := filepath.Join(parent, ".ch03-server")
	cmd := exec.CommandContext(ctx, "go", "build", "-o", out, ".")
	cmd.Dir = parent
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("build server: %w", err)
	}
	return out, nil
}

func waitForPort(ctx context.Context, port string) error {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.Dial("tcp", "localhost:"+port)
		if err == nil {
			c.Close()
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("server did not come up on :%s", port)
}
