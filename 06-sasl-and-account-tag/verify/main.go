// verify exercises chapter 06: SASL authentication and account-tag.
//
// Phase 1: Alice connects and registers an account via the IRCv3
//          draft/account-registration `REGISTER` flow. Ergo auto-authenticates
//          on successful registration; we observe that 900 RPL_LOGGEDIN.
//          Alice disconnects.
// Phase 2: Alice reconnects from scratch and authenticates via SASL PLAIN
//          (the standard mechanism). We assert 903 SASL succeeded.
// Phase 3: Bob connects without authentication.
// Phase 4: Both join #room. Alice sends a PRIVMSG.
//          Bob's view of the message MUST include @account=Alice.
//          Bob also sends a PRIVMSG; Alice's view of THAT message must NOT
//          include an account tag (Bob is unauthenticated).
package main

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

const port = "16672"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL:", err)
		os.Exit(1)
	}
	fmt.Println("PASS: SASL PLAIN works; account-tag distinguishes authenticated from anonymous")
}

func run() error {
	if !portReady(port, 5*time.Second) {
		return fmt.Errorf("Ergo not listening on :%s — run ./start-ergo.sh first", port)
	}

	fmt.Println("--- phase 1: register Alice via REGISTER (auto-auths) ---")
	if err := phase1Register(); err != nil {
		return err
	}

	fmt.Println("--- phase 2: reconnect Alice with SASL PLAIN ---")
	alice, err := dialAndAuth("Alice", "hunter2")
	if err != nil {
		return err
	}
	defer alice.close()

	fmt.Println("--- phase 3: bob connects unauthenticated ---")
	bob, err := dialAndCAPs("bob", false)
	if err != nil {
		return err
	}
	defer bob.close()

	fmt.Println("--- phase 4: PRIVMSG with and without account-tag ---")

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

	// Alice → #room: bob should receive with @account=Alice tag.
	if err := alice.send("PRIVMSG #room :hello from authenticated Alice"); err != nil {
		return err
	}
	line, err := bob.expect("PRIVMSG #room :hello from authenticated Alice")
	if err != nil {
		return err
	}
	if !strings.Contains(line, "account=Alice") {
		return fmt.Errorf("authenticated PRIVMSG lacks account=Alice tag: %s", line)
	}
	fmt.Println("  ✓ Alice's message carried account=Alice")

	// Bob → #room: alice should receive WITHOUT an account tag.
	if err := bob.send("PRIVMSG #room :hello from anonymous bob"); err != nil {
		return err
	}
	line, err = alice.expect("PRIVMSG #room :hello from anonymous bob")
	if err != nil {
		return err
	}
	if strings.Contains(line, "account=") &&
		!strings.Contains(line, "account=;") &&
		!strings.Contains(line, "account= ") {
		// Some servers emit `account=` with empty value for unauthenticated users.
		// What we want to NOT see is `account=somename`.
		return fmt.Errorf("unauthenticated PRIVMSG carries an account tag with a value: %s", line)
	}
	fmt.Println("  ✓ Bob's message had no account=value tag")
	return nil
}

// phase1Register: register Alice's account using the IRCv3
// draft/account-registration `REGISTER` command. Ergo auto-logs the user in.
func phase1Register() error {
	c, err := net.Dial("tcp", "localhost:"+port)
	if err != nil {
		return err
	}
	defer c.Close()
	rd := bufio.NewReader(c)
	send := func(s string) { _, _ = c.Write([]byte(s + "\r\n")) }

	send("CAP LS 302")
	send("NICK Alice")
	send("USER Alice 0 * :Alice")
	send("CAP REQ :sasl message-tags server-time account-tag draft/account-registration")
	send("REGISTER * * hunter2")
	deadline := time.Now().Add(5 * time.Second)
	registered := false
	for time.Now().Before(deadline) {
		c.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		line, err := rd.ReadString('\n')
		if err != nil {
			if isTimeout(err) {
				continue
			}
			return err
		}
		line = strings.TrimRight(line, "\r\n")
		fmt.Printf("  reg <- %s\n", line)
		if strings.Contains(line, "REGISTER SUCCESS") || strings.Contains(line, " 900 ") {
			registered = true
		}
		if strings.Contains(line, "REGISTER FAIL") {
			return fmt.Errorf("registration failed: %s", line)
		}
		if registered && strings.Contains(line, " 001 ") {
			send("QUIT :registered")
			return nil
		}
	}
	if !registered {
		return fmt.Errorf("did not see REGISTER SUCCESS")
	}
	send("CAP END")
	return nil
}

// ---------- per-client helpers ----------

type client struct {
	c    net.Conn
	rd   *bufio.Reader
	nick string
}

// dialAndCAPs opens a connection, requests message-tags+server-time+account-tag,
// and (if auth is false) completes registration without SASL.
func dialAndCAPs(nick string, auth bool) (*client, error) {
	c, err := net.Dial("tcp", "localhost:"+port)
	if err != nil {
		return nil, err
	}
	cl := &client{c: c, rd: bufio.NewReader(c), nick: nick}
	cl.write("CAP LS 302")
	cl.write("NICK " + nick)
	cl.write("USER " + nick + " 0 * :" + nick)
	caps := "message-tags server-time account-tag echo-message"
	if auth {
		caps = "sasl " + caps
	}
	cl.write("CAP REQ :" + caps)
	if _, err := cl.expect("ACK"); err != nil {
		return nil, err
	}
	if !auth {
		cl.write("CAP END")
		if _, err := cl.expect(" 001 "); err != nil {
			return nil, err
		}
	}
	return cl, nil
}

func dialAndAuth(account, password string) (*client, error) {
	cl, err := dialAndCAPs(account, true)
	if err != nil {
		return nil, err
	}
	cl.write("AUTHENTICATE PLAIN")
	if _, err := cl.expect("AUTHENTICATE +"); err != nil {
		return nil, err
	}
	cred := base64.StdEncoding.EncodeToString([]byte("\x00" + account + "\x00" + password))
	cl.write("AUTHENTICATE " + cred)
	if _, err := cl.expect(" 903 "); err != nil {
		return nil, err
	}
	cl.write("CAP END")
	if _, err := cl.expect(" 001 "); err != nil {
		return nil, err
	}
	return cl, nil
}

func (c *client) write(s string) {
	_, _ = c.c.Write([]byte(s + "\r\n"))
	fmt.Printf("  %s -> %s\n", c.nick, s)
}

func (c *client) send(s string) error {
	_, err := c.c.Write([]byte(s + "\r\n"))
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
