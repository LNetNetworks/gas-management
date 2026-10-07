package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

// routedMux arma el mux del servicio con un controller utilizable contra el nodo simulado.
func routedMux(t *testing.T, nodeURL string) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	relayingController(t, nodeURL).Routes(mux)
	return mux
}

// TestMethodMismatchIsNotSwallowedByTheCatchAll cubre la tarea 1.1 y la tabla de D1. Es la razon de
// ser de esa decision: con los patrones con metodo de ServeMux, estas peticiones terminarian
// atendidas por el camino JSON-RPC con un 200 que el cliente no pidio.
func TestMethodMismatchIsNotSwallowedByTheCatchAll(t *testing.T) {
	withBus(t)
	node := mockNode(t, common.HexToAddress("0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91"), hubRejectedReceipt)
	defer node.Close()
	mux := routedMux(t, node.URL)

	for _, caso := range []struct{ method, path string }{
		{http.MethodPost, "/info"},
		{http.MethodPut, "/info"},
		{http.MethodPost, "/nonce/0x82a978b3f5962a5b0957d9ee9eef472ee55b42f1"},
		{http.MethodDelete, "/nonce/0x82a978b3f5962a5b0957d9ee9eef472ee55b42f1"},
	} {
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(caso.method, caso.path, nil))

		if recorder.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s -> %d, se esperaba 405", caso.method, caso.path, recorder.Code)
		}
		if strings.Contains(recorder.Body.String(), "jsonrpc") {
			t.Errorf("%s %s lo atendio el camino JSON-RPC: %s", caso.method, caso.path, recorder.Body.String())
		}
		if allow := recorder.Header().Get("Allow"); allow == "" {
			t.Errorf("%s %s no informa que metodo acepta", caso.method, caso.path)
		}
	}
}

// TestMissingOrInvalidAddressIsRejected cubre la tarea 1.2: sin direccion, o con algo que no lo es,
// se responde 400 y no cae al camino JSON-RPC.
func TestMissingOrInvalidAddressIsRejected(t *testing.T) {
	withBus(t)
	node := mockNode(t, common.HexToAddress("0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91"), hubRejectedReceipt)
	defer node.Close()
	mux := routedMux(t, node.URL)

	for _, path := range []string{"/nonce/", "/nonce/basura", "/nonce/0x123"} {
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))

		if recorder.Code != http.StatusBadRequest {
			t.Errorf("GET %s -> %d, se esperaba 400", path, recorder.Code)
		}
		if strings.Contains(recorder.Body.String(), "jsonrpc") {
			t.Errorf("GET %s lo atendio el camino JSON-RPC: %s", path, recorder.Body.String())
		}
	}
}

// TestNonceWithoutTrailingSlashRedirects: `/nonce` sin barra final lo redirige el mux, que es el
// comportamiento estandar y no hace falta tocarlo.
func TestNonceWithoutTrailingSlashRedirects(t *testing.T) {
	withBus(t)
	node := mockNode(t, common.HexToAddress("0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91"), hubRejectedReceipt)
	defer node.Close()
	mux := routedMux(t, node.URL)

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/nonce", nil))

	if recorder.Code != http.StatusMovedPermanently {
		t.Errorf("GET /nonce -> %d, se esperaba una redireccion", recorder.Code)
	}
	if destino := recorder.Header().Get("Location"); destino != "/nonce/" {
		t.Errorf("redirige a %q, se esperaba /nonce/", destino)
	}
}

// TestCatchAllStillReceivesEverythingElse cubre la tarea 1.3: ningun path que hoy llega al camino
// JSON-RPC dejo de llegar.
func TestCatchAllStillReceivesEverythingElse(t *testing.T) {
	withBus(t)
	node := mockNode(t, common.HexToAddress("0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91"), hubRejectedReceipt)
	defer node.Close()
	mux := routedMux(t, node.URL)

	// Paths que un cliente JSON-RPC puede usar, incluidos los que se parecen a las rutas nuevas.
	for _, path := range []string{"/", "/eth_call", "/cualquier/cosa", "/informacion", "/relayer", "/nonces"} {
		recorder := httptest.NewRecorder()
		body := `{"jsonrpc":"2.0","id":1,"method":"eth_sendRawTransaction","params":["0xdeadbee"]}`
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))

		if recorder.Code != http.StatusOK {
			t.Errorf("POST %s -> %d, se esperaba que lo atendiera el camino JSON-RPC", path, recorder.Code)
		}
		if !strings.Contains(recorder.Body.String(), "jsonrpc") {
			t.Errorf("POST %s no lo atendio el camino JSON-RPC: %s", path, recorder.Body.String())
		}
	}
}

// TestJSONRPCResponsesAreUnchanged cubre la tarea 1.4: `POST /` responde igual que antes de esta
// capacidad, en el caso exitoso y en el rechazado.
func TestJSONRPCResponsesAreUnchanged(t *testing.T) {
	withBus(t)
	node := mockNode(t, common.HexToAddress("0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91"), hubRejectedReceipt)
	defer node.Close()
	mux := routedMux(t, node.URL)

	// Rechazo: mismo cuerpo y mismo codigo de error que antes del router.
	rechazo := httptest.NewRecorder()
	mux.ServeHTTP(rechazo, rawTxRequest("0xdeadbee"))
	esperado := `{"jsonrpc":"2.0","id":1,"error":{"code":-32012,"message":"Error Decoding Raw Transaction: encoding/hex: odd length hex string"}}`
	if got := strings.TrimSpace(rechazo.Body.String()); got != esperado {
		t.Errorf("la respuesta de un rechazo cambio:\n  quedo:    %s\n  esperada: %s", got, esperado)
	}

	// Exito: la respuesta sigue siendo el hash y nada mas.
	to := common.HexToAddress("0x82a978b3f5962a5b0957d9ee9eef472ee55b42f1")
	data := gasModelData("0x6057361d", common.HexToAddress("0x173cf75f0905338597fcd38f5ce13e6840b230e9"), 4102444800)
	exito := httptest.NewRecorder()
	mux.ServeHTTP(exito, rawTxRequest(signedRawTx(t, &to, data, 7, 200000)))

	cuerpo := exito.Body.String()
	if !strings.Contains(cuerpo, `"result":"0x`) {
		t.Errorf("la respuesta de un relay exitoso cambio de forma: %s", cuerpo)
	}
	if strings.Contains(cuerpo, `"error"`) {
		t.Errorf("un relay que antes era exitoso ahora falla: %s", cuerpo)
	}
}
