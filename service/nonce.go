package service

import (
	"context"
	"math/big"

	bl "github.com/LACNetNetworks/gas-relay-signer/blockchain"
	"github.com/LACNetNetworks/gas-relay-signer/errors"
	"github.com/ethereum/go-ethereum/common"
)

// NonceState son los dos nonces de un usuario y cuanto tiene en vuelo.
//
// `OnChain` es lo que dice el RelayHub. `Next` es con lo que hay que firmar ahora, contando las
// metatx que este servicio ya relayo y todavia no se minaron: encadenar sin ese valor produce
// nonces repetidos.
type NonceState struct {
	Address common.Address
	OnChain *big.Int
	Next    *big.Int
	Pending uint64
}

// NonceOf resuelve los dos nonces de una direccion.
//
// `peek` pide consultar sin reservar. Sin el reparto de nonces encendido no hay reserva que evitar
// y la respuesta es la misma con y sin el. Ver design.md, D7.
func (service *RelaySignerService) NonceOf(ctx context.Context, address common.Address, peek bool) (NonceState, error) {
	if service.Config.Application.RelayHubContractAddress == nil {
		return NonceState{}, errors.FailedKeyConfig.New("relayHub contract address not resolved", -32610)
	}

	nodeAddress, err := service.NodeAddress()
	if err != nil {
		return NonceState{}, err
	}

	client := new(bl.Client)
	if err := client.Connect(service.Config.Application.NodeURL); err != nil {
		return NonceState{}, err
	}
	defer client.Close()

	onChain, err := client.GetTransactionCount(*service.Config.Application.RelayHubContractAddress, address, nodeAddress)
	if err != nil {
		return NonceState{}, err
	}

	state := NonceState{Address: address, OnChain: onChain, Next: onChain}

	// El proximo a usar sale del tracker cuando lo hay: es lo que ya entregamos a quien encadena.
	// Con el reparto encendido, ademas, este pedido toma posicion en la cola del usuario salvo que
	// se haya pedido mirar sin reservar.
	if entregado, err := service.HandOutNonce(address.Hex(), peek); err == nil {
		next := new(big.Int).SetUint64(entregado)
		if next.Cmp(onChain) > 0 {
			state.Next = next
			// Lo que el tracker lleva de adelanto es lo relayado y todavia sin reflejar en la
			// cadena. Es el valor que este servicio informaba antes del reordenamiento, y es el que
			// se sigue informando con el reordenamiento apagado.
			state.Pending = new(big.Int).Sub(next, onChain).Uint64()
		}
	}

	// Con el reordenamiento encendido, lo en vuelo es lo que el tracker CUENTA -enviadas sin
	// resultado mas retenidas esperando turno- y no una diferencia calculada contra la cadena. Es
	// contra ese numero que se valida al enviar, asi que informar otro dejaria al cliente decidiendo
	// con una cuenta distinta de la que el servicio aplica.
	if service.Config != nil && service.Config.Reorder.Enabled {
		state.Pending = uint64(service.inflightOf(address.Hex()))
	}

	return state, nil
}
