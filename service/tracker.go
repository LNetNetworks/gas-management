package service

import (
	"time"
)

// Tracker de nonces en vuelo por usuario.
//
// Es la UNICA fuente de verdad sobre el proximo nonce de un usuario: reemplaza al cache `senders`
// en lugar de convivir con el. Dos cuentas separadas sobre el mismo numero terminan discrepando, y
// el sintoma es un usuario que no puede relayar sin que nada este roto. Ver design.md, D1.
//
// Con el reordenamiento APAGADO se comporta exactamente como el cache que reemplaza: se escribe
// despues de enviar, se lee solo para responder el nonce pendiente, expira por TTL y se descarta
// entero ante un `BadTransactionSent`. No reserva, no valida y no retiene.

// nonceEntry es la cadena de nonces de un usuario: el PROXIMO nonce a usar, cuantas metatx suyas
// estan en vuelo, y cuando se actualizo.
//
// El valor tiene IDENTIDAD, y esa identidad es el puntero: al romperse la cadena se descarta entera
// y la proxima metatx relee la cadena de bloques. Un resultado que llega tarde y pertenece a una
// cadena ya descartada compara distinto y no toca a la que la reemplazo. Sin esto, el conteo de en
// vuelo queda corrido para siempre despues del primer fallo y el usuario agota su cupo sin tener
// nada en vuelo. Ver design.md, D4.
//
// `updatedAt` sostiene el TTL que ya tenia el cache: es la red de seguridad si el tracker divergiera
// y el cliente nunca consultara el receipt de una metatx fallida.
type nonceEntry struct {
	next      uint64
	pending   int
	updatedAt time.Time

	// expiry es la gracia antes de olvidar a un usuario que se quedo sin metatx en vuelo. Vive en
	// la entrada y no en un mapa aparte para que descartar la cadena cancele tambien su borrado
	// diferido, sin dos estructuras que puedan discrepar.
	expiry *time.Timer
}

// graceWindow es cuanto sobrevive una cadena sin metatx en vuelo antes de olvidarse.
//
// Es la misma ventana que la de la retencion, a proposito: las dos responden a la misma pregunta
// -cuanto dura una rafaga- y dos numeros distintos para lo mismo serian una perilla mas sin
// informacion nueva. Ver design.md, D4.
func (service *RelaySignerService) graceWindow() time.Duration {
	if service.Config != nil && service.Config.Reorder.WindowMs > 0 {
		return time.Duration(service.Config.Reorder.WindowMs) * time.Millisecond
	}
	return 0
}

// chainOf devuelve la cadena vigente de un usuario, o nil si no hay ninguna o si expiro.
//
// Se llama con el lock TOMADO por el llamador, que es lo que permite componer una lectura y una
// escritura en una sola seccion critica -leer el esperado y reservar sobre el, sin que otra metatx
// del mismo usuario se meta en el medio-.
func (service *RelaySignerService) chainLocked(key string) *nonceEntry {
	entry := service.senders[key]
	if entry == nil {
		return nil
	}
	if time.Since(entry.updatedAt) > service.nonceCacheTTL() {
		service.dropLocked(key, entry)
		return nil
	}
	return entry
}

// reserveLocked anota que `nonce` se acaba de usar: el proximo es al menos nonce+1 y hay una metatx
// mas en vuelo. Devuelve la cadena sobre la que se reservo, que es lo que hay que devolver despues
// para liberarla.
//
// NO suma +1 a ciegas: dos envios que firmaron el MISMO nonce solo pueden consumir uno on-chain, y
// el incremento incondicional dejaba el contador por delante del real para siempre.
func (service *RelaySignerService) reserveLocked(key string, nonce uint64, inFlight bool) *nonceEntry {
	if service.senders == nil {
		service.senders = make(map[string]*nonceEntry)
	}

	next := nonce + 1
	entry := service.chainLocked(key)
	if entry == nil {
		entry = &nonceEntry{next: next}
		service.senders[key] = entry
	} else if entry.next > next {
		next = entry.next
	}

	// La rafaga sigue: cancelar el borrado diferido, si lo habia.
	entry.stopExpiry()
	entry.next = next
	entry.updatedAt = time.Now()
	if inFlight {
		entry.pending++
	}
	return entry
}

// releaseChain descuenta una metatx que ya se resolvio.
//
// Si la cadena que se pasa no es la vigente no toca nada: el contador de la cadena nueva no es
// asunto de un resultado de la vieja. Al llegar a cero se programa la gracia, no el borrado: el
// cliente puede tener metatx de la misma rafaga todavia en camino, y necesitan que el proximo nonce
// siga contando lo que ya se reservo.
func (service *RelaySignerService) releaseChain(key string, chain *nonceEntry) {
	service.sendersLock.Lock()
	defer service.sendersLock.Unlock()

	if chain == nil || service.senders[key] != chain {
		return
	}
	if chain.pending > 0 {
		chain.pending--
	}
	service.notifyTurnLocked(key)
	if chain.pending > 0 || chain.expiry != nil {
		return
	}

	grace := service.graceWindow()
	if grace <= 0 {
		service.dropLocked(key, chain)
		return
	}
	chain.expiry = time.AfterFunc(grace, func() {
		service.sendersLock.Lock()
		defer service.sendersLock.Unlock()
		if current := service.senders[key]; current == chain && chain.pending <= 0 {
			service.dropLocked(key, chain)
		}
	})
}

// forgetChain descarta lo que el servicio sabe de un usuario: la proxima metatx se valida contra la
// cadena de bloques.
//
// Se llama cuando la cadena de nonces se rompio -un envio que fallo, o un rechazo del hub que no
// consumio el nonce-. Reservar sobre un hueco no sirve: si el nonce `n` nunca se consumio, todo lo
// reservado despues esta mal.
func (service *RelaySignerService) forgetChain(key string) {
	service.sendersLock.Lock()
	defer service.sendersLock.Unlock()
	if entry := service.senders[key]; entry != nil {
		service.dropLocked(key, entry)
	}
	// Los que esperaban turno tienen que reevaluar, ahora contra la cadena de bloques.
	service.notifyTurnLocked(key)
}

// dropLocked borra la entrada y cancela su borrado diferido. Se llama con el lock tomado.
func (service *RelaySignerService) dropLocked(key string, entry *nonceEntry) {
	entry.stopExpiry()
	delete(service.senders, key)
}

// stopExpiry cancela la gracia pendiente, si la hay.
func (entry *nonceEntry) stopExpiry() {
	if entry.expiry != nil {
		entry.expiry.Stop()
		entry.expiry = nil
	}
}

// pendingOf es cuantas metatx de ese usuario estan enviadas y sin resolverse.
func (service *RelaySignerService) pendingOf(from string) int {
	service.sendersLock.Lock()
	defer service.sendersLock.Unlock()
	entry := service.chainLocked(senderKey(from))
	if entry == nil {
		return 0
	}
	return entry.pending
}

// Espera de turno: los que quedaron retenidos por el reordenamiento.
//
// Viven con el mismo lock que las cadenas porque se despiertan exactamente cuando una cadena
// cambia: dos locks distintos para dos vistas del mismo hecho dejarian una ventana en la que el
// nonce ya avanzo y el que espera todavia no se entero.

// waitTurnLocked registra a quien espera turno y devuelve el canal por el que se lo despierta. Se
// llama con el lock tomado; el canal se cierra -nunca se escribe- asi que despertar a muchos es una
// sola operacion y no puede bloquear a quien despierta.
func (service *RelaySignerService) waitTurnLocked(key string) chan struct{} {
	if service.turnWaiters == nil {
		service.turnWaiters = make(map[string]map[chan struct{}]struct{})
	}
	waiters := service.turnWaiters[key]
	if waiters == nil {
		waiters = make(map[chan struct{}]struct{})
		service.turnWaiters[key] = waiters
	}
	wake := make(chan struct{})
	waiters[wake] = struct{}{}
	return wake
}

// stopWaitingLocked saca a quien dejo de esperar. Sin esto el mapa se queda con un conjunto vacio
// por cada usuario que alguna vez espero turno.
func (service *RelaySignerService) stopWaitingLocked(key string, wake chan struct{}) {
	waiters := service.turnWaiters[key]
	if waiters == nil {
		return
	}
	delete(waiters, wake)
	if len(waiters) == 0 {
		delete(service.turnWaiters, key)
	}
}

// notifyTurnLocked despierta a todos los que esperan turno de ese usuario para que reevaluen. Se
// llama con el lock tomado, cada vez que el proximo nonce esperado pudo haber cambiado.
func (service *RelaySignerService) notifyTurnLocked(key string) {
	waiters := service.turnWaiters[key]
	if waiters == nil {
		return
	}
	for wake := range waiters {
		close(wake)
	}
	delete(service.turnWaiters, key)
}

// waitingOf es cuantas metatx de ese usuario estan retenidas esperando su turno.
//
// Se lleva en un contador propio y no como el tamano del conjunto de los que duermen: entre dos
// vueltas de la espera, una metatx retenida no esta dormida pero SIGUE retenida, y no contarla ahi
// dejaria pasar por encima del tope justo en la rafaga que el tope existe para acotar.
func (service *RelaySignerService) waitingOf(key string) int {
	return service.heldCount[key]
}

// hold anota que una metatx de ese usuario quedo retenida.
func (service *RelaySignerService) hold(key string) {
	service.sendersLock.Lock()
	defer service.sendersLock.Unlock()
	if service.heldCount == nil {
		service.heldCount = make(map[string]int)
	}
	service.heldCount[key]++
}

// unhold anota que una metatx de ese usuario dejo de estar retenida, por el motivo que sea.
func (service *RelaySignerService) unhold(key string) {
	service.sendersLock.Lock()
	defer service.sendersLock.Unlock()
	if service.heldCount[key] <= 1 {
		delete(service.heldCount, key)
		return
	}
	service.heldCount[key]--
}

// inflightOf es cuantas metatx de ese usuario ocupan lugar: las enviadas y sin resultado, mas las
// retenidas esperando su turno.
//
// Las retenidas cuentan porque cada una ya tiene su nonce tomado: no contarlas dejaria pasar por
// encima del tope justo en la rafaga que el tope existe para acotar.
func (service *RelaySignerService) inflightOf(from string) int {
	key := senderKey(from)
	service.sendersLock.Lock()
	defer service.sendersLock.Unlock()
	inflight := service.waitingOf(key)
	if entry := service.chainLocked(key); entry != nil {
		inflight += entry.pending
	}
	return inflight
}
