// verify exercises chapter 02 end-to-end: two clients, one channel, a PRIVMSG
// from one is delivered to the other. Exits 0 on success, non-zero on failure.
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

const port = "16668"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL:", err)
		os.Exit(1)
	}
	fmt.Println("PASS: alice and bob exchanged PRIVMSG via #room")
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Build the server binary into a temp dir, then run it. `go run`
	// from this subprogram's cwd would look in the wrong place.
	bin, err := buildServer(ctx)
	if err != nil {
		return err
	}
	defer os.Remove(bin)

	cmd := exec.CommandContext(ctx, bin)
	cmd.Env = append(os.Environ(), "LISTEN=:"+port)
	cmd.Stderr = os.Stderr // surface server logs on failure
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

	// Both join #room. alice first so she's already there when bob joins.
	if err := alice.send("JOIN #room"); err != nil {
		return err
	}
	if _, err := alice.expect("353"); err != nil {
		return err
	}
	if err := bob.send("JOIN #room"); err != nil {
		return err
	}
	if _, err := bob.expect("353"); err != nil {
		return err
	}
	// alice should see :bob!...JOIN #room.
	if _, err := alice.expect("JOIN #room"); err != nil {
		return err
	}

	// alice -> #room.
	if err := alice.send("PRIVMSG #room :hello bob"); err != nil {
		return err
	}
	line, err := bob.expect("PRIVMSG #room :hello bob")
	if err != nil {
		return err
	}
	if !strings.HasPrefix(line, ":alice!") {
		return fmt.Errorf("expected message prefixed with :alice!, got %q", line)
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

// expect reads lines until one contains substr, with a per-line timeout.
func (c *client) expect(substr string) (string, error) {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c.c.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
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
	// verify/ runs from inside 02-channels/verify; go up one level for the
	// server package. Module-aware build resolves dependencies normally.
	parent := wd
	if filepath.Base(wd) == "verify" {
		parent = filepath.Dir(wd)
	}
	out := filepath.Join(parent, ".ch02-server")
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
