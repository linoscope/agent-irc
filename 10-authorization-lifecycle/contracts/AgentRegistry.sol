// SPDX-License-Identifier: MIT
// Same as chapters 08-09; chapter 10 reuses without modification.
pragma solidity ^0.8.20;

contract AgentRegistry {
    struct Agent {
        address wallet;
        string  name;
    }
    uint256 public nextAgentId = 1;
    mapping(uint256 => Agent)   public  agents;
    mapping(address => uint256) public  agentIdOf;
    mapping(string  => uint256) internal nameIndex;
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
    function remove() external {
        uint256 agentId = agentIdOf[msg.sender];
        if (agentId == 0) revert NotRegistered();
        Agent memory a = agents[agentId];
        delete nameIndex[a.name];
        delete agents[agentId];
        delete agentIdOf[msg.sender];
        emit AgentRemoved(agentId, msg.sender);
    }
    function nameOf(address wallet) external view returns (string memory) {
        uint256 agentId = agentIdOf[wallet];
        if (agentId == 0) return "";
        return agents[agentId].name;
    }
    function walletOf(uint256 agentId) external view returns (address) {
        return agents[agentId].wallet;
    }
}
