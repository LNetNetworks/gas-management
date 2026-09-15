package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

// muxWithDashboard arma el mux con el monitor habilitado o deshabilitado.
func muxWithDashboard(t *testing.T, enabled bool) *http.ServeMux {
	t.Helper()
	node := mockNode(t, common.HexToAddress("0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91"), hubRejectedReceipt)
	t.Cleanup(node.Close)
	controller := relayingController(t, node.URL)
	controller.Config.Dashboard.Enabled = enabled
	mux := http.NewServeMux()
	controller.Routes(mux)
	return mux
}

func get(t *testing.T, mux *http.ServeMux, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(method, path, nil))
	return recorder
}

// TestDashboardRoutesDoNotExistWhenDisabled cubre la tarea 2.1: con el monitor apagado esos paths
// se atienden como cualquier path desconocido, y NO responden 405. Ver design.md, D7.
func TestDashboardRoutesDoNotExistWhenDisabled(t *testing.T) {
	withBus(t)
	mux := muxWithDashboard(t, false)

	for _, path := range []string{"/dashboard", "/dashboard/stream"} {
		recorder := get(t, mux, http.MethodGet, path)

		if recorder.Code == http.StatusMethodNotAllowed {
			t.Errorf("GET %s -> 405: con el monitor apagado no hay ninguna ruta registrada para ese path", path)
		}
		if strings.Contains(recorder.Body.String(), "<!doctype html>") {
			t.Errorf("GET %s sirvio la pagina con el monitor apagado", path)
		}
		// Lo atiende el camino JSON-RPC, como cualquier path sin manejador propio.
		if recorder.Code != http.StatusOK {
			t.Errorf("GET %s -> %d, se esperaba que lo atendiera el camino JSON-RPC", path, recorder.Code)
		}
	}

	// Y un POST con cuerpo JSON-RPC a esos paths se atiende como siempre.
	recorder := httptest.NewRecorder()
	body := `{"jsonrpc":"2.0","id":1,"method":"eth_sendRawTransaction","params":["0xdeadbee"]}`
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/dashboard", strings.NewReader(body)))
	if !strings.Contains(recorder.Body.String(), "jsonrpc") {
		t.Errorf("POST /dashboard con el monitor apagado no lo atendio el camino JSON-RPC: %s", recorder.Body.String())
	}
}

// TestDashboardRoutesExistWhenEnabled cubre la tarea 2.2.
func TestDashboardRoutesExistWhenEnabled(t *testing.T) {
	withBus(t)
	mux := muxWithDashboard(t, true)

	pagina := get(t, mux, http.MethodGet, "/dashboard")
	if pagina.Code != http.StatusOK {
		t.Fatalf("GET /dashboard -> %d", pagina.Code)
	}
	if !strings.Contains(pagina.Body.String(), "<!doctype html>") {
		t.Errorf("GET /dashboard no sirvio la pagina")
	}

	// El metodo equivocado responde 405 y no cae al camino JSON-RPC.
	for _, caso := range []struct{ method, path string }{
		{http.MethodPost, "/dashboard"},
		{http.MethodPost, "/dashboard/stream"},
		{http.MethodDelete, "/dashboard"},
	} {
		recorder := get(t, mux, caso.method, caso.path)
		if recorder.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s -> %d, se esperaba 405", caso.method, caso.path, recorder.Code)
		}
		if strings.Contains(recorder.Body.String(), "jsonrpc") {
			t.Errorf("%s %s lo atendio el camino JSON-RPC", caso.method, caso.path)
		}
	}
}

// TestExistingRoutesUnchangedByConditionalRegistration cubre la tarea 2.3.
func TestExistingRoutesUnchangedByConditionalRegistration(t *testing.T) {
	withBus(t)

	for _, enabled := range []bool{false, true} {
		mux := muxWithDashboard(t, enabled)

		// El camino JSON-RPC responde igual con el monitor apagado y encendido.
		rechazo := httptest.NewRecorder()
		mux.ServeHTTP(rechazo, rawTxRequest("0xdeadbee"))
		esperado := `{"jsonrpc":"2.0","id":1,"error":{"code":-32012,"message":"Error Decoding Raw Transaction: encoding/hex: odd length hex string"}}`
		if got := strings.TrimSpace(rechazo.Body.String()); got != esperado {
			t.Errorf("con dashboard=%v la respuesta JSON-RPC cambio:\n  %s", enabled, got)
		}

		// Y las rutas de 02 siguen respondiendo.
		if code := get(t, mux, http.MethodGet, "/info").Code; code != http.StatusOK {
			t.Errorf("con dashboard=%v, GET /info -> %d", enabled, code)
		}
		if code := get(t, mux, http.MethodPost, "/info").Code; code != http.StatusMethodNotAllowed {
			t.Errorf("con dashboard=%v, POST /info -> %d, se esperaba 405", enabled, code)
		}
		if code := get(t, mux, http.MethodGet, "/nonce/").Code; code != http.StatusBadRequest {
			t.Errorf("con dashboard=%v, GET /nonce/ -> %d, se esperaba 400", enabled, code)
		}
	}
}
