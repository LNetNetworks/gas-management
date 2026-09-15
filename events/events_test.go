package events

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// waitFor espera hasta que la condicion se cumpla, o falla. La entrega a los suscriptores es
// asincronica a proposito (design.md, D4), asi que un test no puede leer el resultado en la linea
// siguiente al Publish.
func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("no se cumplio a tiempo: %s", what)
}

func line(name string) map[string]interface{} {
	return map[string]interface{}{"event": name, "level": "info", "ts": "2026-09-14T00:00:00.000Z"}
}

// TestSequenceIsStrictlyIncreasing cubre el escenario "Eventos consecutivos".
func TestSequenceIsStrictlyIncreasing(t *testing.T) {
	bus := New(true, 10)

	for i := 0; i < 5; i++ {
		bus.Publish(line(fmt.Sprintf("relay.%d", i)))
	}

	retained := bus.Replay(0)
	if len(retained) != 5 {
		t.Fatalf("se retuvieron %d eventos, se esperaban 5", len(retained))
	}
	for i := 1; i < len(retained); i++ {
		if retained[i].Seq <= retained[i-1].Seq {
			t.Errorf("seq no es estrictamente creciente: %d despues de %d", retained[i].Seq, retained[i-1].Seq)
		}
	}
	// El seq tambien viaja en la linea, que es como lo lee la vista.
	if retained[0].Line["seq"] != retained[0].Seq {
		t.Errorf("el seq de la linea (%v) no coincide con el del evento (%d)", retained[0].Line["seq"], retained[0].Seq)
	}
}

// TestCapacityKeepsTheMostRecent cubre el escenario "Se supera la capacidad": se conservan los mas
// recientes, se descartan los mas antiguos, y la numeracion de lo retenido no se altera.
func TestCapacityKeepsTheMostRecent(t *testing.T) {
	bus := New(true, 3)

	for i := 1; i <= 10; i++ {
		bus.Publish(line(fmt.Sprintf("relay.%d", i)))
	}

	retained := bus.Replay(0)
	if len(retained) != 3 {
		t.Fatalf("se retuvieron %d eventos, la capacidad es 3", len(retained))
	}
	for offset, event := range retained {
		expectedName := fmt.Sprintf("relay.%d", 8+offset)
		if event.Name() != expectedName {
			t.Errorf("en la posicion %d quedo %q, se esperaba %q", offset, event.Name(), expectedName)
		}
		if event.Seq != uint64(8+offset) {
			t.Errorf("el seq de %q es %d, se esperaba %d: la numeracion de lo retenido no se altera",
				event.Name(), event.Seq, 8+offset)
		}
	}
}

// TestRetentionIsBounded: una rafaga prolongada no hace crecer la memoria del proceso.
func TestRetentionIsBounded(t *testing.T) {
	bus := New(true, 4)
	for i := 0; i < 10000; i++ {
		bus.Publish(line("relay.received"))
	}
	if got := len(bus.Replay(0)); got != 4 {
		t.Errorf("tras 10000 eventos con capacidad 4 se retuvieron %d", got)
	}
	if len(bus.ring) != 4 {
		t.Errorf("el anillo crecio a %d, la capacidad es fija en 4", len(bus.ring))
	}
}

// TestReplay cubre los tres escenarios de reanudacion del spec.
func TestReplay(t *testing.T) {
	bus := New(true, 5)
	for i := 0; i < 5; i++ {
		bus.Publish(line(fmt.Sprintf("relay.%d", i)))
	}

	t.Run("desde un seq intermedio", func(t *testing.T) {
		got := bus.Replay(3)
		if len(got) != 2 {
			t.Fatalf("se devolvieron %d eventos, se esperaban 2", len(got))
		}
		if got[0].Seq != 4 || got[1].Seq != 5 {
			t.Errorf("se devolvieron los seq %d y %d, se esperaban 4 y 5", got[0].Seq, got[1].Seq)
		}
	})

	t.Run("desde un punto ya descartado", func(t *testing.T) {
		pequeno := New(true, 2)
		for i := 0; i < 6; i++ {
			pequeno.Publish(line("relay.received"))
		}
		// El seq 1 ya no esta retenido: se devuelve todo lo que queda, sin error.
		got := pequeno.Replay(1)
		if len(got) != 2 {
			t.Fatalf("se devolvieron %d eventos, se esperaba todo lo retenido (2)", len(got))
		}
		if got[0].Seq != 5 || got[1].Seq != 6 {
			t.Errorf("se devolvieron los seq %d y %d, se esperaban 5 y 6", got[0].Seq, got[1].Seq)
		}
	})

	t.Run("sin indicar punto de partida", func(t *testing.T) {
		if got := bus.Replay(0); len(got) != 5 {
			t.Errorf("sin punto de partida se devolvieron %d eventos, se esperaban los 5 retenidos", len(got))
		}
	})
}

// TestSubscribeAndUnsubscribe cubre los escenarios "Un suscriptor recibe lo que se publica" y "Se
// cancela la suscripcion".
func TestSubscribeAndUnsubscribe(t *testing.T) {
	bus := New(true, 10)

	var mutex sync.Mutex
	var primero, segundo []string
	collect := func(target *[]string) func(Event) {
		return func(event Event) {
			mutex.Lock()
			*target = append(*target, event.Name())
			mutex.Unlock()
		}
	}
	countOf := func(target *[]string) func() int {
		return func() int {
			mutex.Lock()
			defer mutex.Unlock()
			return len(*target)
		}
	}

	cancelarPrimero := bus.Subscribe(collect(&primero))
	defer cancelarPrimero()
	cancelarSegundo := bus.Subscribe(collect(&segundo))
	defer cancelarSegundo()

	bus.Publish(line("relay.received"))
	waitFor(t, "el primer suscriptor recibe", func() bool { return countOf(&primero)() == 1 })
	waitFor(t, "el segundo suscriptor recibe", func() bool { return countOf(&segundo)() == 1 })

	cancelarPrimero()
	if bus.SubscriberCount() != 1 {
		t.Errorf("tras cancelar quedan %d suscriptores, se esperaba 1", bus.SubscriberCount())
	}

	bus.Publish(line("relay.sent"))
	waitFor(t, "el que sigue suscripto recibe el segundo evento", func() bool { return countOf(&segundo)() == 2 })

	if countOf(&primero)() != 1 {
		t.Errorf("el suscriptor cancelado recibio %d eventos, se esperaba que no recibiera mas", countOf(&primero)())
	}
}

// TestSlowSubscriberDoesNotBlockPublish: un observador que no lee pierde eventos, no frena el
// relay, y los demas siguen recibiendo. Es el trade-off explicito de D4.
func TestSlowSubscriberDoesNotBlockPublish(t *testing.T) {
	bus := New(true, 10)

	bloqueado := make(chan struct{})
	cancelarLento := bus.Subscribe(func(Event) { <-bloqueado })
	defer func() { close(bloqueado); cancelarLento() }()

	var mutex sync.Mutex
	recibidos := 0
	cancelarSano := bus.Subscribe(func(Event) {
		mutex.Lock()
		recibidos++
		mutex.Unlock()
	})
	defer cancelarSano()

	// Muchos mas eventos que el buffer por suscriptor: el lento se satura y se le descartan.
	hecho := make(chan struct{})
	go func() {
		for i := 0; i < subscriberBuffer*3; i++ {
			bus.Publish(line("relay.received"))
		}
		close(hecho)
	}()

	select {
	case <-hecho:
	case <-time.After(5 * time.Second):
		t.Fatal("Publish se bloqueo por un suscriptor que no lee")
	}

	waitFor(t, "el suscriptor sano recibe todo", func() bool {
		mutex.Lock()
		defer mutex.Unlock()
		return recibidos == subscriberBuffer*3
	})
}

// TestPanickingSubscriberIsIsolated: un suscriptor que entra en panico no tumba el proceso ni
// impide que los demas reciban el mismo evento.
func TestPanickingSubscriberIsIsolated(t *testing.T) {
	bus := New(true, 10)

	var mutex sync.Mutex
	entroEnPanico := 0
	recibidosSano := 0

	cancelarRoto := bus.Subscribe(func(Event) {
		mutex.Lock()
		entroEnPanico++
		mutex.Unlock()
		panic("el suscriptor exploto")
	})
	defer cancelarRoto()

	cancelarSano := bus.Subscribe(func(Event) {
		mutex.Lock()
		recibidosSano++
		mutex.Unlock()
	})
	defer cancelarSano()

	bus.Publish(line("relay.received"))
	bus.Publish(line("relay.sent"))

	waitFor(t, "el suscriptor sano recibe los dos eventos", func() bool {
		mutex.Lock()
		defer mutex.Unlock()
		return recibidosSano == 2
	})
	// El que entra en panico se recupera y sigue atendiendo: el panico no mata su goroutine.
	waitFor(t, "el suscriptor roto sigue recibiendo tras el panico", func() bool {
		mutex.Lock()
		defer mutex.Unlock()
		return entroEnPanico == 2
	})
}

// TestDisabledBusRetainsNothing cubre el escenario "Publicacion con el dashboard apagado".
func TestDisabledBusRetainsNothing(t *testing.T) {
	apagado := New(false, 500)
	for i := 0; i < 100; i++ {
		apagado.Publish(line("relay.received"))
	}
	if got := len(apagado.Replay(0)); got != 0 {
		t.Errorf("con el dashboard apagado se retuvieron %d eventos", got)
	}
	if apagado.Enabled() {
		t.Error("New(false, ...) debe dejar el bus inerte")
	}

	// Un bufferSize 0 explicito deja el bus inerte aunque este habilitado.
	sinCapacidad := New(true, 0)
	sinCapacidad.Publish(line("relay.received"))
	if got := len(sinCapacidad.Replay(0)); got != 0 {
		t.Errorf("con capacidad 0 se retuvieron %d eventos", got)
	}
	if sinCapacidad.Enabled() {
		t.Error("capacidad 0 debe dejar el bus inerte")
	}
}

// TestPublishDoesNotMutateTheCallersLine: el bus se queda con una copia.
func TestPublishDoesNotMutateTheCallersLine(t *testing.T) {
	bus := New(true, 5)
	original := line("relay.received")
	bus.Publish(original)

	if _, yaTiene := original["seq"]; yaTiene {
		t.Error("el bus no debe escribir el seq en el mapa del que publica")
	}
	original["event"] = "modificado despues"
	if bus.Replay(0)[0].Name() != "relay.received" {
		t.Error("lo retenido cambio cuando el llamador modifico su mapa")
	}
}

// BenchmarkPublishDisabled contra BenchmarkPublishEnabled: con el dashboard apagado publicar no
// toma ningun candado y el costo por evento es despreciable frente al camino habilitado.
func BenchmarkPublishDisabled(b *testing.B) {
	bus := New(false, 500)
	l := line("relay.received")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bus.Publish(l)
	}
}

func BenchmarkPublishEnabled(b *testing.B) {
	bus := New(true, 500)
	l := line("relay.received")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bus.Publish(l)
	}
}
