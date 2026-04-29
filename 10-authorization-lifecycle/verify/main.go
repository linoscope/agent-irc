// verify exercises chapter 10:
//
//   1. cross-chain replay protection: the SASL body now binds to chainID +
//      server name. A signature for chain X must not verify against chain Y.
//      This is unit-tested in irc/agentirc/sasl_test.go (already passing);
//      this verify program also exercises it end-to-end.
//
//   2. mutation watcher: alice authenticates, the test then renames her
//      registry entry on-chain, and within ~2s the server emits ERROR /
//      closes the connection. We assert the connection drops.
package main

import (
	"bufio"
	"crypto/ecdsa"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

const (
	port            = "16676"
	domain          = "agent-irc-sasl-v1"
	chainID         = uint64(31337)
	serverName      = "ergo.test"
	registeredKey   = "59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"
	registryAddrEnv = ".registry-address"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL:", err)
		os.Exit(1)
	}
	fmt.Println("PASS: chapter 10 — replay protection + mutation watcher KILLs renamed agent")
}

func run() error {
	if !portReady(port, 5*time.Second) {
		return fmt.Errorf("Ergo not listening on :%s", port)
	}
	key, _ := crypto.HexToECDSA(registeredKey)
	addr := crypto.PubkeyToAddress(key.PublicKey)

	fmt.Println("--- case 1: cross-chain replay rejection ---")
	if err := wrongChainSig(addr, key); err != nil {
		return fmt.Errorf("case 1: %w", err)
	}

	fmt.Println("--- case 2: correct-chain signature succeeds ---")
	c, err := authenticate(addr, key)
	if err != nil {
		return fmt.Errorf("case 2: %w", err)
	}
	defer c.Close()

	fmt.Println("--- case 3: mutation watcher KILLs after on-chain rename ---")
	if err := triggerRename(); err != nil {
		return err
	}
	// We expect the connection to be closed by the server within ~3 watcher
	// cycles. Read until EOF or timeout.
	deadline := time.Now().Add(8 * time.Second)
	rd := bufio.NewReader(c)
	closed := false
	for time.Now().Before(deadline) {
		c.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		line, err := rd.ReadString('\n')
		if err != nil {
			if isTimeout(err) {
				continue
			}
			fmt.Printf("  alice-bot got disconnect: %v\n", err)
			closed = true
			break
		}
		fmt.Printf("  alice-bot <- %s", line)
		if strings.Contains(line, "ERROR") || strings.Contains(line, "QUIT") {
			fmt.Println("  ✓ saw server-initiated termination message")
		}
	}
	if !closed {
		return fmt.Errorf("connection was not closed within 8s of on-chain rename")
	}
	fmt.Println("  ✓ connection terminated by mutation watcher")
	return nil
}

// wrongChainSig signs a body bound to chain 8453 (Base mainnet) and presents
// it to a server expecting chain 31337 (anvil). Should hit 904.
func wrongChainSig(claimed common.Address, signKey *ecdsa.PrivateKey) error {
	c, err := net.Dial("tcp", "localhost:"+port)
	if err != nil {
		return err
	}
	defer c.Close()
	rd := bufio.NewReader(c)
	send := func(line string) {
		_, _ = c.Write([]byte(line + "\r\n"))
	}
	readLine := func() (string, error) {
		c.SetReadDeadline(time.Now().Add(2 * time.Second))
		line, err := rd.ReadString('\n')
		if err != nil {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	send("CAP LS 302")
	send("NICK x")
	send("USER x 0 * :x")
	send("CAP REQ :sasl message-tags")
	if _, err := waitFor(readLine, "ACK", 3*time.Second); err != nil {
		return err
	}
	send("AUTHENTICATE ERC8004")
	if _, err := waitFor(readLine, "AUTHENTICATE +", 2*time.Second); err != nil {
		return err
	}
	send("AUTHENTICATE " + base64.StdEncoding.EncodeToString(claimed.Bytes()))
	chLine, err := waitFor(readLine, "AUTHENTICATE", 2*time.Second)
	if err != nil {
		return err
	}
	chB64 := strings.TrimSpace(chLine[strings.LastIndex(chLine, "AUTHENTICATE ")+len("AUTHENTICATE "):])
	nonce, _ := base64.StdEncoding.DecodeString(chB64)
	// Sign for the WRONG chain.
	body := []byte(fmt.Sprintf("%s\nchain=8453\nserver=%s\nnonce=%s",
		domain, serverName, hex.EncodeToString(nonce)))
	hash := eip191Hash(body)
	sig, _ := crypto.Sign(hash, signKey)
	send("AUTHENTICATE " + base64.StdEncoding.EncodeToString(sig))
	line, err := waitFor(readLine, " 904 ", 3*time.Second)
	if err != nil {
		return fmt.Errorf("expected 904: %w", err)
	}
	if !strings.Contains(line, "signature verification failed") {
		return fmt.Errorf("expected sig failure, got: %s", line)
	}
	fmt.Println("  ✓ wrong-chain signature rejected")
	return nil
}

// authenticate runs the full success path with the correct chain binding.
// Returns a still-connected socket so case 3 can observe the KILL.
func authenticate(claimed common.Address, signKey *ecdsa.PrivateKey) (net.Conn, error) {
	c, err := net.Dial("tcp", "localhost:"+port)
	if err != nil {
		return nil, err
	}
	rd := bufio.NewReader(c)
	send := func(line string) { _, _ = c.Write([]byte(line + "\r\n")) }
	readLine := func() (string, error) {
		c.SetReadDeadline(time.Now().Add(3 * time.Second))
		line, err := rd.ReadString('\n')
		if err != nil {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	send("CAP LS 302")
	send("NICK alicebot-conn")
	send("USER alicebot-conn 0 * :alicebot")
	send("CAP REQ :sasl message-tags account-tag")
	if _, err := waitFor(readLine, "ACK", 4*time.Second); err != nil {
		c.Close()
		return nil, err
	}
	send("AUTHENTICATE ERC8004")
	if _, err := waitFor(readLine, "AUTHENTICATE +", 3*time.Second); err != nil {
		c.Close()
		return nil, err
	}
	send("AUTHENTICATE " + base64.StdEncoding.EncodeToString(claimed.Bytes()))
	chLine, err := waitFor(readLine, "AUTHENTICATE", 3*time.Second)
	if err != nil {
		c.Close()
		return nil, err
	}
	chB64 := strings.TrimSpace(chLine[strings.LastIndex(chLine, "AUTHENTICATE ")+len("AUTHENTICATE "):])
	nonce, _ := base64.StdEncoding.DecodeString(chB64)
	body := []byte(fmt.Sprintf("%s\nchain=%d\nserver=%s\nnonce=%s",
		domain, chainID, serverName, hex.EncodeToString(nonce)))
	sig, _ := crypto.Sign(eip191Hash(body), signKey)
	send("AUTHENTICATE " + base64.StdEncoding.EncodeToString(sig))
	if _, err := waitFor(readLine, " 903 ", 3*time.Second); err != nil {
		c.Close()
		return nil, err
	}
	send("CAP END")
	if _, err := waitFor(readLine, " 001 ", 3*time.Second); err != nil {
		c.Close()
		return nil, err
	}
	fmt.Println("  ✓ alice-bot authenticated and welcomed")
	return c, nil
}

// triggerRename calls setName on the registry, mutating alice-bot → alice2.
func triggerRename() error {
	addrBytes, err := os.ReadFile(registryAddrEnv)
	if err != nil {
		return fmt.Errorf("read .registry-address: %w", err)
	}
	registry := strings.TrimSpace(string(addrBytes))
	cmd := exec.Command("cast", "send",
		"--rpc-url", "http://localhost:8545",
		"--private-key", "0x"+registeredKey,
		registry, "setName(string)", "alice2",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	fmt.Println("  >> sending setName(\"alice2\") on-chain")
	return cmd.Run()
}

func eip191Hash(body []byte) []byte {
	prefix := fmt.Sprintf("\x19Ethereum Signed Message:\n%d", len(body))
	return crypto.Keccak256([]byte(prefix), body)
}

func waitFor(read func() (string, error), substr string, dur time.Duration) (string, error) {
	deadline := time.Now().Add(dur)
	for time.Now().Before(deadline) {
		line, err := read()
		if err != nil {
			if isTimeout(err) {
				continue
			}
			return "", err
		}
		if strings.Contains(line, substr) {
			return line, nil
		}
	}
	return "", fmt.Errorf("timeout waiting for %q", substr)
}

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
