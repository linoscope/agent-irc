// verify exercises chapter 09: the IRC nick is the `.name` field of the
// off-chain JSON pointed to by tokenURI(agentId).
//
// Three cases against a local anvil + ERC-8004 registry deployed by
// ./deploy.sh:
//
//	1. positive — agent registered with agentURI
//	      `data:application/json,{"name":"alice-bot"}`.
//	   SASL succeeds; the server's `900 RPL_LOGGEDIN` and `001 RPL_WELCOME`
//	   both address the session as `alice-bot`, NOT the truncated
//	   wallet-derived account name.
//
//	2. negative — agent registered with a `.name` containing spaces:
//	      `data:application/json,{"name":"bad name with spaces"}`.
//	   SASL fails: `904 ERR_SASLFAIL` with the message
//	   `agent JSON name not IRC-valid`.
//
//	3. negative — agent registered with a `.name` longer than the 32-byte
//	   `MaxIRCNameLen` ceiling. SASL fails: `904 ERR_SASLFAIL`, same reason
//	   (validator rejects on length before charset).
//
// Wire shape (chapter 08+ canonical, see agentirc/sasl.go):
//
//	step 1: AUTHENTICATE <base64(32-byte big-endian agentId)>
//	step 2: AUTHENTICATE <base64(32-byte nonce)>
//	step 3: AUTHENTICATE <base64(65-byte EIP-191 sig over
//	         "agent-irc-sasl-v1\nchain=<id>\nserver=<name>\nagentId=<dec>\nnonce=<hex>")>
//
// The chain + server + agentId binding in step 3's body is what chapter
// 10 uses for replay protection; chapter 09 inherits it unchanged.
package main

import (
	"bufio"
	"crypto/ecdsa"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
	"net"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

const (
	port       = "16675"
	domain     = "agent-irc-sasl-v1"
	chainID    = uint64(31337)
	serverName = "ergo.test"
	// anvil deterministic keys 1 / 2 / 3 — keep in sync with deploy.sh.
	aliceKey = "59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"
	badKey   = "5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a"
	longKey  = "7c852118294e51e653712a81e05800f419141751be58f605c371e15141b007a6"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL:", err)
		os.Exit(1)
	}
	fmt.Println("PASS: chapter 09 — agent-JSON .name becomes the IRC nick; invalid names rejected")
}

func run() error {
	if !portReady(port, 5*time.Second) {
		return fmt.Errorf("Ergo not listening on :%s", port)
	}

	aliceAgentID, err := readAgentID(".alice-agentid")
	if err != nil {
		return fmt.Errorf("read .alice-agentid: %w", err)
	}
	badAgentID, err := readAgentID(".bad-agentid")
	if err != nil {
		return fmt.Errorf("read .bad-agentid: %w", err)
	}
	longAgentID, err := readAgentID(".long-agentid")
	if err != nil {
		return fmt.Errorf("read .long-agentid: %w", err)
	}
	k1, _ := crypto.HexToECDSA(aliceKey)
	k2, _ := crypto.HexToECDSA(badKey)
	k3, _ := crypto.HexToECDSA(longKey)

	fmt.Println("--- case 1: positive — JSON .name 'alice-bot' becomes the IRC nick ---")
	if err := handshake("conn1", aliceAgentID, k1, expectSuccess, "alice-bot"); err != nil {
		return fmt.Errorf("case 1: %w", err)
	}

	fmt.Println("--- case 2: negative — JSON .name 'bad name with spaces' fails charset ---")
	if err := handshake("conn2", badAgentID, k2, expectFailValidation, ""); err != nil {
		return fmt.Errorf("case 2: %w", err)
	}

	fmt.Println("--- case 3: negative — JSON .name longer than 32 bytes fails length ---")
	if err := handshake("conn3", longAgentID, k3, expectFailValidation, ""); err != nil {
		return fmt.Errorf("case 3: %w", err)
	}
	return nil
}

type expectation int

const (
	expectSuccess expectation = iota
	expectFailValidation
)

func handshake(initialNick string, agentID *big.Int, signKey *ecdsa.PrivateKey, expect expectation, expectName string) error {
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

	// Step 1: claim — send 32-byte big-endian agentId.
	agentIDBytes := common.BigToHash(agentID).Bytes()
	send("AUTHENTICATE " + base64.StdEncoding.EncodeToString(agentIDBytes))

	// Step 2: server replies with AUTHENTICATE <base64(nonce)>.
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

	// Step 3: sign body bound to (chain, server, agentId, nonce).
	body := []byte(fmt.Sprintf("%s\nchain=%d\nserver=%s\nagentId=%s\nnonce=%s",
		domain, chainID, serverName, agentID.String(), hex.EncodeToString(nonce)))
	hash := eip191Hash(body)
	sig, _ := crypto.Sign(hash, signKey)
	send("AUTHENTICATE " + base64.StdEncoding.EncodeToString(sig))

	switch expect {
	case expectSuccess:
		// 900 RPL_LOGGEDIN must name the JSON-derived name, not the address.
		loggedIn, err := waitFor(readLine, " 900 ", 4*time.Second)
		if err != nil {
			return err
		}
		if !strings.Contains(loggedIn, expectName) {
			return fmt.Errorf("expected 900 to bind account %q, got %s", expectName, loggedIn)
		}
		if strings.Contains(loggedIn, "0x") {
			return fmt.Errorf("900 still contains 0x-prefixed address — JSON name not used: %s", loggedIn)
		}
		fmt.Printf("  ✓ 900 bound to JSON .name %q\n", expectName)
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
		fmt.Printf("  ✓ 001 addresses %q (server forced NICK to JSON .name)\n", expectName)
	case expectFailValidation:
		line, err := waitFor(readLine, " 904 ", 4*time.Second)
		if err != nil {
			return err
		}
		if !strings.Contains(line, "agent JSON name not IRC-valid") {
			return fmt.Errorf("expected 'agent JSON name not IRC-valid', got: %s", line)
		}
		fmt.Println("  ✓ rejected: JSON .name failed ValidateIRCName")
	}
	return nil
}

func eip191Hash(body []byte) []byte {
	prefix := fmt.Sprintf("\x19Ethereum Signed Message:\n%d", len(body))
	return crypto.Keccak256([]byte(prefix), body)
}

func readAgentID(path string) (*big.Int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return nil, fmt.Errorf("%s is empty", path)
	}
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return nil, fmt.Errorf("%s: not a decimal integer (%q)", path, s)
	}
	return n, nil
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
