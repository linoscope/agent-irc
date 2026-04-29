// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

/// @title AgentRegistry — minimal ERC-8004-compatible Identity Registry.
///
/// Tutorial-grade implementation of the agent identity portion of EIP-8004
/// (Trustless Agent Discovery). Production deployments would inherit from a
/// real ERC-721 implementation (OpenZeppelin), expose richer metadata
/// (agentURI, validation registry pointers, reputation hooks), and add
/// access control (e.g. only the wallet may rename itself).
///
/// What this version supports:
///   - register(name): caller registers themselves; returns agentId
///   - setName(newName): caller renames themselves
///   - nameOf(wallet): the SASL gate's only query — returns "" if not registered
///   - walletOf(agentId): for completeness
///
/// Deploy on Base mainnet for low gas in production. For chapter 08 of the
/// agent-irc tutorial, we deploy to local anvil.
contract AgentRegistry {
    struct Agent {
        address wallet;
        string  name;
    }

    /// agentId 0 is reserved for "not registered".
    uint256 public nextAgentId = 1;

    mapping(uint256 => Agent)   public  agents;        // agentId → Agent
    mapping(address => uint256) public  agentIdOf;     // wallet → agentId
    mapping(string  => uint256) internal nameIndex;    // name → agentId (uniqueness)

    event AgentRegistered(uint256 indexed agentId, address indexed wallet, string name);
    event AgentRenamed(uint256 indexed agentId, string oldName, string newName);
    event AgentRemoved(uint256 indexed agentId, address indexed wallet);

    error AlreadyRegistered();
    error NameTaken();
    error NameEmpty();
    error NotRegistered();

    function register(string calldata name) external returns (uint256 agentId) {
        if (bytes(name).length == 0) revert NameEmpty();
        if (agentIdOf[msg.sender] != 0) revert AlreadyRegistered();
        if (nameIndex[name] != 0) revert NameTaken();

        agentId = nextAgentId++;
        agents[agentId] = Agent({ wallet: msg.sender, name: name });
        agentIdOf[msg.sender] = agentId;
        nameIndex[name] = agentId;
        emit AgentRegistered(agentId, msg.sender, name);
    }

    function setName(string calldata newName) external {
        uint256 agentId = agentIdOf[msg.sender];
        if (agentId == 0) revert NotRegistered();
        if (bytes(newName).length == 0) revert NameEmpty();
        if (nameIndex[newName] != 0) revert NameTaken();

        string memory oldName = agents[agentId].name;
        delete nameIndex[oldName];
        agents[agentId].name = newName;
        nameIndex[newName] = agentId;
        emit AgentRenamed(agentId, oldName, newName);
    }

    /// remove unregisters caller; chapter 10 exercises this for KILL-on-mutation.
    function remove() external {
        uint256 agentId = agentIdOf[msg.sender];
        if (agentId == 0) revert NotRegistered();
        Agent memory a = agents[agentId];
        delete nameIndex[a.name];
        delete agents[agentId];
        delete agentIdOf[msg.sender];
        emit AgentRemoved(agentId, msg.sender);
    }

    /// nameOf is the SASL gate's only query: returns the registered name for
    /// `wallet`, or empty string if not registered. agent-irc-ergo treats
    /// empty string as "not authorized to authenticate."
    function nameOf(address wallet) external view returns (string memory) {
        uint256 agentId = agentIdOf[wallet];
        if (agentId == 0) return "";
        return agents[agentId].name;
    }

    function walletOf(uint256 agentId) external view returns (address) {
        return agents[agentId].wallet;
    }
}
