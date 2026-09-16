// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import {Test} from "forge-std/Test.sol";
import {GasModelToken} from "../src/GasModelToken.sol";
import {MockRelayForwarder} from "./MockRelayForwarder.sol";

/// @dev Esqueleto de test para un contrato del gas model. Los tests corren en la EVM local
///      (no en LNet): se inyecta el mock del forwarder en la dirección hardcodeada del
///      contrato con `vm.etch` y se prueban LOS DOS caminos, directo y relayado.
contract GasModelTokenTest is Test {
    /// Debe coincidir con el `trustedForwarder` hardcodeado en el contrato bajo prueba.
    address internal constant FORWARDER = 0xa4B5eE2906090ce2cDbf5dfff944db26f397037D;

    GasModelToken internal token;
    address internal owner = makeAddr("owner");
    address internal alice = makeAddr("alice");
    address internal relayHub = makeAddr("relayHub");

    function setUp() public {
        MockRelayForwarder mock = new MockRelayForwarder();
        vm.etch(FORWARDER, address(mock).code);
        // Camino directo por defecto: el relayHub no es ninguno de los callers,
        // así que _msgSender() devuelve msg.sender.
        MockRelayForwarder(FORWARDER).set(relayHub, address(0));

        vm.prank(owner);
        token = new GasModelToken(); // constructor sin args: owner = _msgSender() = owner
    }

    function test_DirectPath() public view {
        assertEq(token.owner(), owner);
        assertEq(token.balanceOf(owner), token.INITIAL_SUPPLY());
    }

    /// @notice Camino relayado: si llama el RelayHub, `_msgSender()` es el usuario original.
    function test_RelayedPath() public {
        MockRelayForwarder(FORWARDER).set(relayHub, alice); // el forwarder reporta a alice

        vm.prank(owner);
        token.transfer(alice, 1_000e18);

        vm.prank(relayHub); // msg.sender == relayHub => sender efectivo = alice
        token.transfer(owner, 400e18);

        assertEq(token.balanceOf(alice), 600e18); // salieron del saldo de alice
        assertEq(token.balanceOf(relayHub), 0);
    }
}
