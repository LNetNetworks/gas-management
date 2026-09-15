package controller

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	audit "github.com/LACNetNetworks/gas-relay-signer/audit"
	"github.com/LACNetNetworks/gas-relay-signer/events"
	"github.com/LACNetNetworks/gas-relay-signer/model"
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
