// Package events es el bus de eventos en memoria: el mismo log estructurado que sale por la
// salida estandar, tambien disponible para que alguien lo mire en vivo.
//
// Es un derivado del log, no una instrumentacion aparte, a proposito: cada linea que emite el
// emisor estructurado pasa por aca sin que el codigo que la origina tenga que saber que existe un
// dashboard. Asi no hay dos verdades sobre lo que paso, y agregar un evento al log lo agrega al
// dashboard. La dependencia va en un solo sentido: audit/ importa events/, nunca al reves.
//
// Vive en memoria del proceso, igual que el cache de nonces, y con el mismo supuesto de una sola
// instancia. No sobrevive a un reinicio ni se agrega entre procesos.
package events

import (
	"sync"
	"sync/atomic"
)

// subscriberBuffer es cuantos eventos aguanta el canal de un suscriptor antes de que se le empiecen
// a descartar. Es una cota a la memoria por observador, no una garantia de entrega: el valor
// definitivo se ajusta cuando exista el dashboard real contra el que medirlo.
const subscriberBuffer = 256

// Event es una linea de log ya armada, mas el numero de secuencia que permite reanudar el stream.
type Event struct {
	Seq  uint64
	Line map[string]interface{}
}

// Name, Level y Ts son los campos comunes que todo evento lleva, con acceso comodo.
func (event Event) Name() string  { return event.text("event") }
func (event Event) Level() string { return event.text("level") }
func (event Event) Ts() string    { return event.text("ts") }

// Field devuelve un campo cualquiera de la linea.
func (event Event) Field(key string) interface{} { return event.Line[key] }

func (event Event) text(key string) string {
	if value, ok := event.Line[key].(string); ok {
		return value
	}
	return ""
}

type subscriber struct {
	channel chan Event
}

// Bus retiene los ultimos eventos publicados y los reparte a quien este mirando.
type Bus struct {
	// enabled se consulta ANTES de tomar el mutex: con el dashboard apagado, publicar no puede
	// costar un candado por linea de log. Ver design.md, D6.
	enabled atomic.Bool

	mutex       sync.Mutex
	capacity    int
	ring        []Event
	start       int
	size        int
	sequence    uint64
	subscribers map[uint64]*subscriber
	nextID      uint64
}

// New crea un bus. Con enabled en false, o con capacidad 0, queda inerte: publicar retorna sin
// retener nada ni notificar a nadie.
func New(enabled bool, capacity int) *Bus {
	if capacity < 0 {
		capacity = 0
	}
	bus := &Bus{
		capacity:    capacity,
		subscribers: make(map[uint64]*subscriber),
	}
	if capacity > 0 {
		bus.ring = make([]Event, capacity)
	}
	bus.enabled.Store(enabled && capacity > 0)
	return bus
}

// Enabled indica si el bus esta reteniendo y repartiendo eventos.
func (bus *Bus) Enabled() bool { return bus.enabled.Load() }

// Publish incorpora una linea de log al bus. La llama el emisor estructurado; nadie mas deberia.
//
// Nunca espera y nunca falla: un observador lento pierde eventos en vez de frenar el relay. El
// `seq` es monotonico, asi que el hueco queda visible y se cierra con Replay al reconectar.
func (bus *Bus) Publish(line map[string]interface{}) {
	if !bus.enabled.Load() {
		return
	}

	// Copia propia: quien origino la linea puede seguir usando su mapa, y lo retenido no cambia
	// bajo los pies del que lo esta leyendo.
	copied := make(map[string]interface{}, len(line))
	for key, value := range line {
		copied[key] = value
	}

	bus.mutex.Lock()
	bus.sequence++
	event := Event{Seq: bus.sequence, Line: copied}
	copied["seq"] = event.Seq
	bus.retain(event)
	for _, sub := range bus.subscribers {
		// Envio no bloqueante: si el canal esta lleno se descarta el evento PARA ESE suscriptor
		// y se sigue con los demas. La observacion nunca es causa de un fallo.
		select {
		case sub.channel <- event:
		default:
		}
	}
	bus.mutex.Unlock()
}

// retain guarda el evento en el anillo, descartando el mas antiguo al llegar al tope. La capacidad
// es fija: una rafaga prolongada no hace crecer la memoria del proceso.
func (bus *Bus) retain(event Event) {
	index := (bus.start + bus.size) % bus.capacity
	bus.ring[index] = event
	if bus.size < bus.capacity {
		bus.size++
		return
	}
	bus.start = (bus.start + 1) % bus.capacity
}

// Replay devuelve los eventos retenidos posteriores a afterSeq, en orden de seq creciente.
//
// Pedir desde un punto ya descartado devuelve todo lo que el bus conserva, sin error: el que
// reconecta despues de una pausa larga recibe lo que hay y detecta el hueco por el salto de seq.
// afterSeq 0 devuelve todo lo retenido.
func (bus *Bus) Replay(afterSeq uint64) []Event {
	bus.mutex.Lock()
	defer bus.mutex.Unlock()
	return bus.replayLocked(afterSeq)
}

// replayLocked es el recorrido del anillo, con el candado ya tomado. Lo comparten Replay y
// ReplayAndSubscribe para que las dos devuelvan exactamente lo mismo.
func (bus *Bus) replayLocked(afterSeq uint64) []Event {
	out := make([]Event, 0, bus.size)
	for offset := 0; offset < bus.size; offset++ {
		event := bus.ring[(bus.start+offset)%bus.capacity]
		if event.Seq > afterSeq {
			out = append(out, event)
		}
	}
	return out
}

// Subscribe registra un observador que recibe los eventos publicados a partir de ahora. Devuelve
// la funcion que corta la suscripcion; tras llamarla el observador no recibe nada mas.
//
// Cada suscriptor tiene su canal y su goroutine de entrega: ahi vive el I/O del observador, fuera
// del camino de la metatx, y ahi se recupera cualquier panico suyo.
func (bus *Bus) Subscribe(notify func(Event)) (cancel func()) {
	_, cancel = bus.ReplayAndSubscribe(noReplay, notify)
	return cancel
}

// noReplay pide suscribirse sin recibir nada de lo retenido.
const noReplay = ^uint64(0)

// ReplayAndSubscribe devuelve lo retenido posterior a afterSeq Y registra al observador, las dos
// cosas bajo el MISMO candado.
//
// Hacerlo en dos pasos pierde eventos: entre un Replay y un Subscribe separados, otra goroutine
// puede publicar, y ese evento no sale en lo retenido ni tiene todavia una suscripcion que lo
// reciba. En Node eso no pasa porque el event loop no cede el control en el medio; aca hay que
// garantizarlo. Ver design.md de 03-add-relay-dashboard, D1.
//
// El que solo quiere suscribirse pasa `noReplay` y recibe el historial vacio.
func (bus *Bus) ReplayAndSubscribe(afterSeq uint64, notify func(Event)) (retained []Event, cancel func()) {
	sub := &subscriber{channel: make(chan Event, subscriberBuffer)}

	bus.mutex.Lock()
	if afterSeq != noReplay {
		retained = bus.replayLocked(afterSeq)
	}
	bus.nextID++
	id := bus.nextID
	bus.subscribers[id] = sub
	bus.mutex.Unlock()

	go deliver(sub.channel, notify)

	var once sync.Once
	return retained, func() {
		once.Do(func() {
			bus.mutex.Lock()
			if registered, ok := bus.subscribers[id]; ok {
				delete(bus.subscribers, id)
				// Cerrar bajo el mismo mutex con el que Publish envia: sin eso, cancelar mientras
				// se publica podria cerrar un canal al que se esta escribiendo.
				close(registered.channel)
			}
			bus.mutex.Unlock()
		})
	}
}

// SubscriberCount es cuantos observadores hay conectados.
func (bus *Bus) SubscriberCount() int {
	bus.mutex.Lock()
	defer bus.mutex.Unlock()
	return len(bus.subscribers)
}

func deliver(channel <-chan Event, notify func(Event)) {
	for event := range channel {
		call(notify, event)
	}
}

// call aisla al suscriptor: si entra en panico, el proceso sigue y los demas reciben el evento
// igual. Un observador que falla nunca puede tumbar el servicio que esta observando.
func call(notify func(Event), event Event) {
	defer func() {
		_ = recover()
	}()
	notify(event)
}

// ---------------------------------------------------------------- bus por proceso

// defaultBus arranca inerte: hasta que main lea la configuracion y llame a Init, publicar no
// cuesta nada. Ver design.md, D5.
//
// Es un puntero atomico y no un mutex a proposito: Publish lo lee una vez por linea de log, y el
// requisito es que con el dashboard apagado no se pague ningun candado.
var defaultBus atomic.Pointer[Bus]

func init() {
	defaultBus.Store(New(false, 0))
}

// Init deja el bus del proceso con la configuracion leida. Se llama una vez, desde main, despues
// de leer config.toml y antes de levantar el servidor.
func Init(enabled bool, capacity int) {
	defaultBus.Store(New(enabled, capacity))
}

// Default es el bus del proceso.
func Default() *Bus { return defaultBus.Load() }

// Publish publica en el bus del proceso.
func Publish(line map[string]interface{}) { Default().Publish(line) }

// Replay reanuda desde afterSeq en el bus del proceso.
func Replay(afterSeq uint64) []Event { return Default().Replay(afterSeq) }

// Subscribe se suscribe al bus del proceso.
func Subscribe(notify func(Event)) (cancel func()) { return Default().Subscribe(notify) }

// ReplayAndSubscribe reanuda y se suscribe al bus del proceso, sin ventana entre las dos cosas.
func ReplayAndSubscribe(afterSeq uint64, notify func(Event)) ([]Event, func()) {
	return Default().ReplayAndSubscribe(afterSeq, notify)
}

// SubscriberCount es cuantos observadores hay conectados al bus del proceso.
func SubscriberCount() int { return Default().SubscriberCount() }

// Enabled indica si el bus del proceso esta activo.
func Enabled() bool { return Default().Enabled() }
