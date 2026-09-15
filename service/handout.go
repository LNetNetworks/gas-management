package service

import (
	"sync"
	"time"

	"github.com/LACNetNetworks/gas-relay-signer/model"
)

// El reparto de nonces.
//
// Sin reparto -el default- el servicio contesta lo que sabe, y si dos clientes preguntan a la vez se
// llevan el mismo numero: el segundo firma un nonce que el hub ya no va a aceptar. Salir de ahi es
// problema del cliente.
//
// Con reparto, los pedidos del mismo usuario se serializan y cada uno se lleva un numero distinto.
// Lo que se abre es un TICKET, no una reserva: si vence sin que llegue la metatx que lo use, `next`
// no avanzo y el siguiente que pregunte se lleva ese mismo numero. Con reservas, una reserva sin
// usar deja un hueco que solo su duenio puede destapar y el resto de los clientes se traba detras.
// Ver design.md, D7.
//
// Por que del lado del servicio y no del cliente: el nonce va adentro de lo firmado -el hub recupera
// el `from` de esos mismos bytes-, asi que el relayer no puede reescribirlo al recibir la metatx. Lo
// unico que controla es el numero que entrega antes de que el cliente firme.

// handoutTicket es un numero entregado que todavia no se uso. Se cierra al llegar la metatx que lo
// usa -el caso normal, en el orden de milisegundos- o al vencer su plazo.
type handoutTicket struct {
	closed chan struct{}
	once   sync.Once
}

func (ticket *handoutTicket) close() {
	ticket.once.Do(func() { close(ticket.closed) })
}

// autoNonceEnabled indica si el reparto gobierna las consultas de nonce.
func (service *RelaySignerService) autoNonceEnabled() bool {
	return service.Config != nil && service.Config.Reorder.AutoNonce
}

// ticketWindow es cuanto se espera la metatx que use un nonce entregado.
func (service *RelaySignerService) ticketWindow() time.Duration {
	if service.Config != nil && service.Config.Reorder.AutoNonceTicketMs > 0 {
		return time.Duration(service.Config.Reorder.AutoNonceTicketMs) * time.Millisecond
	}
	return time.Duration(model.DefaultReorderAutoNonceTicketMs) * time.Millisecond
}

// HandOutNonce es el nonce que el cliente tiene que firmar AHORA.
//
// Es lo que contestan las dos puertas -el camino JSON-RPC y `GET /nonce/{address}`-, porque las dos
// son formas de preguntar lo mismo: si cada una llevara su cuenta, un cliente que use una y otra
// firmaria con nonces incompatibles.
//
// `peek` pide mirar sin tomar posicion en la cola: informa lo que hay y no abre ningun ticket.
func (service *RelaySignerService) HandOutNonce(from string, peek bool) (uint64, error) {
	if !service.autoNonceEnabled() || peek {
		return service.expectedNonce(from)
	}

	key := senderKey(from)

	// La cola del usuario: cada pedido espera a que se cierre el ticket del anterior.
	service.handoutsMutex.Lock()
	if service.handouts == nil {
		service.handouts = make(map[string]*handoutTicket)
	}
	previous := service.handouts[key]
	mine := &handoutTicket{closed: make(chan struct{})}
	service.handouts[key] = mine
	service.handoutsMutex.Unlock()

	if previous != nil {
		<-previous.closed
	}

	nonce, err := service.expectedNonce(from)
	if err != nil {
		// Un pedido que no se pudo contestar no puede dejar al siguiente esperando.
		service.closeTicket(key, mine)
		return 0, err
	}

	// Este es el ticket que espera SU metatx: el que llega despues se encola detras, pero el que
	// hay que cerrar cuando la metatx llega es este. Sin distinguirlos, el envio cerraria el ticket
	// del que todavia esta esperando en la cola y el que se llevo el numero nunca liberaria el suyo.
	service.handoutsMutex.Lock()
	if service.openTickets == nil {
		service.openTickets = make(map[string]*handoutTicket)
	}
	service.openTickets[key] = mine
	service.handoutsMutex.Unlock()

	// El ticket se cierra solo al vencer. Antes de eso lo cierra la metatx que lo use, desde el
	// camino de envio.
	time.AfterFunc(service.ticketWindow(), func() { service.closeTicket(key, mine) })

	return nonce, nil
}

// closeTicket cierra un ticket y lo saca de la cola si era el ultimo. Sin lo segundo, el mapa se
// queda con un ticket cerrado por cada usuario que alguna vez consulto.
func (service *RelaySignerService) closeTicket(key string, ticket *handoutTicket) {
	ticket.close()

	service.handoutsMutex.Lock()
	defer service.handoutsMutex.Unlock()
	if service.handouts[key] == ticket {
		delete(service.handouts, key)
	}
	if service.openTickets[key] == ticket {
		delete(service.openTickets, key)
	}
}

// closeOpenTicket cierra el ticket que esperaba su metatx, si hay: la metatx llego.
func (service *RelaySignerService) closeOpenTicket(key string) {
	service.handoutsMutex.Lock()
	ticket := service.openTickets[key]
	service.handoutsMutex.Unlock()

	if ticket != nil {
		service.closeTicket(key, ticket)
	}
}
