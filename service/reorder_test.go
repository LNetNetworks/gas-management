package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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
	service := reorderingService(node, 3000, 16)

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
