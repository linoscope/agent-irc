// verify exercises the canonical ERC-8004 SASL gate in chapter 08b.
//
// Wire shape (chapter-erc8004-canonical fork tag):
//
//	C: AUTHENTICATE ERC8004
//	S: AUTHENTICATE +
//	C: AUTHENTICATE <base64(32-byte uint256 agentId)>   <- claim
//	S: AUTHENTICATE <base64(32-byte nonce)>             <- challenge
//	C: AUTHENTICATE <base64(65-byte EIP-191 sig)>       <- response
//	S: 903 RPL_SASLSUCCESS  | 904 ERR_SASLFAIL
//
// Signed body:
//
//	agent-irc-sasl-v1
//	chain=<chain-id>
//	server=<server-name>
//	agentId=<dec>
//	nonce=<hex>
//
// We exercise 3 cases against a server that has the registry gate enabled:
//
//   1. positive: claim alice-bot's agentId, sign with anvil account 1
//      (the wallet getAgentWallet returns) → 903 RPL_SASLSUCCESS.
//
//   2. wrong-signer-for-claimed-agentId: claim alice-bot's agentId but
//      sign with a different keypair → 904 ERR_SASLFAIL with
//      "signer is not the agent's wallet".
//
//   3. nonexistent agentId: claim a token id that was never minted.
//      ERC-721's ownerOf reverts → getAgentWallet bubbles up → 904
//      ERR_SASLFAIL with "agentId not in registry".
//
// The agentId for case 1 is read from ./.alice-agentid (written by
// deploy.sh). The chapter's server name is "ergo.test" and the local
// anvil chain is 31337; both are flag-overrideable so the same program
// runs against Base mainnet via verify-mainnet.sh.
package main

import (
	"bufio"
	"crypto/ecdsa"
	"encoding/base64"
	"encoding/hex"
	"flag"
	"fmt"
	"math/big"
	"net"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

const domain = "agent-irc-sasl-v1"

var (
	flagPort       = flag.String("port", envOr("PORT", "16674"), "IRC port the fork is listening on")
	flagAgentID    = flag.String("agent-id", envOr("AGENT_ID", ""), "agentId for case 1 (overrides .alice-agentid)")
	flagKey        = flag.String("key", envOr("AGENT_PRIVATE_KEY", ""), "hex private key for case 1 (overrides default anvil account 1)")
	flagChainID    = flag.Uint64("chain-id", envOrU64("CHAIN_ID", 31337), "chain id baked into the SASL body")
	flagServerName = flag.String("server-name", envOr("SERVER_NAME", "ergo.test"), "server name baked into the SASL body")
	flagMode       = flag.String("mode", envOr("MODE", "anvil"), "anvil|mainnet — selects test cases (mainnet skips case 3 to avoid colliding with a real mint)")
)

// anvil account 1 — the wallet alice-bot's registration was minted from.
const anvilAcct1Key = "59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"

func main() {
	flag.Parse()
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL:", err)
		os.Exit(1)
	}
	if *flagMode == "mainnet" {
		fmt.Println("PASS: chapter 08b — canonical ERC-8004 gate verified against mainnet registry")
	} else {
		fmt.Println("PASS: chapter 08b — canonical ERC-8004 gate enforced (agentId + getAgentWallet)")
	}
}

func run() error {
	if !portReady(*flagPort, 5*time.Second) {
		return fmt.Errorf("ergo not listening on :%s", *flagPort)
	}

	agentID, err := resolveAgentID()
	if err != nil {
		return fmt.Errorf("resolve agentId: %w", err)
	}
	signKey, err := resolveSignKey()
	if err != nil {
		return fmt.Errorf("resolve signing key: %w", err)
	}
	expectedAddr := crypto.PubkeyToAddress(signKey.PublicKey)
	fmt.Printf("  expected wallet: %s\n", expectedAddr.Hex())
	fmt.Printf("  agentId:         %s\n", agentID.String())
	fmt.Printf("  chain=%d  server=%s\n", *flagChainID, *flagServerName)

	fmt.Println("--- case 1: positive (claimed agentId + correct wallet signature) ---")
	if err := runHandshake("alice", agentID, signKey, expectSuccess); err != nil {
		return fmt.Errorf("case 1: %w", err)
	}

	fmt.Println("--- case 2: negative (claimed agentId, signed by wrong key) ---")
	wrongKey, _ := crypto.GenerateKey()
	if err := runHandshake("forger", agentID, wrongKey, expectFailWrongSigner); err != nil {
		return fmt.Errorf("case 2: %w", err)
	}

	if *flagMode == "mainnet" {
		// Skipped on mainnet: picking a "guaranteed-nonexistent" agentId
		// requires knowing nextAgentId, and even huge numbers risk colliding
		// with a future legitimate mint. The local-anvil run already covers
		// the revert path against the same contract code.
		fmt.Println("--- case 3 skipped on mainnet (would need a guaranteed-unminted token id) ---")
		return nil
	}

	fmt.Println("--- case 3: negative (nonexistent agentId — ownerOf reverts) ---")
	// 4_000_000_000_000 is far above anvil's `nextAgentId` after one mint
	// (which is now 2). The ERC-721 ownerOf call inside getAgentWallet will
	// revert, the fork bubbles it up as 904.
	bogus := new(big.Int).SetUint64(4_000_000_000_000)
	if err := runHandshake("ghost", bogus, signKey, expectFailNotInRegistry); err != nil {
		return fmt.Errorf("case 3: %w", err)
	}
	return nil
}

// resolveAgentID returns the agentId we're claiming. Order of precedence:
// --agent-id / $AGENT_ID > ./.alice-agentid.
func resolveAgentID() (*big.Int, error) {
	if *flagAgentID != "" {
		bi, ok := new(big.Int).SetString(strings.TrimSpace(*flagAgentID), 10)
		if !ok {
			return nil, fmt.Errorf("--agent-id %q is not a decimal integer", *flagAgentID)
		}
		return bi, nil
	}
	b, err := os.ReadFile(".alice-agentid")
	if err != nil {
		return nil, fmt.Errorf("read .alice-agentid (run ./deploy.sh first): %w", err)
	}
	s := strings.TrimSpace(string(b))
	bi, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return nil, fmt.Errorf(".alice-agentid contents %q is not a decimal integer", s)
	}
	return bi, nil
}

// resolveSignKey loads the private key for the registered agent.
func resolveSignKey() (*ecdsa.PrivateKey, error) {
	hexKey := strings.TrimSpace(strings.TrimPrefix(*flagKey, "0x"))
	if hexKey == "" {
		hexKey = anvilAcct1Key
	}
	return crypto.HexToECDSA(hexKey)
}

type expectation int

const (
	expectSuccess expectation = iota
	expectFailWrongSigner
	expectFailNotInRegistry
)

func runHandshake(nick string, agentID *big.Int, signKey *ecdsa.PrivateKey, expect expectation) error {
	c, err := net.Dial("tcp", "localhost:"+*flagPort)
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

	// Step 1: claim. Pack agentId into a 32-byte big-endian uint256.
	claim := make([]byte, 32)
	agentID.FillBytes(claim)
	send("AUTHENTICATE " + base64.StdEncoding.EncodeToString(claim))

	// On case 3 (bogus id) the server fails right here — no nonce comes
	// back, just a 904. Branch the waitFor pattern so both outcomes are
	// observable from the same line of code.
	line, err := readUntilNonceOrFail(readLine, 6*time.Second)
	if err != nil {
		return err
	}
	if strings.Contains(line, " 904 ") {
		// step-1 short-circuit failure (agentId not in registry, etc.).
		if expect != expectFailNotInRegistry {
			return fmt.Errorf("got step-1 904 but expected step-3 outcome: %s", line)
		}
		if !strings.Contains(line, "not in registry") && !strings.Contains(line, "no wallet") {
			return fmt.Errorf("expected 'not in registry' / 'no wallet', got: %s", line)
		}
		fmt.Println("  ✓ rejected: agentId not in registry")
		return nil
	}

	// Step 2: server's nonce.
	idx := strings.LastIndex(line, "AUTHENTICATE ")
	chB64 := strings.TrimSpace(line[idx+len("AUTHENTICATE "):])
	nonce, err := base64.StdEncoding.DecodeString(chB64)
	if err != nil {
		return fmt.Errorf("bad nonce b64: %w", err)
	}

	// Step 3: sign body = agent-irc-sasl-v1\nchain=<id>\nserver=<name>\nagentId=<dec>\nnonce=<hex>.
	body := []byte(fmt.Sprintf("%s\nchain=%d\nserver=%s\nagentId=%s\nnonce=%s",
		domain, *flagChainID, *flagServerName, agentID.String(), hex.EncodeToString(nonce)))
	sig, err := crypto.Sign(eip191Hash(body), signKey)
	if err != nil {
		return err
	}
	// go-ethereum's crypto.Sign returns v ∈ {0,1}; some verifiers normalize
	// to {27,28}. Either is accepted by agentirc.RecoverAddress.
	send("AUTHENTICATE " + base64.StdEncoding.EncodeToString(sig))

	switch expect {
	case expectSuccess:
		if _, err := waitFor(readLine, " 903 ", 4*time.Second); err != nil {
			return fmt.Errorf("expected 903: %w", err)
		}
		fmt.Println("  ✓ accepted")
	case expectFailWrongSigner:
		line, err := waitFor(readLine, " 904 ", 4*time.Second)
		if err != nil {
			return err
		}
		if !strings.Contains(line, "not the agent's wallet") &&
			!strings.Contains(line, "signature verification failed") {
			return fmt.Errorf("expected 'not the agent's wallet' / 'signature verification failed', got: %s", line)
		}
		fmt.Println("  ✓ rejected: wrong signer for claimed agentId")
	case expectFailNotInRegistry:
		// We've already returned above for the step-1 path, so getting
		// here means a nonce came back even for a bogus id (shouldn't
		// happen against the canonical registry).
		line, err := waitFor(readLine, " 904 ", 4*time.Second)
		if err != nil {
			return err
		}
		return fmt.Errorf("expected step-1 904, got step-3 failure: %s", line)
	}
	return nil
}

// readUntilNonceOrFail reads server lines until either:
//
//   - an `AUTHENTICATE <b64-nonce>` (step-2 challenge) line arrives, OR
//   - a 904 ERR_SASLFAIL line arrives (step-1 short-circuit).
func readUntilNonceOrFail(read func() (string, error), dur time.Duration) (string, error) {
	deadline := time.Now().Add(dur)
	for time.Now().Before(deadline) {
		line, err := read()
		if err != nil {
			if isTimeout(err) {
				continue
			}
			return "", err
		}
		if strings.Contains(line, " 904 ") {
			return line, nil
		}
		// Step-2 challenge: an unprefixed or prefixed AUTHENTICATE with a
		// base64 payload (not the bare AUTHENTICATE + sentinel we already
		// consumed).
		if strings.Contains(line, "AUTHENTICATE ") && !strings.HasSuffix(line, "AUTHENTICATE +") {
			return line, nil
		}
	}
	return "", fmt.Errorf("timeout waiting for step-2 nonce or step-1 904")
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

func envOr(name, fallback string) string {
	if v, ok := os.LookupEnv(name); ok && v != "" {
		return v
	}
	return fallback
}

func envOrU64(name string, fallback uint64) uint64 {
	v, ok := os.LookupEnv(name)
	if !ok || v == "" {
		return fallback
	}
	n, ok2 := new(big.Int).SetString(strings.TrimSpace(v), 10)
	if !ok2 {
		return fallback
	}
	return n.Uint64()
}

// silence unused import linter when common is dropped from refactors.
var _ = common.Address{}
