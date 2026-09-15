package service

import (
	"context"
	"time"

	log "github.com/LACNetNetworks/gas-relay-signer/audit"
	bl "github.com/LACNetNetworks/gas-relay-signer/blockchain"
	"github.com/ethereum/go-ethereum/common"
)

// El watcher de resultados.
//
// Se cuelga de la suscripcion a bloques que ya existe -`ProcessNewBlocks`-, que ya tiene lo caro
// resuelto: reconexion con backoff acotado y la garantia de no morir. En cada cabecera nueva, ademas
// de resetear el cupo de gas, se resuelven los resultados de lo que haya en vuelo. Ver design.md, D5.
//
// Sin esto, el resultado de una metatx relayada por el camino JSON-RPC solo se conoce si el cliente
// vuelve a preguntar: la que nadie consulta no libera nunca su lugar en el tracker, y el cupo de su
// usuario se consume hasta que no puede relayar mas.

// inFlightMetaTx es una metatx enviada de la que todavia no se sabe como termino.
type inFlightMetaTx struct {
	hash      common.Hash
	sentAt    time.Time
	senderKey string
	chain     *nonceEntry
}

// receiptTimeout es cuanto se espera el resultado de una metatx enviada antes de darla por
// indeterminada.
func (service *RelaySignerService) receiptTimeout() time.Duration {
	if service.Config != nil && service.Config.Reorder.ReceiptTimeoutMs > 0 {
		return time.Duration(service.Config.Reorder.ReceiptTimeoutMs) * time.Millisecond
	}
	return defaultReceiptTimeout
}

// inFlight son las metatx enviadas que siguen sin resolverse.
func (service *RelaySignerService) inFlight() []inFlightMetaTx {
	service.metaTxLock.Lock()
	defer service.metaTxLock.Unlock()

	var pending []inFlightMetaTx
	for hash, entry := range service.metaTx {
		if entry.settled || entry.chain == nil {
			continue
		}
		pending = append(pending, inFlightMetaTx{
			hash:      hash,
			sentAt:    entry.rememberedAt,
			senderKey: entry.senderKey,
			chain:     entry.chain,
		})
	}
	return pending
}

// SettleInFlight resuelve, contra el nodo, el resultado de las metatx que siguen en vuelo.
//
// Se llama en cada bloque nuevo. Sin nada en vuelo no abre ninguna conexion ni hace ninguna llamada:
// el costo del watcher es proporcional a lo que hay esperando, no al paso del tiempo.
//
// Ningun fallo de aca puede tumbar el proceso ni interrumpir el seguimiento de bloques: un nodo que
// no responde deja las metatx en vuelo para el proximo bloque, y lo que no se puede interpretar se
// registra como indeterminado.
func (service *RelaySignerService) SettleInFlight(ctx context.Context) {
	if !service.reorderEnabled() {
		return
	}

	pending := service.inFlight()
	if len(pending) == 0 {
		return
	}

	client := new(bl.Client)
	if err := client.Connect(service.Config.Application.NodeURL); err != nil {
		// El nodo no responde: las metatx siguen en vuelo y se reintenta en el proximo bloque. Lo
		// que las saca de ahi si esto se repite es el vencimiento, que se comprueba igual.
		log.Debug(ctx, "watcher.node_unavailable", log.ErrorFields(err))
		service.expireInFlight(ctx, pending)
		return
	}
	defer client.Close()

	for _, metaTx := range pending {
		// Los eventos que salgan de aca pertenecen a la metatx que produjo ese hash, no al bloque
		// que se esta procesando.
		metaTxCtx := service.recallMetaTx(ctx, metaTx.hash)

		receipt, err := client.GetTransactionReceipt(metaTx.hash)
		if err != nil || receipt == nil {
			service.expireOne(metaTxCtx, metaTx, err)
			continue
		}

		outcome := service.readHubEvents(metaTxCtx, metaTx.hash, receipt)
		gasUsed := decimal(receipt.GasUsed)
		var blockNumber *uint64
		if receipt.BlockNumber != nil {
			number := receipt.BlockNumber.Uint64()
			blockNumber = &number
		}
		service.settle(metaTxCtx, metaTx.hash, blockNumber, gasUsed, outcome)
	}
}

// expireInFlight comprueba el vencimiento de un conjunto de metatx sin consultar el nodo.
func (service *RelaySignerService) expireInFlight(ctx context.Context, pending []inFlightMetaTx) {
	for _, metaTx := range pending {
		service.expireOne(service.recallMetaTx(ctx, metaTx.hash), metaTx, nil)
	}
}

// expireOne da por indeterminada una metatx cuyo resultado no llego dentro del plazo, y libera lo
// que retenia.
//
// Sin esto, una metatx perdida deja al usuario con una posicion en vuelo que nunca se libera: su
// cupo se va consumiendo hasta que no puede relayar mas, y el unico remedio seria reiniciar.
func (service *RelaySignerService) expireOne(ctx context.Context, metaTx inFlightMetaTx, cause error) {
	if time.Since(metaTx.sentAt) < service.receiptTimeout() {
		return
	}
	if !service.claimSettled(metaTx.hash) {
		return
	}

	fields := map[string]interface{}{
		"transactionHash": metaTx.hash.Hex(),
		"error": "the metatx was sent but its result was not known within " +
			service.receiptTimeout().String(),
	}
	if cause != nil {
		fields["error"] = cause.Error()
	}
	log.Warn(ctx, "relay.settle_failed", fields)

	service.releaseSent(metaTx.hash, false)
}
