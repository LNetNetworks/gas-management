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

// heldMetaTx es UNA metatx retenida: su nonce, por donde despertarla, y si el cupo ya la desalojo.
//
// El nonce esta aca porque es lo unico que permite elegir a quien descartar cuando el cupo se
// llena: descartar la del medio de una cadena invalida todas las posteriores, descartar la mas
// alta no invalida ninguna. Ver design.md, D1.
//
// `wake` es el canal de la vuelta EN CURSO de la espera, o nil entre dos vueltas: una retenida no
// esta dormida todo el tiempo que esta retenida. El canal se cierra -nunca se escribe- asi que
// despertar a muchos es una sola operacion y no puede bloquear a quien despierta.
type heldMetaTx struct {
	nonce   uint64
	wake    chan struct{}
	evicted bool

	// evictedInflight es el cupo que estaba ocupado en el momento del desalojo, y es el numero que
	// se le informa al cliente.
	//
	// No sirve leerlo al despertar: para entonces esta metatx ya salio del registro -se descuenta
	// al marcarla, D3- y las demas pueden haber drenado, asi que el conteo de ese momento puede
	// quedar POR DEBAJO del maximo. Un rechazo por tope superado que informa menos que el tope es
	// incomprensible para quien lo recibe.
	evictedInflight int
}

// waitingOf es cuantas metatx de ese usuario estan retenidas esperando su turno. Se llama con el
// lock TOMADO.
//
// Cuenta lo REGISTRADO y no lo dormido: entre dos vueltas de la espera, una metatx retenida no
// esta dormida pero SIGUE retenida, y no contarla ahi dejaria pasar por encima del tope justo en
// la rafaga que el tope existe para acotar.
func (service *RelaySignerService) waitingOf(key string) int {
	return len(service.held[key])
}

// hold anota que una metatx de ese usuario quedo retenida y devuelve su entrada, que es como se la
// despierta y como se la desaloja despues.
//
// Se registra en orden de llegada: entre dos retenidas del mismo nonce, la ultima es la que sobra.
func (service *RelaySignerService) hold(key string, nonce uint64) *heldMetaTx {
	service.sendersLock.Lock()
	defer service.sendersLock.Unlock()
	if service.held == nil {
		service.held = make(map[string][]*heldMetaTx)
	}
	entry := &heldMetaTx{nonce: nonce}
	service.held[key] = append(service.held[key], entry)
	return entry
}

// unhold anota que esa metatx dejo de estar retenida, por el motivo que sea.
//
// Es idempotente a proposito: a una desalojada ya se la saco del registro al marcarla, y su
// goroutine llama igual a unhold al despertar. Sin esto el mismo lugar se descontaria dos veces y
// el cupo del usuario quedaria por debajo del real. Ver design.md, D3.
func (service *RelaySignerService) unhold(key string, entry *heldMetaTx) {
	service.sendersLock.Lock()
	defer service.sendersLock.Unlock()
	service.unholdLocked(key, entry)
}

// unholdLocked saca una retenida del registro. Se llama con el lock TOMADO.
func (service *RelaySignerService) unholdLocked(key string, entry *heldMetaTx) {
	waiters := service.held[key]
	for index, waiter := range waiters {
		if waiter != entry {
			continue
		}
		service.held[key] = append(waiters[:index], waiters[index+1:]...)
		break
	}
	// Sin esto el mapa se queda con una lista vacia por cada usuario que alguna vez espero turno.
	if len(service.held[key]) == 0 {
		delete(service.held, key)
	}
}

// waitTurnLocked deja a esa retenida lista para que la despierten y devuelve por donde. Se llama
// con el lock tomado.
func (service *RelaySignerService) waitTurnLocked(entry *heldMetaTx) chan struct{} {
	entry.wake = make(chan struct{})
	return entry.wake
}

// stopWaitingLocked anota que esa retenida dejo de estar dormida. Se llama con el lock tomado.
//
// El canal NO se cierra aca: cerrarlo es la senal de despertar, y quien deja de esperar por su
// cuenta -porque vencio su timer o se fue el cliente- no se despierta a si mismo.
func (service *RelaySignerService) stopWaitingLocked(entry *heldMetaTx) {
	entry.wake = nil
}

// notifyTurnLocked despierta a todos los que esperan turno de ese usuario para que reevaluen. Se
// llama con el lock tomado, cada vez que el proximo nonce esperado pudo haber cambiado.
func (service *RelaySignerService) notifyTurnLocked(key string) {
	for _, entry := range service.held[key] {
		service.wakeLocked(entry)
	}
}

// evictHeldLocked desaloja a una retenida: la marca con el cupo que estaba ocupado, la saca del
// registro y la despierta para que se entere. Se llama con el lock TOMADO, y todo pasa en la misma
// seccion critica.
//
// El lugar se descuenta al MARCAR y no cuando la goroutine desalojada despierte. Si se esperara a
// eso, el lugar liberado no estaria disponible enseguida y el desalojo no serviria de nada. La
// contracara es que la goroutine llama igual a unhold al despertar, por eso unhold es idempotente.
// Ver design.md, D3.
//
// NO reserva el lugar liberado para quien provoco el desalojo: la puerta no tiene donde anotarlo y
// el cupo se hace cumplir en la espera, no en la puerta. Ver design.md, D3 y D6.
func (service *RelaySignerService) evictHeldLocked(key string, entry *heldMetaTx, inflight int) {
	entry.evicted = true
	entry.evictedInflight = inflight
	service.unholdLocked(key, entry)
	service.wakeLocked(entry)
}

// highestHeldLocked es la retenida de nonce mas alto de ese usuario, o nil si no tiene ninguna. Se
// llama con el lock TOMADO.
//
// Es la que sobra cuando el cupo se llena: descartar la del medio de una cadena de nonces invalida
// todas las posteriores -quedan esperando un esperado que ya nunca va a avanzar-, mientras que
// descartar la mas alta no invalida ninguna.
//
// Ante un empate gana la que llego DESPUES: dos retenidas con el mismo nonce son duplicados y el
// hub solo va a aceptar una. Como el registro esta en orden de llegada, alcanza con exigir nonce
// estrictamente mayor para quedarse con la ultima de las empatadas. Ver design.md, D4.
func (service *RelaySignerService) highestHeldLocked(key string) *heldMetaTx {
	var highest *heldMetaTx
	for _, entry := range service.held[key] {
		if highest == nil || entry.nonce >= highest.nonce {
			highest = entry
		}
	}
	return highest
}

// wakeLocked despierta a una retenida concreta, si esta dormida. Se llama con el lock tomado.
func (service *RelaySignerService) wakeLocked(entry *heldMetaTx) {
	if entry.wake == nil {
		return
	}
	close(entry.wake)
	entry.wake = nil
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
	return service.inflightLocked(key)
}

// inflightLocked es lo mismo que inflightOf sobre una clave ya normalizada. Se llama con el lock
// TOMADO, que es lo que permite decidir el cupo y desalojar en una sola seccion critica.
func (service *RelaySignerService) inflightLocked(key string) int {
	inflight := service.waitingOf(key)
	if entry := service.chainLocked(key); entry != nil {
		inflight += entry.pending
	}
	return inflight
}
