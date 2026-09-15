package service

import (
	"context"
	"time"

	log "github.com/LACNetNetworks/gas-relay-signer/audit"
	"github.com/ethereum/go-ethereum/common"
)

// Correlacion de los eventos de cierre de una metatx.
//
// En el relayer de Node los eventos de cierre nacen dentro del contexto de log de la metatx, asi
// que heredan su identificador sin que nadie lo pase. Aca no: el receipt se procesa en una peticion
// HTTP DISTINTA -la que el cliente hace despues para consultarlo- y manana tambien en el watcher de
// bloques. Sin este puente, relay.settled y relay.hub_rejected saldrian sin metaTxId y la vista en
// vivo los descartaria en silencio.
//
// Es el mismo patron que el cache de nonces: mapa en memoria del proceso, lock propio, TTL por
// entrada y tope de entradas. Una entrada vencida no rompe nada, cuesta un evento de cierre sin
// correlacion. Ver design.md, D11.

const (
	// metaTxMemoryTTL es cuanto se recuerda una metatx enviada. Generoso frente a lo que tarda un
	// bloque, y acotado para que una entrada que nadie consulta no se quede para siempre.
	metaTxMemoryTTL = 10 * time.Minute

	// metaTxMemoryMax acota la memoria: una rafaga sostenida no puede hacer crecer el mapa sin
	// limite. Al llegar al tope se descarta la entrada mas antigua.
	metaTxMemoryMax = 4096
)

// metaTxEntry es lo que hay que recordar para que un evento posterior siga perteneciendo a su
// metatx: los identificadores de la peticion que la relayo.
type metaTxEntry struct {
	reqID        string
	metaTxID     string
	rememberedAt time.Time
}

// rememberMetaTx anota, contra el hash de la transaccion enviada, la correlacion de la peticion que
// la relayo. Se llama justo despues del envio, que es el unico momento en que se tienen las dos
// cosas a mano.
func (service *RelaySignerService) rememberMetaTx(ctx context.Context, hash common.Hash) {
	metaTxID := log.MetaTxID(ctx)
	if metaTxID == "" {
		return
	}

	service.metaTxLock.Lock()
	defer service.metaTxLock.Unlock()
	if service.metaTx == nil {
		service.metaTx = make(map[common.Hash]*metaTxEntry)
	}
	service.evictLocked()
	service.metaTx[hash] = &metaTxEntry{
		reqID:        log.RequestID(ctx),
		metaTxID:     metaTxID,
		rememberedAt: time.Now(),
	}
}

// recallMetaTx devuelve un contexto que lleva la correlacion de la metatx que produjo ese hash.
//
// Los identificadores son los de la peticion que RELAYO la metatx, no los de la que vino a
// consultar el receipt: es lo que hace que el evento de cierre quede junto a los demas eventos de
// esa metatx. Si no hay nada recordado, devuelve el contexto tal cual y el evento sale sin
// correlacion, que es el comportamiento que habria sin este mapa.
func (service *RelaySignerService) recallMetaTx(ctx context.Context, hash common.Hash) context.Context {
	service.metaTxLock.Lock()
	entry := service.metaTx[hash]
	if entry != nil && time.Since(entry.rememberedAt) > metaTxMemoryTTL {
		delete(service.metaTx, hash)
		entry = nil
	}
	service.metaTxLock.Unlock()

	if entry == nil {
		return ctx
	}
	if entry.reqID != "" {
		ctx = log.WithRequestID(ctx, entry.reqID)
	}
	return log.WithMetaTxID(ctx, entry.metaTxID)
}

// forgetMetaTx libera una entrada cuya metatx ya se resolvio.
func (service *RelaySignerService) forgetMetaTx(hash common.Hash) {
	service.metaTxLock.Lock()
	defer service.metaTxLock.Unlock()
	delete(service.metaTx, hash)
}

// evictLocked descarta lo vencido y, si aun asi se llego al tope, lo mas antiguo. Se llama con el
// lock tomado.
func (service *RelaySignerService) evictLocked() {
	for hash, entry := range service.metaTx {
		if time.Since(entry.rememberedAt) > metaTxMemoryTTL {
			delete(service.metaTx, hash)
		}
	}
	if len(service.metaTx) < metaTxMemoryMax {
		return
	}
	var oldestHash common.Hash
	var oldest time.Time
	for hash, entry := range service.metaTx {
		if oldest.IsZero() || entry.rememberedAt.Before(oldest) {
			oldest, oldestHash = entry.rememberedAt, hash
		}
	}
	delete(service.metaTx, oldestHash)
}
