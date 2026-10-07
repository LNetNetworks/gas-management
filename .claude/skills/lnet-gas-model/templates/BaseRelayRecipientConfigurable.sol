// SPDX-License-Identifier: MIT
pragma solidity >=0.8.0 <0.9.0;

/**
 * Variante de BaseRelayRecipient con el trustedForwarder inyectado por constructor
 * Útil para desplegar el
 * mismo contrato en varias redes (mainnet / protestnet / nodo dev) y para tests, donde
 * se pasa la dirección del mock en lugar de usar `vm.etch`.
 *
 * `trustedForwarder_` debe ser el **BaseRelayRecipientProxy**, NO el RelayHub: el hub no
 * expone `getRelayHub()`, el `abi.decode` revertiría y el constructor se caería (el deploy
 * devuelve dirección 0x0).
 *
 * Toda subclase debe usar `_msgSender()` en lugar de `msg.sender`.
 */
abstract contract BaseRelayRecipient {
    /// @dev Forwarder del que aceptamos llamadas relayadas.
    address internal trustedForwarder;

    constructor(address trustedForwarder_) {
        trustedForwarder = trustedForwarder_;
    }

    /**
     * @dev Devuelve el remitente real: si la llamada vino por el RelayHub, el usuario
     *      original de la metatx; si no, `msg.sender`. `staticcall` para poder ser `view`.
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
}
