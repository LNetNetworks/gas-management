package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LACNetNetworks/gas-relay-signer/events"
	"github.com/LACNetNetworks/gas-relay-signer/service"
	"github.com/ethereum/go-ethereum/common"
)

// Topics de los eventos del RelayHub que describen como termino una metatx.
const (
	topicTransactionRelayed = "0x548af85d7bc344f47cbfacdfce1ffea1ecd862e5e235ca9ec919e767c14049a8"
	topicContractDeployed   = "0x8a14d1d7200360982eafa429b53edf408f7f589e6da6558f3c116c7f708327b3"
	topicRelayed            = "0x79f72f9dacecfa9af3cfe946364971d0ef4826ffd35451658b283d58a382c20f"
)

// Datos ABI de TransactionRelayed: los argumentos no indexados son `executed` (bool) y `output`
// (bytes), o sea el booleano, el desplazamiento del bytes y su longitud.
var (
	dataRelayedOK      = "0x" + word("1") + word("40") + word("0")
	dataRelayedReverts = "0x" + word("0") + word("40") + word("0")
)

// ContractDeployed lleva la direccion del contrato creado en el data.
const dataContractDeployed = "0x000000000000000000000000c0ffee254729296a45a3885639ac7e10f9d54979"

// word rellena un valor hexadecimal a una palabra ABI de 32 bytes.
func word(value string) string {
	return strings.Repeat("0", 64-len(value)) + value
}

// receiptWith arma un receipt con los logs indicados.
func receiptWith(logs ...string) string {
	return `{"jsonrpc":"2.0","id":1,"result":{
	  "blockHash":"0x6e3aa24e261e61832624749b64049104c6105ba870d3375484548ffdb133eeea",
	  "blockNumber":"0xaae545","contractAddress":null,"cumulativeGasUsed":"0x309f0",
	  "from":"0xd00e6624a73f88b39f82ab34e8bf2b4d226fd768","gasUsed":"0x309f0",
	  "logs":[` + strings.Join(logs, ",") + `],
	  "logsBloom":"0x` + zeroBloom + `","status":"0x1",
	  "to":"0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91",
	  "transactionHash":"0x41167872ab8e13bf7ea5ea366786da656b3f32181410523b97ffecf0ee9cd945",
	  "transactionIndex":"0x0"}}`
}

func hubLog(topic, data string) string {
	return `{"address":"0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91",
	  "topics":["` + topic + `"],"data":"` + data + `",
	  "blockNumber":"0xaae545","transactionHash":"0x41167872ab8e13bf7ea5ea366786da656b3f32181410523b97ffecf0ee9cd945",
	  "transactionIndex":"0x0","blockHash":"0x6e3aa24e261e61832624749b64049104c6105ba870d3375484548ffdb133eeea",
	  "logIndex":"0x0","removed":false}`
}

// relayPost envia una metatx por `POST /relay` y devuelve la respuesta.
func relayPost(t *testing.T, mux *http.ServeMux, body string) (int, map[string]interface{}) {
	t.Helper()
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/relay", strings.NewReader(body)))

	var decoded map[string]interface{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("POST /relay no devolvio JSON (%v): %s", err, recorder.Body.String())
	}
	return recorder.Code, decoded
}

// relayableTx arma una metatx valida con el sufijo del modelo de gas.
func relayableTx(t *testing.T, to *common.Address, nonce uint64) string {
	t.Helper()
	data := gasModelData("0x6057361d", common.HexToAddress("0x173cf75f0905338597fcd38f5ce13e6840b230e9"), 4102444800)
	return signedRawTx(t, to, data, nonce, 200000)
}

func relayMux(t *testing.T, receipt string) *http.ServeMux {
	t.Helper()
	node := mockNode(t, common.HexToAddress("0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91"), receipt)
	t.Cleanup(node.Close)
	mux := http.NewServeMux()
	relayingController(t, node.URL).Routes(mux)
	return mux
}

// TestRelayRejectsABodyWithoutTransaction cubre la tarea 5.1.
func TestRelayRejectsABodyWithoutTransaction(t *testing.T) {
	withBus(t)
	mux := relayMux(t, receiptWith(hubLog(topicTransactionRelayed, dataRelayedOK)))

	for _, body := range []string{`{}`, `{"rawTx":""}`, `{"rawTx":"no-es-hexadecimal"}`, `{"rawTx":123}`, `no es json`} {
		status, response := relayPost(t, mux, body)
		if status != http.StatusBadRequest {
			t.Errorf("con el cuerpo %q -> %d, se esperaba 400", body, status)
		}
		if motivo, _ := response["error"].(string); !strings.Contains(motivo, "rawTx") {
			t.Errorf("con el cuerpo %q el motivo no dice que se esperaba: %v", body, response["error"])
		}
	}
}

// TestRelayAcceptsTheAlias cubre la otra mitad de 5.1: el alias se procesa igual.
func TestRelayAcceptsTheAlias(t *testing.T) {
	withBus(t)
	mux := relayMux(t, receiptWith(hubLog(topicTransactionRelayed, dataRelayedOK)))

	to := common.HexToAddress("0x82a978b3f5962a5b0957d9ee9eef472ee55b42f1")
	status, response := relayPost(t, mux, `{"signedTransaction":"`+relayableTx(t, &to, 11)+`"}`)

	if status != http.StatusOK {
		t.Fatalf("POST /relay con el alias -> %d: %v", status, response)
	}
	if hash, _ := response["transactionHash"].(string); !strings.HasPrefix(hash, "0x") {
		t.Errorf("no se devolvio el hash: %v", response)
	}
}

// TestRelayRejectsTheSameAsTheJSONRPCDoor cubre la tarea 5.2.
func TestRelayRejectsTheSameAsTheJSONRPCDoor(t *testing.T) {
	withBus(t)
	mux := relayMux(t, receiptWith(hubLog(topicTransactionRelayed, dataRelayedOK)))

	// Una raw tx que el camino JSON-RPC rechaza.
	rpcRecorder := httptest.NewRecorder()
	mux.ServeHTTP(rpcRecorder, rawTxRequest("0xdeadbee"))
	var rpcResponse struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rpcRecorder.Body.Bytes(), &rpcResponse)
	if rpcResponse.Error.Message == "" {
		t.Fatalf("el camino JSON-RPC no la rechazo: %s", rpcRecorder.Body.String())
	}

	status, rest := relayPost(t, mux, `{"rawTx":"0xdeadbee"}`)
	if status != http.StatusBadRequest {
		t.Errorf("POST /relay -> %d, se esperaba 400", status)
	}
	if rest["error"] != rpcResponse.Error.Message {
		t.Errorf("las dos puertas dan motivos distintos:\n  JSON-RPC: %q\n  REST:     %v",
			rpcResponse.Error.Message, rest["error"])
	}
	if rest["code"] != service.CodeBadRawTx {
		t.Errorf("code = %v, se esperaba %s", rest["code"], service.CodeBadRawTx)
	}
}

// TestRelayReturnsTheDecodedResult cubre la tarea 5.4 para una llamada a un contrato.
func TestRelayReturnsTheDecodedResult(t *testing.T) {
	withBus(t)
	mux := relayMux(t, receiptWith(hubLog(topicTransactionRelayed, dataRelayedOK)))

	to := common.HexToAddress("0x82a978b3f5962a5b0957d9ee9eef472ee55b42f1")
	status, result := relayPost(t, mux, `{"rawTx":"`+relayableTx(t, &to, 21)+`"}`)
	if status != http.StatusOK {
		t.Fatalf("POST /relay -> %d: %v", status, result)
	}

	for _, field := range []string{
		"transactionHash", "isDeploy", "deployedAddress", "blockNumber", "gasUsed",
		"errorCode", "errorCodeName", "executed", "output", "from", "to", "nonce",
		"metaTxGasLimit", "events", "simulated",
	} {
		if _, present := result[field]; !present {
			t.Errorf("falta el campo %q del resultado: %v", field, result)
		}
	}
	if result["executed"] != true {
		t.Errorf("executed = %v, se esperaba true", result["executed"])
	}
	if result["isDeploy"] != false {
		t.Errorf("isDeploy = %v, se esperaba false", result["isDeploy"])
	}
	if result["to"] != to.Hex() {
		t.Errorf("to = %v, se esperaba %s", result["to"], to.Hex())
	}
	if result["nonce"] != float64(21) {
		t.Errorf("nonce = %v, se esperaba 21", result["nonce"])
	}
	if result["blockNumber"] != float64(0xaae545) {
		t.Errorf("blockNumber = %v", result["blockNumber"])
	}
	if result["simulated"] != false {
		t.Errorf("simulated = %v: este servicio no hace pre-chequeo por simulacion", result["simulated"])
	}
}

// TestRelayReportsADeployedAddress cubre la otra mitad de 5.4: en un deploy la direccion sale del
// evento del hub, no del receipt.
func TestRelayReportsADeployedAddress(t *testing.T) {
	withBus(t)
	mux := relayMux(t, receiptWith(
		hubLog(topicContractDeployed, dataContractDeployed),
		hubLog(topicTransactionRelayed, dataRelayedOK)))

	status, result := relayPost(t, mux, `{"rawTx":"`+relayableTx(t, nil, 31)+`"}`)
	if status != http.StatusOK {
		t.Fatalf("POST /relay -> %d: %v", status, result)
	}
	if result["isDeploy"] != true {
		t.Errorf("isDeploy = %v, se esperaba true", result["isDeploy"])
	}
	// La direccion es la del contrato creado, no la del receipt -que es de la tx envolvente-.
	if result["deployedAddress"] != common.HexToAddress("0xc0ffee254729296a45a3885639AC7E10F9d54979").Hex() {
		t.Errorf("deployedAddress = %v, se esperaba la del evento ContractDeployed", result["deployedAddress"])
	}
	if result["to"] != nil {
		t.Errorf("to = %v, un deploy no tiene destino", result["to"])
	}
}

// TestRelayReportsARevertAsSuccessfulResponse cubre la tarea 5.5: el contrato destino revirtio,
// pero la metatx SI se relayo. No es un rechazo del relay.
func TestRelayReportsARevertAsSuccessfulResponse(t *testing.T) {
	withBus(t)
	mux := relayMux(t, receiptWith(hubLog(topicTransactionRelayed, dataRelayedReverts)))

	to := common.HexToAddress("0x82a978b3f5962a5b0957d9ee9eef472ee55b42f1")
	status, result := relayPost(t, mux, `{"rawTx":"`+relayableTx(t, &to, 41)+`"}`)

	if status != http.StatusOK {
		t.Fatalf("POST /relay -> %d: un revert del contrato destino no es un rechazo del relay: %v", status, result)
	}
	if result["executed"] != false {
		t.Errorf("executed = %v, se esperaba false", result["executed"])
	}
	if motivo, _ := result["output"].(string); motivo == "" {
		t.Errorf("no se informo el motivo del revert: %v", result)
	}
	if _, esRechazo := result["code"]; esRechazo {
		t.Errorf("un revert no debe llevar codigo de rechazo: %v", result)
	}
}

// TestRelayReportsTheHubRejection cubre la tarea 5.6.
func TestRelayReportsTheHubRejection(t *testing.T) {
	withBus(t)
	mux := relayMux(t, hubRejectedReceipt)

	to := common.HexToAddress("0x82a978b3f5962a5b0957d9ee9eef472ee55b42f1")
	status, result := relayPost(t, mux, `{"rawTx":"`+relayableTx(t, &to, 51)+`"}`)
	if status != http.StatusOK {
		t.Fatalf("POST /relay -> %d: %v", status, result)
	}
	if result["errorCode"] != float64(2) {
		t.Errorf("errorCode = %v, se esperaba 2", result["errorCode"])
	}
	// La misma traduccion del enum que ya usaba el servicio.
	if result["errorCodeName"] != "BadNonce" {
		t.Errorf("errorCodeName = %v, se esperaba BadNonce", result["errorCodeName"])
	}
	if result["executed"] != false {
		t.Errorf("executed = %v, el hub la rechazo", result["executed"])
	}
}

// TestRelayEmitsTheSameEvents cubre la tarea 5.9.
func TestRelayEmitsTheSameEvents(t *testing.T) {
	withBus(t)
	mux := relayMux(t, receiptWith(hubLog(topicTransactionRelayed, dataRelayedOK)))

	to := common.HexToAddress("0x82a978b3f5962a5b0957d9ee9eef472ee55b42f1")
	if status, result := relayPost(t, mux, `{"rawTx":"`+relayableTx(t, &to, 61)+`"}`); status != http.StatusOK {
		t.Fatalf("POST /relay -> %d: %v", status, result)
	}

	porMetaTx := make(map[string][]string)
	for _, event := range events.Replay(0) {
		if id, ok := event.Field("metaTxId").(string); ok && id != "" {
			porMetaTx[id] = append(porMetaTx[id], event.Name())
		}
	}
	if len(porMetaTx) != 1 {
		t.Fatalf("los eventos deberian pertenecer a una sola metatx, quedaron %d grupos: %v", len(porMetaTx), porMetaTx)
	}
	for id, secuencia := range porMetaTx {
		for _, esperado := range []string{"relay.received", "relay.decoded", "relay.sent"} {
			encontrado := false
			for _, name := range secuencia {
				if name == esperado {
					encontrado = true
				}
			}
			if !encontrado {
				t.Errorf("la metatx %s no emitio %s: %v", id, esperado, secuencia)
			}
		}
	}
}

// TestConcurrentRelaysDoNotBlockEachOther cubre las tareas 5.3 y 6.2: varias esperas simultaneas no
// se bloquean entre si, y el camino JSON-RPC sigue respondiendo mientras tanto.
func TestConcurrentRelaysDoNotBlockEachOther(t *testing.T) {
	withBus(t)
	mux := relayMux(t, receiptWith(hubLog(topicTransactionRelayed, dataRelayedOK)))

	to := common.HexToAddress("0x82a978b3f5962a5b0957d9ee9eef472ee55b42f1")
	const simultaneos = 5

	inicio := time.Now()
	var waiting sync.WaitGroup
	codigos := make([]int, simultaneos)
	for i := 0; i < simultaneos; i++ {
		waiting.Add(1)
		go func(index int) {
			defer waiting.Done()
			recorder := httptest.NewRecorder()
			body := `{"rawTx":"` + relayableTx(t, &to, uint64(100+index)) + `"}`
			mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/relay", strings.NewReader(body)))
			codigos[index] = recorder.Code
		}(i)
	}

	// Mientras esperan, el camino JSON-RPC tiene que seguir respondiendo.
	jsonrpc := httptest.NewRecorder()
	mux.ServeHTTP(jsonrpc, rawTxRequest("0xdeadbee"))
	if !strings.Contains(jsonrpc.Body.String(), "jsonrpc") {
		t.Errorf("el camino JSON-RPC dejo de responder mientras habia relays esperando: %s", jsonrpc.Body.String())
	}

	waiting.Wait()
	for i, code := range codigos {
		if code != http.StatusOK {
			t.Errorf("el relay %d -> %d", i, code)
		}
	}
	// Si las esperas se serializaran, esto tardaria multiplos del sondeo.
	if transcurrido := time.Since(inicio); transcurrido > 10*time.Second {
		t.Errorf("las esperas se serializaron: %v para %d relays", transcurrido, simultaneos)
	}
}

// relayMuxWith arma el mux contra un nodo simulado con opciones, y deja tocar la configuracion.
func relayMuxWith(t *testing.T, receipt string, opts mockNodeOptions, tune func(*RelayController)) *http.ServeMux {
	t.Helper()
	node := mockNode(t, common.HexToAddress("0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91"), receipt, opts)
	t.Cleanup(node.Close)
	controller := relayingController(t, node.URL)
	if tune != nil {
		tune(controller)
		// Tocar la configuracion despues de construir el servicio obliga a resolver de nuevo: en el
		// arranque real la configuracion esta completa antes de que se resuelva nada.
		controller.RelaySignerService.ResolveAccountRules(context.Background())
	}
	mux := http.NewServeMux()
	controller.Routes(mux)
	return mux
}

// TestRelayUsesTheErrorCatalog cubre la tarea 5.7: cada rechazo lleva su codigo del catalogo, y lo
// que no tiene codigo propio cae en el generico en lugar de estrenar uno. Ver design.md, D5.
func TestRelayUsesTheErrorCatalog(t *testing.T) {
	to := common.HexToAddress("0x82a978b3f5962a5b0957d9ee9eef472ee55b42f1")
	receipt := receiptWith(hubLog(topicTransactionRelayed, dataRelayedOK))

	t.Run("transaccion indecodificable", func(t *testing.T) {
		withBus(t)
		mux := relayMuxWith(t, receipt, mockNodeOptions{}, nil)
		status, response := relayPost(t, mux, `{"rawTx":"0xdeadbee"}`)
		if status != http.StatusBadRequest || response["code"] != service.CodeBadRawTx {
			t.Errorf("-> %d con code=%v, se esperaba 400 y %s", status, response["code"], service.CodeBadRawTx)
		}
	})

	t.Run("sender no permitido", func(t *testing.T) {
		withBus(t)
		mux := relayMuxWith(t, receipt, mockNodeOptions{senderNotPermitted: true}, func(c *RelayController) {
			c.Config.Security.PermissionsEnabled = true
			c.Config.Security.AccountContractAddress = "0x4683519EF834572017Cb583246B717449A4B752c"
		})
		status, response := relayPost(t, mux, `{"rawTx":"`+relayableTx(t, &to, 71)+`"}`)
		if status != http.StatusBadRequest || response["code"] != service.CodeSenderNotPermitted {
			t.Errorf("-> %d con code=%v, se esperaba 400 y %s", status, response["code"], service.CodeSenderNotPermitted)
		}
	})

	t.Run("motivo sin codigo propio", func(t *testing.T) {
		withBus(t)
		// El cupo de gas excedido es un rechazo propio de este servicio que el catalogo de Node no
		// contempla: usa el generico en lugar de inventar un codigo.
		mux := relayMuxWith(t, receipt, mockNodeOptions{tinyGasLimit: true}, nil)
		status, response := relayPost(t, mux, `{"rawTx":"`+relayableTx(t, &to, 72)+`"}`)
		if status != http.StatusBadRequest || response["code"] != service.CodeRelayError {
			t.Errorf("-> %d con code=%v, se esperaba 400 y %s", status, response["code"], service.CodeRelayError)
		}
		if motivo, _ := response["error"].(string); !strings.Contains(motivo, "gas limit") {
			t.Errorf("el motivo se perdio al caer en el generico: %v", response["error"])
		}
	})
}

// TestRelayTimeoutIsNotARejection cubre la tarea 5.8: la metatx SE ENVIO. Un cliente que lo trate
// como rechazo y la reenvie produce un nonce repetido.
func TestRelayTimeoutIsNotARejection(t *testing.T) {
	withBus(t)
	// El nodo nunca devuelve receipt: la metatx queda sin minarse.
	mux := relayMuxWith(t, `{"jsonrpc":"2.0","id":1,"result":null}`, mockNodeOptions{}, func(c *RelayController) {
		c.Config.Reorder.ReceiptTimeoutMs = 300
	})

	to := common.HexToAddress("0x82a978b3f5962a5b0957d9ee9eef472ee55b42f1")
	status, response := relayPost(t, mux, `{"rawTx":"`+relayableTx(t, &to, 81)+`"}`)

	if status != http.StatusBadRequest {
		t.Fatalf("-> %d: %v", status, response)
	}
	if response["code"] != service.CodeReceiptTimeout {
		t.Errorf("code = %v, se esperaba %s: tiene que distinguirse de un rechazo",
			response["code"], service.CodeReceiptTimeout)
	}
	// El hash va en el motivo para que el cliente la consulte en lugar de reenviarla.
	if motivo, _ := response["error"].(string); !strings.Contains(motivo, "0x") {
		t.Errorf("el motivo no trae el hash de la metatx enviada: %v", response["error"])
	}
	if detalle, _ := response["details"].(map[string]interface{}); detalle == nil || detalle["sent"] != true {
		t.Errorf("el detalle no indica que la metatx se envio: %v", response["details"])
	}
	// Y se envio de verdad: quedo su relay.sent en el bus.
	if _, found := eventNamed("relay.sent"); !found {
		t.Errorf("no se emitio relay.sent: la metatx no llego a enviarse")
	}
}

// TestMethodMismatchOnRelay completa la tarea 1.1, que esperaba a que `/relay` estuviera registrada.
func TestMethodMismatchOnRelay(t *testing.T) {
	withBus(t)
	mux := relayMux(t, receiptWith(hubLog(topicTransactionRelayed, dataRelayedOK)))

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/relay", nil))

	if recorder.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /relay -> %d, se esperaba 405", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), "jsonrpc") {
		t.Errorf("GET /relay lo atendio el camino JSON-RPC: %s", recorder.Body.String())
	}
}
