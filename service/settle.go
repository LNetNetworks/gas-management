package service

import (
	"context"

	log "github.com/LACNetNetworks/gas-relay-signer/audit"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// Como termino una metatx, leido de los eventos del RelayHub en su receipt.
//
// Vive aparte del resultado que devuelve `POST /relay` porque hay DOS caminos que necesitan lo
// mismo: la respuesta sincronica, que ademas arma el cuerpo para el cliente, y el watcher, que solo
// quiere saber como termino para registrarlo y liberar lo que la metatx tenia en vuelo. Dos lecturas
// distintas del mismo receipt terminarian discrepando.
type metaTxOutcome struct {
	events          []string
	executed        *bool
	errorCode       *uint8
	errorCodeName   *string
	output          *string
	deployedAddress *string

	// hubRejected indica que el hub rechazo la metatx sin consumir su nonce. Es la senal de que la
	// cadena de nonces de ese usuario se rompio y hay que descartarla entera.
	hubRejected bool
}

// readHubEvents recorre los eventos del RelayHub del receipt y arma como termino la metatx.
//
// Tiene efectos ademas de leer: un `BadTransactionSent` descarta lo que el servicio sabe de los
// nonces de ese usuario y registra el rechazo. Los dos son parte de reconocer ese evento, no del
// camino que lo consulta, asi que viven aca y no duplicados en cada llamador.
func (service *RelaySignerService) readHubEvents(ctx context.Context, hash common.Hash, receipt *types.Receipt) metaTxOutcome {
	outcome := metaTxOutcome{events: []string{}}
	if receipt == nil {
		return outcome
	}

	topics := topicsOfHub()
	var sawContractDeployed, sawTransactionRelayed, sawBadTransaction, sawRelayed bool

	for _, lg := range receipt.Logs {
		if len(lg.Topics) == 0 {
			continue
		}
		switch lg.Topics[0].Hex() {
		case topics.contractDeployed:
			sawContractDeployed = true
			outcome.events = append(outcome.events, "ContractDeployed")
			// En un deploy la direccion sale de ESTE evento y no del receipt: el campo del receipt
			// trae la direccion de la transaccion envolvente, que es del nodo, no la del contrato
			// del usuario.
			deployed := common.BytesToAddress(lg.Data).Hex()
			outcome.deployedAddress = &deployed
		case topics.transactionRelay:
			sawTransactionRelayed = true
			outcome.events = append(outcome.events, "TransactionRelayed")
			executed, output := transactionRelayedFailed(ctx, nil, lg.Data)
			outcome.executed = &executed
			if !executed {
				reason := decodeRevertReason(output)
				outcome.output = &reason
			}
		case topics.badTransaction:
			sawBadTransaction = true
			outcome.hubRejected = true
			outcome.events = append(outcome.events, "BadTransactionSent")
			code, badSender := badTransactionErrorCode(ctx, nil, lg.Data)
			service.invalidateNonce(badSender.Hex())
			hubRejected(ctx, hash.Hex(), badSender, code)

			executed := false
			name := errorCodeName(code)
			outcome.executed = &executed
			outcome.errorCode = &code
			outcome.errorCodeName = &name
		case topics.relayed:
			sawRelayed = true
			outcome.events = append(outcome.events, "Relayed")
		case topics.gasUsedByRelayHub:
			outcome.events = append(outcome.events, "GasUsedByTransaction")
		}
	}

	// Fallo silencioso en DEPLOY: la verificacion paso (Relayed) pero el CREATE interno revirtio en
	// el constructor, asi que no hay ContractDeployed ni TransactionRelayed ni BadTransactionSent y
	// el hub retorno OK. Sin esto se reportaria como exitoso y el cliente no se entera.
	if sawRelayed && !sawContractDeployed && !sawTransactionRelayed && !sawBadTransaction {
		executed := false
		reason := "deploy reverted: contract constructor failed (no code created)"
		outcome.executed = &executed
		outcome.output = &reason
	}

	// El hub acepto y ejecuto: si no hubo ningun evento que diga lo contrario, se ejecuto.
	if outcome.executed == nil && (sawTransactionRelayed || sawContractDeployed) {
		executed := true
		outcome.executed = &executed
	}

	return outcome
}

// settle registra que una metatx se resolvio y libera lo que tenia en vuelo.
//
// El cierre se registra UNA SOLA VEZ por metatx, sin importar quien llegue primero -el watcher o la
// consulta del cliente-. La vista en vivo reconstruye el estado por metatx, y dos cierres para la
// misma la harian contar dos veces. El arbitro es el puente de correlacion, que ya es donde vive lo
// que se sabe de cada hash enviado. Ver design.md, D6.
func (service *RelaySignerService) settle(ctx context.Context, hash common.Hash, blockNumber *uint64, gasUsed string, outcome metaTxOutcome) {
	if !service.claimSettled(hash) {
		return
	}

	log.Info(ctx, "relay.settled", map[string]interface{}{
		"blockNumber":     blockNumber,
		"gasUsed":         gasUsed,
		"executed":        outcome.executed,
		"errorCodeName":   outcome.errorCodeName,
		"deployedAddress": outcome.deployedAddress,
	})

	service.releaseSent(hash, outcome.hubRejected)
}

// claimSettled marca el cierre de esa metatx y responde si le toco registrarlo.
//
// Un hash que no esta recordado -porque expiro, o porque nadie lo anoto- se cierra igual: sin
// memoria no hay nadie con quien competir, y callarse ahi seria perder el evento.
func (service *RelaySignerService) claimSettled(hash common.Hash) bool {
	service.metaTxLock.Lock()
	defer service.metaTxLock.Unlock()

	entry := service.metaTx[hash]
	if entry == nil {
		return true
	}
	if entry.settled {
		return false
	}
	entry.settled = true
	return true
}

// releaseSent devuelve al tracker el lugar que ocupaba esa metatx.
//
// Si el hub la rechazo sin consumir su nonce, la cadena de ese usuario ya se descarto al leer el
// evento: descontar sobre ella no corresponde. En cualquier otro caso se descuenta contra la cadena
// SOBRE LA QUE SE RESERVO, que es lo que evita que un resultado atrasado toque a la que la
// reemplazo.
func (service *RelaySignerService) releaseSent(hash common.Hash, rejectedByHub bool) {
	service.metaTxLock.Lock()
	entry := service.metaTx[hash]
	var key string
	var chain *nonceEntry
	if entry != nil {
		key, chain = entry.senderKey, entry.chain
		entry.chain = nil
	}
	service.metaTxLock.Unlock()

	if chain == nil || key == "" {
		return
	}
	if rejectedByHub {
		// La cadena entera se descarto al leer el rechazo; los que esperaban turno ya se
		// despertaron para reevaluar contra la cadena de bloques.
		return
	}
	service.releaseChain(key, chain)
}
