package service

import (
	"context"
	"time"

	log "github.com/LACNetNetworks/gas-relay-signer/audit"
	"github.com/LACNetNetworks/gas-relay-signer/errors"
	"github.com/LACNetNetworks/gas-relay-signer/model"
)

// El buffer de reordenamiento.
//
// Sobre HTTP el orden de llegada no esta garantizado y el hub exige el nonce exacto: una metatx
// adelantada por la red muere gastando una transaccion del writer node. Aca espera a que se cierre
// el hueco.
//
// La espera vive en la goroutine de la peticion, sobre un canal, y no en una cola con despachador:
// menos piezas para el mismo resultado, y nada que apagar cuando el usuario se vacia. Ver D3.
//
//	llega nonce n                      esperado = e
//	     |
//	     +-- n <= e ---> enviar
//	     |
//	     +-- n >  e ---> relay.held
//	                      |
//	                 espera hasta: avanza e  -> reevaluar (y renovar la ventana)
//	                               vence     -> relay.turn(window_expired) -> rechazo BAD_NONCE
//	                               sobra     -> relay.turn(too_many_inflight) -> rechazo
//	                               desalojada-> relay.turn(too_many_inflight) -> rechazo
//
// "sobra" es ser la de nonce mas alto con el cupo pasado; "desalojada" es que una de nonce mas bajo
// haya ocupado su lugar. Las dos se deciden por nonce, nunca por orden de llegada.

// Motivos por los que termina una espera. Viajan en `reason` de `relay.turn`.
const (
	turnInTurn          = "in_turn"
	turnWindowExpired   = "window_expired"
	turnTooManyInflight = "too_many_inflight"
	turnClientGone      = "client_gone"
)

// reorderWindow es cuanto puede estar retenida una metatx SIN que avance el nonce esperado de su
// usuario.
func (service *RelaySignerService) reorderWindow() time.Duration {
	if service.Config != nil && service.Config.Reorder.WindowMs > 0 {
		return time.Duration(service.Config.Reorder.WindowMs) * time.Millisecond
	}
	return time.Duration(model.DefaultReorderWindowMs) * time.Millisecond
}

// awaitTurn retiene la metatx cuyo nonce es mayor al esperado hasta que le toque el turno.
//
// Devolver nil NO significa que el nonce sea el correcto: significa que la espera termino. Si
// termino por vencimiento, el envio la rechaza con el mismo BAD_NONCE de siempre, sin haber gastado
// nada. Devuelve error solo cuando el motivo del rechazo es otro -el cupo lleno, el cliente que se
// fue- porque ahi el motivo real quedaria tapado por un BAD_NONCE que no explica nada.
func (service *RelaySignerService) awaitTurn(ctx context.Context, prepared *PreparedMetaTx) error {
	key := senderKey(prepared.SenderKey)
	window := service.reorderWindow()
	deadline := time.Now().Add(window)
	startedAt := time.Now()

	var lastExpected uint64
	var seenExpected bool
	var entry *heldMetaTx

	// release solo registra si la metatx llego a retenerse: una que se envia derecho no deja ni
	// relay.held ni relay.turn.
	release := func(reason string, expected uint64) {
		if entry == nil {
			return
		}
		service.unhold(key, entry)
		entry = nil
		log.Info(ctx, "relay.turn", map[string]interface{}{
			"heldMs": time.Since(startedAt).Milliseconds(),
			"reason": reason,
			"nonce":  prepared.Nonce,
			// El esperado al terminar la espera: con `in_turn` es el que la habilita, y con
			// `window_expired` es contra el que se la va a rechazar.
			"expected": expected,
		})
	}

	for {
		expected, err := service.expectedNonce(prepared.SenderKey)
		if err != nil {
			release(turnWindowExpired, 0)
			return Reject(err, CodeRelayError)
		}

		if prepared.Nonce <= expected {
			release(turnInTurn, expected)
			return nil
		}

		if entry == nil {
			entry = service.hold(key, prepared.Nonce)
			log.Info(ctx, "relay.held", map[string]interface{}{
				"nonce":    prepared.Nonce,
				"expected": expected,
				"gap":      prepared.Nonce - expected,
				"windowMs": window.Milliseconds(),
			})
		}

		// La ventana mide ESTANCAMIENTO: se renueva cada vez que el esperado avanza. Si midiera el
		// total, una rafaga larga perderia la cola por reloj estando todo sano.
		if !seenExpected || expected > lastExpected {
			seenExpected, lastExpected = true, expected
			deadline = time.Now().Add(window)
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			release(turnWindowExpired, expected)
			return nil
		}

		// El cupo se vuelve a comprobar ACA y no solo en la puerta, por dos motivos: mientras esta
		// metatx espera, el de su usuario se puede llenar -y sin esto el motivo real quedaria
		// tapado por el BAD_NONCE del vencimiento-, y porque la puerta no serializa a las
		// peticiones de un mismo usuario, asi que es ACA donde el cupo se hace cumplir.
		//
		// La que sobra no es la que esta despertando sino la de nonce mas alto: descartar una del
		// medio de la cadena invalida todas las posteriores. Ver surplusHeld y design.md, D6.
		if inflight, surplus := service.surplusHeld(key, entry); surplus {
			release(turnTooManyInflight, expected)
			return tooManyInflight(inflight, service.maxInflightPerUser())
		}

		service.sendersLock.Lock()
		wake := service.waitTurnLocked(entry)
		service.sendersLock.Unlock()

		timer := time.NewTimer(remaining)
		select {
		case <-wake:
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			service.sendersLock.Lock()
			service.stopWaitingLocked(entry)
			service.sendersLock.Unlock()
			release(turnClientGone, expected)
			return Reject(errors.New("the client closed the connection while the metatx waited for its turn", -32000),
				CodeRelayError)
		}
		timer.Stop()

		service.sendersLock.Lock()
		service.stopWaitingLocked(entry)
		// Desperto por desalojo, no para reevaluar: el cupo de su usuario se lleno con metatx de
		// nonce mas bajo, que son las que pueden destrabar la cola. Se rechaza con el motivo real y
		// no con el BAD_NONCE del vencimiento, que no explicaria nada.
		evicted := entry.evicted
		inflight := service.inflightLocked(key)
		if evicted {
			// El conteo de ahora no sirve: esta metatx ya salio del registro al marcarse. Se
			// informa el que justifico el desalojo.
			inflight = entry.evictedInflight
		}
		service.sendersLock.Unlock()

		if evicted {
			release(turnTooManyInflight, expected)
			return tooManyInflight(inflight, service.maxInflightPerUser())
		}
	}
}
