package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/LACNetNetworks/gas-relay-signer/events"
	"github.com/ethereum/go-ethereum/common"
)

// relayedReceipt es un receipt con el evento TransactionRelayed del RelayHub: la metatx se ejecuto
// bien. `%s` es el hash de la transaccion.
const relayedReceiptTemplate = `{"jsonrpc":"2.0","id":1,"result":{
  "blockHash":"0x6e3aa24e261e61832624749b64049104c6105ba870d3375484548ffdb133eeea",
  "blockNumber":"0xaae545","contractAddress":null,"cumulativeGasUsed":"0x309f0",
  "from":"0xd00e6624a73f88b39f82ab34e8bf2b4d226fd768","gasUsed":"0x309f0",
  "logs":[{"address":"0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91",
    "topics":["%TOPIC%"],
    "data":"0x%DATA%","blockNumber":"0xaae545",
    "transactionHash":"%HASH%","transactionIndex":"0x0",
    "blockHash":"0x6e3aa24e261e61832624749b64049104c6105ba870d3375484548ffdb133eeea",
    "logIndex":"0x0","removed":false}],
  "logsBloom":"0x%BLOOM%","status":"0x1",
  "to":"0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91",
  "transactionHash":"%HASH%","transactionIndex":"0x0"}}`

// relayedReceipt arma el receipt de una metatx ejecutada con exito.
func relayedReceipt() string {
	pad := func(value string) string { return strings.Repeat("0", 64-len(value)) + value }
	receipt := strings.ReplaceAll(relayedReceiptTemplate, "%TOPIC%", topicsOfHub().transactionRelay)
	receipt = strings.ReplaceAll(receipt, "%DATA%", pad("1")+pad("40")+pad("0"))
	receipt = strings.ReplaceAll(receipt, "%HASH%", "0x"+pad("1"))
	receipt = strings.ReplaceAll(receipt, "%BLOOM%", strings.Repeat("0", 512))
	return receipt
}

func settledEvents() []events.Event {
	var settled []events.Event
	for _, event := range events.Replay(0) {
		if event.Name() == "relay.settled" {
			settled = append(settled, event)
		}
	}
	return settled
}

// Una metatx relayada por el camino JSON-RPC y nunca consultada igual deja su cierre: el watcher lo
// detecta sin que el cliente pregunte. Cubre la tarea 4.1.
func TestWatcherSettlesWithoutTheClientAsking(t *testing.T) {
	events.Init(true, 100)
	defer events.Init(false, 0)
	node := newFakeNode(345)
	defer node.close()
	service := reorderingService(node, 3000, 16)

	quien := user("0b")
	hash, err := service.ReserveGasAndSend(context.Background(), metaTxOf(quien, 345))
	if err != nil {
		t.Fatalf("la metatx deberia enviarse: %v", err)
	}
	if len(settledEvents()) != 0 {
		t.Fatal("todavia no se puede haber cerrado: nadie consulto el receipt")
	}

	node.respondReceipt(relayedReceipt())
	service.SettleInFlight(context.Background())

	settled := settledEvents()
	if len(settled) != 1 {
		t.Fatalf("se esperaba un relay.settled, hubo %d", len(settled))
	}
	// El campo viaja como puntero -asi se emite desde siempre- y serializa a true/false.
	executed, ok := settled[0].Field("executed").(*bool)
	if !ok || executed == nil || !*executed {
		t.Errorf("la metatx se ejecuto bien: executed = %v", settled[0].Field("executed"))
	}
	_ = hash
}

// Sin nada en vuelo el watcher no habla con el nodo. Cubre la tarea 4.2.
func TestWatcherIsSilentWithNothingInFlight(t *testing.T) {
	events.Init(true, 100)
	defer events.Init(false, 0)
	node := newFakeNode(345)
	defer node.close()
	service := reorderingService(node, 3000, 16)

	antes := node.requests()
	for i := 0; i < 5; i++ {
		service.SettleInFlight(context.Background())
	}
	if node.requests() != antes {
		t.Errorf("el watcher hizo %d llamadas sin nada en vuelo", node.requests()-antes)
	}
}

// El cierre se registra UNA sola vez, sin importar quien llegue primero. Cubre la tarea 4.3.
func TestSettleHappensOnlyOnce(t *testing.T) {
	events.Init(true, 100)
	defer events.Init(false, 0)
	node := newFakeNode(345)
	defer node.close()
	service := reorderingService(node, 3000, 16)

	quien := user("0b")
	if _, err := service.ReserveGasAndSend(context.Background(), metaTxOf(quien, 345)); err != nil {
		t.Fatalf("la metatx deberia enviarse: %v", err)
	}

	node.respondReceipt(relayedReceipt())
	service.SettleInFlight(context.Background()) // el watcher llega primero
	service.SettleInFlight(context.Background()) // y despues otro bloque, o el cliente

	if settled := settledEvents(); len(settled) != 1 {
		t.Errorf("el cierre se registro %d veces, tiene que registrarse una sola", len(settled))
	}
}

// Al cerrarse una metatx baja lo en vuelo y las retenidas de ese usuario reevaluan su turno. Cubre
// la tarea 4.4.
func TestSettleReleasesInFlightAndWakesTheHeld(t *testing.T) {
	events.Init(true, 200)
	defer events.Init(false, 0)
	node := newFakeNode(345)
	defer node.close()
	service := reorderingService(node, 3000, 2)

	quien := user("0b")
	if _, err := service.ReserveGasAndSend(context.Background(), metaTxOf(quien, 345)); err != nil {
		t.Fatalf("la primera deberia enviarse: %v", err)
	}
	if service.pendingOf(quien.Hex()) != 1 {
		t.Fatalf("en vuelo esperado 1, fue %d", service.pendingOf(quien.Hex()))
	}

	node.respondReceipt(relayedReceipt())
	service.SettleInFlight(context.Background())

	if pending := service.pendingOf(quien.Hex()); pending != 0 {
		t.Errorf("tras el cierre, en vuelo esperado 0, fue %d", pending)
	}
	// La cadena sobrevive por la gracia: el proximo nonce sigue siendo el reservado.
	if next, ok := service.cachedNonce(quien.Hex()); !ok || next != 346 {
		t.Errorf("durante la gracia el proximo nonce deberia ser 346, fue %d (ok=%v)", next, ok)
	}
}

// Una metatx cuyo resultado nunca llega no puede quedar en vuelo para siempre. Cubre la tarea 4.5.
func TestUnresolvedMetaTxExpires(t *testing.T) {
	events.Init(true, 100)
	defer events.Init(false, 0)
	node := newFakeNode(345)
	defer node.close()
	service := reorderingService(node, 3000, 16)
	// Plazo minusculo: la metatx vence enseguida.
	service.Config.Reorder.ReceiptTimeoutMs = 1

	quien := user("0b")
	if _, err := service.ReserveGasAndSend(context.Background(), metaTxOf(quien, 345)); err != nil {
		t.Fatalf("la metatx deberia enviarse: %v", err)
	}

	time.Sleep(20 * time.Millisecond)
	// El nodo no tiene receipt para ella: nunca se resolvio.
	service.SettleInFlight(context.Background())

	var fallidos int
	for _, event := range events.Replay(0) {
		if event.Name() == "relay.settle_failed" {
			fallidos++
		}
	}
	if fallidos != 1 {
		t.Errorf("se esperaba un relay.settle_failed, hubo %d", fallidos)
	}
	if pending := service.pendingOf(quien.Hex()); pending != 0 {
		t.Errorf("una metatx vencida tiene que liberar su lugar, quedan %d en vuelo", pending)
	}
}

// El nodo caido no tumba el proceso ni pierde lo que hay en vuelo: se reintenta en el proximo
// bloque. Cubre la tarea 4.6.
func TestWatcherSurvivesTheNodeGoingDown(t *testing.T) {
	events.Init(true, 100)
	defer events.Init(false, 0)
	node := newFakeNode(345)
	service := reorderingService(node, 3000, 16)

	quien := user("0b")
	if _, err := service.ReserveGasAndSend(context.Background(), metaTxOf(quien, 345)); err != nil {
		t.Fatalf("la metatx deberia enviarse: %v", err)
	}

	node.close() // el nodo se cae

	service.SettleInFlight(context.Background())

	if pending := service.pendingOf(quien.Hex()); pending != 1 {
		t.Errorf("con el nodo caido la metatx sigue en vuelo, se esperaba 1 y hay %d", pending)
	}
	if len(settledEvents()) != 0 {
		t.Error("con el nodo caido no se puede afirmar como termino ninguna metatx")
	}
}

// El hub rechaza la metatx: se registra el rechazo y se descarta la cadena de nonces del usuario.
func TestWatcherHandlesAHubRejection(t *testing.T) {
	events.Init(true, 100)
	defer events.Init(false, 0)
	node := newFakeNode(345)
	defer node.close()
	service := reorderingService(node, 3000, 16)

	quien := common.HexToAddress("0x82A978B3f5962A5b0957d9ee9eEf472EE55B42F1")
	if _, err := service.ReserveGasAndSend(context.Background(), metaTxOf(quien, 345)); err != nil {
		t.Fatalf("la metatx deberia enviarse: %v", err)
	}

	node.respondReceipt(badTransactionReceipt)
	service.SettleInFlight(context.Background())

	var rechazos int
	for _, event := range events.Replay(0) {
		if event.Name() == "relay.hub_rejected" {
			rechazos++
		}
	}
	if rechazos != 1 {
		t.Errorf("se esperaba un relay.hub_rejected, hubo %d", rechazos)
	}
	if _, ok := service.cachedNonce(quien.Hex()); ok {
		t.Error("un rechazo del hub tiene que descartar la cadena de nonces de ese usuario")
	}
}

// Cerrar una metatx despierta a las retenidas de ese usuario para que reevaluen su turno: sin eso,
// la que espera se entera recien cuando vence su propio temporizador. Cubre la otra mitad de 4.4.
func TestReleaseWakesTheWaiters(t *testing.T) {
	service := trackerService(3000)
	key := senderKey("0xa0f03c489a1bcd53883289d3c476100220b21b0b")

	_, chain := reserveNext(service, key, 10)

	// Un waiter es, por construccion, una metatx retenida: primero se registra y recien despues se
	// duerme sobre su canal.
	entry := service.hold(key, 11)
	service.sendersLock.Lock()
	wake := service.waitTurnLocked(entry)
	service.sendersLock.Unlock()

	service.releaseChain(key, chain)

	select {
	case <-wake:
	case <-time.After(time.Second):
		t.Error("liberar una metatx tiene que despertar a las retenidas de ese usuario")
	}
}
