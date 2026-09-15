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
// `peek` pide consultar sin reservar. Este servicio no reserva nonces, asi que hoy no cambia nada:
// se acepta para que un cliente escrito contra el relayer de referencia funcione sin cambios, y
// cobra efecto cuando exista la reserva. Ver design.md, D7.
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

	// El proximo a usar sale del cache cuando lo hay: es lo que ya entregamos a quien encadena.
	if cached, ok := service.cachedNonce(address.Hex()); ok {
		next := new(big.Int).SetUint64(cached)
		if next.Cmp(onChain) > 0 {
			state.Next = next
			// Lo que el cache lleva de adelanto es exactamente lo relayado y todavia sin reflejar
			// en la cadena. El tracker de 04-add-nonce-reordering lo vuelve autoritativo sin
			// cambiar la forma de la respuesta.
			state.Pending = new(big.Int).Sub(next, onChain).Uint64()
		}
	}

	_ = peek
	return state, nil
}
