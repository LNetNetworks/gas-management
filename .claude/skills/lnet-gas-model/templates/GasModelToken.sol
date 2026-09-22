// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import {ERC20} from "@openzeppelin/contracts/token/ERC20/ERC20.sol";
import {ERC20Burnable} from "@openzeppelin/contracts/token/ERC20/extensions/ERC20Burnable.sol";
import {Ownable} from "@openzeppelin/contracts/access/Ownable.sol";
import {Context} from "@openzeppelin/contracts/utils/Context.sol";

import {BaseRelayRecipient} from "./BaseRelayRecipient.sol";

/// @title GasModelToken — ERC20 (OpenZeppelin v5) compatible con el gas model de LNet
/// @dev Patrón estándar de un ERC20 bajo el gas model:
///      - **constructor sin argumentos**: el gas model añade 64 bytes
///        (nodeAddress + expirationDate) al initcode; name/símbolo van hardcodeados y el
///        owner/receptor del suministro salen de `_msgSender()` (el deployer relayado);
///      - `_msgSender()` sobreescrito resolviendo el conflicto con `Context`, con lo cual
///        `transfer`, `approve`, `burn` y `onlyOwner` de OZ ya ven al usuario relayado.
contract GasModelToken is ERC20, ERC20Burnable, Ownable, BaseRelayRecipient {
    uint256 public constant INITIAL_SUPPLY = 1_000_000 * 10 ** 18;

    constructor() ERC20("Gas Model Token", "GMT") Ownable(_msgSender()) {
        _mint(_msgSender(), INITIAL_SUPPLY);
    }

    /// @dev Resuelve el conflicto de `_msgSender()` entre `Context` (OZ) y `BaseRelayRecipient`.
    ///      Debe ser `view` (el de `Context` lo es), de ahí el `staticcall`.
    function _msgSender() internal view override(BaseRelayRecipient, Context) returns (address sender) {
        (, bytes memory bytesRelayHub) = trustedForwarder.staticcall(abi.encodeWithSignature("getRelayHub()"));

        if (msg.sender == abi.decode(bytesRelayHub, (address))) {
            (, bytes memory bytesSender) =
                trustedForwarder.staticcall(abi.encodeWithSignature("getMsgSender()"));
            return abi.decode(bytesSender, (address));
        } else {
            return msg.sender;
        }
    }
}
