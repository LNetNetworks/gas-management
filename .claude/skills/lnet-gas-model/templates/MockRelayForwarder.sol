// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

/**
 * Mock del trustedForwarder de LNet para tests de Foundry.
 *
 * Los tests corren localmente (no en LNet), así que se inyecta este mock en la
 * dirección hardcodeada del forwarder con vm.etch:
 *
 *   address constant FORWARDER = 0xEAA5420AF59305c5ecacCB38fcDe70198001d147;
 *
 *   function setUp() public {
 *       MockRelayForwarder mock = new MockRelayForwarder();
 *       vm.etch(FORWARDER, address(mock).code);
 *       // Camino directo: relayHub distinto a los callers -> _msgSender() == msg.sender
 *       MockRelayForwarder(FORWARDER).set(makeAddr("relayHub"), address(0));
 *   }
 *
 * Para probar el camino relayado: set(relayHub, remitenteOriginal) y luego
 * vm.prank(relayHub) antes de llamar al contrato — _msgSender() debe devolver
 * el remitente original, no el relayHub.
 */
contract MockRelayForwarder {
    address public relayHub;
    address public origin;

    function set(address _relayHub, address _origin) external {
        relayHub = _relayHub;
        origin = _origin;
    }

    function getRelayHub() external view returns (address) {
        return relayHub;
    }

    function getMsgSender() external view returns (address) {
        return origin;
    }
}
