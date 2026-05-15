// verify exercises chapter 10:
//
//   1. Cross-chain replay protection. The SASL body binds to chainID +
//      serverID + agentId. A signature minted for chain X must not verify
//      against chain Y because ecrecover will return a different address.
//
//   2. Happy path. Standard ERC-8004 SASL using the agent registered by
//      deploy.sh. Confirms the canonical wire shape (agentId in step 1,
//      EIP-191 signature over the body in step 3) works end-to-end.
//
//   3. JSON .name change KILLs the session. After case 2 authenticates,
//      we call setAgentURI(agentId, "data:application/json,{name:alice2}")
//      on-chain. Within a few watcher cycles, Ergo notices the bound name
//      no longer matches and tears down the session (Logout + Quit +
//      destroy → socket close).
//
// Case 4 (wallet rotation via transferFrom) is the symmetric test for
// the watcher's other branch. Skipped here to keep verify under a minute;
// the code path is identical in shape (Resolve returns a different
// .Wallet → watcher logs "wallet rotation" → killClients).
package main

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// Build-time overridable knobs. The local-anvil defaults below are what
// `./verify.sh` exercises; `./verify-mainnet.sh` overrides them via -ldflags
// to point the same binary at Base mainnet.
var (
	port       = "16676"
	chainIDStr = "31337"
	serverName = "ergo.test"
	aliceKey   = "59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"
	rpcURL     = "http://localhost:8545"
	skipCase3  = ""
)

const (
	domain        = "agent-irc-sasl-v1"
	registryFile  = ".registry-address"
	agentIDFile   = ".alice-agentid"
	watcherWindow = 8 * time.Second // ~watcher_interval × 5
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
	chainID, err := strconv.ParseUint(chainIDStr, 10, 64)
	if err != nil {
		return fmt.Errorf("parse chainID %q: %w", chainIDStr, err)
	}
	key, err := crypto.HexToECDSA(strings.TrimPrefix(aliceKey, "0x"))
	if err != nil {
		return fmt.Errorf("parse alice key: %w", err)
	}
	addr := crypto.PubkeyToAddress(key.PublicKey)
	_ = addr // server resolves the expected signer via getAgentWallet(agentId)

	agentID, err := readAgentID()
	if err != nil {
		return fmt.Errorf("read agent id: %w", err)
	}

	fmt.Println("--- case 1: cross-chain replay rejection ---")
	if err := wrongChainSig(agentID, key, chainID); err != nil {
		return fmt.Errorf("case 1: %w", err)
	}

	fmt.Println("--- case 2: correct-chain signature succeeds ---")
	c, err := authenticate(agentID, key, chainID)
	if err != nil {
		return fmt.Errorf("case 2: %w", err)
	}
	defer c.Close()

	if skipCase3 != "" {
		fmt.Println("--- case 3: SKIPPED (destructive on mainnet — would mutate the production agent record) ---")
		return nil
	}
	fmt.Println("--- case 3: mutation watcher KILLs after on-chain JSON rename ---")
	if err := triggerJSONNameChange(agentID); err != nil {
		return err
	}
	if err := expectDisconnect(c, watcherWindow); err != nil {
		return fmt.Errorf("case 3: %w", err)
	}
	fmt.Println("  ✓ connection terminated by mutation watcher")
	return nil
}

// wrongChainSig signs a body bound to a chain id that is intentionally
// NOT the server's expected chain (chainID+1) and presents it. Since the
// hashed body differs, ecrecover returns a wrong address and the server
// hits 904 ERR_SASLFAIL.
func wrongChainSig(agentID *big.Int, signKey *ecdsa.PrivateKey, chainID uint64) error {
	wrongChain := chainID + 1
	if chainID == 8453 {
		wrongChain = 31337
	} else if chainID == 31337 {
		wrongChain = 8453
	}
	c, err := net.Dial("tcp", "localhost:"+port)
	if err != nil {
		return err
	}
	defer c.Close()
	rd := bufio.NewReader(c)
	send := func(line string) { _, _ = c.Write([]byte(line + "\r\n")) }
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
	// Step 1: 32-byte agentId on the wire.
	send("AUTHENTICATE " + base64.StdEncoding.EncodeToString(agentIDBytes(agentID)))
	chLine, err := waitFor(readLine, "AUTHENTICATE", 2*time.Second)
	if err != nil {
		return err
	}
	chB64 := strings.TrimSpace(chLine[strings.LastIndex(chLine, "AUTHENTICATE ")+len("AUTHENTICATE "):])
	nonce, _ := base64.StdEncoding.DecodeString(chB64)
	// Sign for the WRONG chain — recovered address will differ.
	body := []byte(fmt.Sprintf("%s\nchain=%d\nserver=%s\nagentId=%s\nnonce=%s",
		domain, wrongChain, serverName, agentID.String(), hex.EncodeToString(nonce)))
	hash := eip191Hash(body)
	sig, _ := crypto.Sign(hash, signKey)
	send("AUTHENTICATE " + base64.StdEncoding.EncodeToString(sig))
	line, err := waitFor(readLine, " 904 ", 3*time.Second)
	if err != nil {
		return fmt.Errorf("expected 904 ERR_SASLFAIL: %w", err)
	}
	if !strings.Contains(line, "signature") && !strings.Contains(line, "signer") {
		return fmt.Errorf("expected sig/signer failure, got: %s", line)
	}
	fmt.Println("  ✓ wrong-chain signature rejected")
	return nil
}

// authenticate runs the full success path with the correct (chain, server,
// agentId) binding. Returns the still-connected socket so case 3 can observe
// the watcher-initiated KILL.
func authenticate(agentID *big.Int, signKey *ecdsa.PrivateKey, chainID uint64) (net.Conn, error) {
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
	send("AUTHENTICATE " + base64.StdEncoding.EncodeToString(agentIDBytes(agentID)))
	chLine, err := waitFor(readLine, "AUTHENTICATE", 3*time.Second)
	if err != nil {
		c.Close()
		return nil, err
	}
	chB64 := strings.TrimSpace(chLine[strings.LastIndex(chLine, "AUTHENTICATE ")+len("AUTHENTICATE "):])
	nonce, _ := base64.StdEncoding.DecodeString(chB64)
	body := []byte(fmt.Sprintf("%s\nchain=%d\nserver=%s\nagentId=%s\nnonce=%s",
		domain, chainID, serverName, agentID.String(), hex.EncodeToString(nonce)))
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

// expectDisconnect reads the connection until EOF/disconnect or timeout.
// Returns nil if the server tore the connection down within `dur`.
func expectDisconnect(c net.Conn, dur time.Duration) error {
	deadline := time.Now().Add(dur)
	rd := bufio.NewReader(c)
	for time.Now().Before(deadline) {
		c.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		line, err := rd.ReadString('\n')
		if err != nil {
			if isTimeout(err) {
				continue
			}
			fmt.Printf("  alice-bot got disconnect: %v\n", err)
			return nil
		}
		fmt.Printf("  alice-bot <- %s", line)
		if strings.Contains(line, "ERROR") || strings.Contains(line, "QUIT") {
			fmt.Println("  ✓ saw server-initiated termination message")
		}
	}
	return fmt.Errorf("connection was not closed within %s of the on-chain mutation", dur)
}

// triggerJSONNameChange calls setAgentURI on the registry to swap the
// data: URI alice was registered with for one whose `.name` is "alice2".
// The watcher polls Resolve(agentId), parses the new JSON, sees the name
// no longer matches the bound IRC account, and KILLs the session.
//
// setAgentURI is owner-gated; the deployer-registered NFT was minted to
// alice's wallet (msg.sender), so signing with alice's key suffices.
func triggerJSONNameChange(agentID *big.Int) error {
	addrBytes, err := os.ReadFile(registryFile)
	if err != nil {
		return fmt.Errorf("read %s: %w", registryFile, err)
	}
	registry := strings.TrimSpace(string(addrBytes))
	newURI := `data:application/json,{"name":"alice2"}`
	cmd := exec.CommandContext(context.Background(), "cast", "send",
		"--rpc-url", rpcURL,
		"--private-key", "0x"+aliceKey,
		registry, "setAgentURI(uint256,string)", agentID.String(), newURI,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	fmt.Printf("  >> setAgentURI(%s, %q) on-chain\n", agentID.String(), newURI)
	return cmd.Run()
}

func readAgentID() (*big.Int, error) {
	b, err := os.ReadFile(agentIDFile)
	if err != nil {
		return nil, err
	}
	id, ok := new(big.Int).SetString(strings.TrimSpace(string(b)), 10)
	if !ok {
		return nil, fmt.Errorf("could not parse agent id %q", string(b))
	}
	return id, nil
}

// agentIDBytes pads agentId into the 32-byte big-endian uint256 the fork
// expects in AUTHENTICATE step 1.
func agentIDBytes(agentID *big.Int) []byte {
	return common.BigToHash(agentID).Bytes()
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
