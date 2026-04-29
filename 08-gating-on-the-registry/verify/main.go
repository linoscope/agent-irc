// verify exercises the on-chain ERC-8004 gate.
//
// 3 cases:
//
//	1. positive: anvil account 1 (registered as "alice-bot" by deploy.sh)
//	   signs a valid challenge → 903 RPL_SASLSUCCESS
//
//	2. negative — not registered: a fresh keypair signs a valid challenge
//	   → 904 ERR_SASLFAIL with message "address not in registry"
//
//	3. negative — signature mismatch: any other key signs while claiming
//	   the registered address → 904 with "signature verification failed"
package main

import (
	"bufio"
	"crypto/ecdsa"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

const (
	port   = "16674"
	domain = "agent-irc-sasl-v1"
	// anvil account 1 — registered as "alice-bot" by deploy.sh.
	registeredKeyHex = "59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL:", err)
		os.Exit(1)
	}
	fmt.Println("PASS: chapter 08 — registry membership gate enforced")
}

func run() error {
	if !portReady(port, 5*time.Second) {
		return fmt.Errorf("Ergo not listening on :%s", port)
	}

	registeredKey, err := crypto.HexToECDSA(registeredKeyHex)
	if err != nil {
		return err
	}
	registeredAddr := crypto.PubkeyToAddress(registeredKey.PublicKey)

	fmt.Println("--- case 1: positive (registered + correct sig) ---")
	if err := runHandshake("alice", registeredAddr, registeredKey, expectSuccess); err != nil {
		return fmt.Errorf("case 1: %w", err)
	}

	fmt.Println("--- case 2: negative (unregistered keypair) ---")
	freshKey, _ := crypto.GenerateKey()
	freshAddr := crypto.PubkeyToAddress(freshKey.PublicKey)
	if err := runHandshake("ghost", freshAddr, freshKey, expectFailNotRegistered); err != nil {
		return fmt.Errorf("case 2: %w", err)
	}

	fmt.Println("--- case 3: negative (registered addr, wrong sig key) ---")
	wrongKey, _ := crypto.GenerateKey()
	if err := runHandshake("forger", registeredAddr, wrongKey, expectFailSignature); err != nil {
		return fmt.Errorf("case 3: %w", err)
	}

	return nil
}

type expectation int

const (
	expectSuccess expectation = iota
	expectFailNotRegistered
	expectFailSignature
)

func runHandshake(nick string, claimed common.Address, signKey *ecdsa.PrivateKey, expect expectation) error {
	c, err := net.Dial("tcp", "localhost:"+port)
	if err != nil {
		return err
	}
	defer c.Close()
	rd := bufio.NewReader(c)
	send := func(line string) {
		_, _ = c.Write([]byte(line + "\r\n"))
		fmt.Printf("  %s -> %s\n", nick, line)
	}
	readLine := func() (string, error) {
		c.SetReadDeadline(time.Now().Add(3 * time.Second))
		line, err := rd.ReadString('\n')
		if err != nil {
			return "", err
		}
		line = strings.TrimRight(line, "\r\n")
		fmt.Printf("  %s <- %s\n", nick, line)
		return line, nil
	}

	send("CAP LS 302")
	send("NICK " + nick)
	send("USER " + nick + " 0 * :" + nick)
	send("CAP REQ :sasl message-tags server-time account-tag")
	if _, err := waitFor(readLine, "ACK", 4*time.Second); err != nil {
		return err
	}
	send("AUTHENTICATE ERC8004")
	if _, err := waitFor(readLine, "AUTHENTICATE +", 3*time.Second); err != nil {
		return err
	}
	send("AUTHENTICATE " + base64.StdEncoding.EncodeToString(claimed.Bytes()))
	chLine, err := waitFor(readLine, "AUTHENTICATE", 3*time.Second)
	if err != nil {
		return err
	}
	idx := strings.LastIndex(chLine, "AUTHENTICATE ")
	chB64 := strings.TrimSpace(chLine[idx+len("AUTHENTICATE "):])
	nonce, err := base64.StdEncoding.DecodeString(chB64)
	if err != nil {
		return fmt.Errorf("bad nonce b64: %w", err)
	}
	body := []byte(domain + "\nnonce=" + hex.EncodeToString(nonce))
	hash := eip191Hash(body)
	sig, err := crypto.Sign(hash, signKey)
	if err != nil {
		return err
	}
	send("AUTHENTICATE " + base64.StdEncoding.EncodeToString(sig))

	switch expect {
	case expectSuccess:
		if _, err := waitFor(readLine, " 903 ", 4*time.Second); err != nil {
			return fmt.Errorf("expected 903: %w", err)
		}
		fmt.Println("  ✓ accepted")
	case expectFailNotRegistered:
		line, err := waitFor(readLine, " 904 ", 4*time.Second)
		if err != nil {
			return err
		}
		if !strings.Contains(line, "not in registry") {
			return fmt.Errorf("expected 'not in registry', got: %s", line)
		}
		fmt.Println("  ✓ rejected: not in registry")
	case expectFailSignature:
		line, err := waitFor(readLine, " 904 ", 4*time.Second)
		if err != nil {
			return err
		}
		if !strings.Contains(line, "signature verification failed") {
			return fmt.Errorf("expected 'signature verification failed', got: %s", line)
		}
		fmt.Println("  ✓ rejected: signature mismatch")
	}
	return nil
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
