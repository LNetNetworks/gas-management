// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import {Initializable} from "@openzeppelin/contracts-upgradeable/proxy/utils/Initializable.sol";

/**
 * Versión upgradeable de BaseRelayRecipient (gas model LACChain).
 *
 * Un contrato que quiera recibir transacciones relayadas debe heredar de aquí y usar
 * "_msgSender()" en lugar de "msg.sender". A diferencia de la versión no-upgradeable,
 * el `trustedForwarder` NO se inicializa inline (en un proxy eso no persiste): se fija
 * vía `__BaseRelayRecipient_init` desde la función `initialize` del contrato.
 */
abstract contract BaseRelayRecipientUpgradeable is Initializable {
    /// @notice Forwarder singleton del que aceptamos llamadas relayadas.
    address internal trustedForwarder;

    /// @dev Inicializa el forwarder. Llamar desde el `initialize` del contrato hijo.
    function __BaseRelayRecipient_init(address _trustedForwarder) internal onlyInitializing {
        trustedForwarder = _trustedForwarder;
    }

    /**
     * Devuelve el remitente real de la llamada.
     * Si la llamada llegó por el RelayHub, devuelve el remitente original (relayado).
     * Usa `staticcall` para ser compatible con `view`.
     */
    function _msgSender() internal view virtual returns (address sender) {
        bytes memory bytesRelayHub;
        (, bytesRelayHub) = trustedForwarder.staticcall(abi.encodeWithSignature("getRelayHub()"));

        if (msg.sender == abi.decode(bytesRelayHub, (address))) {
            bytes memory bytesSender;
            (, bytesSender) = trustedForwarder.staticcall(abi.encodeWithSignature("getMsgSender()"));
            return abi.decode(bytesSender, (address));
        } else {
            return msg.sender;
        }
    }

    /// @dev Reserva de slots de almacenamiento para futuras versiones (storage gap).
    uint256[49] private __gap;
}
