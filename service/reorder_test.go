package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LACNetNetworks/gas-relay-signer/events"
	"github.com/LACNetNetworks/gas-relay-signer/model"
	"github.com/ethereum/go-ethereum/common"
)

// Un nodo falso que responde el camino completo de envio, para poder probar el reordenamiento
// contra `ReserveGasAndSend` y no contra una imitacion suya.
//
// Lleva la cuenta de cuantas transacciones se enviaron: es lo que permite comprobar que un rechazo
// NO gasto una transaccion del writer node, que es el punto de todo este cambio.
type fakeNode struct {
	server *httptest.Server

	// hubNonce es el nonce que el hub dice para cualquier usuario. Los tests lo fijan una vez; de
	// ahi en adelante manda el tracker.
	hubNonce uint64

	sent     int64
	failSend atomic.Bool

	// calls cuenta TODAS las peticiones que recibe: es lo que permite comprobar que sin nada en
	// vuelo el watcher no llama al nodo.
	calls int64
	// sendDelay simula un nodo lento al enviar. Es lo que permite distinguir una ventana que mide
	// estancamiento de una que mide espera total, sin depender de la velocidad de la maquina.
	sendDelay atomic.Int64
	// receipt es lo que se responde a eth_getTransactionReceipt. Vacio significa que la metatx
	// todavia no se mino.
	receipt atomic.Value
}

// Selectores del modelo de gas que el nodo tiene que distinguir.
const (
	selectorRelayHubFromProxy = "7bdf2ec7"
	selectorGetNonce          = "2d0335ab"
)

func newFakeNode(hubNonce uint64) *fakeNode {
	node := &fakeNode{hubNonce: hubNonce}
	node.server = httptest.NewServer(http.HandlerFunc(node.handle))
	return node
}

func (node *fakeNode) close() { node.server.Close() }

// sends es cuantas transacciones del writer node se gastaron.
func (node *fakeNode) sends() int { return int(atomic.LoadInt64(&node.sent)) }

// respondReceipt fija lo que el nodo responde al pedir un receipt.
func (node *fakeNode) respondReceipt(receipt string) { node.receipt.Store(receipt) }

// requests es cuantas peticiones recibio el nodo.
func (node *fakeNode) requests() int { return int(atomic.LoadInt64(&node.calls)) }

func (node *fakeNode) handle(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&node.calls, 1)
	body, _ := io.ReadAll(r.Body)
	text := string(body)
	var request struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	_ = json.Unmarshal(body, &request)
	w.Header().Set("Content-Type", "application/json")

	word := func(value string) string {
		return "0x" + strings.Repeat("0", 64-len(value)) + value
	}

	switch {
	case strings.Contains(text, selectorRelayHubFromProxy):
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":"%s"}`, word("ff6d55d01fb12695ea00c071ad8af3ce44cf3a91"))
	case strings.Contains(text, selectorGetNonce):
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":"%s"}`, word(fmt.Sprintf("%x", node.hubNonce)))
	case request.Method == "eth_call":
		// Cupo de gas del nodo: generoso, para que nunca sea el motivo del rechazo.
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":"%s"}`, word("3b9aca00"))
	case request.Method == "eth_getTransactionReceipt":
		receipt, _ := node.receipt.Load().(string)
		if receipt == "" {
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":null}`)
			return
		}
		fmt.Fprint(w, receipt)
	case request.Method == "eth_getTransactionCount":
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"0x6"}`)
	case request.Method == "eth_sendRawTransaction":
		if node.failSend.Load() {
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"the node rejected the transaction"}}`)
			return
		}
		if delay := node.sendDelay.Load(); delay > 0 {
			time.Sleep(time.Duration(delay) * time.Millisecond)
		}
		count := atomic.AddInt64(&node.sent, 1)
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":"%s"}`, word(fmt.Sprintf("%x", count)))
	default:
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":null}`)
	}
}

// reorderingService arma un servicio apuntado al nodo falso, con el reordenamiento encendido.
func reorderingService(node *fakeNode, windowMs, maxInflight int) *RelaySignerService {
	relayHub := common.HexToAddress("0xfF6D55d01FB12695EA00c071aD8aF3CE44cF3A91")
	service := new(RelaySignerService)
	service.Config = &model.Config{Application: model.ApplicationConfig{
		NodeURL: node.server.URL,
		Key:     "b3e7374dca5ca90c3899dbb2c978051437fb15534c945bf59df16d6c80be27c0",
	}}
	service.Config.Application.RelayHubContractAddress = &relayHub
	service.Config.Reorder = model.ReorderConfig{
		Enabled:            true,
		WindowMs:           windowMs,
		MaxInflightPerUser: maxInflight,
		ReceiptTimeoutMs:   60000,
	}
	service.senders = make(map[string]*nonceEntry)
	service.metaTx = make(map[common.Hash]*metaTxEntry)
	return service
}

// metaTxOf arma una metatx lista para enviar. No hace falta una raw tx valida: lo que se prueba es
// el camino de reserva y envio, que trabaja sobre la metatx ya decodificada.
func metaTxOf(from common.Address, nonce uint64) *PreparedMetaTx {
	to := common.HexToAddress("0x6E6bBf31aA45042D53128339383fcd1c377B42c7")
	return &PreparedMetaTx{
		From:           from,
		To:             &to,
		SigningData:    []byte{0x01, 0x02, 0x03},
		V:              27,
		Nonce:          nonce,
		MetaTxGasLimit: 2303780,
		SenderKey:      from.Hex(),
	}
}

func user(last string) common.Address {
	return common.HexToAddress("0xa0f03c489a1bcd53883289d3c476100220b21b" + last)
}

// hubNoncesSent son los `hubNonce` de los relay.sent que quedaron en el bus, en orden de emision.
func hubNoncesSent() []uint64 {
	var nonces []uint64
	for _, event := range events.Replay(0) {
		if event.Name() != "relay.sent" {
			continue
		}
		if nonce, ok := event.Field("hubNonce").(uint64); ok {
			nonces = append(nonces, nonce)
		}
	}
	return nonces
}

func eventNames() []string {
	var names []string
	for _, event := range events.Replay(0) {
		names = append(names, event.Name())
	}
	return names
}

func countEvents(name string) int {
	count := 0
	for _, event := range events.Replay(0) {
		if event.Name() == name {
			count++
		}
	}
	return count
}

// ---------------------------------------------------------------- seccion 2

// Dos metatx del mismo usuario con nonces consecutivos se envian EN ORDEN de nonce: es lo que hace
// que la `n` se mine antes que la `n+1` y que encadenar funcione.
func TestSameUserSendsInNonceOrder(t *testing.T) {
	events.Init(true, 100)
	defer events.Init(false, 0)
	node := newFakeNode(345)
	defer node.close()
	service := reorderingService(node, 3000, 16)

	quien := user("0b")
	if _, err := service.ReserveGasAndSend(context.Background(), metaTxOf(quien, 345)); err != nil {
		t.Fatalf("la primera metatx deberia enviarse: %v", err)
	}
	if _, err := service.ReserveGasAndSend(context.Background(), metaTxOf(quien, 346)); err != nil {
		t.Fatalf("la segunda metatx deberia enviarse: %v", err)
	}

	enviados := hubNoncesSent()
	if len(enviados) != 2 || enviados[0] != 345 || enviados[1] != 346 {
		t.Errorf("orden de envio esperado [345 346], fue %v", enviados)
	}
	if pending := service.pendingOf(quien.Hex()); pending != 2 {
		t.Errorf("en vuelo esperado 2, fue %d", pending)
	}
}

// Metatx de usuarios distintos no se esperan entre si: el candado es por usuario, no global.
func TestDifferentUsersDoNotWaitForEachOther(t *testing.T) {
	events.Init(true, 100)
	defer events.Init(false, 0)
	node := newFakeNode(345)
	defer node.close()
	// La ventana es corta para que el retenido termine DENTRO del test. Si sobrevive, emite su
	// relay.turn cuando el bus ya es el del test siguiente, y lo hace fallar por eventos ajenos.
	service := reorderingService(node, 500, 16)

	// Uno queda retenido -su nonce esta adelantado- y el otro tiene que pasar igual.
	retenido := make(chan struct{})
	go func() {
		defer close(retenido)
		_, _ = service.ReserveGasAndSend(context.Background(), metaTxOf(user("0b"), 400))
	}()

	waitUntil(t, "el primero queda retenido", func() bool { return service.inflightOf(user("0b").Hex()) > 0 })

	listo := make(chan error, 1)
	go func() {
		_, err := service.ReserveGasAndSend(context.Background(), metaTxOf(user("0c"), 345))
		listo <- err
	}()

	select {
	case err := <-listo:
		if err != nil {
			t.Fatalf("el segundo usuario deberia poder enviar mientras el primero espera: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("el segundo usuario quedo esperando al primero: el candado no es por usuario")
	}

	// El retenido tiene que terminar ACA, no despues de que el test cierre el nodo.
	select {
	case <-retenido:
	case <-time.After(5 * time.Second):
		t.Error("el retenido sobrevivio al test: va a contaminar el bus de eventos del siguiente")
	}
}

// Un nonce ya consumido se rechaza ANTES de gastar una transaccion del writer node.
func TestNonceAlreadyConsumedIsRejectedWithoutSending(t *testing.T) {
	events.Init(true, 100)
	defer events.Init(false, 0)
	node := newFakeNode(345)
	defer node.close()
	service := reorderingService(node, 3000, 16)

	_, err := service.ReserveGasAndSend(context.Background(), metaTxOf(user("0b"), 344))
	if err == nil {
		t.Fatal("una metatx con un nonce ya consumido tiene que rechazarse")
	}
	if code := CodeOf(err); code != CodeBadNonce {
		t.Errorf("codigo esperado %s, fue %s", CodeBadNonce, code)
	}
	details := DetailsOf(err)
	if details["expected"] != uint64(345) || details["got"] != uint64(344) {
		t.Errorf("el detalle deberia indicar el esperado y el recibido, fue %v", details)
	}
	if node.sends() != 0 {
		t.Errorf("no se tenia que gastar ninguna transaccion del writer node, se gastaron %d", node.sends())
	}
}

// Encadenar varias metatx sin esperar a que se minen: ninguna se rechaza por traer un nonce que la
// cadena de bloques todavia no refleja.
func TestChainedMetaTxAreAccepted(t *testing.T) {
	events.Init(true, 100)
	defer events.Init(false, 0)
	node := newFakeNode(345)
	defer node.close()
	service := reorderingService(node, 3000, 16)

	quien := user("0b")
	for nonce := uint64(345); nonce < 351; nonce++ {
		if _, err := service.ReserveGasAndSend(context.Background(), metaTxOf(quien, nonce)); err != nil {
			t.Fatalf("la metatx con nonce %d deberia enviarse: %v", nonce, err)
		}
	}

	if node.sends() != 6 {
		t.Errorf("se esperaban 6 envios, hubo %d", node.sends())
	}
	if pending := service.pendingOf(quien.Hex()); pending != 6 {
		t.Errorf("en vuelo esperado 6, fue %d", pending)
	}
}

// Una rafaga concurrente de varios usuarios no puede producir carreras ni bloqueos. Corre con -race.
func TestConcurrentBurstAcrossUsers(t *testing.T) {
	events.Init(true, 500)
	defer events.Init(false, 0)
	node := newFakeNode(345)
	defer node.close()
	service := reorderingService(node, 1000, 16)

	var wg sync.WaitGroup
	for u := 0; u < 6; u++ {
		quien := user(fmt.Sprintf("%02x", 0x10+u))
		wg.Add(1)
		go func(quien common.Address) {
			defer wg.Done()
			for nonce := uint64(345); nonce < 350; nonce++ {
				if _, err := service.ReserveGasAndSend(context.Background(), metaTxOf(quien, nonce)); err != nil {
					t.Errorf("%s nonce %d: %v", quien.Hex(), nonce, err)
					return
				}
			}
		}(quien)
	}
	wg.Wait()

	if node.sends() != 30 {
		t.Errorf("se esperaban 30 envios, hubo %d", node.sends())
	}
}

// Un envio que falla descarta la cadena entera: reservar sobre un hueco no sirve.
func TestFailedSendForgetsTheChain(t *testing.T) {
	events.Init(true, 100)
	defer events.Init(false, 0)
	node := newFakeNode(345)
	defer node.close()
	service := reorderingService(node, 3000, 16)

	quien := user("0b")
	if _, err := service.ReserveGasAndSend(context.Background(), metaTxOf(quien, 345)); err != nil {
		t.Fatalf("la primera metatx deberia enviarse: %v", err)
	}

	node.failSend.Store(true)
	if _, err := service.ReserveGasAndSend(context.Background(), metaTxOf(quien, 346)); err == nil {
		t.Fatal("un envio que falla tiene que devolver error")
	}

	if _, ok := service.cachedNonce(quien.Hex()); ok {
		t.Error("tras un envio fallido la cadena tiene que descartarse entera")
	}

	// La proxima se valida contra la cadena de bloques, no contra lo que se habia reservado.
	node.failSend.Store(false)
	if _, err := service.ReserveGasAndSend(context.Background(), metaTxOf(quien, 345)); err != nil {
		t.Errorf("la proxima metatx deberia validarse contra la cadena de bloques: %v", err)
	}
}

// ---------------------------------------------------------------- seccion 3

// Dos metatx que llegan en orden invertido se envian las dos, en orden de nonce.
func TestOutOfOrderArrivalIsReordered(t *testing.T) {
	events.Init(true, 100)
	defer events.Init(false, 0)
	node := newFakeNode(345)
	defer node.close()
	service := reorderingService(node, 3000, 16)

	quien := user("0b")

	adelantada := make(chan error, 1)
	go func() {
		_, err := service.ReserveGasAndSend(context.Background(), metaTxOf(quien, 346))
		adelantada <- err
	}()

	// Se espera a que quede retenida antes de mandar la que faltaba.
	waitUntil(t, "la metatx adelantada queda retenida y cuenta como en vuelo",
		func() bool { return service.inflightOf(quien.Hex()) > 0 })

	if _, err := service.ReserveGasAndSend(context.Background(), metaTxOf(quien, 345)); err != nil {
		t.Fatalf("la metatx que faltaba deberia enviarse: %v", err)
	}

	select {
	case err := <-adelantada:
		if err != nil {
			t.Fatalf("la retenida deberia enviarse al cerrarse el hueco: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("la retenida no se desperto al cerrarse el hueco")
	}

	enviados := hubNoncesSent()
	if len(enviados) != 2 || enviados[0] != 345 || enviados[1] != 346 {
		t.Errorf("orden de envio esperado [345 346], fue %v", enviados)
	}
	if countEvents("relay.held") != 1 || countEvents("relay.turn") != 1 {
		t.Errorf("se esperaba un relay.held y un relay.turn, hubo %v", eventNames())
	}
}

// Una metatx que nunca recibe su turno se rechaza con el mismo motivo que un nonce equivocado, y
// sin haber enviado nada.
func TestHeldMetaTxExpiresWithBadNonce(t *testing.T) {
	events.Init(true, 100)
	defer events.Init(false, 0)
	node := newFakeNode(345)
	defer node.close()
	service := reorderingService(node, 120, 16)

	_, err := service.ReserveGasAndSend(context.Background(), metaTxOf(user("0b"), 400))
	if err == nil {
		t.Fatal("una metatx retenida que vence tiene que rechazarse")
	}
	if code := CodeOf(err); code != CodeBadNonce {
		t.Errorf("codigo esperado %s, fue %s", CodeBadNonce, code)
	}
	if node.sends() != 0 {
		t.Errorf("una retenida que vence no puede haber enviado nada, se enviaron %d", node.sends())
	}

	var motivo interface{}
	for _, event := range events.Replay(0) {
		if event.Name() == "relay.turn" {
			motivo = event.Field("reason")
		}
	}
	if motivo != turnWindowExpired {
		t.Errorf("el motivo del fin de espera deberia ser %q, fue %v", turnWindowExpired, motivo)
	}
}

// La ventana mide ESTANCAMIENTO: una rafaga mas larga que la ventana, pero que avanza, no pierde
// ninguna metatx por reloj.
func TestWindowMeasuresStagnationNotTotalWait(t *testing.T) {
	events.Init(true, 200)
	defer events.Init(false, 0)
	node := newFakeNode(345)
	defer node.close()
	// La rafaga entera dura MUCHO mas que la ventana -cada envio tarda 200 ms y son seis-, pero
	// ningun tramo se estanca mas que ella. Si la ventana midiera la espera total, la cola se
	// caeria; como mide estancamiento, no se pierde ninguna.
	node.sendDelay.Store(200)
	service := reorderingService(node, 600, 16)

	quien := user("0b")
	const rafaga = 6

	esperando := make(chan error, rafaga)
	for nonce := uint64(346); nonce < 345+rafaga; nonce++ {
		go func(nonce uint64) {
			_, err := service.ReserveGasAndSend(context.Background(), metaTxOf(quien, nonce))
			esperando <- err
		}(nonce)
	}

	waitUntil(t, "toda la rafaga queda retenida",
		func() bool { return service.inflightOf(quien.Hex()) >= rafaga-1 })

	// La que faltaba llega, y despues el resto avanza de a una, mas lento que la ventana entera.
	arranque := time.Now()
	if _, err := service.ReserveGasAndSend(context.Background(), metaTxOf(quien, 345)); err != nil {
		t.Fatalf("la primera deberia enviarse: %v", err)
	}

	for i := 0; i < rafaga-1; i++ {
		select {
		case err := <-esperando:
			if err != nil {
				t.Fatalf("ninguna de la rafaga deberia perderse por reloj: %v", err)
			}
		case <-time.After(15 * time.Second):
			t.Fatal("la rafaga quedo trabada")
		}
	}

	// La comprobacion que le da sentido al test: la rafaga duro mas que la ventana.
	if duracion := time.Since(arranque); duracion < service.reorderWindow() {
		t.Fatalf("la rafaga duro %v, menos que la ventana: el test no probo nada", duracion)
	}

	if node.sends() != rafaga {
		t.Errorf("se esperaban %d envios, hubo %d", rafaga, node.sends())
	}
}

// El tope de metatx en vuelo por usuario: al superarlo se rechaza indicando cuantas hay y el
// maximo, y las de otros usuarios siguen atendiendose.
func TestInflightCapPerUser(t *testing.T) {
	events.Init(true, 200)
	defer events.Init(false, 0)
	node := newFakeNode(345)
	defer node.close()
	service := reorderingService(node, 3000, 3)

	quien := user("0b")
	for nonce := uint64(345); nonce < 348; nonce++ {
		if _, err := service.ReserveGasAndSend(context.Background(), metaTxOf(quien, nonce)); err != nil {
			t.Fatalf("la metatx con nonce %d deberia enviarse: %v", nonce, err)
		}
	}

	_, err := service.ReserveGasAndSend(context.Background(), metaTxOf(quien, 348))
	if err == nil {
		t.Fatal("la cuarta metatx tenia que rechazarse por el tope")
	}
	if code := CodeOf(err); code != CodeTooManyInflight {
		t.Errorf("codigo esperado %s, fue %s", CodeTooManyInflight, code)
	}
	details := DetailsOf(err)
	if details["inflight"] != 3 || details["max"] != 3 {
		t.Errorf("el detalle deberia indicar cuantas hay y el maximo, fue %v", details)
	}

	// Las que ya estaban en vuelo siguen su curso, y otro usuario no se ve afectado.
	if pending := service.pendingOf(quien.Hex()); pending != 3 {
		t.Errorf("las que ya estaban en vuelo tienen que seguir: %d", pending)
	}
	if _, err := service.ReserveGasAndSend(context.Background(), metaTxOf(user("0c"), 345)); err != nil {
		t.Errorf("otro usuario deberia poder enviar: %v", err)
	}
}

// Una metatx que se envia derecho no deja ni relay.held ni relay.turn.
func TestInTurnMetaTxLeavesNoHoldEvents(t *testing.T) {
	events.Init(true, 100)
	defer events.Init(false, 0)
	node := newFakeNode(345)
	defer node.close()
	service := reorderingService(node, 3000, 16)

	if _, err := service.ReserveGasAndSend(context.Background(), metaTxOf(user("0b"), 345)); err != nil {
		t.Fatalf("deberia enviarse: %v", err)
	}
	if countEvents("relay.held") != 0 || countEvents("relay.turn") != 0 {
		t.Errorf("una metatx en turno no deja eventos de retencion, hubo %v", eventNames())
	}
}

// Con el reordenamiento APAGADO no se entra al camino nuevo: no se valida el nonce, no se retiene y
// el watcher no hace nada. Lo que no se ejecuta no puede cambiar el comportamiento por accidente.
// Cubre la tarea 6.3.
func TestNothingNewRunsWithReorderingOff(t *testing.T) {
	events.Init(true, 100)
	defer events.Init(false, 0)
	node := newFakeNode(345)
	defer node.close()
	service := reorderingService(node, 3000, 16)
	service.Config.Reorder.Enabled = false

	quien := user("0b")

	// Un nonce que el hub no va a aceptar se envia igual, como antes de esta capacidad.
	if _, err := service.ReserveGasAndSend(context.Background(), metaTxOf(quien, 1)); err != nil {
		t.Fatalf("con el reordenamiento apagado la metatx se envia igual: %v", err)
	}
	if node.sends() != 1 {
		t.Errorf("se esperaba un envio, hubo %d", node.sends())
	}

	// No se cuenta lo en vuelo: nadie lo libera, asi que contarlo dejaria al usuario sin cupo.
	if pending := service.pendingOf(quien.Hex()); pending != 0 {
		t.Errorf("con el reordenamiento apagado no se cuenta lo en vuelo, hay %d", pending)
	}

	// Y el watcher no habla con el nodo.
	antes := node.requests()
	node.respondReceipt(relayedReceipt())
	service.SettleInFlight(context.Background())
	if node.requests() != antes {
		t.Errorf("con el reordenamiento apagado el watcher hizo %d llamadas", node.requests()-antes)
	}

	for _, event := range events.Replay(0) {
		if event.Name() == "relay.held" || event.Name() == "relay.turn" || event.Name() == "relay.settled" {
			t.Errorf("con el reordenamiento apagado no se puede emitir %s", event.Name())
		}
	}
}

// waitUntil espera a que se cumpla una condicion, en lugar de dormir un rato y suponer.
//
// Un `sleep` fijo como punto de sincronizacion convierte a un test en una apuesta sobre lo rapida
// que esta la maquina: bajo carga -o con el detector de carreras- la condicion todavia no se
// cumplio y el test falla sin que haya nada roto.
func waitUntil(t *testing.T, que string, condicion func() bool) {
	t.Helper()
	limite := time.Now().Add(5 * time.Second)
	for time.Now().Before(limite) {
		if condicion() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("no se cumplio a tiempo: %s", que)
}

// Una metatx desalojada por cupo se entera de por que: recibe el rechazo por tope superado, no el
// BAD_NONCE del vencimiento, y deja un relay.turn con el motivo real.
func TestEvictedMetaTxIsRejectedByCapNotByNonce(t *testing.T) {
	events.Init(true, 100)
	defer events.Init(false, 0)
	node := newFakeNode(345)
	defer node.close()
	service := reorderingService(node, 3000, 16)

	quien := user("0b")

	desalojada := make(chan error, 1)
	go func() {
		_, err := service.ReserveGasAndSend(context.Background(), metaTxOf(quien, 400))
		desalojada <- err
	}()

	waitUntil(t, "la adelantada queda retenida", func() bool { return service.inflightOf(quien.Hex()) > 0 })

	service.sendersLock.Lock()
	service.evictHeldLocked(senderKey(quien.Hex()), service.highestHeldLocked(senderKey(quien.Hex())))
	service.sendersLock.Unlock()

	select {
	case err := <-desalojada:
		if err == nil {
			t.Fatal("una desalojada por cupo tiene que rechazarse")
		}
		if code := CodeOf(err); code != CodeTooManyInflight {
			t.Errorf("codigo esperado %s, fue %s", CodeTooManyInflight, code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("la desalojada no desperto")
	}

	if node.sends() != 0 {
		t.Errorf("una desalojada no puede haber enviado nada, se enviaron %d", node.sends())
	}

	var motivo interface{}
	for _, event := range events.Replay(0) {
		if event.Name() == "relay.turn" {
			motivo = event.Field("reason")
		}
	}
	if motivo != turnTooManyInflight {
		t.Errorf("el motivo del fin de espera deberia ser %q, fue %v", turnTooManyInflight, motivo)
	}
}

// holdBurst deja retenidas las metatx de esos nonces y devuelve por donde llega el resultado de
// cada una. Todas quedan esperando porque ninguna es la que el hub espera a continuacion.
//
// Al terminar el test espera a que TODAS hayan terminado. Una retenida que sobrevive a su test
// emite su relay.turn al vencer la ventana, y para entonces el bus ya es el del test siguiente:
// el sintoma es un test que falla por eventos que no son suyos.
func holdBurst(t *testing.T, service *RelaySignerService, quien common.Address, nonces ...uint64) map[uint64]chan error {
	t.Helper()
	resultados := make(map[uint64]chan error, len(nonces))
	var enVuelo sync.WaitGroup
	for _, nonce := range nonces {
		// El canal tiene lugar para el resultado, asi que la goroutine termina lea el test o no.
		salida := make(chan error, 1)
		resultados[nonce] = salida
		enVuelo.Add(1)
		go func(nonce uint64, salida chan error) {
			defer enVuelo.Done()
			_, err := service.ReserveGasAndSend(context.Background(), metaTxOf(quien, nonce))
			salida <- err
		}(nonce, salida)
	}
	t.Cleanup(func() {
		terminadas := make(chan struct{})
		go func() { enVuelo.Wait(); close(terminadas) }()
		select {
		case <-terminadas:
		case <-time.After(10 * time.Second):
			t.Error("quedaron metatx de la rafaga sin terminar al final del test")
		}
	})
	waitUntil(t, "toda la rafaga queda retenida",
		func() bool { return service.inflightOf(quien.Hex()) == len(nonces) })
	return resultados
}

// Escenario "Se supera el tope": la que llega tiene el nonce mas alto de todas, asi que la que
// sobra es ella y las retenidas siguen su curso.
func TestCapRejectsTheArrivingWhenItIsTheHighest(t *testing.T) {
	events.Init(true, 200)
	defer events.Init(false, 0)
	node := newFakeNode(345)
	defer node.close()
	service := reorderingService(node, 3000, 3)

	quien := user("0b")
	holdBurst(t, service, quien, 346, 347, 348)

	_, err := service.ReserveGasAndSend(context.Background(), metaTxOf(quien, 349))
	if err == nil {
		t.Fatal("la que llega con el nonce mas alto tiene que rechazarse")
	}
	if code := CodeOf(err); code != CodeTooManyInflight {
		t.Errorf("codigo esperado %s, fue %s", CodeTooManyInflight, code)
	}
	details := DetailsOf(err)
	if details["inflight"] != 3 || details["max"] != 3 {
		t.Errorf("el detalle deberia indicar cuantas hay y el maximo, fue %v", details)
	}
	if retenidas := service.inflightOf(quien.Hex()); retenidas != 3 {
		t.Errorf("las retenidas siguen su curso: deberian seguir siendo 3, fueron %d", retenidas)
	}
	if node.sends() != 0 {
		t.Errorf("nada de esto pudo enviar, se enviaron %d", node.sends())
	}

	// Se destraba la cola para que la rafaga termine aca y no sobreviva al test.
	if _, err := service.ReserveGasAndSend(context.Background(), metaTxOf(quien, 345)); err != nil {
		t.Fatalf("la que destraba la cola tiene que admitirse: %v", err)
	}
}

// Escenario "Se supera el tope y la que llega destraba la cola": con el cupo lleno llega la metatx
// de nonce mas bajo, que es la unica que puede hacer avanzar al resto. Se desaloja la mas alta.
func TestCapEvictsTheHighestWhenTheArrivingUnblocksTheChain(t *testing.T) {
	events.Init(true, 200)
	defer events.Init(false, 0)
	node := newFakeNode(345)
	defer node.close()
	service := reorderingService(node, 3000, 3)

	quien := user("0b")
	retenidas := holdBurst(t, service, quien, 346, 347, 348)

	// 345 es la que faltaba: con el cupo lleno tiene que entrar igual, desalojando a la de 348.
	if _, err := service.ReserveGasAndSend(context.Background(), metaTxOf(quien, 345)); err != nil {
		t.Fatalf("la que destraba la cola tiene que admitirse: %v", err)
	}

	esperarRechazo := func(nonce uint64) {
		t.Helper()
		select {
		case err := <-retenidas[nonce]:
			if code := CodeOf(err); code != CodeTooManyInflight {
				t.Errorf("la de nonce %d deberia rechazarse por tope, fue %v", nonce, code)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("la de nonce %d no termino", nonce)
		}
	}
	esperarEnvio := func(nonce uint64) {
		t.Helper()
		select {
		case err := <-retenidas[nonce]:
			if err != nil {
				t.Errorf("la de nonce %d deberia enviarse al destrabarse la cola: %v", nonce, err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("la de nonce %d no termino", nonce)
		}
	}

	esperarRechazo(348)
	esperarEnvio(346)
	esperarEnvio(347)

	enviados := hubNoncesSent()
	if len(enviados) != 3 || enviados[0] != 345 || enviados[1] != 346 || enviados[2] != 347 {
		t.Errorf("la cadena tiene que salir entera y en orden [345 346 347], fue %v", enviados)
	}
}

// Los casos borde de D4, sobre la decision de la puerta con el cupo lleno.
func TestCapEdgeCases(t *testing.T) {
	quien := user("0b")

	t.Run("nonce igual al maximo retenido: se rechaza la que llega", func(t *testing.T) {
		events.Init(true, 200)
		defer events.Init(false, 0)
		node := newFakeNode(345)
		defer node.close()
		service := reorderingService(node, 3000, 3)

		holdBurst(t, service, quien, 346, 347, 348)

		// Con nonce igual no hay nada que ganar cambiando de victima, y preferir a la que ya
		// espera conserva el trabajo hecho.
		_, err := service.ReserveGasAndSend(context.Background(), metaTxOf(quien, 348))
		if code := CodeOf(err); code != CodeTooManyInflight {
			t.Errorf("la que empata con el maximo tiene que rechazarse por tope, fue %v", code)
		}
		if retenidas := service.inflightOf(quien.Hex()); retenidas != 3 {
			t.Errorf("no se desaloja a nadie por un empate: quedaron %d", retenidas)
		}

		if _, err := service.ReserveGasAndSend(context.Background(), metaTxOf(quien, 345)); err != nil {
			t.Fatalf("la que destraba la cola tiene que admitirse: %v", err)
		}
	})

	t.Run("dos retenidas con el mismo nonce: desaloja la que llego despues", func(t *testing.T) {
		events.Init(true, 200)
		defer events.Init(false, 0)
		node := newFakeNode(345)
		defer node.close()
		service := reorderingService(node, 3000, 3)

		key := senderKey(quien.Hex())
		primera := service.hold(key, 348)
		segunda := service.hold(key, 348)
		service.hold(key, 347)
		defer func() {
			service.unhold(key, primera)
			service.unhold(key, segunda)
		}()

		service.sendersLock.Lock()
		defer service.sendersLock.Unlock()
		if highest := service.highestHeldLocked(key); highest != segunda {
			t.Error("entre dos retenidas del mismo nonce sobra la que llego despues: son duplicados")
		}
	})

	t.Run("cupo lleno solo de en-vuelo: se rechaza la que llega", func(t *testing.T) {
		events.Init(true, 200)
		defer events.Init(false, 0)
		node := newFakeNode(345)
		defer node.close()
		service := reorderingService(node, 3000, 3)

		// Tres en vuelo, ninguna retenida: no hay a quien desalojar. Lo ya enviado gasto una
		// transaccion del writer node y no se puede deshacer.
		for nonce := uint64(345); nonce < 348; nonce++ {
			if _, err := service.ReserveGasAndSend(context.Background(), metaTxOf(quien, nonce)); err != nil {
				t.Fatalf("la metatx con nonce %d deberia enviarse: %v", nonce, err)
			}
		}

		enviadas := node.sends()
		_, err := service.ReserveGasAndSend(context.Background(), metaTxOf(quien, 344))
		if code := CodeOf(err); code != CodeTooManyInflight {
			t.Errorf("sin retenidas a quien desalojar se rechaza la que llega, fue %v", code)
		}
		if node.sends() != enviadas {
			t.Error("la rechazada no puede haber gastado una transaccion del writer node")
		}
	})
}

// La puerta usa `>=` y la espera `>`, y la diferencia es DELIBERADA: no son el mismo operador
// porque no son la misma pregunta.
//
//	puerta  "hay lugar para una mas?"  -> con el cupo justo al tope, NO
//	espera  "el cupo ya se paso?"      -> con el cupo justo al tope, TAMPOCO
//
// Si la espera usara `>=`, una rafaga que llena el cupo exacto se suicidaria sola: la de nonce mas
// alto se rechazaria estando el cupo dentro del tope. Este test existe para que el `>` no se
// "corrija" por parecer un error de tipeo. Ver design.md, D6.
func TestGateAndWaitUseDifferentOperatorsOnPurpose(t *testing.T) {
	events.Init(true, 200)
	defer events.Init(false, 0)
	node := newFakeNode(345)
	defer node.close()
	service := reorderingService(node, 3000, 3)

	quien := user("0b")
	key := senderKey(quien.Hex())

	baja := service.hold(key, 346)
	service.hold(key, 347)
	alta := service.hold(key, 348)
	defer func() {
		for _, entry := range append([]*heldMetaTx{}, service.held[key]...) {
			service.unhold(key, entry)
		}
	}()

	// Cupo JUSTO en el tope: 3 de 3.
	if _, admitted := service.makeRoomFor(key, 349); admitted {
		t.Error("la puerta usa >=: con el cupo justo al tope no hay lugar para una mas")
	}
	if _, surplus := service.surplusHeld(key, alta); surplus {
		t.Error("la espera usa >: con el cupo justo al tope todavia no se paso, no sobra nadie")
	}

	// Cupo PASADO: 4 de 3, que es lo que deja una rafaga simultanea al cruzar la puerta.
	masAlta := service.hold(key, 349)
	if _, surplus := service.surplusHeld(key, masAlta); !surplus {
		t.Error("pasado el tope, la de nonce mas alto es la que sobra")
	}
	// Y solo ella: el predicado de la espera lo satisface EXACTAMENTE UNA, que es lo que hace que
	// el cupo converja en vez de vaciarse de golpe.
	for nombre, entry := range map[string]*heldMetaTx{"la mas baja": baja, "la anterior maxima": alta} {
		if _, surplus := service.surplusHeld(key, entry); surplus {
			t.Errorf("%s no sobra: sobra solo la de nonce mas alto", nombre)
		}
	}
}

// El punto del cambio: la misma rafaga, llegue en el orden que llegue, descarta SIEMPRE a las
// mismas -las de nonce mas alto- y deja salir una cadena contigua.
//
// Antes de este cambio el descarte dependia del orden de llegada HTTP: se rechazaba a la que
// llegaba tarde, aunque fuera la del medio de la cadena, y todas las posteriores morian esperando
// un esperado que ya nunca iba a avanzar.
//
// Lo que NO se comprueba es cuantas sobreviven: el cupo acota la admision y no la salida, asi que
// una retenida a la que le llega su turno se envia aunque el cupo este pasado. Ver design.md, D7.
func TestBurstOverCapDiscardsTheHighestInAnyArrivalOrder(t *testing.T) {
	const cupo = 3

	ordenes := [][]uint64{
		{346, 347, 348, 349, 350},
		{350, 349, 348, 347, 346},
		{348, 346, 350, 347, 349},
		{349, 350, 346, 348, 347},
	}

	for _, orden := range ordenes {
		t.Run(fmt.Sprint(orden), func(t *testing.T) {
			events.Init(true, 200)
			defer events.Init(false, 0)
			node := newFakeNode(345)
			defer node.close()
			service := reorderingService(node, 3000, cupo)

			quien := user("0b")

			// Las metatx se lanzan DE A UNA y en el orden del caso: es el orden de llegada HTTP lo
			// que se esta variando, no la concurrencia.
			resultados := make(map[uint64]chan error, len(orden))
			var enVuelo sync.WaitGroup
			for _, nonce := range orden {
				salida := make(chan error, 1)
				resultados[nonce] = salida
				antes := service.inflightOf(quien.Hex())
				enVuelo.Add(1)
				go func(nonce uint64, salida chan error) {
					defer enVuelo.Done()
					_, err := service.ReserveGasAndSend(context.Background(), metaTxOf(quien, nonce))
					salida <- err
				}(nonce, salida)
				if antes < cupo {
					waitUntil(t, "la metatx llega y ocupa su lugar",
						func() bool { return service.inflightOf(quien.Hex()) > antes })
				} else {
					waitUntil(t, "la que llega resuelve contra el cupo lleno",
						func() bool { return len(salida) == 1 || noEsLaMasAlta(service, quien, nonce) })
				}
			}

			// La que faltaba destraba la cola.
			if _, err := service.ReserveGasAndSend(context.Background(), metaTxOf(quien, 345)); err != nil {
				t.Fatalf("la que destraba la cola tiene que admitirse: %v", err)
			}

			terminadas := make(chan struct{})
			go func() { enVuelo.Wait(); close(terminadas) }()
			select {
			case <-terminadas:
			case <-time.After(10 * time.Second):
				t.Fatal("alguna metatx de la rafaga quedo colgada")
			}

			var sobrevivientes, descartadas []uint64
			for nonce, salida := range resultados {
				err := <-salida
				switch {
				case err == nil:
					sobrevivientes = append(sobrevivientes, nonce)
				case CodeOf(err) == CodeTooManyInflight:
					descartadas = append(descartadas, nonce)
				default:
					t.Errorf("la de nonce %d no puede rechazarse por otro motivo: %s", nonce, CodeOf(err))
				}
			}
			sort.Slice(sobrevivientes, func(i, j int) bool { return sobrevivientes[i] < sobrevivientes[j] })
			sort.Slice(descartadas, func(i, j int) bool { return descartadas[i] < descartadas[j] })

			// Lo determinista: las descartadas son las MAS ALTAS, sin importar el orden de llegada.
			// Ninguna descartada puede ser menor que una sobreviviente, que es lo que significa
			// "nunca se descarta del medio".
			for _, descartada := range descartadas {
				for _, sobreviviente := range sobrevivientes {
					if descartada < sobreviviente {
						t.Errorf("se descarto del medio: %d se rechazo y %d sobrevivio (orden %v)",
							descartada, sobreviviente, orden)
					}
				}
			}

			// Y la cadena que sale es contigua desde la que destrabo la cola: ninguna huerfana.
			enviadas := hubNoncesSent()
			for indice, nonce := range enviadas {
				if esperado := uint64(345 + indice); nonce != esperado {
					t.Errorf("la cadena enviada tiene que ser contigua desde 345, fue %v (orden %v)",
						enviadas, orden)
					break
				}
			}
			if len(enviadas) != len(sobrevivientes)+1 {
				t.Errorf("lo enviado tiene que ser 345 mas las sobrevivientes: enviadas %v, sobrevivientes %v",
					enviadas, sobrevivientes)
			}
		})
	}
}

// noEsLaMasAlta dice si esa metatx ya no es la retenida de nonce mas alto de su usuario, que es la
// senal de que la que llego con el cupo lleno ya resolvio -entro desalojando a otra-.
func noEsLaMasAlta(service *RelaySignerService, quien common.Address, nonce uint64) bool {
	key := senderKey(quien.Hex())
	service.sendersLock.Lock()
	defer service.sendersLock.Unlock()
	highest := service.highestHeldLocked(key)
	return highest != nil && highest.nonce != nonce
}

// El cupo es una cota a la que el sistema CONVERGE, no una exactitud instante a instante.
//
// La puerta no serializa a las peticiones de un mismo usuario -lee el cupo y suelta el lock antes
// de que la metatx ocupe su lugar-, asi que una rafaga simultanea puede pasar de largo por un
// momento. La comprobacion de la espera lo devuelve al tope rechazando de a una a las de nonce mas
// alto. Lo que el test fija es que ese desborde es transitorio, que nadie muere por BAD_NONCE
// -el motivo real no queda tapado- y que ninguna goroutine queda colgada. Ver design.md, D3 y D6.
func TestCapConvergesAfterASimultaneousBurst(t *testing.T) {
	events.Init(true, 500)
	defer events.Init(false, 0)
	node := newFakeNode(345)
	defer node.close()
	const cupo = 3
	service := reorderingService(node, 3000, cupo)

	quien := user("0b")
	const rafaga = 8

	// Todas cruzan la puerta a la vez: es la carrera lo que se quiere provocar.
	largada := make(chan struct{})
	resultados := make(chan error, rafaga)
	var enVuelo sync.WaitGroup
	for nonce := uint64(346); nonce < 346+rafaga; nonce++ {
		enVuelo.Add(1)
		go func(nonce uint64) {
			defer enVuelo.Done()
			<-largada
			_, err := service.ReserveGasAndSend(context.Background(), metaTxOf(quien, nonce))
			resultados <- err
		}(nonce)
	}

	// Se mide el desborde mientras la rafaga corre.
	desborde := make(chan int, 1)
	muestreoListo := make(chan struct{})
	go func() {
		maximo := 0
		close(muestreoListo)
		for fin := time.Now().Add(2 * time.Second); time.Now().Before(fin); {
			if actual := service.inflightOf(quien.Hex()); actual > maximo {
				maximo = actual
			}
		}
		desborde <- maximo
	}()
	<-muestreoListo

	close(largada)
	// La que faltaba destraba la cola para que la rafaga resuelva.
	waitUntil(t, "la rafaga llega", func() bool { return service.inflightOf(quien.Hex()) > 0 })
	if _, err := service.ReserveGasAndSend(context.Background(), metaTxOf(quien, 345)); err != nil &&
		CodeOf(err) != CodeTooManyInflight {
		t.Fatalf("la que destraba la cola no deberia fallar por otro motivo: %v", err)
	}

	terminadas := make(chan struct{})
	go func() { enVuelo.Wait(); close(terminadas) }()
	select {
	case <-terminadas:
	case <-time.After(15 * time.Second):
		t.Fatal("alguna goroutine de la rafaga quedo colgada")
	}

	enviadas, rechazadas := 0, 0
	for i := 0; i < rafaga; i++ {
		switch err := <-resultados; {
		case err == nil:
			enviadas++
		case CodeOf(err) == CodeTooManyInflight:
			rechazadas++
		default:
			t.Errorf("el motivo real no puede quedar tapado: se esperaba %s, fue %s",
				CodeTooManyInflight, CodeOf(err))
		}
	}
	if enviadas+rechazadas != rafaga {
		t.Errorf("toda la rafaga tiene que resolver: %d enviadas + %d rechazadas de %d",
			enviadas, rechazadas, rafaga)
	}

	maximo := <-desborde
	t.Logf("desborde maximo observado: %d retenidas con cupo %d", maximo, cupo)

	// Las retenidas que sobraban se fueron todas: el registro del usuario queda vacio.
	service.sendersLock.Lock()
	retenidas := service.waitingOf(senderKey(quien.Hex()))
	service.sendersLock.Unlock()
	if retenidas != 0 {
		t.Errorf("el desborde de retenidas tiene que resolverse entero, quedaron %d", retenidas)
	}

	// Y lo que salio es una cadena CONTIGUA desde la que destrabo la cola: es el punto del cambio,
	// que ninguna del medio se pierda dejando huerfanas a las siguientes.
	enviadasNonces := hubNoncesSent()
	for indice, nonce := range enviadasNonces {
		if esperado := uint64(345 + indice); nonce != esperado {
			t.Errorf("la cadena enviada tiene que ser contigua desde 345: fue %v", enviadasNonces)
			break
		}
	}
}

// Un cliente que insiste en bucle con una metatx de nonce bajo desaloja una y otra vez a las altas.
// El riesgo es un ciclo de desalojos mutuos que no termine nunca; lo que se fija es que el sistema
// converge -toda insistencia resuelve- y que no queda ninguna goroutine colgada.
func TestEvictionStormConverges(t *testing.T) {
	events.Init(true, 500)
	defer events.Init(false, 0)
	node := newFakeNode(345)
	defer node.close()
	const cupo = 3
	service := reorderingService(node, 3000, cupo)

	quien := user("0b")

	// El cupo arranca lleno de retenidas altas.
	holdBurst(t, service, quien, 348, 349, 350)

	// Y el cliente insiste con la misma metatx de nonce bajo, que siempre desaloja a la mas alta.
	const insistencias = 12
	var insistiendo sync.WaitGroup
	resultados := make(chan error, insistencias)
	for i := 0; i < insistencias; i++ {
		insistiendo.Add(1)
		go func() {
			defer insistiendo.Done()
			ctx, cancelar := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancelar()
			_, err := service.ReserveGasAndSend(ctx, metaTxOf(quien, 346))
			resultados <- err
		}()
	}

	terminadas := make(chan struct{})
	go func() { insistiendo.Wait(); close(terminadas) }()
	select {
	case <-terminadas:
	case <-time.After(20 * time.Second):
		t.Fatal("la tormenta de desalojos no converge: quedaron goroutines colgadas")
	}

	// Todas resuelven, y ninguna con un motivo que tape la causa real.
	for i := 0; i < insistencias; i++ {
		switch err := <-resultados; {
		case err == nil, CodeOf(err) == CodeTooManyInflight, CodeOf(err) == CodeBadNonce,
			CodeOf(err) == CodeRelayError:
		default:
			t.Errorf("motivo inesperado en la tormenta: %s", CodeOf(err))
		}
	}

	// El cupo del usuario no puede haber quedado por debajo de cero ni el registro con basura.
	service.sendersLock.Lock()
	defer service.sendersLock.Unlock()
	if retenidas := service.waitingOf(senderKey(quien.Hex())); retenidas < 0 || retenidas > cupo {
		t.Errorf("retenidas fuera de rango tras la tormenta: %d con cupo %d", retenidas, cupo)
	}
}

// El tope es POR USUARIO: un desalojo por cupo nunca alcanza a la metatx de otro, y otro usuario se
// atiende con normalidad mientras uno esta en su tope.
func TestEvictionNeverReachesAnotherUser(t *testing.T) {
	events.Init(true, 300)
	defer events.Init(false, 0)
	node := newFakeNode(345)
	defer node.close()
	service := reorderingService(node, 3000, 3)

	uno, otro := user("0b"), user("0c")

	// `uno` llena su cupo con retenidas altas.
	holdBurst(t, service, uno, 348, 349, 350)
	// `otro` tambien tiene una retenida, que nada de lo de `uno` puede tocar.
	holdBurst(t, service, otro, 400)

	retenidasDe := func(quien common.Address) int {
		service.sendersLock.Lock()
		defer service.sendersLock.Unlock()
		return service.waitingOf(senderKey(quien.Hex()))
	}

	// Llega la de nonce bajo de `uno`: desaloja a la de 350, que es suya.
	if _, err := service.ReserveGasAndSend(context.Background(), metaTxOf(uno, 345)); err != nil {
		t.Fatalf("la que destraba la cola de uno tiene que admitirse: %v", err)
	}

	if restantes := retenidasDe(otro); restantes != 1 {
		t.Errorf("el desalojo no puede alcanzar a otro usuario: le quedaron %d retenidas", restantes)
	}

	// Y `otro` se atiende con normalidad mientras `uno` esta en su tope.
	if _, err := service.ReserveGasAndSend(context.Background(), metaTxOf(otro, 345)); err != nil {
		t.Errorf("otro usuario deberia atenderse con normalidad: %v", err)
	}
}

// Con el reordenamiento APAGADO no se entra al camino del cupo: no se desaloja, no se rechaza por
// tope y el registro de retenidas ni se toca, por bajo que sea `maxInflightPerUser`.
//
// Es la invariante 2 del proyecto: lo que no se ejecuta no puede cambiar el comportamiento por
// accidente.
func TestCapAndEvictionDoNotRunWithReorderingOff(t *testing.T) {
	events.Init(true, 200)
	defer events.Init(false, 0)
	node := newFakeNode(345)
	defer node.close()
	// Cupo de 1: encendido, la segunda metatx se rechazaria por tope.
	service := reorderingService(node, 3000, 1)
	service.Config.Reorder.Enabled = false

	quien := user("0b")
	const rafaga = 5
	for nonce := uint64(345); nonce < 345+rafaga; nonce++ {
		if _, err := service.ReserveGasAndSend(context.Background(), metaTxOf(quien, nonce)); err != nil {
			t.Fatalf("con el reordenamiento apagado el cupo no rige, la de nonce %d fallo: %v", nonce, err)
		}
	}
	// Y una adelantada tampoco se retiene.
	if _, err := service.ReserveGasAndSend(context.Background(), metaTxOf(quien, 900)); err != nil {
		t.Fatalf("con el reordenamiento apagado una adelantada se envia igual: %v", err)
	}

	if node.sends() != rafaga+1 {
		t.Errorf("se esperaban %d envios, hubo %d", rafaga+1, node.sends())
	}

	service.sendersLock.Lock()
	defer service.sendersLock.Unlock()
	if len(service.held) != 0 {
		t.Errorf("el registro de retenidas no se puede tocar con el reordenamiento apagado: %v", service.held)
	}
}
