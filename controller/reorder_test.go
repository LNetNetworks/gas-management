package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LACNetNetworks/gas-relay-signer/events"
	"github.com/LACNetNetworks/gas-relay-signer/model"
	"github.com/ethereum/go-ethereum/common"
)

// reorderingMux arma el ruteo completo con el reordenamiento encendido.
func reorderingMux(t *testing.T, windowMs int) *http.ServeMux {
	t.Helper()
	return relayMuxWith(t, receiptWith(hubLog(topicTransactionRelayed, dataRelayedOK)), mockNodeOptions{},
		func(controller *RelayController) {
			controller.Config.Reorder = model.ReorderConfig{
				Enabled:            true,
				WindowMs:           windowMs,
				MaxInflightPerUser: 16,
				ReceiptTimeoutMs:   60000,
			}
		})
}

// Una metatx retenida esperando su turno no puede demorar al resto del servicio: ni a las otras
// rutas ni al camino JSON-RPC. Cubre la tarea 3.6.
func TestHeldMetaTxDoesNotStallTheService(t *testing.T) {
	withBus(t)
	mux := reorderingMux(t, 3000)
	to := common.HexToAddress("0x82a978b3f5962a5b0957d9ee9eef472ee55b42f1")

	// Una metatx adelantada -el hub simulado va por el 345- queda retenida con la peticion abierta.
	retenida := make(chan int, 1)
	go func() {
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, rawTxRequest(relayableTx(t, &to, nonceOnChain+1)))
		retenida <- recorder.Code
	}()

	// Se espera a que la retencion este en curso: un sleep fijo como punto de sincronizacion
	// convierte al test en una apuesta sobre lo rapida que esta la maquina.
	esperarRetencion(t)

	// Mientras espera, las demas rutas responden con normalidad.
	inicio := time.Now()
	for _, ruta := range []string{"/info", "/nonce/0x82a978b3f5962a5b0957d9ee9eef472ee55b42f1"} {
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, ruta, nil))
		if recorder.Code != http.StatusOK {
			t.Errorf("%s respondio %d mientras habia una metatx retenida", ruta, recorder.Code)
		}
	}
	if transcurrido := time.Since(inicio); transcurrido > 2*time.Second {
		t.Errorf("las rutas se demoraron %v por una metatx retenida", transcurrido)
	}

	// Y el camino JSON-RPC tambien.
	jsonrpc := httptest.NewRecorder()
	mux.ServeHTTP(jsonrpc, rawTxRequest("0xdeadbee"))
	if !strings.Contains(jsonrpc.Body.String(), "jsonrpc") {
		t.Errorf("el camino JSON-RPC dejo de responder con una metatx retenida: %s", jsonrpc.Body.String())
	}

	// Al cerrarse el hueco, la retenida sale.
	enTurno := httptest.NewRecorder()
	mux.ServeHTTP(enTurno, rawTxRequest(relayableTx(t, &to, nonceOnChain)))
	if enTurno.Code != http.StatusOK {
		t.Fatalf("la metatx que faltaba respondio %d", enTurno.Code)
	}

	select {
	case code := <-retenida:
		if code != http.StatusOK {
			t.Errorf("la retenida respondio %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("la retenida no se desperto al cerrarse el hueco")
	}
}

// Con el reordenamiento APAGADO nada se retiene: una metatx adelantada se envia de inmediato y el
// bus no contiene eventos de retencion. Cubre parte de la tarea 6.3.
func TestNothingIsHeldWithReorderingOff(t *testing.T) {
	withBus(t)
	mux := relayMux(t, receiptWith(hubLog(topicTransactionRelayed, dataRelayedOK)))
	to := common.HexToAddress("0x82a978b3f5962a5b0957d9ee9eef472ee55b42f1")

	listo := make(chan int, 1)
	go func() {
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, rawTxRequest(relayableTx(t, &to, nonceOnChain+50)))
		listo <- recorder.Code
	}()

	select {
	case code := <-listo:
		if code != http.StatusOK {
			t.Errorf("con el reordenamiento apagado la metatx adelantada respondio %d", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("con el reordenamiento apagado no se puede retener nada, y quedo esperando")
	}

	for _, event := range events.Replay(0) {
		if event.Name() == "relay.held" || event.Name() == "relay.turn" {
			t.Errorf("con el reordenamiento apagado no se puede emitir %s", event.Name())
		}
	}
}

// GET /info informa los valores VIGENTES del bloque de reordenamiento, incluidos los del reparto de
// nonces, que hasta este cambio salian fijos. Cubre la tarea 6.2.
func TestInfoReportsTheReorderParameters(t *testing.T) {
	withBus(t)
	mux := relayMuxWith(t, receiptWith(hubLog(topicTransactionRelayed, dataRelayedOK)), mockNodeOptions{},
		func(controller *RelayController) {
			controller.Config.Reorder = model.ReorderConfig{
				Enabled:            true,
				WindowMs:           4500,
				MaxInflightPerUser: 9,
				ReceiptTimeoutMs:   12345,
				AutoNonce:          true,
				AutoNonceTicketMs:  1500,
			}
		})

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/info", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /info respondio %d", recorder.Code)
	}

	var info map[string]interface{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &info); err != nil {
		t.Fatalf("no se pudo leer la respuesta: %v", err)
	}

	esperado := map[string]interface{}{
		"reorderEnabled":     true,
		"reorderWindowMs":    float64(4500),
		"maxInflightPerUser": float64(9),
		"receiptTimeoutMs":   float64(12345),
		"autoNonce":          true,
		"autoNonceTicketMs":  float64(1500),
	}
	for campo, valor := range esperado {
		if info[campo] != valor {
			t.Errorf("%s = %v, se esperaba %v", campo, info[campo], valor)
		}
	}
}

// esperarRetencion espera a que el bus muestre una metatx retenida.
func esperarRetencion(t *testing.T) {
	t.Helper()
	limite := time.Now().Add(5 * time.Second)
	for time.Now().Before(limite) {
		for _, event := range events.Replay(0) {
			if event.Name() == "relay.held" {
				return
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("ninguna metatx quedo retenida")
}
