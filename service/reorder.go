package service

import (
	"context"
	"math/big"
	"sync"

	bl "github.com/LACNetNetworks/gas-relay-signer/blockchain"
	"github.com/LACNetNetworks/gas-relay-signer/errors"
	"github.com/LACNetNetworks/gas-relay-signer/model"
	"github.com/ethereum/go-ethereum/common"
)

// Reserva del nonce del hub y envio, serializados POR USUARIO.
//
// Son dos candados con orden fijo -primero el del usuario, despues el global- y el del usuario no
// se sostiene esperando ningun receipt. El invariante que hace que encadenar funcione es que los
// nonces del hub se reserven en el mismo orden en que se toman los de la CUENTA del writer node:
// las transacciones de una cuenta se ejecutan en orden de nonce, asi que la metatx `n` se mina
// antes que la `n+1` aunque caigan en el mismo bloque. Por eso reservar y enviar van bajo el mismo
// candado de usuario, y no en dos pasos. Ver design.md, D2.

// userLock es el candado de un usuario, con cuantos lo estan usando. El contador existe para poder
// borrar la entrada: sin el, el mapa crece con un candado por cada usuario que relayo alguna vez.
type userLock struct {
	mutex sync.Mutex
	users int
}

// lockUser toma el candado de ese usuario y devuelve como soltarlo.
func (service *RelaySignerService) lockUser(key string) func() {
	service.userLocksMutex.Lock()
	if service.userLocks == nil {
		service.userLocks = make(map[string]*userLock)
	}
	lock := service.userLocks[key]
	if lock == nil {
		lock = new(userLock)
		service.userLocks[key] = lock
	}
	lock.users++
	service.userLocksMutex.Unlock()

	lock.mutex.Lock()

	return func() {
		lock.mutex.Unlock()

		service.userLocksMutex.Lock()
		lock.users--
		if lock.users == 0 {
			delete(service.userLocks, key)
		}
		service.userLocksMutex.Unlock()
	}
}

// expectedNonce es el nonce que este servicio va a aceptar para ese usuario.
//
// Sale del tracker cuando hay una cadena viva -que es lo que permite encadenar, porque la cadena de
// bloques todavia no refleja lo enviado- y de la cadena de bloques cuando no la hay.
//
// `from` se toma TAL COMO lo recibio el llamador y no como direccion: hay caminos que pasan una
// cadena que no es una address, y normalizarla les cambiaria la entrada del tracker bajo los pies.
// Solo se interpreta como direccion para consultar la cadena de bloques.
func (service *RelaySignerService) expectedNonce(from string) (uint64, error) {
	if next, ok := service.cachedNonce(from); ok {
		return next, nil
	}

	if service.Config.Application.RelayHubContractAddress == nil {
		return 0, errors.FailedKeyConfig.New("relayHub contract address not resolved", -32610)
	}
	nodeAddress, err := service.NodeAddress()
	if err != nil {
		return 0, err
	}
	client := new(bl.Client)
	if err := client.Connect(service.Config.Application.NodeURL); err != nil {
		return 0, err
	}
	defer client.Close()

	onChain, err := client.GetTransactionCount(*service.Config.Application.RelayHubContractAddress,
		common.HexToAddress(from), nodeAddress)
	if err != nil {
		return 0, err
	}
	if onChain == nil {
		return 0, errors.New("could not read the hub nonce", -32000)
	}
	return onChain.Uint64(), nil
}

// badNonce es el rechazo por nonce equivocado, con el esperado y el recibido.
//
// Es el MISMO rechazo que produce una metatx retenida que vence sin recibir su turno: retener no
// puede empeorar el resultado, asi que lo peor que le pasa a una retenida es exactamente lo que le
// pasaba antes de existir el buffer.
func badNonce(expected, got uint64, pending int) *Rejection {
	cause := errors.New(
		"invalid nonce: the RelayHub expects "+decimal(expected)+" for this sender but the metatx carries "+decimal(got),
		-32000)
	return RejectWithDetails(cause, CodeBadNonce, map[string]interface{}{
		"expected": expected,
		"got":      got,
		"pending":  pending,
	})
}

// decimal evita arrastrar strconv y fmt por un solo numero.
func decimal(value uint64) string { return new(big.Int).SetUint64(value).String() }

// reorderEnabled indica si el reordenamiento gobierna el camino de envio.
func (service *RelaySignerService) reorderEnabled() bool {
	return service.Config != nil && service.Config.Reorder.Enabled
}

// sendReordered es el camino de envio CON reordenamiento: espera el turno si hace falta, valida el
// nonce contra el tracker y recien entonces envia.
//
// Todo lo que hay aca solo corre con el flag encendido. Con el flag apagado no se entra a esta
// funcion: lo que no se ejecuta no puede cambiar el comportamiento por accidente. Ver design.md, D8.
func (service *RelaySignerService) sendReordered(ctx context.Context, prepared *PreparedMetaTx) (common.Hash, error) {
	key := senderKey(prepared.SenderKey)

	// El cupo se comprueba ANTES de tomar el candado del usuario: una rafaga que ya se paso del
	// techo no tiene que hacer cola para enterarse.
	if inflight := service.inflightOf(prepared.SenderKey); inflight >= service.maxInflightPerUser() {
		return common.Hash{}, tooManyInflight(inflight, service.maxInflightPerUser())
	}

	if err := service.awaitTurn(ctx, prepared); err != nil {
		return common.Hash{}, err
	}

	unlock := service.lockUser(key)
	defer unlock()

	expected, err := service.expectedNonce(prepared.SenderKey)
	if err != nil {
		return common.Hash{}, Reject(err, CodeRelayError)
	}
	if prepared.Nonce != expected {
		return common.Hash{}, badNonce(expected, prepared.Nonce, service.pendingOf(prepared.SenderKey))
	}

	hash, err := service.reserveGasAndSend(ctx, prepared)
	if err != nil {
		// El envio fallo, asi que ESTE nonce no se consumio, pero los que ya estan en vuelo si.
		// Se descarta la cadena entera y que la proxima relea la cadena de bloques: reservar sobre
		// un hueco no sirve. Ver design.md, D4.
		service.forgetChain(key)
		return common.Hash{}, err
	}
	return hash, nil
}

// maxInflightPerUser es el techo de metatx de un mismo usuario a la vez en vuelo o retenidas.
func (service *RelaySignerService) maxInflightPerUser() int {
	if service.Config != nil && service.Config.Reorder.MaxInflightPerUser > 0 {
		return service.Config.Reorder.MaxInflightPerUser
	}
	return model.DefaultReorderMaxInflightPerUser
}

// tooManyInflight es el rechazo por rafaga que se paso del techo, con cuantas hay y cual es el
// maximo.
//
// Existe para acotar el dano cuando la cadena de nonces se rompe: al rechazarse la metatx `k`, las
// `k+1` en adelante ya salieron y cada una gasto una transaccion del writer node.
func tooManyInflight(inflight, max int) *Rejection {
	cause := errors.New(
		"exceeded the inflight tx limit for this address: "+decimal(uint64(inflight))+
			" metatx in flight, maximum "+decimal(uint64(max))+
			". Wait for them to be mined before sending more", -32000)
	return RejectWithDetails(cause, CodeTooManyInflight, map[string]interface{}{
		"inflight": inflight,
		"max":      max,
	})
}

// noteSent anota el envio en el tracker y devuelve la cadena sobre la que quedo reservado, junto
// con cuantas metatx le quedan en vuelo a ese usuario para el evento `relay.sent`.
//
// Con el reordenamiento apagado se anota como siempre -sin contar lo en vuelo, porque nadie lo
// libera- y `pendingForUser` sigue saliendo sin valor, que es lo que la vista ya sabe interpretar.
func (service *RelaySignerService) noteSent(prepared *PreparedMetaTx) (*nonceEntry, interface{}) {
	if !service.reorderEnabled() {
		service.incrementTransactionCount(prepared.SenderKey, prepared.Nonce)
		return nil, nil
	}

	key := senderKey(prepared.SenderKey)
	// La metatx que usaba el nonce entregado llego: se cierra su ticket y el siguiente que consulte
	// no tiene que esperar el plazo completo.
	service.closeOpenTicket(key)

	service.sendersLock.Lock()
	chain := service.reserveLocked(key, prepared.Nonce, true)
	pending := chain.pending
	// El nonce esperado avanzo: le toca a la siguiente de la rafaga.
	service.notifyTurnLocked(key)
	service.sendersLock.Unlock()

	return chain, pending
}
