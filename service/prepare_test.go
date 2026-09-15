package service

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/LACNetNetworks/gas-relay-signer/errors"
	"github.com/LACNetNetworks/gas-relay-signer/rpc"
)

// TestRejectionSpeaksBothVocabularies cubre la tarea 2.2: del mismo error salen la respuesta
// JSON-RPC con codigo numerico y la respuesta REST con codigo simbolico. Es lo que impide que las
// dos puertas den motivos distintos para el mismo rechazo.
func TestRejectionSpeaksBothVocabularies(t *testing.T) {
	casos := []struct {
		cause    error
		code     string
		numerico int
	}{
		{errors.MalformedRawTransaction.New("Error Decoding Raw Transaction", -32012), CodeBadRawTx, -32012},
		{errors.New("bad signature ECDSA", -32000), CodeBadMetaTx, -32000},
		{errors.New("account sender is not permitted to send transactions", -32000), CodeSenderNotPermitted, -32000},
	}

	for _, caso := range casos {
		rejection := Reject(caso.cause, caso.code)

		// Puerta JSON-RPC: el codigo numerico sale del error original, como siempre.
		response := new(rpc.JsonrpcMessage).ErrorResponse(rejection)
		if response.Error.Code != caso.numerico {
			t.Errorf("para %q la respuesta JSON-RPC lleva code=%d, se esperaba %d",
				caso.cause.Error(), response.Error.Code, caso.numerico)
		}
		if response.Error.Message != caso.cause.Error() {
			t.Errorf("el mensaje JSON-RPC cambio: %q", response.Error.Message)
		}

		// Puerta REST: el codigo simbolico del catalogo, del mismo error.
		if CodeOf(rejection) != caso.code {
			t.Errorf("para %q el codigo simbolico es %q, se esperaba %q",
				caso.cause.Error(), CodeOf(rejection), caso.code)
		}
		if rejection.Error() != caso.cause.Error() {
			t.Errorf("el motivo cambio entre puertas: %q vs %q", rejection.Error(), caso.cause.Error())
		}
	}
}

// TestUnclassifiedErrorFallsBackToTheGenericCode: lo que no tiene codigo propio usa el generico, no
// uno inventado.
func TestUnclassifiedErrorFallsBackToTheGenericCode(t *testing.T) {
	if got := CodeOf(errors.New("algo inesperado", -32603)); got != CodeRelayError {
		t.Errorf("un error sin clasificar devolvio %q, se esperaba %q", got, CodeRelayError)
	}
	if got := CodeOf(Reject(errors.New("x", -1), "")); got != CodeRelayError {
		t.Errorf("un rechazo sin codigo devolvio %q, se esperaba %q", got, CodeRelayError)
	}
}

// TestPreparedMetaTxIsRejectedTheSameWayByBothDoors cubre la tarea 2.1: lo extraido no conoce HTTP
// y rechaza lo mismo que rechazaba el camino JSON-RPC.
func TestPreparedMetaTxIsRejectedTheSameWayByBothDoors(t *testing.T) {
	relaySignerService := serviceAgainst("")

	casos := []struct {
		nombre string
		rawTx  string
		code   string
	}{
		{"hexadecimal impar", "0xdeadbee", CodeBadRawTx},
		{"sin firma utilizable", "0xdeadbeef", CodeBadMetaTx},
	}
	for _, caso := range casos {
		prepared, err := relaySignerService.PrepareMetaTx(context.Background(), caso.rawTx)
		if err == nil {
			t.Errorf("%s: se esperaba un rechazo, quedo %+v", caso.nombre, prepared)
			continue
		}
		if CodeOf(err) != caso.code {
			t.Errorf("%s: codigo %q, se esperaba %q", caso.nombre, CodeOf(err), caso.code)
		}
		// El mismo error sigue sirviendo para la respuesta JSON-RPC.
		response := new(rpc.JsonrpcMessage).ErrorResponse(err)
		if response.Error.Message == "" {
			t.Errorf("%s: la respuesta JSON-RPC quedo sin motivo", caso.nombre)
		}
	}
}

// TestConcurrentSendsReserveGasAtomically cubre la tarea 2.3: la reserva del cupo y el envio son
// atomicos frente a otros envios, y el lock no se traba consigo mismo.
func TestConcurrentSendsReserveGasAtomically(t *testing.T) {
	relaySignerService := serviceAgainstNode(t, newTestNode(t, ""))
	resetGasWindow()

	const envios = 8
	const gasPorEnvio = 200000

	var waiting sync.WaitGroup
	hashes := make([]string, envios)
	fallos := make([]error, envios)
	for i := 0; i < envios; i++ {
		waiting.Add(1)
		go func(index int) {
			defer waiting.Done()
			hash, err := relaySignerService.ReserveGasAndSend(context.Background(), &PreparedMetaTx{
				SigningData:    []byte{0x01},
				MetaTxGasLimit: gasPorEnvio,
				Nonce:          uint64(index),
				SenderKey:      "0xabc",
			})
			hashes[index], fallos[index] = hash.Hex(), err
		}(i)
	}
	waiting.Wait()

	for i, err := range fallos {
		if err != nil {
			t.Errorf("el envio %d fallo: %v", i, err)
		}
		if hashes[i] == "" || strings.Count(hashes[i], "0") == len(hashes[i])-2 {
			t.Errorf("el envio %d no devolvio hash: %q", i, hashes[i])
		}
	}

	// El cupo acumulado es exactamente lo reservado: ninguna actualizacion se perdio.
	if acumulado := gasWindow(); acumulado != envios*gasPorEnvio {
		t.Errorf("el cupo acumulado es %d, se esperaba %d: se perdio alguna reserva",
			acumulado, envios*gasPorEnvio)
	}
}
