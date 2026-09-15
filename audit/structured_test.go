package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"sync"
	"testing"

	pkgerrors "github.com/pkg/errors"

	relayerrors "github.com/LACNetNetworks/gas-relay-signer/errors"
	"github.com/LACNetNetworks/gas-relay-signer/events"
	"github.com/LACNetNetworks/gas-relay-signer/rpc"
)

// captureOutput sustituye las salidas del emisor durante un test y las restaura al terminar.
func captureOutput(t *testing.T) (out, errOut *bytes.Buffer) {
	t.Helper()
	out, errOut = new(bytes.Buffer), new(bytes.Buffer)
	outputMutex.Lock()
	previousOut, previousErr := stdoutWriter, stderrWriter
	stdoutWriter, stderrWriter = out, errOut
	outputMutex.Unlock()
	t.Cleanup(func() {
		outputMutex.Lock()
		stdoutWriter, stderrWriter = previousOut, previousErr
		outputMutex.Unlock()
	})
	return out, errOut
}

// restoreLevel deja el nivel como estaba al terminar el test.
func restoreLevel(t *testing.T) {
	t.Helper()
	previous := configuredLevel.Load()
	t.Cleanup(func() { configuredLevel.Store(previous) })
}

func decodeOneLine(t *testing.T, buffer *bytes.Buffer) map[string]interface{} {
	t.Helper()
	text := strings.TrimRight(buffer.String(), "\n")
	if text == "" {
		t.Fatal("no se emitio ninguna linea")
	}
	if strings.Contains(text, "\n") {
		t.Fatalf("se emitio mas de una linea:\n%s", text)
	}
	var line map[string]interface{}
	if err := json.Unmarshal([]byte(text), &line); err != nil {
		t.Fatalf("la linea no es un objeto JSON valido (%v): %s", err, text)
	}
	return line
}

// TestEmitsOneJSONLinePerEvent cubre el escenario "Se emite un evento de operacion".
func TestEmitsOneJSONLinePerEvent(t *testing.T) {
	out, errOut := captureOutput(t)

	Info(context.Background(), "relay.received", map[string]interface{}{"rawTxBytes": 120})

	line := decodeOneLine(t, out)
	for _, field := range []string{"ts", "level", "event", "instanceId"} {
		if _, present := line[field]; !present {
			t.Errorf("falta el campo obligatorio %q en la linea: %v", field, line)
		}
	}
	if line["event"] != "relay.received" || line["level"] != "info" {
		t.Errorf("event o level incorrectos: %v", line)
	}
	if errOut.Len() != 0 {
		t.Errorf("un evento info no debe salir por la salida de error: %s", errOut.String())
	}
}

// TestErrorLevelGoesToStderr cubre el escenario "Un evento de error".
func TestErrorLevelGoesToStderr(t *testing.T) {
	out, errOut := captureOutput(t)

	Error(context.Background(), "relay.settle_failed", nil)
	Warn(context.Background(), "relay.rejected", nil)

	if out.Len() != 0 {
		t.Errorf("warn y error no deben salir por la salida estandar: %s", out.String())
	}
	emitted := strings.Count(strings.TrimRight(errOut.String(), "\n"), "\n") + 1
	if emitted != 2 {
		t.Errorf("se esperaban 2 lineas por la salida de error, se emitieron %d", emitted)
	}
}

// TestInstanceIDIsStableWithinTheProcess cubre los dos escenarios del identificador de instancia.
func TestInstanceIDIsStableWithinTheProcess(t *testing.T) {
	out, _ := captureOutput(t)

	Info(context.Background(), "relay.received", nil)
	Info(context.Background(), "relay.sent", nil)

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("se esperaban 2 lineas, se emitieron %d", len(lines))
	}
	var first, second map[string]interface{}
	_ = json.Unmarshal([]byte(lines[0]), &first)
	_ = json.Unmarshal([]byte(lines[1]), &second)
	if first["instanceId"] != second["instanceId"] {
		t.Errorf("dos lineas del mismo proceso llevan instanceId distintos: %v y %v",
			first["instanceId"], second["instanceId"])
	}
	if first["instanceId"] != InstanceID() {
		t.Errorf("la linea lleva %v y el proceso reporta %q", first["instanceId"], InstanceID())
	}
	if InstanceID() == "" {
		t.Error("el instanceId no puede estar vacio")
	}
}

// TestMultilineErrorStaysOnOneLine cubre el escenario "Un error con traza de varias lineas".
func TestMultilineErrorStaysOnOneLine(t *testing.T) {
	out, errOut := captureOutput(t)

	// pkg/errors adjunta una traza que %+v imprime en varias lineas; es la que usa errors/.
	wrapped := pkgerrors.Wrap(errors.New("no se pudo firmar"), "fallo el envio")
	Error(context.Background(), "relay.rejected", ErrorFields(wrapped))

	line := decodeOneLine(t, errOut)
	if out.Len() != 0 {
		t.Errorf("no se esperaba nada por la salida estandar: %s", out.String())
	}
	stack, present := line["stack"].(string)
	if !present {
		t.Fatalf("se esperaba el campo stack aplanado, quedo: %v", line)
	}
	if strings.Contains(stack, "\n") {
		t.Error("la traza debe emitirse en una sola linea")
	}
	if fragments := strings.Count(stack, " | ") + 1; fragments > maxStackFrames+1 {
		t.Errorf("la traza tiene %d fragmentos, el tope es %d", fragments, maxStackFrames)
	}
	if !strings.Contains(line["error"].(string), "no se pudo firmar") {
		t.Errorf("el campo error perdio el mensaje original: %v", line["error"])
	}
}

// TestLevelFiltersTheConsoleNotTheBus cubre el escenario "Un evento por debajo del nivel
// configurado": no se escribe en la salida, pero si se publica en el bus.
func TestLevelFiltersTheConsoleNotTheBus(t *testing.T) {
	out, errOut := captureOutput(t)
	restoreLevel(t)

	events.Init(true, 10)
	t.Cleanup(func() { events.Init(false, 0) })

	InitStructured("info", false)
	Debug(context.Background(), "relay.decoded", map[string]interface{}{"nonce": 7})

	if out.Len() != 0 || errOut.Len() != 0 {
		t.Errorf("un evento debug con el nivel en info no debe escribirse: %s%s", out.String(), errOut.String())
	}
	retained := events.Replay(0)
	if len(retained) != 1 {
		t.Fatalf("el bus retuvo %d eventos, se esperaba 1: el nivel regula la consola, no el bus", len(retained))
	}
	if retained[0].Name() != "relay.decoded" || retained[0].Level() != "debug" {
		t.Errorf("el evento publicado no es el esperado: %v", retained[0].Line)
	}
}

// TestUnserializableFieldEmitsReducedLine cubre el escenario "Un campo no serializable".
func TestUnserializableFieldEmitsReducedLine(t *testing.T) {
	out, _ := captureOutput(t)

	// Un canal no tiene representacion JSON.
	Info(context.Background(), "relay.received", map[string]interface{}{"roto": make(chan int)})

	line := decodeOneLine(t, out)
	if _, present := line["logError"]; !present {
		t.Errorf("se esperaba la marca del fallo de serializacion, quedo: %v", line)
	}
	for _, field := range []string{"ts", "level", "event"} {
		if _, present := line[field]; !present {
			t.Errorf("la linea reducida debe conservar %q: %v", field, line)
		}
	}
	if line["event"] != "relay.received" {
		t.Errorf("la linea reducida perdio el nombre del evento: %v", line)
	}

	// El fallo no deja al emisor roto: lo que sigue del procesamiento se registra igual. Esta es
	// la razon por la que la peticion que lo origino puede responderse con normalidad.
	out.Reset()
	Info(context.Background(), "relay.sent", map[string]interface{}{"hubNonce": 41})
	if siguiente := decodeOneLine(t, out); siguiente["event"] != "relay.sent" {
		t.Errorf("el emisor quedo inutilizable tras un evento no serializable: %v", siguiente)
	}
}

// TestCorrelationIDsTravelInTheLine: reqId y metaTxId del contexto llegan a la linea.
func TestCorrelationIDsTravelInTheLine(t *testing.T) {
	out, _ := captureOutput(t)

	ctx := WithMetaTxID(WithRequestID(context.Background(), "abc123"), "def456")
	Info(ctx, "relay.decoded", nil)

	line := decodeOneLine(t, out)
	if line["reqId"] != "abc123" || line["metaTxId"] != "def456" {
		t.Errorf("la correlacion no llego a la linea: %v", line)
	}
}

// TestErrorFieldsAlwaysCarriesTheMessage: `error` es el campo obligatorio; `code` solo aparece
// cuando el error lo trae. Ver design.md, D12.
func TestErrorFieldsAlwaysCarriesTheMessage(t *testing.T) {
	sinCodigo := ErrorFields(errors.New("cuerpo ilegible"))
	if sinCodigo["error"] != "cuerpo ilegible" {
		t.Errorf("error = %v, se esperaba el mensaje", sinCodigo["error"])
	}
	if _, present := sinCodigo["code"]; present {
		t.Errorf("un error sin codigo no debe emitir code: %v", sinCodigo)
	}

	conCodigo := ErrorFields(codedError{message: "firma malformada", code: -32010})
	if conCodigo["code"] != -32010 {
		t.Errorf("code = %v, se esperaba -32010", conCodigo["code"])
	}
	if conCodigo["error"] != "firma malformada" {
		t.Errorf("error = %v, se esperaba el mensaje", conCodigo["error"])
	}
}

type codedError struct {
	message string
	code    int
}

func (err codedError) Error() string  { return err.message }
func (err codedError) ErrorCode() int { return err.code }

// TestErrorCodeMatchesTheJSONRPCResponse es el requisito de D12 comprobado de punta a punta: el
// `code` que se registra y el que recibe el cliente salen de la misma fuente, asi que el log y la
// respuesta no pueden indicar motivos distintos para el mismo rechazo.
func TestErrorCodeMatchesTheJSONRPCResponse(t *testing.T) {
	for _, err := range []error{
		relayerrors.New("signature malformed", -32010),
		relayerrors.BadTransaction.New("bad V parameter", -32011),
		relayerrors.FailedContract.New("can't instance RelayHub contract", -32603),
	} {
		response := new(rpc.JsonrpcMessage).ErrorResponse(err)
		logged := ErrorFields(err)
		if logged["code"] != response.Error.Code {
			t.Errorf("para %q el log registra code=%v y el cliente recibe %d",
				err.Error(), logged["code"], response.Error.Code)
		}
		if logged["error"] != response.Error.Message {
			t.Errorf("para %q el log registra error=%v y el cliente recibe %q",
				err.Error(), logged["error"], response.Error.Message)
		}
	}
}

// TestTextLogIsUntouched: el log de texto conserva su formato y sus entradas, y ninguno de los dos
// logs altera el contenido del otro.
func TestTextLogIsUntouched(t *testing.T) {
	out, _ := captureOutput(t)

	var texto bytes.Buffer
	previous := GeneralLogger
	GeneralLogger = log.New(&texto, "General Logger:\t", log.Ldate|log.Ltime|log.Lshortfile)
	t.Cleanup(func() { GeneralLogger = previous })

	GeneralLogger.Println("Is a rawTransaction")
	Info(context.Background(), "relay.received", nil)
	GeneralLogger.Println("JSON-RPC Method: eth_sendRawTransaction")

	textLines := strings.Split(strings.TrimRight(texto.String(), "\n"), "\n")
	if len(textLines) != 2 {
		t.Fatalf("el log de texto recibio %d entradas, se esperaban 2: %q", len(textLines), texto.String())
	}
	for _, entry := range textLines {
		if !strings.HasPrefix(entry, "General Logger:\t") {
			t.Errorf("el log de texto cambio de formato: %q", entry)
		}
		if strings.HasPrefix(strings.TrimSpace(entry), "{") {
			t.Errorf("una linea JSON se colo en el log de texto: %q", entry)
		}
	}
	structured := decodeOneLine(t, out)
	if structured["event"] != "relay.received" {
		t.Errorf("el log estructurado no recibio su linea: %v", structured)
	}
	if strings.Contains(out.String(), "General Logger") {
		t.Error("el log de texto se colo en el log estructurado")
	}
}

// TestConcurrentEmissionDoesNotInterleave: dos goroutines emitiendo a la vez no parten una linea.
func TestConcurrentEmissionDoesNotInterleave(t *testing.T) {
	out, _ := captureOutput(t)

	var waiting sync.WaitGroup
	for i := 0; i < 50; i++ {
		waiting.Add(1)
		go func() {
			defer waiting.Done()
			Info(context.Background(), "relay.received", map[string]interface{}{"rawTxBytes": 4096})
		}()
	}
	waiting.Wait()

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 50 {
		t.Fatalf("se emitieron %d lineas, se esperaban 50", len(lines))
	}
	for _, entry := range lines {
		var decoded map[string]interface{}
		if err := json.Unmarshal([]byte(entry), &decoded); err != nil {
			t.Fatalf("una linea quedo partida: %q", entry)
		}
	}
}

// TestDetachedContextSurvivesTheResponse cubre la tarea 4.4: un evento emitido despues de que el
// handler retorno conserva la correlacion, y el trabajo desprendido no se aborta por eso.
func TestDetachedContextSurvivesTheResponse(t *testing.T) {
	out, _ := captureOutput(t)

	// El ctx de la peticion, con su correlacion.
	request, cancel := context.WithCancel(
		WithMetaTxID(WithRequestID(context.Background(), "req-1"), "meta-1"))
	detached := Detach(request)

	// El handler retorna: el contexto de la peticion se cancela.
	cancel()

	if request.Err() == nil {
		t.Fatal("el contexto de la peticion deberia estar cancelado tras retornar el handler")
	}
	if detached.Err() != nil {
		t.Errorf("el contexto desprendido no debe cancelarse con la peticion: %v", detached.Err())
	}
	if detached.Done() != nil {
		select {
		case <-detached.Done():
			t.Error("el contexto desprendido quedo cancelado")
		default:
		}
	}

	Info(detached, "relay.settled", map[string]interface{}{"blockNumber": 11184709})

	line := decodeOneLine(t, out)
	if line["reqId"] != "req-1" || line["metaTxId"] != "meta-1" {
		t.Errorf("un evento posterior a la respuesta perdio la correlacion: %v", line)
	}
}
