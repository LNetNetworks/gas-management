package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LACNetNetworks/gas-relay-signer/events"
	"github.com/LACNetNetworks/gas-relay-signer/model"
	"github.com/ethereum/go-ethereum/common"
)

// La direccion del writer node que sale de la WRITER_KEY que usan estos tests.
const writerNodeAddress = "0x63949701cD0e1Cc04Dfea0AFBf410968F10fF4b6"

// otroNodo es el nodo al que apuntan las metatx de `relayableTx`: no es este servicio.
const otroNodo = "0x173cf75f0905338597fcd38f5ce13e6840b230e9"

// validatingMux arma el ruteo con las exigencias del sufijo encendidas.
func validatingMux(t *testing.T, validation model.ValidationConfig, opts mockNodeOptions) *http.ServeMux {
	t.Helper()
	return relayMuxWith(t, receiptWith(hubLog(topicTransactionRelayed, dataRelayedOK)), opts,
		func(controller *RelayController) { controller.Config.Validation = validation })
}

// metaTxHacia arma una metatx dirigida a un nodo, con la expiracion indicada.
func metaTxHacia(t *testing.T, nodeAddress string, expiration uint64) string {
	t.Helper()
	to := common.HexToAddress("0x82a978b3f5962a5b0957d9ee9eef472ee55b42f1")
	data := gasModelData("0x6057361d", common.HexToAddress(nodeAddress), expiration)
	return signedRawTx(t, &to, data, nonceOnChain, 200000)
}

// seEnvio indica si alguna metatx llego a enviarse al hub.
func seEnvio() bool {
	for _, event := range events.Replay(0) {
		if event.Name() == "relay.sent" {
			return true
		}
	}
	return false
}

// Una metatx dirigida a OTRO writer node se rechaza antes de gastar una transaccion. Cubre 2.2.
func TestMetaTxForAnotherNodeIsRejected(t *testing.T) {
	withBus(t)
	mux := validatingMux(t, model.ValidationConfig{EnforceNodeAddress: true}, mockNodeOptions{})

	status, response := relayPost(t, mux, `{"rawTx":"`+metaTxHacia(t, otroNodo, 4102444800)+`"}`)

	if status != http.StatusBadRequest {
		t.Errorf("respondio %d, se esperaba 400", status)
	}
	if response["code"] != "WRONG_NODE_ADDRESS" {
		t.Errorf("code = %v, se esperaba WRONG_NODE_ADDRESS", response["code"])
	}
	// Las direcciones viajan en su forma con mayusculas de checksum: se comparan sin distinguir.
	motivo := strings.ToLower(response["error"].(string))
	if !strings.Contains(motivo, strings.ToLower(otroNodo)) ||
		!strings.Contains(motivo, strings.ToLower(writerNodeAddress)) {
		t.Errorf("el motivo tiene que indicar los dos nodos: %q", response["error"])
	}
	if seEnvio() {
		t.Error("no se tenia que enviar nada al hub")
	}
}

// Una metatx dirigida a ESTE nodo pasa la validacion.
func TestMetaTxForThisNodeIsAccepted(t *testing.T) {
	withBus(t)
	mux := validatingMux(t, model.ValidationConfig{EnforceNodeAddress: true}, mockNodeOptions{})

	status, response := relayPost(t, mux, `{"rawTx":"`+metaTxHacia(t, writerNodeAddress, 4102444800)+`"}`)

	if status != http.StatusOK {
		t.Errorf("respondio %d con %v, se esperaba 200", status, response["error"])
	}
}

// Una metatx ya vencida se rechaza con EXPIRED. Cubre 2.3.
func TestExpiredMetaTxIsRejected(t *testing.T) {
	withBus(t)
	mux := validatingMux(t, model.ValidationConfig{EnforceExpiration: true, MinExpirationSeconds: 300,
		ExpirationToleranceSeconds: 2}, mockNodeOptions{})

	vencida := uint64(time.Now().Add(-time.Minute).Unix())
	status, response := relayPost(t, mux, `{"rawTx":"`+metaTxHacia(t, otroNodo, vencida)+`"}`)

	if status != http.StatusBadRequest {
		t.Errorf("respondio %d, se esperaba 400", status)
	}
	if response["code"] != "EXPIRED" {
		t.Errorf("code = %v, se esperaba EXPIRED", response["code"])
	}
	detalle, _ := response["details"].(map[string]interface{})
	if detalle["expiration"] == nil || detalle["now"] == nil {
		t.Errorf("el detalle tiene que informar la expiracion y el instante evaluado: %v", response["details"])
	}
	if seEnvio() {
		t.Error("no se tenia que enviar nada al hub")
	}
}

// Una metatx con menos ventana que el minimo se rechaza; una al filo, dentro de la tolerancia, pasa.
// Cubre 2.4.
func TestExpirationWindowIsEnforcedWithTolerance(t *testing.T) {
	validation := model.ValidationConfig{EnforceExpiration: true, MinExpirationSeconds: 300, ExpirationToleranceSeconds: 5}

	t.Run("por debajo del limite efectivo", func(t *testing.T) {
		withBus(t)
		mux := validatingMux(t, validation, mockNodeOptions{})

		justa := uint64(time.Now().Add(100 * time.Second).Unix())
		status, response := relayPost(t, mux, `{"rawTx":"`+metaTxHacia(t, otroNodo, justa)+`"}`)

		if status != http.StatusBadRequest {
			t.Errorf("respondio %d, se esperaba 400", status)
		}
		if response["code"] != "EXPIRATION_TOO_LOW" {
			t.Errorf("code = %v, se esperaba EXPIRATION_TOO_LOW", response["code"])
		}
		detalle, _ := response["details"].(map[string]interface{})
		for _, campo := range []string{"remainingSeconds", "minimumSeconds", "toleranceSeconds"} {
			if detalle[campo] == nil {
				t.Errorf("el detalle tiene que informar %s: %v", campo, response["details"])
			}
		}
		if seEnvio() {
			t.Error("no se tenia que enviar nada al hub")
		}
	})

	t.Run("al filo, dentro de la tolerancia", func(t *testing.T) {
		withBus(t)
		mux := validatingMux(t, validation, mockNodeOptions{})

		// Firmada para `ahora + 300`, llega con 298: es el cliente que hizo lo correcto.
		alFilo := uint64(time.Now().Add(298 * time.Second).Unix())
		status, response := relayPost(t, mux, `{"rawTx":"`+metaTxHacia(t, otroNodo, alFilo)+`"}`)

		if status != http.StatusOK {
			t.Errorf("respondio %d con %v: una metatx al filo dentro de la tolerancia tiene que pasar",
				status, response["error"])
		}
	})
}

// El rechazo por el sufijo ocurre DESPUES de relay.decoded y ANTES del chequeo de permisos: la
// metatx deja la traza de lo que traia y no se paga la consulta a la cadena. Cubre 2.6.
func TestSuffixValidationRunsAfterDecodeAndBeforePermissioning(t *testing.T) {
	withBus(t)
	// El sender NO esta permitido: si el chequeo de permisos corriera primero, el codigo seria
	// SENDER_NOT_PERMITTED.
	mux := relayMuxWith(t, receiptWith(hubLog(topicTransactionRelayed, dataRelayedOK)),
		mockNodeOptions{senderNotPermitted: true},
		func(controller *RelayController) {
			controller.Config.Validation = model.ValidationConfig{EnforceNodeAddress: true}
			controller.Config.Security.PermissionsEnabled = true
		})

	_, response := relayPost(t, mux, `{"rawTx":"`+metaTxHacia(t, otroNodo, 4102444800)+`"}`)

	if response["code"] != "WRONG_NODE_ADDRESS" {
		t.Errorf("code = %v: la validacion del sufijo tiene que correr antes del permisionado", response["code"])
	}

	var orden []string
	var idDecoded, idRejected interface{}
	for _, event := range events.Replay(0) {
		switch event.Name() {
		case "relay.decoded":
			orden = append(orden, "decoded")
			idDecoded = event.Field("metaTxId")
		case "relay.rejected":
			orden = append(orden, "rejected")
			idRejected = event.Field("metaTxId")
		}
	}
	if len(orden) != 2 || orden[0] != "decoded" || orden[1] != "rejected" {
		t.Errorf("orden de eventos = %v, se esperaba decoded y despues rejected", orden)
	}
	if idDecoded == nil || idDecoded != idRejected {
		t.Errorf("los dos eventos tienen que compartir el metaTxId: %v vs %v", idDecoded, idRejected)
	}
}

// Un sufijo ausente o incompleto NO rechaza: una raw tx que hoy se relaya se sigue relayando.
// Cubre 2.7.
func TestMetaTxWithoutSuffixIsStillRelayed(t *testing.T) {
	to := common.HexToAddress("0x82a978b3f5962a5b0957d9ee9eef472ee55b42f1")
	validation := model.ValidationConfig{EnforceNodeAddress: true, EnforceExpiration: true,
		MinExpirationSeconds: 300, ExpirationToleranceSeconds: 2}

	casos := map[string][]byte{
		"sin sufijo":   {0x60, 0x57, 0x36, 0x1d},
		"sufijo corto": append([]byte{0x60, 0x57, 0x36, 0x1d}, make([]byte, 40)...),
		"data vacio":   {},
	}
	for nombre, data := range casos {
		t.Run(nombre, func(t *testing.T) {
			withBus(t)
			mux := validatingMux(t, validation, mockNodeOptions{})

			raw := signedRawTx(t, &to, data, nonceOnChain, 200000)
			status, response := relayPost(t, mux, `{"rawTx":"`+raw+`"}`)

			if status != http.StatusOK {
				t.Errorf("respondio %d con %v: sin sufijo legible no hay nada que validar",
					status, response["error"])
			}
		})
	}
}

// La misma metatx invalida se rechaza por el mismo motivo por las dos puertas, cada una con la
// forma de respuesta que le corresponde. Cubre 2.8.
func TestBothDoorsRejectForTheSameReason(t *testing.T) {
	withBus(t)
	mux := validatingMux(t, model.ValidationConfig{EnforceNodeAddress: true}, mockNodeOptions{})
	raw := metaTxHacia(t, otroNodo, 4102444800)

	// Puerta REST: 400 con codigo del catalogo.
	status, porRelay := relayPost(t, mux, `{"rawTx":"`+raw+`"}`)
	if status != http.StatusBadRequest || porRelay["code"] != "WRONG_NODE_ADDRESS" {
		t.Fatalf("POST /relay -> %d %v", status, porRelay["code"])
	}

	// Puerta JSON-RPC: 200 con el error en el cuerpo.
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, rawTxRequest(raw))
	if recorder.Code != http.StatusOK {
		t.Errorf("POST / respondio %d, el camino JSON-RPC responde 200 con el error en el cuerpo", recorder.Code)
	}
	cuerpo := recorder.Body.String()
	if !strings.Contains(cuerpo, "targets node") {
		t.Errorf("el motivo por el camino JSON-RPC no es el mismo: %s", cuerpo)
	}
	motivoRelay, _ := porRelay["error"].(string)
	if !strings.Contains(cuerpo, strings.Split(motivoRelay, " but ")[0]) {
		t.Errorf("las dos puertas dan motivos distintos:\n  /relay: %s\n  /: %s", motivoRelay, cuerpo)
	}
}

// GET /info informa de donde salio la direccion del contrato de reglas y el chequeo real del nodo,
// en los tres escenarios. Cubre 6.1.
func TestInfoReportsTheResolvedRules(t *testing.T) {
	t.Run("direccion configurada", func(t *testing.T) {
		withBus(t)
		mux := relayMuxWith(t, receiptWith(hubLog(topicTransactionRelayed, dataRelayedOK)), mockNodeOptions{},
			func(controller *RelayController) {
				controller.Config.Security.PermissionsEnabled = true
				controller.Config.Security.AccountContractAddress = "0x4683519EF834572017Cb583246B717449A4B752c"
			})

		_, info := getJSON(t, mux, "/info")

		if info["accountRulesSource"] != "config" {
			t.Errorf("accountRulesSource = %v, se esperaba config", info["accountRulesSource"])
		}
		if info["accountRulesAddress"] != "0x4683519EF834572017Cb583246B717449A4B752c" {
			t.Errorf("accountRulesAddress = %v", info["accountRulesAddress"])
		}
		if info["nodePermitted"] == nil {
			t.Error("con contrato de reglas hay que informar si el nodo esta permitido")
		}
	})

	t.Run("red sin contrato de reglas", func(t *testing.T) {
		withBus(t)
		mux := relayMuxWith(t, receiptWith(hubLog(topicTransactionRelayed, dataRelayedOK)), mockNodeOptions{},
			func(controller *RelayController) {
				controller.Config.Security.PermissionsEnabled = false
				controller.Config.Security.AccountContractAddress = ""
			})

		_, info := getJSON(t, mux, "/info")

		if info["accountRulesAddress"] != nil || info["accountRulesSource"] != nil {
			t.Errorf("sin contrato de reglas los dos campos van sin valor: %v / %v",
				info["accountRulesAddress"], info["accountRulesSource"])
		}
	})
}

// El minimo y la tolerancia se informan como la exigencia vigente. Cubre 6.2.
func TestInfoReportsTheExpirationWindowInEffect(t *testing.T) {
	t.Run("exigencia encendida", func(t *testing.T) {
		withBus(t)
		mux := validatingMux(t, model.ValidationConfig{EnforceExpiration: true, MinExpirationSeconds: 120,
			ExpirationToleranceSeconds: 7}, mockNodeOptions{})

		_, info := getJSON(t, mux, "/info")

		if info["minExpirationSeconds"] != float64(120) || info["expirationToleranceSeconds"] != float64(7) {
			t.Errorf("se esperaba 120/7, fue %v/%v", info["minExpirationSeconds"], info["expirationToleranceSeconds"])
		}
	})

	t.Run("exigencia apagada", func(t *testing.T) {
		withBus(t)
		mux := validatingMux(t, model.ValidationConfig{MinExpirationSeconds: 120, ExpirationToleranceSeconds: 7},
			mockNodeOptions{})

		_, info := getJSON(t, mux, "/info")

		if info["minExpirationSeconds"] != float64(0) || info["expirationToleranceSeconds"] != float64(0) {
			t.Errorf("con la exigencia apagada no rige ninguna ventana: %v/%v",
				info["minExpirationSeconds"], info["expirationToleranceSeconds"])
		}
	})
}

// La forma del cuerpo de GET /info no cambia: los mismos campos de siempre, con valores que dejan
// de ser fijos. Cubre 6.3.
func TestInfoKeepsItsShape(t *testing.T) {
	withBus(t)
	mux := validatingMux(t, model.ValidationConfig{EnforceNodeAddress: true, EnforceExpiration: true,
		MinExpirationSeconds: 300, ExpirationToleranceSeconds: 2}, mockNodeOptions{})

	_, info := getJSON(t, mux, "/info")

	for _, campo := range infoFields {
		if _, present := info[campo]; !present {
			t.Errorf("GET /info dejo de informar %q", campo)
		}
	}
	if len(info) != len(infoFields) {
		t.Errorf("GET /info informa %d campos y el contrato tiene %d: %v", len(info), len(infoFields), info)
	}
}
