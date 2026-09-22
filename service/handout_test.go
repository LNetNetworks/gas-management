package service

import (
	"context"
	"testing"
	"time"

	"github.com/LACNetNetworks/gas-relay-signer/events"
)

// Con el reparto APAGADO -el default- consultar no cambia ningun estado y la respuesta con y sin
// `peek` es la misma. Cubre la tarea 5.1.
func TestWithoutHandoutPeekChangesNothing(t *testing.T) {
	events.Init(true, 50)
	defer events.Init(false, 0)
	node := newFakeNode(345)
	defer node.close()
	service := reorderingService(node, 3000, 16)

	quien := user("0b")

	conPeek, err := service.NonceOf(context.Background(), quien, true)
	if err != nil {
		t.Fatalf("la consulta con peek fallo: %v", err)
	}
	sinPeek, err := service.NonceOf(context.Background(), quien, false)
	if err != nil {
		t.Fatalf("la consulta sin peek fallo: %v", err)
	}

	if conPeek.Next.Cmp(sinPeek.Next) != 0 || conPeek.OnChain.Cmp(sinPeek.OnChain) != 0 {
		t.Errorf("con el reparto apagado las dos consultas tienen que dar lo mismo: %v vs %v",
			conPeek.Next, sinPeek.Next)
	}
	if _, reservado := service.cachedNonce(quien.Hex()); reservado {
		t.Error("con el reparto apagado consultar no puede reservar nada")
	}
}

// Con el reparto encendido, el segundo que pregunta espera al primero y se lleva el nonce siguiente
// cuando la metatx del primero llega. Cubre la tarea 5.2.
func TestHandoutSerializesQueriesOfTheSameUser(t *testing.T) {
	events.Init(true, 100)
	defer events.Init(false, 0)
	node := newFakeNode(345)
	defer node.close()
	service := reorderingService(node, 3000, 16)
	service.Config.Reorder.AutoNonce = true
	service.Config.Reorder.AutoNonceTicketMs = 5000

	quien := user("0b")

	primero, err := service.HandOutNonce(quien.Hex(), false)
	if err != nil {
		t.Fatalf("el primer pedido fallo: %v", err)
	}
	if primero != 345 {
		t.Errorf("el primero deberia llevarse el 345, se llevo %d", primero)
	}

	// El segundo queda esperando el ticket del primero.
	segundo := make(chan uint64, 1)
	go func() {
		nonce, err := service.HandOutNonce(quien.Hex(), false)
		if err != nil {
			t.Errorf("el segundo pedido fallo: %v", err)
			return
		}
		segundo <- nonce
	}()

	select {
	case nonce := <-segundo:
		t.Fatalf("el segundo no deberia haberse llevado el %d todavia: el ticket del primero sigue abierto", nonce)
	case <-time.After(150 * time.Millisecond):
	}

	// Llega la metatx del primero: cierra su ticket y avanza el proximo nonce.
	if _, err := service.ReserveGasAndSend(context.Background(), metaTxOf(quien, primero)); err != nil {
		t.Fatalf("la metatx del primero deberia enviarse: %v", err)
	}

	select {
	case nonce := <-segundo:
		if nonce != primero+1 {
			t.Errorf("el segundo deberia llevarse el %d, se llevo %d", primero+1, nonce)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("el segundo quedo esperando aunque la metatx del primero ya llego")
	}
}

// Un numero entregado y nunca usado no tapa la cola: al vencer su ticket, el siguiente se lleva ese
// mismo numero. Cubre la tarea 5.3.
func TestUnusedHandoutDoesNotBlockTheNumber(t *testing.T) {
	events.Init(true, 50)
	defer events.Init(false, 0)
	node := newFakeNode(345)
	defer node.close()
	service := reorderingService(node, 3000, 16)
	service.Config.Reorder.AutoNonce = true
	service.Config.Reorder.AutoNonceTicketMs = 60

	quien := user("0b")

	primero, err := service.HandOutNonce(quien.Hex(), false)
	if err != nil {
		t.Fatalf("el primer pedido fallo: %v", err)
	}

	// El primero nunca envia su metatx: su ticket vence.
	segundo, err := service.HandOutNonce(quien.Hex(), false)
	if err != nil {
		t.Fatalf("el segundo pedido fallo: %v", err)
	}

	if segundo != primero {
		t.Errorf("un ticket vencido no puede tapar el numero: primero %d, segundo %d", primero, segundo)
	}
}

// Con el reparto encendido, `peek` sigue sin reservar: informa sin tomar posicion en la cola.
// Cubre la tarea 5.4.
func TestPeekDoesNotTakeATicket(t *testing.T) {
	events.Init(true, 50)
	defer events.Init(false, 0)
	node := newFakeNode(345)
	defer node.close()
	service := reorderingService(node, 3000, 16)
	service.Config.Reorder.AutoNonce = true
	service.Config.Reorder.AutoNonceTicketMs = 5000

	quien := user("0b")

	// Una consulta con peek no abre ticket: la siguiente sin peek no tiene a quien esperar.
	if _, err := service.NonceOf(context.Background(), quien, true); err != nil {
		t.Fatalf("la consulta con peek fallo: %v", err)
	}

	listo := make(chan struct{})
	go func() {
		defer close(listo)
		if _, err := service.HandOutNonce(quien.Hex(), false); err != nil {
			t.Errorf("el pedido sin peek fallo: %v", err)
		}
	}()

	select {
	case <-listo:
	case <-time.After(time.Second):
		t.Fatal("peek dejo la cola tomada: el pedido siguiente quedo esperando")
	}
}

// Las dos puertas reparten de la MISMA secuencia: un numero entregado por una no lo vuelve a
// entregar la otra.
func TestBothDoorsHandOutFromTheSameSequence(t *testing.T) {
	events.Init(true, 100)
	defer events.Init(false, 0)
	node := newFakeNode(345)
	defer node.close()
	service := reorderingService(node, 3000, 16)
	service.Config.Reorder.AutoNonce = true
	service.Config.Reorder.AutoNonceTicketMs = 5000

	quien := user("0b")

	// Por el camino JSON-RPC.
	porRPC, err := service.HandOutNonce(quien.Hex(), false)
	if err != nil {
		t.Fatalf("el pedido por el camino JSON-RPC fallo: %v", err)
	}

	porRuta := make(chan uint64, 1)
	go func() {
		state, err := service.NonceOf(context.Background(), quien, false)
		if err != nil {
			t.Errorf("el pedido por GET /nonce fallo: %v", err)
			return
		}
		porRuta <- state.Next.Uint64()
	}()

	// La metatx del primero llega y avanza la secuencia.
	if _, err := service.ReserveGasAndSend(context.Background(), metaTxOf(quien, porRPC)); err != nil {
		t.Fatalf("la metatx deberia enviarse: %v", err)
	}

	select {
	case nonce := <-porRuta:
		if nonce == porRPC {
			t.Errorf("las dos puertas entregaron el mismo numero %d", nonce)
		}
		if nonce != porRPC+1 {
			t.Errorf("la segunda puerta deberia entregar el %d, entrego %d", porRPC+1, nonce)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("la consulta por GET /nonce quedo esperando")
	}
}
