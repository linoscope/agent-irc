// Package erc8004 implements the client side of the agent-irc-ergo
// ERC8004 SASL mechanism, aligned with the canonical ERC-8004 spec
// (https://eips.ethereum.org/EIPS/eip-8004).
//
// The wire protocol is three AUTHENTICATE rounds, all base64-encoded:
//
//	C -> AUTHENTICATE ERC8004
//	S -> AUTHENTICATE +                          (kick off)
//	C -> AUTHENTICATE <b64(32-byte uint256 agentId)>   (claim)
//	S -> AUTHENTICATE <b64(32-byte nonce)>             (challenge)
//	C -> AUTHENTICATE <b64(65-byte EIP-191 signature)> (prove)
//	S -> 903 RPL_SASLSUCCESS                           (or 904 on failure)
//
// Unlike the pre-canonical shape, the client now declares an agentId rather
// than an address. The server resolves that agentId to its expected signing
// wallet via getAgentWallet(uint256) on the ERC-8004 registry. This is
// required by the spec: ERC-8004 has no on-chain reverse address→agentId
// lookup, so the client must say which on-chain identity it's claiming.
//
// The signed body is EIP-191 (personal_sign):
//
//	agent-irc-sasl-v1
//	chain=<chain-id>
//	server=<server-name>
//	agentId=<decimal>
//	nonce=<hex>
//
// chainID + serverID + agentID + nonce binding defeats cross-chain,
// cross-server, and cross-agent replay.
package erc8004

import (
	"crypto/ecdsa"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

const Domain = "agent-irc-sasl-v1"

// AgentIDSize is the byte length of a uint256 agentId on the wire.
const AgentIDSize = 32

// LoadKey reads a hex-encoded secp256k1 private key from path.
// Accepts "0x"-prefixed and un-prefixed hex; trims whitespace.
func LoadKey(path string) (*ecdsa.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read key %s: %w", path, err)
	}
	hexStr := strings.TrimSpace(string(raw))
	hexStr = strings.TrimPrefix(hexStr, "0x")
	key, err := crypto.HexToECDSA(hexStr)
	if err != nil {
		return nil, fmt.Errorf("parse key %s: %w", path, err)
	}
	return key, nil
}

// Address returns the 20-byte Ethereum address derived from key.
func Address(key *ecdsa.PrivateKey) common.Address {
	return crypto.PubkeyToAddress(key.PublicKey)
}

// AgentIDBytes returns agentID as the 32-byte big-endian uint256 the server
// expects in SASL step 1.
func AgentIDBytes(agentID *big.Int) []byte {
	b := make([]byte, AgentIDSize)
	agentID.FillBytes(b)
	return b
}

// SignChallenge produces the 65-byte signature the server's
// agentirc.VerifyChallenge expects: EIP-191-personal_sign over a body that
// binds chainID + serverName + agentID + nonce.
//
// agentID may be nil for chapter-07-style (pre-canonical, address-only)
// flows; the body then reduces to the legacy chain+server+nonce shape.
func SignChallenge(key *ecdsa.PrivateKey, chainID uint64, serverName string, agentID *big.Int, nonce []byte) ([]byte, error) {
	body := ChallengeBody(chainID, serverName, agentID, nonce)
	prefix := fmt.Sprintf("\x19Ethereum Signed Message:\n%d", len(body))
	hash := crypto.Keccak256([]byte(prefix), body)
	return crypto.Sign(hash, key)
}

// ChallengeBody returns the bytes that get EIP-191-hashed. Exposed for
// testing — production code paths go through SignChallenge.
func ChallengeBody(chainID uint64, serverName string, agentID *big.Int, nonce []byte) []byte {
	if agentID == nil {
		return []byte(fmt.Sprintf("%s\nchain=%d\nserver=%s\nnonce=%s",
			Domain, chainID, serverName, hex.EncodeToString(nonce)))
	}
	return []byte(fmt.Sprintf("%s\nchain=%d\nserver=%s\nagentId=%s\nnonce=%s",
		Domain, chainID, serverName, agentID.String(), hex.EncodeToString(nonce)))
}
