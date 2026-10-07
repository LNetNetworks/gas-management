// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import {BaseRelayRecipient} from "./BaseRelayRecipientConfigurable.sol";

/**
 * @title GasModelStorage
 * @notice Contrato mínimo listo para el gas model de LNet: guarda un uint256 y registra
 *         quién lo cambió. Punto de partida para cualquier contrato propio.
 * @dev Reglas aplicadas:
 *      - hereda BaseRelayRecipient (forwarder por constructor) y usa `_msgSender()`;
 *      - nunca `msg.sender` ni `tx.origin` (serían el RelayHub y el writer node);
 *      - el evento lleva el sender resuelto, porque el `from` del receipt es el nodo relayer;
 *      - sin `payable`/`msg.value`: la metatx no transporta valor.
 */
contract GasModelStorage is BaseRelayRecipient {
    uint256 private value;
    address public owner;

    event ValueChanged(address indexed sender, uint256 newValue);

    error NotOwner();

    /// @param trustedForwarder_ BaseRelayRecipientProxy de la red destino.
    ///        mainnet 648541: 0xEAA5420AF59305c5ecacCB38fcDe70198001d147
    ///        protestnet/dev 648540: 0xa4B5eE2906090ce2cDbf5dfff944db26f397037D
    constructor(address trustedForwarder_) BaseRelayRecipient(trustedForwarder_) {
        owner = _msgSender(); // NO msg.sender: sería el RelayHub
    }

    modifier onlyOwner() {
        if (_msgSender() != owner) revert NotOwner();
        _;
    }

    function store(uint256 newValue) external {
        value = newValue;
        emit ValueChanged(_msgSender(), newValue);
    }

    function retrieve() external view returns (uint256) {
        return value;
    }

    function transferOwnership(address newOwner) external onlyOwner {
        owner = newOwner;
    }
}
