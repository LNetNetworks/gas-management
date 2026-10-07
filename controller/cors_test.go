package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

// handlerWithOrigins arma el manejador completo del servicio con los origenes indicados.
func handlerWithOrigins(t *testing.T, origins ...string) http.Handler {
	t.Helper()
	node := mockNode(t, common.HexToAddress("0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91"), hubRejectedReceipt)
	t.Cleanup(node.Close)
	controller := relayingController(t, node.URL)
	controller.Config.CORS.AllowedOrigins = origins
	controller.Config.Dashboard.Enabled = true
	return controller.Handler()
}

func request(t *testing.T, handler http.Handler, method, path, origin string) *httptest.ResponseRecorder {
	if t != nil {
		t.Helper()
	}
	req := httptest.NewRequest(method, path, nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

// TestNoCorsHeadersWithoutConfiguredOrigins cubre la tarea 6.2: sin origenes configurados el
// servicio se comporta exactamente como antes de esta capacidad.
func TestNoCorsHeadersWithoutConfiguredOrigins(t *testing.T) {
	withBus(t)
	handler := handlerWithOrigins(t)

	recorder := request(t, handler, http.MethodGet, "/info", "https://dapp.example")

	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /info -> %d", recorder.Code)
	}
	for _, header := range []string{"Access-Control-Allow-Origin", "Access-Control-Allow-Methods", "Vary"} {
		if value := recorder.Header().Get(header); value != "" {
			t.Errorf("sin origenes configurados no deberia emitirse %s, quedo %q", header, value)
		}
	}
}

// TestCorsAuthorisesOnlyConfiguredOrigins cubre la tarea 6.3.
func TestCorsAuthorisesOnlyConfiguredOrigins(t *testing.T) {
	withBus(t)
	handler := handlerWithOrigins(t, "https://dapp.example", "https://otra.example")

	t.Run("origen configurado", func(t *testing.T) {
		recorder := request(t, handler, http.MethodGet, "/info", "https://dapp.example")
		if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "https://dapp.example" {
			t.Errorf("Access-Control-Allow-Origin = %q", got)
		}
		if got := recorder.Header().Get("Access-Control-Allow-Methods"); got == "" {
			t.Errorf("no se declararon los metodos aceptados")
		}
		if got := recorder.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, "Content-Type") {
			t.Errorf("Access-Control-Allow-Headers = %q", got)
		}
		// Sin esto una cache intermedia podria servirle a un origen lo autorizado para otro.
		if got := recorder.Header().Get("Vary"); !strings.Contains(got, "Origin") {
			t.Errorf("Vary = %q, se esperaba que incluyera Origin", got)
		}
	})

	t.Run("origen no configurado", func(t *testing.T) {
		recorder := request(t, handler, http.MethodGet, "/info", "https://ajena.example")
		if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("se autorizo un origen no configurado: %q", got)
		}
		// Pero la peticion se procesa igual: la restriccion la aplica el navegador, no el servicio.
		if recorder.Code != http.StatusOK {
			t.Errorf("GET /info desde un origen no configurado -> %d, se esperaba que se procesara", recorder.Code)
		}
		if recorder.Body.Len() == 0 {
			t.Errorf("la respuesta llego vacia: el servicio no debe rechazar la peticion")
		}
	})
}

// TestCorsWrapperReachesEveryRoute cubre la tarea 6.1: las cabeceras alcanzan a todas las rutas,
// porque van como envoltura y no dentro de cada manejador.
func TestCorsWrapperReachesEveryRoute(t *testing.T) {
	withBus(t)
	handler := handlerWithOrigins(t, "https://dapp.example")

	for _, caso := range []struct{ method, path string }{
		{http.MethodGet, "/info"},
		{http.MethodGet, "/nonce/0x82a978b3f5962a5b0957d9ee9eef472ee55b42f1"},
		{http.MethodGet, "/dashboard"},
		{http.MethodPost, "/"},
		{http.MethodGet, "/un/path/cualquiera"},
	} {
		recorder := request(t, handler, caso.method, caso.path, "https://dapp.example")
		if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "https://dapp.example" {
			t.Errorf("%s %s no llevo la autorizacion: %q", caso.method, caso.path, got)
		}
	}
}

// TestPreflightIsAnsweredByTheWrapper cubre la tarea 6.4: la consulta previa no llega al manejador.
func TestPreflightIsAnsweredByTheWrapper(t *testing.T) {
	withBus(t)
	handler := handlerWithOrigins(t, "https://dapp.example")

	recorder := request(t, handler, http.MethodOptions, "/relay", "https://dapp.example")

	if recorder.Code != http.StatusNoContent {
		t.Errorf("la consulta previa -> %d, se esperaba 204", recorder.Code)
	}
	if recorder.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Errorf("la consulta previa no declaro los metodos aceptados")
	}
	// Lo esencial: no se relayo ninguna metatx.
	for _, event := range eventNames() {
		if strings.HasPrefix(event, "relay.") {
			t.Errorf("la consulta previa produjo %s: no debe llegar al manejador", event)
		}
	}
	if recorder.Body.Len() != 0 {
		t.Errorf("la consulta previa devolvio cuerpo: %s", recorder.Body.String())
	}
}

// TestCorsDoesNotChangeExistingResponses cubre la tarea 6.5.
func TestCorsDoesNotChangeExistingResponses(t *testing.T) {
	withBus(t)

	sinCors := handlerWithOrigins(t)
	conCors := handlerWithOrigins(t, "https://dapp.example")

	for _, caso := range []struct {
		nombre string
		enviar func(http.Handler) *httptest.ResponseRecorder
	}{
		{"rechazo JSON-RPC", func(h http.Handler) *httptest.ResponseRecorder {
			recorder := httptest.NewRecorder()
			h.ServeHTTP(recorder, rawTxRequest("0xdeadbee"))
			return recorder
		}},
		{"GET /nonce/ sin direccion", func(h http.Handler) *httptest.ResponseRecorder {
			return request(nil, h, http.MethodGet, "/nonce/", "https://dapp.example")
		}},
		{"POST /info con metodo equivocado", func(h http.Handler) *httptest.ResponseRecorder {
			return request(nil, h, http.MethodPost, "/info", "https://dapp.example")
		}},
	} {
		a, b := caso.enviar(sinCors), caso.enviar(conCors)
		if a.Code != b.Code {
			t.Errorf("%s: el codigo cambio con CORS, %d vs %d", caso.nombre, a.Code, b.Code)
		}
		if a.Body.String() != b.Body.String() {
			t.Errorf("%s: el cuerpo cambio con CORS:\n  sin: %s\n  con: %s", caso.nombre, a.Body.String(), b.Body.String())
		}
	}
}
