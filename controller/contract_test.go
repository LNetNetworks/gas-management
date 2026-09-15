package controller

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LACNetNetworks/gas-relay-signer/events"
	"github.com/LACNetNetworks/gas-relay-signer/model"
	"github.com/LACNetNetworks/gas-relay-signer/service"
	"github.com/ethereum/go-ethereum/common"
)

// eventContract es el vocabulario de eventos tal como lo fija el spec relay-event-stream.
//
// La pagina del dashboard reconstruye el estado de cada metatx a partir de estos nombres y campos
// exactos, asi que renombrar uno o dejar de emitirlo la rompe sin producir ningun error visible.
// Este test es lo que hace ruidoso ese silencio.
var eventContract = map[string][]string{
	"relay.received":      {"rawTxHash", "rawTxBytes"},
	"relay.decoded":       {"from", "to", "isDeploy", "nonce", "userGasLimit", "metaTxGasLimit", "nodeAddress", "expiration", "expiresInSeconds", "dataBytes", "selector"},
	"relay.sent":          {"transactionHash", "hubNonce", "writerNodeNonce", "metaTxGasLimit", "simulated", "simulatedErrorCodeName", "pendingForUser"},
	"relay.rejected":      {"error"},
	"relay.hub_rejected":  {"transactionHash", "from", "errorCode", "errorCodeName"},
	"relay.held":          {"nonce", "expected", "gap", "windowMs"},
	"relay.turn":          {"heldMs", "reason"},
	"relay.settled":       {"blockNumber", "gasUsed", "executed", "errorCodeName", "deployedAddress"},
	"relay.settle_failed": {"error"},
}

// commonFields van en todo evento de una metatx, ademas de sus campos propios.
var commonFields = []string{"ts", "level", "event", "instanceId", "seq", "metaTxId"}

// deferredEvents son los que el contrato define pero cuya emision corresponde al reordenamiento de
// nonces y a su watcher de receipts, que todavia no existen.
var deferredEvents = map[string]bool{
	"relay.held": true, "relay.turn": true,
	"relay.settled": true, "relay.settle_failed": true,
}

// mockNode responde las llamadas RPC que hace el camino de relay completo.
func mockNode(t *testing.T, relayHub common.Address, receipt string) *httptest.Server {
	t.Helper()
	abiWord := func(hexValue string) string {
		return "0x" + strings.Repeat("0", 64-len(hexValue)) + hexValue
	}
	relayHubWord := abiWord(strings.ToLower(strings.TrimPrefix(relayHub.Hex(), "0x")))

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		request := string(body)
		w.Header().Set("Content-Type", "application/json")

		var decoded struct {
			Method string   `json:"method"`
			Params []string `json:"params"`
		}
		_ = json.Unmarshal(body, &decoded)

		switch {
		// La resolucion de la direccion del RelayHub contra el proxy.
		case strings.Contains(request, service.DATA_CALL_RELAYHUB):
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"1000","result":"` + relayHubWord + `"}`))
		// Cualquier otro eth_call del camino es el cupo de gas del nodo: generoso, para que la
		// metatx pase la verificacion y llegue al envio.
		case strings.Contains(request, `"eth_call"`):
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"` + abiWord("3b9aca00") + `"}`))
		case strings.Contains(request, `"eth_getTransactionCount"`):
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x6"}`))
		case strings.Contains(request, `"eth_sendRawTransaction"`):
			// Un nodo real responde el hash de la transaccion que acaba de recibir, no uno fijo:
			// es el que el cliente va a usar despues para pedir el receipt.
			hash := relayedTxHash
			if len(decoded.Params) > 0 {
				hash = service.RawTxHash(decoded.Params[0])
			}
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"` + hash + `"}`))
		case strings.Contains(request, `"eth_getTransactionReceipt"`):
			_, _ = w.Write([]byte(receipt))
		default:
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":null}`))
		}
	}))
}

// relayedTxHash es el hash con el que responde el nodo simulado al difundir la metatx, y por el que
// despues se consulta el receipt.
const relayedTxHash = "0x41167872ab8e13bf7ea5ea366786da656b3f32181410523b97ffecf0ee9cd945"

// hubRejectedReceipt es un receipt con BadTransactionSent: el hub rechazo la metatx al ejecutarla.
const hubRejectedReceipt = `{"jsonrpc":"2.0","id":1,"result":{
  "blockHash":"0x6e3aa24e261e61832624749b64049104c6105ba870d3375484548ffdb133eeea",
  "blockNumber":"0xaae545","contractAddress":null,"cumulativeGasUsed":"0x309f0",
  "from":"0xd00e6624a73f88b39f82ab34e8bf2b4d226fd768","gasUsed":"0x309f0",
  "logs":[{"address":"0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91",
    "topics":["0xc62bb53370aadcfe652881fc57ef9ca04a7c473e83b963413f2cf2b5d66c3ef3"],
    "data":"0x000000000000000000000000173cf75f0905338597fcd38f5ce13e6840b230e900000000000000000000000082a978b3f5962a5b0957d9ee9eef472ee55b42f10000000000000000000000000000000000000000000000000000000000000002",
    "blockNumber":"0xaae545","transactionHash":"0x41167872ab8e13bf7ea5ea366786da656b3f32181410523b97ffecf0ee9cd945",
    "transactionIndex":"0x0","blockHash":"0x6e3aa24e261e61832624749b64049104c6105ba870d3375484548ffdb133eeea",
    "logIndex":"0x0","removed":false}],
  "logsBloom":"0x` + zeroBloom + `","status":"0x1",
  "to":"0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91",
  "transactionHash":"0x41167872ab8e13bf7ea5ea366786da656b3f32181410523b97ffecf0ee9cd945",
  "transactionIndex":"0x0"}}`

// Un Bloom son 256 bytes exactos: con menos, el receipt entero se descarta en silencio.
const zeroBloom = "00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"

// relayingController arma un controller con el servicio apuntando al nodo simulado.
func relayingController(t *testing.T, nodeURL string) *RelayController {
	t.Helper()
	previous, hadKey := os.LookupEnv("WRITER_KEY")
	os.Setenv("WRITER_KEY", "0xb3e7374dca5ca90c3899dbb2c978051437fb15534c945bf59df16d6c80be27c0")
	t.Cleanup(func() {
		if hadKey {
			os.Setenv("WRITER_KEY", previous)
			return
		}
		os.Unsetenv("WRITER_KEY")
	})

	config := &model.Config{Application: model.ApplicationConfig{
		NodeURL:         nodeURL,
		ContractAddress: "0x39Ec8898eAD9d5995858EC4eEfA47ccC9DDe9cf0",
	}}
	relaySignerService := new(service.RelaySignerService)
	if err := relaySignerService.Init(config); err != nil {
		t.Fatalf("no se pudo inicializar el servicio: %v", err)
	}

	controller := new(RelayController)
	controller.Init(config, relaySignerService)
	return controller
}

// receiptRequest arma la consulta del receipt de una metatx ya enviada.
func receiptRequest(hash string) *http.Request {
	body := `{"jsonrpc":"2.0","id":2,"method":"eth_getTransactionReceipt","params":["` + hash + `"]}`
	return httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
}

// TestEventContract cubre la tarea 5.8: recorre los eventos que produce el camino de relay y falla
// si a alguno le falta metaTxId o cualquier campo de la tabla del spec.
func TestEventContract(t *testing.T) {
	withBus(t)

	relayHub := common.HexToAddress("0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91")
	node := mockNode(t, relayHub, hubRejectedReceipt)
	defer node.Close()

	controller := relayingController(t, node.URL)

	// Una metatx completa: se envia y despues se consulta su receipt, que trae el rechazo del hub.
	to := common.HexToAddress("0x82a978b3f5962a5b0957d9ee9eef472ee55b42f1")
	data := gasModelData("0x6057361d", common.HexToAddress("0x173cf75f0905338597fcd38f5ce13e6840b230e9"), uint64(time.Now().Unix()+600))
	relayed := httptest.NewRecorder()
	controller.SignTransaction(relayed, rawTxRequest(signedRawTx(t, &to, data, 34, 200000)))

	// El cliente toma el hash que le respondio el relay y con ese consulta el receipt, igual que
	// haria una dapp. Es el camino por el que el evento de cierre tiene que reencontrar su metatx.
	var relayResponse struct {
		Result string `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(relayed.Body.Bytes(), &relayResponse); err != nil {
		t.Fatalf("la respuesta del relay no es JSON-RPC: %v (%s)", err, relayed.Body.String())
	}
	if relayResponse.Error != nil {
		t.Fatalf("la metatx no llego a enviarse: %s", relayResponse.Error.Message)
	}
	controller.SignTransaction(httptest.NewRecorder(), receiptRequest(relayResponse.Result))

	// Y una rechazada, para que el contrato de relay.rejected tambien quede recorrido.
	controller.SignTransaction(httptest.NewRecorder(), rawTxRequest("0xdeadbee"))

	seen := make(map[string]bool)
	for _, event := range events.Replay(0) {
		expected, known := eventContract[event.Name()]
		if !known {
			continue
		}
		seen[event.Name()] = true

		if deferredEvents[event.Name()] {
			t.Errorf("%s no deberia emitirse todavia: su emision corresponde al reordenamiento de nonces", event.Name())
		}
		for _, field := range append(expected, commonFields...) {
			if _, present := event.Line[field]; !present {
				t.Errorf("%s (seq %d) no lleva el campo %q del contrato: %v",
					event.Name(), event.Seq, field, event.Line)
			}
		}
		if id, _ := event.Field("metaTxId").(string); id == "" {
			t.Errorf("%s lleva un metaTxId vacio: la vista lo descartaria en silencio", event.Name())
		}
	}

	// Los cinco que esta capacidad si emite tienen que haber aparecido, o el test no probo nada.
	for _, name := range []string{"relay.received", "relay.decoded", "relay.sent", "relay.rejected", "relay.hub_rejected"} {
		if !seen[name] {
			t.Errorf("no se emitio %s; se vieron %v", name, eventNames())
		}
	}
}

// TestBurstKeepsEachMetaTxSeparate cubre la tarea 6.2: en una rafaga, cada metatx tiene su
// relay.received, su relay.decoded y luego su relay.sent o su relay.rejected, sin mezclarse. Y no
// aparece ningun relay.held ni relay.turn, porque el reordenamiento todavia no existe.
func TestBurstKeepsEachMetaTxSeparate(t *testing.T) {
	withBus(t)

	node := mockNode(t, common.HexToAddress("0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91"), hubRejectedReceipt)
	defer node.Close()
	controller := relayingController(t, node.URL)

	to := common.HexToAddress("0x82a978b3f5962a5b0957d9ee9eef472ee55b42f1")
	const aceptadas, rechazadas = 6, 3

	var waiting sync.WaitGroup
	for i := 0; i < aceptadas; i++ {
		waiting.Add(1)
		go func(nonce uint64) {
			defer waiting.Done()
			data := gasModelData("0x6057361d", common.HexToAddress("0x173cf75f0905338597fcd38f5ce13e6840b230e9"), uint64(time.Now().Unix()+600))
			controller.SignTransaction(httptest.NewRecorder(), rawTxRequest(signedRawTx(t, &to, data, nonce, 200000)))
		}(uint64(i))
	}
	for i := 0; i < rechazadas; i++ {
		waiting.Add(1)
		go func() {
			defer waiting.Done()
			controller.SignTransaction(httptest.NewRecorder(), rawTxRequest("0xdeadbee"))
		}()
	}
	waiting.Wait()

	porMetaTx := make(map[string][]string)
	for _, event := range events.Replay(0) {
		if event.Name() == "relay.held" || event.Name() == "relay.turn" {
			t.Errorf("apareció %s: el reordenamiento de nonces no existe en esta capacidad", event.Name())
		}
		if id, ok := event.Field("metaTxId").(string); ok && id != "" {
			porMetaTx[id] = append(porMetaTx[id], event.Name())
		}
	}

	if len(porMetaTx) != aceptadas+rechazadas {
		t.Fatalf("se esperaban %d metatx distintas, quedaron %d", aceptadas+rechazadas, len(porMetaTx))
	}
	var enviadas, negadas int
	for id, secuencia := range porMetaTx {
		if secuencia[0] != "relay.received" {
			t.Errorf("la metatx %s no empieza por relay.received: %v", id, secuencia)
		}
		switch ultimo := secuencia[len(secuencia)-1]; ultimo {
		case "relay.sent":
			enviadas++
			if len(secuencia) != 3 || secuencia[1] != "relay.decoded" {
				t.Errorf("la metatx enviada %s no siguio received -> decoded -> sent: %v", id, secuencia)
			}
		case "relay.rejected":
			negadas++
		default:
			t.Errorf("la metatx %s termino en %s: toda metatx termina enviada o rechazada", id, ultimo)
		}
	}
	if enviadas != aceptadas || negadas != rechazadas {
		t.Errorf("quedaron %d enviadas y %d rechazadas, se esperaban %d y %d", enviadas, negadas, aceptadas, rechazadas)
	}
}

// TestHubRejectionJoinsItsMetaTx cubre la tarea 6.5: el rechazo del hub, observado en una peticion
// posterior, queda en el bus junto a los eventos previos de la misma metatx.
func TestHubRejectionJoinsItsMetaTx(t *testing.T) {
	withBus(t)

	node := mockNode(t, common.HexToAddress("0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91"), hubRejectedReceipt)
	defer node.Close()
	controller := relayingController(t, node.URL)

	to := common.HexToAddress("0x82a978b3f5962a5b0957d9ee9eef472ee55b42f1")
	data := gasModelData("0x6057361d", common.HexToAddress("0x173cf75f0905338597fcd38f5ce13e6840b230e9"), uint64(time.Now().Unix()+600))

	relayed := httptest.NewRecorder()
	controller.SignTransaction(relayed, rawTxRequest(signedRawTx(t, &to, data, 34, 200000)))
	var relayResponse struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal(relayed.Body.Bytes(), &relayResponse); err != nil || relayResponse.Result == "" {
		t.Fatalf("la metatx no llego a enviarse: %s", relayed.Body.String())
	}

	// Peticion posterior e independiente: el cliente consulta el receipt.
	controller.SignTransaction(httptest.NewRecorder(), receiptRequest(relayResponse.Result))

	porMetaTx := make(map[string][]string)
	for _, event := range events.Replay(0) {
		if id, ok := event.Field("metaTxId").(string); ok && id != "" {
			porMetaTx[id] = append(porMetaTx[id], event.Name())
		}
	}
	if len(porMetaTx) != 1 {
		t.Fatalf("todos los eventos deben pertenecer a la misma metatx, quedaron %d grupos: %v", len(porMetaTx), porMetaTx)
	}
	for id, secuencia := range porMetaTx {
		esperado := []string{"relay.received", "relay.decoded", "relay.sent", "relay.hub_rejected"}
		if len(secuencia) != len(esperado) {
			t.Fatalf("la metatx %s tiene %v, se esperaba %v", id, secuencia, esperado)
		}
		for i, name := range esperado {
			if secuencia[i] != name {
				t.Errorf("en la posicion %d quedo %s, se esperaba %s: %v", i, secuencia[i], name, secuencia)
			}
		}
	}
}
