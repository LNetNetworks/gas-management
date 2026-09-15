package model

import (
	"math"
	"strconv"
	"strings"

	"github.com/spf13/viper"
)

// Valores por defecto de los bloques [reorder], [dashboard] y [log]. Son conservadores a
// proposito: con ellos el servicio se comporta igual que antes de que existieran.
const (
	DefaultReorderWindowMs           = 3000
	DefaultReorderMaxInflightPerUser = 16
	DefaultReorderReceiptTimeoutMs   = 60000
	DefaultDashboardBufferSize       = 500
	DefaultLogLevel                  = "info"
)

// Niveles de log aceptados por `log.level`, de menor a mayor severidad.
var logLevels = []string{"debug", "info", "warn", "error"}

// DiscardedKey es una clave que traia un valor no utilizable y se reemplazo por su default. El
// arranque no se interrumpe por esto: la clave se registra y el servicio sigue.
type DiscardedKey struct {
	Key    string
	Value  interface{}
	Reason string
}

// DefaultReorderConfig devuelve el bloque [reorder] como si estuviera ausente.
func DefaultReorderConfig() ReorderConfig {
	return ReorderConfig{
		Enabled:            false,
		WindowMs:           DefaultReorderWindowMs,
		MaxInflightPerUser: DefaultReorderMaxInflightPerUser,
		ReceiptTimeoutMs:   DefaultReorderReceiptTimeoutMs,
	}
}

// DefaultDashboardConfig devuelve el bloque [dashboard] como si estuviera ausente.
func DefaultDashboardConfig() DashboardConfig {
	return DashboardConfig{Enabled: false, BufferSize: DefaultDashboardBufferSize}
}

// DefaultLogConfig devuelve el bloque [log] como si estuviera ausente.
func DefaultLogConfig() LogConfig {
	return LogConfig{Level: DefaultLogLevel, RawTx: false}
}

// DefaultCorsConfig devuelve el bloque [cors] como si estuviera ausente: sin ningun origen, o sea
// sin emitir cabeceras.
func DefaultCorsConfig() CorsConfig {
	return CorsConfig{AllowedOrigins: nil}
}

// LoadRuntimeBlocks lee [reorder], [dashboard] y [log] clave por clave, fuera del Unmarshal que
// carga el resto de la configuracion.
//
// Va por separado porque el Unmarshal es todo o nada: un `bufferSize = "muchos"` no falla solo esa
// clave, falla el decode entero y se lleva puesto el arranque. Aca cada clave se resuelve sola, y
// la que no sirve se reemplaza por su default y se devuelve en `discarded` para que el llamador la
// registre. Tampoco se declaran defaults en el viper, porque eso haria que `IsSet` devuelva true
// para una clave ausente y se perderia la unica forma de distinguirla de un valor explicito.
func LoadRuntimeBlocks(v *viper.Viper) (ReorderConfig, DashboardConfig, LogConfig, CorsConfig, []DiscardedKey) {
	var discarded []DiscardedKey

	reorder := DefaultReorderConfig()
	reorder.Enabled = readBool(v, "reorder.enabled", reorder.Enabled, &discarded)
	reorder.WindowMs = readInt(v, "reorder.windowMs", reorder.WindowMs, 0, &discarded)
	reorder.MaxInflightPerUser = readInt(v, "reorder.maxInflightPerUser", reorder.MaxInflightPerUser, 1, &discarded)
	reorder.ReceiptTimeoutMs = readInt(v, "reorder.receiptTimeoutMs", reorder.ReceiptTimeoutMs, 0, &discarded)

	dashboard := DefaultDashboardConfig()
	dashboard.Enabled = readBool(v, "dashboard.enabled", dashboard.Enabled, &discarded)
	// El minimo es 0, no 1: un 0 explicito es una eleccion valida que deja el bus sin capacidad.
	dashboard.BufferSize = readInt(v, "dashboard.bufferSize", dashboard.BufferSize, 0, &discarded)

	logCfg := DefaultLogConfig()
	logCfg.Level = readLevel(v, "log.level", logCfg.Level, &discarded)
	logCfg.RawTx = readBool(v, "log.rawTx", logCfg.RawTx, &discarded)

	cors := DefaultCorsConfig()
	cors.AllowedOrigins = readOrigins(v, "cors.allowedOrigins", &discarded)

	return reorder, dashboard, logCfg, cors, discarded
}

// readOrigins lee la lista de origenes permitidos. Una lista mal formada se descarta entera y deja
// el servicio cerrado, que es el default: ante la duda, no se abre.
func readOrigins(v *viper.Viper, key string, discarded *[]DiscardedKey) []string {
	if !v.IsSet(key) {
		return nil
	}
	raw := v.Get(key)
	values, ok := raw.([]interface{})
	if !ok {
		*discarded = append(*discarded, DiscardedKey{Key: key, Value: raw, Reason: "no es una lista"})
		return nil
	}
	origins := make([]string, 0, len(values))
	for _, value := range values {
		origin, ok := value.(string)
		if !ok || strings.TrimSpace(origin) == "" {
			*discarded = append(*discarded, DiscardedKey{Key: key, Value: raw, Reason: "contiene un origen que no es texto"})
			return nil
		}
		origins = append(origins, strings.TrimSpace(origin))
	}
	return origins
}

func readBool(v *viper.Viper, key string, def bool, discarded *[]DiscardedKey) bool {
	if !v.IsSet(key) {
		return def
	}
	raw := v.Get(key)
	value, ok := toBool(raw)
	if !ok {
		*discarded = append(*discarded, DiscardedKey{Key: key, Value: raw, Reason: "no es un booleano"})
		return def
	}
	return value
}

// readInt resuelve una clave numerica. Separa dos fallos distintos a proposito: el valor que no es
// un numero (el decodificador lo rechaza) y el numero fuera de rango (el decodificador lo acepta
// sin objetar, asi que la unica forma de detectarlo es comprobarlo aca).
func readInt(v *viper.Viper, key string, def, min int, discarded *[]DiscardedKey) int {
	if !v.IsSet(key) {
		return def
	}
	raw := v.Get(key)
	value, ok := toInt(raw)
	if !ok {
		*discarded = append(*discarded, DiscardedKey{Key: key, Value: raw, Reason: "no es un numero entero"})
		return def
	}
	if value < min {
		*discarded = append(*discarded, DiscardedKey{
			Key: key, Value: raw, Reason: "fuera de rango (minimo " + strconv.Itoa(min) + ")",
		})
		return def
	}
	return value
}

func readLevel(v *viper.Viper, key, def string, discarded *[]DiscardedKey) string {
	if !v.IsSet(key) {
		return def
	}
	raw := v.Get(key)
	text, ok := raw.(string)
	if ok {
		level := strings.ToLower(strings.TrimSpace(text))
		for _, accepted := range logLevels {
			if level == accepted {
				return level
			}
		}
	}
	*discarded = append(*discarded, DiscardedKey{
		Key: key, Value: raw, Reason: "nivel no reconocido (debe ser " + strings.Join(logLevels, ", ") + ")",
	})
	return def
}

func toInt(raw interface{}) (int, bool) {
	switch value := raw.(type) {
	case int:
		return value, true
	case int32:
		return int(value), true
	case int64:
		return int(value), true
	case float64:
		// Un decimal no es un entero: 3.5 milisegundos no es un valor que se pueda aplicar.
		if value != math.Trunc(value) {
			return 0, false
		}
		return int(value), true
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return 0, false
		}
		return parsed, true
	}
	return 0, false
}

func toBool(raw interface{}) (bool, bool) {
	switch value := raw.(type) {
	case bool:
		return value, true
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(value))
		if err != nil {
			return false, false
		}
		return parsed, true
	}
	return false, false
}
