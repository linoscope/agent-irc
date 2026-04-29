// verify exercises chapter 09: registry-returned name becomes the IRC nick.
//
// Cases:
//
//	1. positive: account 1 (registered as "alice-bot", valid IRC name)
//	   authenticates → 903, then 001 lists the account name "alice-bot"
//	   (NOT the address-derived "0x70997970C51812dc")
//
//	2. negative: account 2 (registered as "bad name", contains a space)
//	   authenticates → 904 ERR_SASLFAIL with "registry name not IRC-valid"
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
	port            = "16675"
	domain          = "agent-irc-sasl-v1"
	registeredKey1  = "59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"
	registeredKey2  = "5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL:", err)
		os.Exit(1)
	}
	fmt.Println("PASS: chapter 09 — registry name becomes IRC nick; invalid names rejected")
}

func run() error {
	if !portReady(port, 5*time.Second) {
		return fmt.Errorf("Ergo not listening on :%s", port)
	}

	k1, _ := crypto.HexToECDSA(registeredKey1)
	a1 := crypto.PubkeyToAddress(k1.PublicKey)
	k2, _ := crypto.HexToECDSA(registeredKey2)
	a2 := crypto.PubkeyToAddress(k2.PublicKey)

	fmt.Println("--- case 1: positive — registry name becomes the IRC nick ---")
	if err := handshake("conn1", a1, k1, expectSuccess, "alice-bot"); err != nil {
		return fmt.Errorf("case 1: %w", err)
	}

	fmt.Println("--- case 2: negative — registry name fails IRC validation ---")
	if err := handshake("conn2", a2, k2, expectFailValidation, ""); err != nil {
		return fmt.Errorf("case 2: %w", err)
	}
	return nil
}

type expectation int

const (
	expectSuccess expectation = iota
	expectFailValidation
)

func handshake(initialNick string, claimed common.Address, signKey *ecdsa.PrivateKey, expect expectation, expectName string) error {
	c, err := net.Dial("tcp", "localhost:"+port)
	if err != nil {
		return err
	}
	defer c.Close()
	rd := bufio.NewReader(c)
	send := func(line string) {
		_, _ = c.Write([]byte(line + "\r\n"))
		fmt.Printf("  %s -> %s\n", initialNick, line)
	}
	readLine := func() (string, error) {
		c.SetReadDeadline(time.Now().Add(3 * time.Second))
		line, err := rd.ReadString('\n')
		if err != nil {
			return "", err
		}
		line = strings.TrimRight(line, "\r\n")
		fmt.Printf("  %s <- %s\n", initialNick, line)
		return line, nil
	}

	send("CAP LS 302")
	send("NICK " + initialNick)
	send("USER " + initialNick + " 0 * :" + initialNick)
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
		return err
	}
	body := []byte(domain + "\nnonce=" + hex.EncodeToString(nonce))
	hash := eip191Hash(body)
	sig, _ := crypto.Sign(hash, signKey)
	send("AUTHENTICATE " + base64.StdEncoding.EncodeToString(sig))

	switch expect {
	case expectSuccess:
		// Confirm 900 RPL_LOGGEDIN names the *registry* name, not the address.
		loggedIn, err := waitFor(readLine, " 900 ", 4*time.Second)
		if err != nil {
			return err
		}
		if !strings.Contains(loggedIn, expectName) {
			return fmt.Errorf("expected 900 to bind account %q, got %s", expectName, loggedIn)
		}
		if strings.Contains(loggedIn, "0x") {
			return fmt.Errorf("900 still contains 0x-prefixed address — registry name not used: %s", loggedIn)
		}
		fmt.Printf("  ✓ 900 bound to registry name %q\n", expectName)
		if _, err := waitFor(readLine, " 903 ", 3*time.Second); err != nil {
			return err
		}
		send("CAP END")
		welcome, err := waitFor(readLine, " 001 ", 4*time.Second)
		if err != nil {
			return err
		}
		if !strings.Contains(welcome, expectName) {
			return fmt.Errorf("001 should address %q, got %s", expectName, welcome)
		}
		fmt.Printf("  ✓ 001 addresses %q\n", expectName)
	case expectFailValidation:
		line, err := waitFor(readLine, " 904 ", 4*time.Second)
		if err != nil {
			return err
		}
		if !strings.Contains(line, "registry name not IRC-valid") {
			return fmt.Errorf("expected 'registry name not IRC-valid', got: %s", line)
		}
		fmt.Println("  ✓ rejected: registry name failed validation")
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
