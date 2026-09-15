package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LACNetNetworks/gas-relay-signer/model"
	"github.com/ethereum/go-ethereum/common"
	"github.com/spf13/viper"
)

func getJSON(t *testing.T, mux *http.ServeMux, path string) (int, map[string]interface{}) {
	t.Helper()
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))

	var body map[string]interface{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("GET %s no devolvio JSON (%v): %s", path, err, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("GET %s -> Content-Type %q", path, got)
	}
	return recorder.Code, body
}

// --------------------------------------------------------------------------- GET /info

// TestInfoReportsIdentityAndAddresses cubre las tareas 3.1, 3.2 y 3.3: identidad, direcciones con
// su origen, y el estado operativo que se lee de la cadena.
func TestInfoReportsIdentityAndAddresses(t *testing.T) {
	withBus(t)
	node := mockNode(t, common.HexToAddress("0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91"), hubRejectedReceipt)
	defer node.Close()
	mux := routedMux(t, node.URL)

	status, info := getJSON(t, mux, "/info")
	if status != http.StatusOK {
		t.Fatalf("GET /info -> %d", status)
	}

	for _, field := range []string{
		"nodeAddress", "relayHubAddress", "relayHubSource", "relayHubProxyAddress",
		"chainId", "rpcUrl", "nodeBalance", "currentGasLimit",
		"accountRulesAddress", "accountRulesSource", "nodePermitted", "enforceAccountRules",
		"minExpirationSeconds", "expirationToleranceSeconds",
		"reorderEnabled", "reorderWindowMs", "maxInflightPerUser", "receiptTimeoutMs",
		"autoNonce", "autoNonceTicketMs",
	} {
		if _, present := info[field]; !present {
			t.Errorf("falta el campo %q: %v", field, info)
		}
	}

	if address, _ := info["nodeAddress"].(string); !strings.HasPrefix(address, "0x") || len(address) != 42 {
		t.Errorf("nodeAddress = %v, se esperaba una direccion", info["nodeAddress"])
	}
	if info["relayHubSource"] != "proxy" {
		t.Errorf("relayHubSource = %v: en este servicio la direccion siempre se resuelve del proxy",
			info["relayHubSource"])
	}
	if info["rpcUrl"] != node.URL {
		t.Errorf("rpcUrl = %v, se esperaba %s", info["rpcUrl"], node.URL)
	}
	// El proxy es el que va como trustedForwarder de los contratos: tiene que estar.
	if proxy, _ := info["relayHubProxyAddress"].(string); proxy == "" {
		t.Errorf("relayHubProxyAddress vacio: es el valor que un integrador viene a buscar")
	}
	// Balance y cupo salen de la cadena, no de la configuracion.
	if balance, _ := info["nodeBalance"].(string); balance == "" {
		t.Errorf("nodeBalance = %v, se esperaba el balance leido del nodo", info["nodeBalance"])
	}
	if gasLimit, _ := info["currentGasLimit"].(string); gasLimit == "" {
		t.Errorf("currentGasLimit = %v, se esperaba el cupo leido del hub", info["currentGasLimit"])
	}
}

// TestInfoReportsPermissioningDisabled cubre la mitad de la tarea 3.4: con el permisionado
// deshabilitado se responde igual y no se consulta el contrato de reglas.
func TestInfoReportsPermissioningDisabled(t *testing.T) {
	withBus(t)
	node := mockNode(t, common.HexToAddress("0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91"), hubRejectedReceipt)
	defer node.Close()
	mux := routedMux(t, node.URL)

	status, info := getJSON(t, mux, "/info")
	if status != http.StatusOK {
		t.Fatalf("GET /info -> %d", status)
	}
	if info["enforceAccountRules"] != false {
		t.Errorf("enforceAccountRules = %v, se esperaba false", info["enforceAccountRules"])
	}
	if info["accountRulesAddress"] != nil {
		t.Errorf("accountRulesAddress = %v: con el permisionado apagado no hay contrato que informar",
			info["accountRulesAddress"])
	}
	if info["nodePermitted"] != nil {
		t.Errorf("nodePermitted = %v: no se consulta el contrato de reglas si el chequeo esta apagado",
			info["nodePermitted"])
	}
}

// TestInfoReportsEffectiveParameters cubre la tarea 3.5: los parametros informados son los que el
// servicio esta usando, no los del archivo cuando una clave fue descartada por invalida.
func TestInfoReportsEffectiveParameters(t *testing.T) {
	withBus(t)
	node := mockNode(t, common.HexToAddress("0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91"), hubRejectedReceipt)
	defer node.Close()
	mux := routedMux(t, node.URL)

	_, info := getJSON(t, mux, "/info")

	// El controller de prueba se arma con los defaults, que son los mismos que aplica el arranque
	// cuando una clave falta o se descarta.
	if info["reorderEnabled"] != false {
		t.Errorf("reorderEnabled = %v, se esperaba false", info["reorderEnabled"])
	}
	// Este servicio no valida la expiracion: lo informa en lugar de callarlo.
	if info["minExpirationSeconds"] != float64(0) || info["expirationToleranceSeconds"] != float64(0) {
		t.Errorf("los parametros de expiracion deberian informarse en cero: %v / %v",
			info["minExpirationSeconds"], info["expirationToleranceSeconds"])
	}
	// El reparto de nonces esta apagado por defecto, y se informa como tal.
	if info["autoNonce"] != false {
		t.Errorf("autoNonce = %v, el reparto esta apagado por defecto", info["autoNonce"])
	}
}

// TestInfoSurvivesAnUnreachableNode cubre la tarea 3.6: una consulta a la cadena que falla deja ese
// campo sin valor y el resto de la respuesta completa.
func TestInfoSurvivesAnUnreachableNode(t *testing.T) {
	withBus(t)
	node := mockNode(t, common.HexToAddress("0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91"), hubRejectedReceipt)
	mux := routedMux(t, node.URL)
	// El nodo se cae despues de que el servicio arranco, que es el escenario real.
	node.Close()

	status, info := getJSON(t, mux, "/info")
	if status != http.StatusOK {
		t.Errorf("GET /info -> %d: es la ruta a la que se acude cuando algo anda mal, no puede caerse", status)
	}
	if info["chainId"] != nil || info["nodeBalance"] != nil {
		t.Errorf("los campos que exigen consultar la cadena deberian quedar sin valor: %v / %v",
			info["chainId"], info["nodeBalance"])
	}
	// Lo que sale de la configuracion llega igual.
	if info["rpcUrl"] == nil || info["relayHubProxyAddress"] == nil {
		t.Errorf("se perdio lo que no depende de la cadena: %v", info)
	}
	for _, field := range []string{"chainId", "nodeBalance", "currentGasLimit"} {
		if _, present := info[field]; !present {
			t.Errorf("el campo %q se omitio en lugar de informarse sin valor", field)
		}
	}
}

// --------------------------------------------------------------------------- GET /nonce/{address}

// TestNonceReportsBothValues cubre las tareas 4.1 y 4.2.
func TestNonceReportsBothValues(t *testing.T) {
	withBus(t)
	node := mockNode(t, common.HexToAddress("0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91"), hubRejectedReceipt)
	defer node.Close()
	mux := routedMux(t, node.URL)

	const address = "0x82a978b3f5962a5b0957d9ee9eef472ee55b42f1"
	status, body := getJSON(t, mux, "/nonce/"+address)
	if status != http.StatusOK {
		t.Fatalf("GET /nonce -> %d: %v", status, body)
	}

	for _, field := range []string{"address", "nonce", "nonceHex", "nextNonce", "nextNonceHex", "pending"} {
		if _, present := body[field]; !present {
			t.Errorf("falta el campo %q: %v", field, body)
		}
	}
	// La direccion se devuelve normalizada.
	if body["address"] != common.HexToAddress(address).Hex() {
		t.Errorf("address = %v, se esperaba la forma normalizada", body["address"])
	}
	// Sin actividad previa los dos nonces coinciden y no hay nada en vuelo.
	if body["nonce"] != body["nextNonce"] {
		t.Errorf("sin metatx en vuelo los dos nonces deberian coincidir: %v y %v",
			body["nonce"], body["nextNonce"])
	}
	if body["pending"] != float64(0) {
		t.Errorf("pending = %v, se esperaba 0", body["pending"])
	}
	hex, _ := body["nonceHex"].(string)
	if !strings.HasPrefix(hex, "0x") {
		t.Errorf("nonceHex = %v, se esperaba hexadecimal", body["nonceHex"])
	}
}

// TestNoncePeekDoesNotChangeTheAnswer cubre la tarea 4.3: el parametro se acepta y, mientras no
// haya reserva, no cambia nada. Ver design.md, D7.
func TestNoncePeekDoesNotChangeTheAnswer(t *testing.T) {
	withBus(t)
	node := mockNode(t, common.HexToAddress("0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91"), hubRejectedReceipt)
	defer node.Close()
	mux := routedMux(t, node.URL)

	const address = "0x82a978b3f5962a5b0957d9ee9eef472ee55b42f1"
	_, sinPeek := getJSON(t, mux, "/nonce/"+address)
	for _, variante := range []string{"?peek=true", "?peek=1", "?peek=cualquier-cosa"} {
		_, conPeek := getJSON(t, mux, "/nonce/"+address+variante)
		for _, field := range []string{"address", "nonce", "nextNonce", "pending"} {
			if sinPeek[field] != conPeek[field] {
				t.Errorf("%s cambio %q: %v vs %v", variante, field, sinPeek[field], conPeek[field])
			}
		}
	}
}

// TestNonceIsCaseInsensitive cubre la otra mitad de la tarea 4.4: la misma direccion escrita de dos
// formas no puede producir dos estados distintos.
func TestNonceIsCaseInsensitive(t *testing.T) {
	withBus(t)
	node := mockNode(t, common.HexToAddress("0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91"), hubRejectedReceipt)
	defer node.Close()
	mux := routedMux(t, node.URL)

	minusculas := "0x82a978b3f5962a5b0957d9ee9eef472ee55b42f1"
	checksum := common.HexToAddress(minusculas).Hex()

	_, primera := getJSON(t, mux, "/nonce/"+minusculas)
	_, segunda := getJSON(t, mux, "/nonce/"+checksum)

	for _, field := range []string{"address", "nonce", "nextNonce", "pending"} {
		if primera[field] != segunda[field] {
			t.Errorf("la misma direccion escrita distinto cambio %q: %v vs %v",
				field, primera[field], segunda[field])
		}
	}
}

// TestNonceMatchesTheJSONRPCDoor cubre la tarea 4.5: las dos puertas informan el mismo nonce.
func TestNonceMatchesTheJSONRPCDoor(t *testing.T) {
	withBus(t)
	node := mockNode(t, common.HexToAddress("0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91"), hubRejectedReceipt)
	defer node.Close()
	mux := routedMux(t, node.URL)

	const address = "0x82a978b3f5962a5b0957d9ee9eef472ee55b42f1"
	_, rest := getJSON(t, mux, "/nonce/"+address)

	recorder := httptest.NewRecorder()
	body := `{"jsonrpc":"2.0","id":1,"method":"eth_getTransactionCount","params":["` + address + `","pending"]}`
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))

	var jsonrpc struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &jsonrpc); err != nil {
		t.Fatalf("la respuesta JSON-RPC no se pudo leer: %s", recorder.Body.String())
	}
	if jsonrpc.Result != rest["nextNonceHex"] {
		t.Errorf("las dos puertas informan nonces distintos: JSON-RPC %q y REST %v",
			jsonrpc.Result, rest["nextNonceHex"])
	}
}

// TestInfoReportsPermissioningEnabled cubre la otra mitad de la tarea 3.4.
func TestInfoReportsPermissioningEnabled(t *testing.T) {
	withBus(t)
	node := mockNode(t, common.HexToAddress("0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91"), hubRejectedReceipt)
	defer node.Close()

	controller := relayingController(t, node.URL)
	controller.Config.Security.PermissionsEnabled = true
	controller.Config.Security.AccountContractAddress = "0x4683519EF834572017Cb583246B717449A4B752c"
	mux := http.NewServeMux()
	controller.Routes(mux)

	_, info := getJSON(t, mux, "/info")

	if info["enforceAccountRules"] != true {
		t.Errorf("enforceAccountRules = %v, se esperaba true", info["enforceAccountRules"])
	}
	if info["accountRulesAddress"] != controller.Config.Security.AccountContractAddress {
		t.Errorf("accountRulesAddress = %v", info["accountRulesAddress"])
	}
	if info["accountRulesSource"] != "config" {
		t.Errorf("accountRulesSource = %v: en este servicio sale de config.toml", info["accountRulesSource"])
	}
	if info["nodePermitted"] == nil {
		t.Errorf("nodePermitted sin valor: con el permisionado activo hay que informar si el nodo lo esta")
	}
}

// TestInfoReportsWhatTheServiceUsesNotTheFile cubre la tarea 3.5 de punta a punta: una clave
// invalida en el archivo cae a su default, y es el default lo que informa /info.
func TestInfoReportsWhatTheServiceUsesNotTheFile(t *testing.T) {
	withBus(t)
	node := mockNode(t, common.HexToAddress("0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91"), hubRejectedReceipt)
	defer node.Close()

	// Un config.toml con la ventana de reorden fuera de rango.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"),
		[]byte("[application]\nport = \"9001\"\n\n[reorder]\nwindowMs = -1\n"), 0o600); err != nil {
		t.Fatalf("no se pudo escribir el config de prueba: %v", err)
	}
	v := viper.New()
	v.SetConfigName("config")
	v.AddConfigPath(dir)
	if err := v.ReadInConfig(); err != nil {
		t.Fatalf("no se pudo leer el config de prueba: %v", err)
	}
	reorder, dashboard, logCfg, _, discarded := model.LoadRuntimeBlocks(v)
	if len(discarded) == 0 {
		t.Fatal("se esperaba que la clave invalida se descartara")
	}

	controller := relayingController(t, node.URL)
	controller.Config.Reorder, controller.Config.Dashboard, controller.Config.Log = reorder, dashboard, logCfg
	mux := http.NewServeMux()
	controller.Routes(mux)

	_, info := getJSON(t, mux, "/info")
	if info["reorderWindowMs"] != float64(model.DefaultReorderWindowMs) {
		t.Errorf("reorderWindowMs = %v, se esperaba el default %d que el servicio esta usando, no el -1 del archivo",
			info["reorderWindowMs"], model.DefaultReorderWindowMs)
	}
}

// TestNonceAdvancesWithInflightMetaTx cubre la otra mitad de las tareas 4.1 y 4.2: con metatx en
// vuelo, el proximo nonce supera al de la cadena y se informa cuantas hay.
func TestNonceAdvancesWithInflightMetaTx(t *testing.T) {
	withBus(t)
	node := mockNode(t, common.HexToAddress("0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91"), hubRejectedReceipt)
	defer node.Close()

	controller := relayingController(t, node.URL)
	mux := http.NewServeMux()
	controller.Routes(mux)

	// El nodo simulado informa el nonce 0x159 = 345 en la cadena; se relaya con uno mas alto para
	// que el cache quede por delante, que es lo que ocurre al encadenar.
	to := common.HexToAddress("0x82a978b3f5962a5b0957d9ee9eef472ee55b42f1")
	data := gasModelData("0x6057361d", common.HexToAddress("0x173cf75f0905338597fcd38f5ce13e6840b230e9"), 4102444800)
	relayed := httptest.NewRecorder()
	mux.ServeHTTP(relayed, rawTxRequest(signedRawTx(t, &to, data, 400, 200000)))
	if !strings.Contains(relayed.Body.String(), `"result"`) {
		t.Fatalf("la metatx no llego a enviarse: %s", relayed.Body.String())
	}

	decoded, _ := eventNamed("relay.decoded")
	from, _ := decoded.Field("from").(string)
	if from == "" {
		t.Fatal("no se pudo determinar el emisor de la metatx relayada")
	}

	_, body := getJSON(t, mux, "/nonce/"+from)
	if body["nonce"] == body["nextNonce"] {
		t.Errorf("con una metatx en vuelo los nonces deberian diferir: %v y %v",
			body["nonce"], body["nextNonce"])
	}
	if pending, _ := body["pending"].(float64); pending < 1 {
		t.Errorf("pending = %v, se esperaba al menos una metatx en vuelo", body["pending"])
	}
}
