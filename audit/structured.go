package audit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LACNetNetworks/gas-relay-signer/events"
)

// Log estructurado: una linea JSON por evento, a la salida estandar.
//
// Va EN PARALELO al log de texto de este mismo paquete, que no se toca: hay operadores que lo
// parsean. Una linea por evento no es estetico, es funcional: un objeto JSON plano se puede
// filtrar por `event`, `reqId` o `metaTxId` con herramientas estandar, mientras que un log
// multilinea se parte en entradas separadas y deja de ser buscable.
//
// `warn` y `error` salen por la salida de error para que el recolector de logs los clasifique como
// tales; `debug` e `info` por la salida estandar.

// Level es el nivel de un evento.
type Level string

const (
	LevelDebug Level = "debug"
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

var severity = map[Level]int{LevelDebug: 10, LevelInfo: 20, LevelWarn: 30, LevelError: 40}

// maxStackFrames acota la traza de un error. Sin este tope una traza profunda convierte la linea
// del evento en un muro y tapa el resto del log.
const maxStackFrames = 6

// instanceID identifica a este proceso y va en cada linea a proposito: es la unica forma de ver,
// leyendo el log, si dos instancias estuvieron atendiendo a la vez. Importa porque el cache de
// nonces vive en memoria y asume una sola instancia usando la clave del writer node.
var instanceID = newID(4)

// configuredLevel es el nivel minimo que se ESCRIBE en la salida. No afecta que se publica en el
// bus: ese nivel regula la consola, no la observabilidad.
var configuredLevel atomic.Int32

// logRawTx habilita el volcado de la transaccion firmada completa. Apagado por defecto: un deploy
// son varios KB de initcode y la raw tx ya queda identificada por su hash y su tamano.
var logRawTx atomic.Bool

// Salidas, sustituibles en tests.
var (
	outputMutex  sync.Mutex
	stdoutWriter io.Writer = os.Stdout
	stderrWriter io.Writer = os.Stderr
)

func init() {
	configuredLevel.Store(int32(severity[LevelInfo]))
}

// InitStructured deja el emisor con la configuracion leida. Se llama una vez, desde main, despues
// de leer config.toml y antes de levantar el servidor.
//
// No se usa init() porque corre antes de que exista la configuracion, ni una inicializacion
// perezosa, que esconderia el orden justo donde importa. Ver design.md, D5.
func InitStructured(level string, rawTx bool) {
	if value, ok := severity[Level(strings.ToLower(strings.TrimSpace(level)))]; ok {
		configuredLevel.Store(int32(value))
	}
	logRawTx.Store(rawTx)
}

// ShouldLogRawTx indica si hay que incluir la transaccion firmada completa en el evento.
func ShouldLogRawTx() bool { return logRawTx.Load() }

// InstanceID es el identificador de este proceso, tal como sale en cada linea.
func InstanceID() string { return instanceID }

// Debug, Info, Warn y Error emiten un evento de operacion.
func Debug(ctx context.Context, event string, fields map[string]interface{}) {
	emit(ctx, LevelDebug, event, fields)
}
func Info(ctx context.Context, event string, fields map[string]interface{}) {
	emit(ctx, LevelInfo, event, fields)
}
func Warn(ctx context.Context, event string, fields map[string]interface{}) {
	emit(ctx, LevelWarn, event, fields)
}
func Error(ctx context.Context, event string, fields map[string]interface{}) {
	emit(ctx, LevelError, event, fields)
}

// emit arma la linea, la publica en el bus y recien despues decide si escribirla.
//
// El orden importa y es un requisito, no una casualidad: el filtro por nivel se aplica DESPUES de
// publicar, porque regula cuanto se ensucia la consola y no cuanto se puede observar en vivo.
func emit(ctx context.Context, level Level, event string, fields map[string]interface{}) {
	line := map[string]interface{}{
		"ts":         time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		"level":      string(level),
		"event":      event,
		"instanceId": instanceID,
	}
	if id := RequestID(ctx); id != "" {
		line["reqId"] = id
	}
	if id := MetaTxID(ctx); id != "" {
		line["metaTxId"] = id
	}
	for key, value := range fields {
		line[key] = value
	}

	events.Publish(line)

	if severity[level] < int(configuredLevel.Load()) {
		return
	}
	write(level, line)
}

func write(level Level, line map[string]interface{}) {
	text, err := json.Marshal(line)
	if err != nil {
		// Un campo no serializable no puede interrumpir la peticion que lo origino: se emite una
		// linea reducida que conserva lo identificable y deja constancia del fallo.
		text, err = json.Marshal(map[string]interface{}{
			"ts":       line["ts"],
			"level":    line["level"],
			"event":    line["event"],
			"logError": "no se pudo serializar el evento: " + err.Error(),
		})
		if err != nil {
			return
		}
	}

	outputMutex.Lock()
	defer outputMutex.Unlock()
	target := stdoutWriter
	if level == LevelWarn || level == LevelError {
		target = stderrWriter
	}
	_, _ = fmt.Fprintln(target, string(text))
}

// ErrorFields aplana un error a campos loggeables.
//
// `error` va siempre: es el campo con el que la vista muestra el motivo de un rechazo, y sin el no
// muestra ninguno. `code` sale de la MISMA fuente con la que se arma la respuesta JSON-RPC, para
// que el log y lo que recibio el cliente no puedan contar cosas distintas. Ver design.md, D12.
func ErrorFields(err error) map[string]interface{} {
	if err == nil {
		return map[string]interface{}{}
	}
	out := map[string]interface{}{
		"error":     flatten(err.Error()),
		"errorType": fmt.Sprintf("%T", err),
	}
	if coded, ok := err.(interface{ ErrorCode() int }); ok {
		out["code"] = coded.ErrorCode()
	}
	if stack := stackOf(err); stack != "" {
		out["stack"] = stack
	}
	return out
}

// stackOf devuelve la traza del error, aplanada y acotada, o "" si el error no trae ninguna.
func stackOf(err error) string {
	verbose := fmt.Sprintf("%+v", err)
	if verbose == err.Error() {
		return ""
	}
	return flatten(verbose)
}

// flatten convierte un texto multilinea en una sola linea, acotada a maxStackFrames fragmentos.
// La codificacion JSON ya garantiza que la linea no se parta; esto es para que sea legible y para
// que una traza profunda no tape el resto del log.
func flatten(text string) string {
	fragments := make([]string, 0, maxStackFrames)
	truncated := false
	for _, raw := range strings.Split(text, "\n") {
		fragment := strings.TrimSpace(raw)
		if fragment == "" {
			continue
		}
		if len(fragments) == maxStackFrames {
			truncated = true
			break
		}
		fragments = append(fragments, fragment)
	}
	joined := strings.Join(fragments, " | ")
	if truncated {
		joined += " | ..."
	}
	return joined
}

func newID(size int) string {
	buffer := make([]byte, size)
	if _, err := rand.Read(buffer); err != nil {
		// Un identificador es correlacion, no seguridad: si el generador falla, un valor derivado
		// del reloj sigue sirviendo y el servicio no se detiene por esto.
		return fmt.Sprintf("%0*x", size*2, time.Now().UnixNano())
	}
	return hex.EncodeToString(buffer)
}

// NewRequestID identifica una peticion HTTP. Corto a proposito: alcanza para correlacionar dentro
// de una ventana de log y no infla cada linea.
func NewRequestID() string { return newID(6) }

// NewMetaTxID identifica una metatx concreta.
func NewMetaTxID() string { return newID(4) }
