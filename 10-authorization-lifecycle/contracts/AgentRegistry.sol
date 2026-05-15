// SPDX-License-Identifier: MIT
// A non-upgradeable, single-file ERC-8004 Identity Registry. Behavior matches
// the canonical deployment on Base mainnet at
// 0x8004A169FB4a3325136EB29fA0ceB6D2e539a432
// (source: https://github.com/erc-8004/erc-8004-contracts —
// IdentityRegistryUpgradeable.sol). The canonical version is upgradeable via
// UUPS proxy; we strip the proxy machinery so a tutorial reader sees the
// whole identity layer in ~150 lines. The external interface and event
// signatures are otherwise identical.
//
// Spec: https://eips.ethereum.org/EIPS/eip-8004
pragma solidity ^0.8.20;

import {ERC721} from "../lib/openzeppelin-contracts/contracts/token/ERC721/ERC721.sol";
import {ERC721URIStorage}
    from "../lib/openzeppelin-contracts/contracts/token/ERC721/extensions/ERC721URIStorage.sol";
import {EIP712} from "../lib/openzeppelin-contracts/contracts/utils/cryptography/EIP712.sol";
import {ECDSA} from "../lib/openzeppelin-contracts/contracts/utils/cryptography/ECDSA.sol";

contract AgentRegistry is ERC721URIStorage, EIP712 {
    /// EIP-712 typehash for the setAgentWallet authorization. Pre-signed by
    /// the NFT owner; rotated wallet does NOT need to sign anything.
    bytes32 public constant SET_AGENT_WALLET_TYPEHASH =
        keccak256("SetAgentWallet(uint256 agentId,address newWallet,uint256 deadline)");

    /// Per the spec, register() may include an initial metadata batch.
    struct MetadataEntry {
        string key;
        bytes  value;
    }

    /// Next agentId. Starts at 1 so 0 is a clean "no agent" sentinel.
    uint256 public nextAgentId = 1;

    /// agentId → designated signing wallet. address(0) means "fall back to
    /// ownerOf(agentId)". Set by setAgentWallet; cleared by unsetAgentWallet.
    mapping(uint256 => address) private _agentWallet;

    /// agentId → key → bytes. Spec-mandated extensible KV store.
    mapping(uint256 => mapping(string => bytes)) private _metadata;

    /// Spec-mandated events. Topic-0 hashes match the canonical contract so
    /// existing indexers and chapter 10's mutation watcher work against both
    /// deployments without recompilation.
    event Registered(uint256 indexed agentId, string agentURI, address indexed owner);
    event URIUpdated(uint256 indexed agentId, string newURI, address indexed updatedBy);
    event MetadataSet(
        uint256 indexed agentId,
        string  indexed metadataKey,
        string  metadataKeyData,
        bytes   metadataValue
    );
    event AgentWalletSet(uint256 indexed agentId, address indexed newWallet);
    event AgentWalletUnset(uint256 indexed agentId);

    error NotAuthorized();
    error InvalidSignature();
    error DeadlineExpired();

    constructor() ERC721("AgentIdentity", "AGENT") EIP712("AgentRegistry", "1") {}

    // ---- registration (3 overloads, per spec) ---------------------------

    function register() external returns (uint256 agentId) {
        agentId = _mintNext("");
    }

    function register(string calldata agentURI) external returns (uint256 agentId) {
        agentId = _mintNext(agentURI);
    }

    function register(string calldata agentURI, MetadataEntry[] calldata metadata)
        external
        returns (uint256 agentId)
    {
        agentId = _mintNext(agentURI);
        for (uint256 i = 0; i < metadata.length; ++i) {
            _setMetadata(agentId, metadata[i].key, metadata[i].value);
        }
    }

    function _mintNext(string memory agentURI) internal returns (uint256 agentId) {
        agentId = nextAgentId++;
        _safeMint(msg.sender, agentId);
        if (bytes(agentURI).length > 0) {
            _setTokenURI(agentId, agentURI);
        }
        emit Registered(agentId, agentURI, msg.sender);
    }

    // ---- mutation --------------------------------------------------------

    function setAgentURI(uint256 agentId, string calldata newURI) external {
        if (ownerOf(agentId) != msg.sender) revert NotAuthorized();
        _setTokenURI(agentId, newURI);
        emit URIUpdated(agentId, newURI, msg.sender);
    }

    /// Owner-authorized via EIP-712 signature so hardware-wallet flows can
    /// pre-sign rotations. The newWallet need not sign anything.
    function setAgentWallet(
        uint256 agentId,
        address newWallet,
        uint256 deadline,
        bytes calldata signature
    ) external {
        if (block.timestamp > deadline) revert DeadlineExpired();
        bytes32 structHash = keccak256(
            abi.encode(SET_AGENT_WALLET_TYPEHASH, agentId, newWallet, deadline)
        );
        bytes32 digest = _hashTypedDataV4(structHash);
        address recovered = ECDSA.recover(digest, signature);
        if (recovered != ownerOf(agentId)) revert InvalidSignature();
        _agentWallet[agentId] = newWallet;
        emit AgentWalletSet(agentId, newWallet);
    }

    function unsetAgentWallet(uint256 agentId) external {
        if (ownerOf(agentId) != msg.sender) revert NotAuthorized();
        delete _agentWallet[agentId];
        emit AgentWalletUnset(agentId);
    }

    function setMetadata(uint256 agentId, string calldata key, bytes calldata value) external {
        if (ownerOf(agentId) != msg.sender) revert NotAuthorized();
        _setMetadata(agentId, key, value);
    }

    function _setMetadata(uint256 agentId, string memory key, bytes memory value) internal {
        _metadata[agentId][key] = value;
        emit MetadataSet(agentId, key, key, value);
    }

    // ---- view ------------------------------------------------------------

    /// The agent's *signing* wallet. Falls back to the NFT owner when no
    /// override has been set. This is the function the agent-irc SASL handler
    /// calls during the AUTHENTICATE round to map (claimed agentId) → (the
    /// address whose signature we expect to recover).
    function getAgentWallet(uint256 agentId) external view returns (address) {
        address w = _agentWallet[agentId];
        return w == address(0) ? ownerOf(agentId) : w;
    }

    function getMetadata(uint256 agentId, string calldata key)
        external
        view
        returns (bytes memory)
    {
        return _metadata[agentId][key];
    }
}
