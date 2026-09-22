package controller

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	audit "github.com/LACNetNetworks/gas-relay-signer/audit"
	"github.com/LACNetNetworks/gas-relay-signer/events"
	"github.com/LACNetNetworks/gas-relay-signer/model"
	"github.com/LACNetNetworks/gas-relay-signer/service"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rlp"
)

// withBus deja el bus del proceso activo durante el test y lo apaga al terminar. Los eventos se
// comprueban por el bus y no por la salida estandar porque es el mismo contenido -el bus es un
// derivado del log- y no obliga a interceptar descriptores.
func withBus(t *testing.T) {
	t.Helper()
	events.Init(true, 100)
	t.Cleanup(func() { events.Init(false, 0) })
}

// eventNamed busca el primer evento con ese nombre entre los retenidos.
func eventNamed(name string) (events.Event, bool) {
	for _, event := range events.Replay(0) {
		if event.Name() == name {
			return event, true
		}
	}
	return events.Event{}, false
}

// failingBody es un cuerpo de peticion que no se puede leer.
type failingBody struct{}

func (failingBody) Read([]byte) (int, error) { return 0, errors.New("conexion interrumpida") }
func (failingBody) Close() error             { return nil }

func newController() *RelayController {
	controller := new(RelayController)
	controller.Init(&model.Config{}, nil)
	return controller
}

// TestUnreadableBodyIsStillCorrelated cubre la tarea 4.2: el reqId se genera antes de leer el
// cuerpo, asi que una peticion que nunca llega a tener una metatx tambien deja rastro.
func TestUnreadableBodyIsStillCorrelated(t *testing.T) {
	withBus(t)

	request := httptest.NewRequest(http.MethodPost, "/", failingBody{})
	recorder := httptest.NewRecorder()

	newController().SignTransaction(recorder, request)

	event, found := eventNamed("http.bad_body")
	if !found {
		t.Fatalf("no se emitio ningun evento para el cuerpo ilegible, el bus tiene %d eventos", len(events.Replay(0)))
	}
	reqID, _ := event.Field("reqId").(string)
	if reqID == "" {
		t.Errorf("el evento salio sin reqId: %v", event.Line)
	}
	if event.Level() != "warn" {
		t.Errorf("level = %q, un cuerpo ilegible es informacion de operacion (warn)", event.Level())
	}
	if _, present := event.Line["metaTxId"]; present {
		t.Errorf("una peticion sin metatx no debe llevar metaTxId: %v", event.Line)
	}
}

// TestUnparseableBodyIsStillCorrelated: lo mismo cuando el cuerpo se lee pero no es JSON-RPC.
func TestUnparseableBodyIsStillCorrelated(t *testing.T) {
	withBus(t)

	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("esto no es json"))
	recorder := httptest.NewRecorder()

	newController().SignTransaction(recorder, request)

	event, found := eventNamed("http.bad_body")
	if !found {
		t.Fatal("no se emitio ningun evento para el cuerpo no parseable")
	}
	if reqID, _ := event.Field("reqId").(string); reqID == "" {
		t.Errorf("el evento salio sin reqId: %v", event.Line)
	}
	if _, present := event.Line["error"]; !present {
		t.Errorf("el evento debe llevar el motivo en `error`: %v", event.Line)
	}
}

// TestEachRequestGetsItsOwnRequestID: dos peticiones no comparten identificador.
func TestEachRequestGetsItsOwnRequestID(t *testing.T) {
	withBus(t)

	controller := newController()
	for i := 0; i < 2; i++ {
		controller.SignTransaction(httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPost, "/", strings.NewReader("no es json")))
	}

	retained := events.Replay(0)
	if len(retained) != 2 {
		t.Fatalf("se esperaban 2 eventos, el bus tiene %d", len(retained))
	}
	first, _ := retained[0].Field("reqId").(string)
	second, _ := retained[1].Field("reqId").(string)
	if first == "" || second == "" {
		t.Fatalf("falta el reqId en alguno de los eventos: %q y %q", first, second)
	}
	if first == second {
		t.Errorf("dos peticiones distintas comparten reqId %q", first)
	}
}

// TestUnreadableBodyResponseIsUnchanged: agregar el evento no cambio lo que recibe el cliente.
// El flujo de esta rama se conserva tal cual estaba, incluida la ausencia de respuesta.
func TestUnreadableBodyResponseIsUnchanged(t *testing.T) {
	withBus(t)

	recorder := httptest.NewRecorder()
	newController().SignTransaction(recorder, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("no es json")))

	if recorder.Code != http.StatusOK {
		t.Errorf("codigo de respuesta = %d, antes era %d", recorder.Code, http.StatusOK)
	}
	if recorder.Body.Len() != 0 {
		t.Errorf("cuerpo de respuesta = %q, antes era vacio", recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, antes era application/json", got)
	}
}

// rawTxRequest arma una peticion JSON-RPC eth_sendRawTransaction con la raw tx indicada.
func rawTxRequest(rawTx string) *http.Request {
	body := `{"jsonrpc":"2.0","id":1,"method":"eth_sendRawTransaction","params":["` + rawTx + `"]}`
	return httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
}

// eventsOf agrupa los eventos retenidos por metaTxId, en orden de publicacion.
func eventsOf() map[string][]events.Event {
	byMetaTx := make(map[string][]events.Event)
	for _, event := range events.Replay(0) {
		if id, ok := event.Field("metaTxId").(string); ok && id != "" {
			byMetaTx[id] = append(byMetaTx[id], event)
		}
	}
	return byMetaTx
}

// TestMalformedRawTxStillEmitsReceived cubre la tarea 5.1: relay.received se emite ANTES de
// decodificar, asi que una raw tx que no se puede decodificar tambien deja rastro.
func TestMalformedRawTxStillEmitsReceived(t *testing.T) {
	withBus(t)

	newController().SignTransaction(httptest.NewRecorder(), rawTxRequest("0xdeadbeef"))

	received, found := eventNamed("relay.received")
	if !found {
		t.Fatalf("una raw tx malformada debe emitir relay.received igual, el bus tiene %d eventos", len(events.Replay(0)))
	}
	if id, _ := received.Field("metaTxId").(string); id == "" {
		t.Errorf("relay.received salio sin metaTxId: %v", received.Line)
	}
	if received.Field("rawTxHash") == "" || received.Field("rawTxHash") == nil {
		t.Errorf("falta rawTxHash: %v", received.Line)
	}
	if received.Field("rawTxBytes") != 4 {
		t.Errorf("rawTxBytes = %v, se esperaban 4 para 0xdeadbeef", received.Field("rawTxBytes"))
	}
	if _, present := received.Line["rawTx"]; present {
		t.Errorf("la transaccion firmada completa no se registra por defecto: %v", received.Line)
	}
}

// TestRejectionSharesTheMetaTxID cubre la tarea 5.6: el rechazo comparte el metaTxId de los
// eventos previos de esa metatx, y lleva el motivo en `error`.
func TestRejectionSharesTheMetaTxID(t *testing.T) {
	withBus(t)

	newController().SignTransaction(httptest.NewRecorder(), rawTxRequest("0xdeadbeef"))

	received, foundReceived := eventNamed("relay.received")
	rejected, foundRejected := eventNamed("relay.rejected")
	if !foundReceived || !foundRejected {
		t.Fatalf("se esperaban relay.received y relay.rejected, el bus tiene %d eventos", len(events.Replay(0)))
	}
	if received.Field("metaTxId") != rejected.Field("metaTxId") {
		t.Errorf("relay.rejected (%v) no comparte el metaTxId de relay.received (%v)",
			rejected.Field("metaTxId"), received.Field("metaTxId"))
	}
	if received.Field("reqId") != rejected.Field("reqId") {
		t.Errorf("los eventos de una misma peticion deben compartir reqId: %v y %v",
			received.Field("reqId"), rejected.Field("reqId"))
	}
	motivo, _ := rejected.Field("error").(string)
	if motivo == "" {
		t.Errorf("relay.rejected debe llevar el motivo en `error`: %v", rejected.Line)
	}
	if rejected.Level() != "warn" {
		t.Errorf("level = %q, un rechazo es informacion de operacion (warn)", rejected.Level())
	}
	if rejected.Seq <= received.Seq {
		t.Errorf("el rechazo (seq %d) debe publicarse despues de la recepcion (seq %d)", rejected.Seq, received.Seq)
	}
}

// TestTwoMetaTxDoNotShareIdentifier cubre la tarea 4.3: dos metatx distintas tienen identificador
// propio y sus eventos no se mezclan.
func TestTwoMetaTxDoNotShareIdentifier(t *testing.T) {
	withBus(t)

	controller := newController()
	controller.SignTransaction(httptest.NewRecorder(), rawTxRequest("0xdeadbeef"))
	controller.SignTransaction(httptest.NewRecorder(), rawTxRequest("0xc0ffee"))

	byMetaTx := eventsOf()
	if len(byMetaTx) != 2 {
		t.Fatalf("se esperaban 2 metatx distintas, quedaron %d: %v", len(byMetaTx), byMetaTx)
	}
	for id, group := range byMetaTx {
		if len(group) != 2 {
			t.Errorf("la metatx %s tiene %d eventos, se esperaban relay.received y relay.rejected", id, len(group))
		}
		// Cada grupo describe una sola raw tx: sus eventos no se mezclaron con los de la otra.
		hashes := make(map[interface{}]bool)
		for _, event := range group {
			if hash := event.Field("rawTxHash"); hash != nil {
				hashes[hash] = true
			}
		}
		if len(hashes) > 1 {
			t.Errorf("la metatx %s mezclo eventos de mas de una raw tx: %v", id, hashes)
		}
	}
}

// TestRawTxIsLoggedOnlyWhenEnabled: por defecto solo el hash y el tamano; con el volcado
// habilitado, tambien la transaccion firmada completa.
func TestRawTxIsLoggedOnlyWhenEnabled(t *testing.T) {
	withBus(t)
	audit.InitStructured("info", true)
	t.Cleanup(func() { audit.InitStructured("info", false) })

	newController().SignTransaction(httptest.NewRecorder(), rawTxRequest("0xdeadbeef"))

	received, found := eventNamed("relay.received")
	if !found {
		t.Fatal("no se emitio relay.received")
	}
	if received.Field("rawTx") != "0xdeadbeef" {
		t.Errorf("con el volcado habilitado se esperaba la raw tx completa, quedo %v", received.Field("rawTx"))
	}
}

// TestCodedRejectionCarriesItsCode completa la tarea 5.6: cuando el rechazo trae codigo, el evento
// lo lleva, y es el mismo que viaja en la respuesta JSON-RPC al cliente.
func TestCodedRejectionCarriesItsCode(t *testing.T) {
	withBus(t)

	// Longitud impar: falla el decode hexadecimal y el servicio rechaza con -32012.
	recorder := httptest.NewRecorder()
	newController().SignTransaction(recorder, rawTxRequest("0xdeadbee"))

	rejected, found := eventNamed("relay.rejected")
	if !found {
		t.Fatal("no se emitio relay.rejected")
	}
	if rejected.Field("code") != -32012 {
		t.Errorf("code = %v, se esperaba -32012", rejected.Field("code"))
	}
	if errorType, _ := rejected.Field("errorType").(string); errorType == "" {
		t.Errorf("falta errorType: %v", rejected.Line)
	}

	var response struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("la respuesta no es JSON-RPC: %v (%s)", err, recorder.Body.String())
	}
	if rejected.Field("code") != response.Error.Code {
		t.Errorf("el evento registra code=%v y el cliente recibe %d",
			rejected.Field("code"), response.Error.Code)
	}
	if rejected.Field("error") != response.Error.Message {
		t.Errorf("el evento registra error=%v y el cliente recibe %q",
			rejected.Field("error"), response.Error.Message)
	}
}

// --------------------------------------------------------------- relay.decoded

// signedRawTx arma una raw tx firmada pre-EIP155 (v = 27/28), que es la unica forma que el
// RelayHub acepta y la que el servicio exige.
func signedRawTx(t *testing.T, to *common.Address, data []byte, nonce, gas uint64) string {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("no se pudo generar la clave de prueba: %v", err)
	}
	var tx *types.Transaction
	if to == nil {
		tx = types.NewContractCreation(nonce, big.NewInt(0), gas, big.NewInt(0), data)
	} else {
		tx = types.NewTransaction(nonce, *to, big.NewInt(0), gas, big.NewInt(0), data)
	}
	signed, err := types.SignTx(tx, types.HomesteadSigner{}, key)
	if err != nil {
		t.Fatalf("no se pudo firmar la tx de prueba: %v", err)
	}
	raw, err := rlp.EncodeToBytes(signed)
	if err != nil {
		t.Fatalf("no se pudo serializar la tx de prueba: %v", err)
	}
	return "0x" + hex.EncodeToString(raw)
}

// gasModelData arma un data con el sufijo del modelo de gas: innerData + address + expiration.
func gasModelData(selector string, nodeAddress common.Address, expiration uint64) []byte {
	inner, _ := hex.DecodeString(strings.TrimPrefix(selector, "0x"))
	suffix := make([]byte, 64)
	copy(suffix[12:32], nodeAddress.Bytes())
	new(big.Int).SetUint64(expiration).FillBytes(suffix[32:64])
	return append(inner, suffix...)
}

// controllerWithService devuelve un controller con un servicio utilizable, para los casos que
// pasan de la decodificacion.
func controllerWithService() *RelayController {
	relaySignerService := new(service.RelaySignerService)
	relaySignerService.Config = &model.Config{}
	controller := new(RelayController)
	controller.Init(&model.Config{}, relaySignerService)
	return controller
}

// TestDecodedCarriesEveryContractField cubre la tarea 5.2: relay.decoded lleva los once campos
// que la pagina consume, incluidos los cuatro que salen del sufijo del modelo de gas.
func TestDecodedCarriesEveryContractField(t *testing.T) {
	withBus(t)

	nodeAddress := common.HexToAddress("0x173cf75f0905338597fcd38f5ce13e6840b230e9")
	expiration := uint64(time.Now().Unix() + 600)
	to := common.HexToAddress("0x82a978b3f5962a5b0957d9ee9eef472ee55b42f1")
	rawTx := signedRawTx(t, &to, gasModelData("0x6057361d", nodeAddress, expiration), 34, 200000)

	controllerWithService().SignTransaction(httptest.NewRecorder(), rawTxRequest(rawTx))

	decoded, found := eventNamed("relay.decoded")
	if !found {
		t.Fatalf("no se emitio relay.decoded; el bus tiene %v", eventNames())
	}

	contractFields := []string{
		"from", "to", "isDeploy", "nonce", "userGasLimit", "metaTxGasLimit",
		"nodeAddress", "expiration", "expiresInSeconds", "dataBytes", "selector",
	}
	for _, field := range contractFields {
		if _, present := decoded.Line[field]; !present {
			t.Errorf("falta el campo %q del contrato: %v", field, decoded.Line)
		}
	}
	if decoded.Field("nodeAddress") != nodeAddress.Hex() {
		t.Errorf("nodeAddress = %v, se esperaba %s", decoded.Field("nodeAddress"), nodeAddress.Hex())
	}
	if decoded.Field("expiration") != expiration {
		t.Errorf("expiration = %v, se esperaba %d", decoded.Field("expiration"), expiration)
	}
	if segundos, _ := decoded.Field("expiresInSeconds").(int64); segundos <= 0 || segundos > 600 {
		t.Errorf("expiresInSeconds = %v, se esperaba un valor cercano a 600", decoded.Field("expiresInSeconds"))
	}
	if decoded.Field("selector") != "0x6057361d" {
		t.Errorf("selector = %v, se esperaba 0x6057361d", decoded.Field("selector"))
	}
	if decoded.Field("to") != to.Hex() || decoded.Field("isDeploy") != false {
		t.Errorf("to/isDeploy incorrectos: %v", decoded.Line)
	}
	if decoded.Field("nonce") != uint64(34) || decoded.Field("userGasLimit") != uint64(200000) {
		t.Errorf("nonce/userGasLimit incorrectos: %v", decoded.Line)
	}
	if decoded.Field("dataBytes") != 68 {
		t.Errorf("dataBytes = %v, se esperaban 68 (4 de selector + 64 de sufijo)", decoded.Field("dataBytes"))
	}
	// metaTxGasLimit = dataBytes*105 + 300000 + userGasLimit, igual que en el relayer de Node.
	if decoded.Field("metaTxGasLimit") != uint64(68*105+300000+200000) {
		t.Errorf("metaTxGasLimit = %v, se esperaba %d", decoded.Field("metaTxGasLimit"), 68*105+300000+200000)
	}
}

// TestDeployIsReportedAsSuch: una metatx sin destino es un deploy.
func TestDeployIsReportedAsSuch(t *testing.T) {
	withBus(t)

	rawTx := signedRawTx(t, nil, gasModelData("0x60806040", common.HexToAddress("0x1"), uint64(time.Now().Unix()+60)), 0, 500000)
	controllerWithService().SignTransaction(httptest.NewRecorder(), rawTxRequest(rawTx))

	decoded, found := eventNamed("relay.decoded")
	if !found {
		t.Fatalf("no se emitio relay.decoded; el bus tiene %v", eventNames())
	}
	if decoded.Field("isDeploy") != true {
		t.Errorf("isDeploy = %v, se esperaba true", decoded.Field("isDeploy"))
	}
	if decoded.Field("to") != nil {
		t.Errorf("to = %v, un deploy no tiene destino", decoded.Field("to"))
	}
}

// TestSuffixDecodingNeverRejects cubre la tarea 5.3: el sufijo se decodifica SOLO para registrar.
// Un data mas corto que el sufijo emite los cuatro campos sin valor, y la metatx sigue su curso.
func TestSuffixDecodingNeverRejects(t *testing.T) {
	withBus(t)

	// 36 bytes de data: menos que los 64 del sufijo.
	to := common.HexToAddress("0x6e6bbf31aa45042d53128339383fcd1c377b42c7")
	corto, _ := hex.DecodeString("6057361d" + strings.Repeat("0", 64))
	rawTx := signedRawTx(t, &to, corto, 1, 200000)

	controllerWithService().SignTransaction(httptest.NewRecorder(), rawTxRequest(rawTx))

	decoded, found := eventNamed("relay.decoded")
	if !found {
		t.Fatalf("un data sin sufijo debe emitir relay.decoded igual; el bus tiene %v", eventNames())
	}
	for _, field := range []string{"nodeAddress", "expiration", "expiresInSeconds", "selector"} {
		if _, present := decoded.Line[field]; !present {
			t.Errorf("el campo %q debe emitirse igual aunque no tenga valor aplicable: %v", field, decoded.Line)
		}
		if decoded.Field(field) != nil {
			t.Errorf("%q = %v, se esperaba sin valor", field, decoded.Field(field))
		}
	}
	// Los campos que no dependen del sufijo siguen estando.
	if decoded.Field("from") == nil || decoded.Field("dataBytes") != 36 {
		t.Errorf("los campos que no dependen del sufijo se perdieron: %v", decoded.Line)
	}
	// Y la metatx no se rechazo POR el sufijo: llego hasta la verificacion del cupo de gas.
	if rejected, found := eventNamed("relay.rejected"); found {
		motivo, _ := rejected.Field("error").(string)
		if strings.Contains(strings.ToLower(motivo), "suffix") || strings.Contains(strings.ToLower(motivo), "sufijo") {
			t.Errorf("el sufijo no debe rechazar la metatx, pero el motivo fue: %q", motivo)
		}
		if rejected.Seq < decoded.Seq {
			t.Errorf("el rechazo (seq %d) ocurrio antes de relay.decoded (seq %d)", rejected.Seq, decoded.Seq)
		}
	}
}

// eventNames lista los eventos retenidos, para los mensajes de error.
func eventNames() []string {
	var names []string
	for _, event := range events.Replay(0) {
		names = append(names, event.Name())
	}
	return names
}
