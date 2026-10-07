package model

import (
	"math"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/spf13/viper"
)

// Valores por defecto de los bloques [reorder], [dashboard] y [log]. Son conservadores a
// proposito: con ellos el servicio se comporta igual que antes de que existieran.
const (
	DefaultReorderWindowMs = 3000
	// DefaultReorderMaxInflightPerUser iguala el techo que la red impone POR CUENTA: Besu acota
	// cuantas transacciones pendientes admite de una sola cuenta -tx-pool-limit-by-account-percentage,
	// ~5 con los defaults- y esa cuenta es la del writer node, remitente de TODAS las envolventes.
	//
	// Por encima de ese techo no se mina ni una metatx mas -el que decide es Besu- y en cambio se
	// admiten metatx que van a ocupar la ventana entera para terminar rechazadas por nonce, que no
	// es el motivo real. Si los validadores suben su techo, este numero los sigue.
	DefaultReorderMaxInflightPerUser = 5
	DefaultReorderReceiptTimeoutMs   = 60000
	DefaultReorderAutoNonceTicketMs  = 2000
	DefaultDashboardBufferSize       = 500
	DefaultLogLevel                  = "info"

	// Defaults de la validacion del sufijo del modelo de gas y de la resolucion del permisionado.
	// Los dos primeros son los medidos en el relayer de referencia.
	DefaultMinExpirationSeconds       = 300
	DefaultExpirationToleranceSeconds = 2
	DefaultAccountRulesCacheMs        = 30000
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
		AutoNonceTicketMs:  DefaultReorderAutoNonceTicketMs,
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
	reorder.AutoNonce = readBool(v, "reorder.autoNonce", reorder.AutoNonce, &discarded)
	reorder.AutoNonceTicketMs = readInt(v, "reorder.autoNonceTicketMs", reorder.AutoNonceTicketMs, 0, &discarded)

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

// DefaultValidationConfig devuelve el bloque [validation] como si estuviera ausente.
func DefaultValidationConfig() ValidationConfig {
	return ValidationConfig{
		EnforceNodeAddress:         false,
		EnforceExpiration:          false,
		MinExpirationSeconds:       DefaultMinExpirationSeconds,
		ExpirationToleranceSeconds: DefaultExpirationToleranceSeconds,
	}
}

// DefaultPermissioningConfig devuelve las claves de permisionado como si estuvieran ausentes.
func DefaultPermissioningConfig() PermissioningConfig {
	return PermissioningConfig{AccountRulesCacheMs: DefaultAccountRulesCacheMs}
}

// LoadValidationBlocks lee [validation] y las claves nuevas de [security], clave por clave.
//
// Va aparte de LoadRuntimeBlocks para no cambiarle la firma a algo que ya usan varios llamadores,
// pero sigue exactamente el mismo criterio: la clave que no sirve cae a su default, se devuelve en
// `discarded` y el arranque no se interrumpe.
func LoadValidationBlocks(v *viper.Viper) (ValidationConfig, PermissioningConfig, []DiscardedKey) {
	var discarded []DiscardedKey

	validation := DefaultValidationConfig()
	validation.EnforceNodeAddress = readBool(v, "validation.enforceNodeAddress", validation.EnforceNodeAddress, &discarded)
	validation.EnforceExpiration = readBool(v, "validation.enforceExpiration", validation.EnforceExpiration, &discarded)
	validation.MinExpirationSeconds = readInt(v, "validation.minExpirationSeconds", validation.MinExpirationSeconds, 0, &discarded)
	validation.ExpirationToleranceSeconds = readInt(v, "validation.expirationToleranceSeconds",
		validation.ExpirationToleranceSeconds, 0, &discarded)

	permissioning := DefaultPermissioningConfig()
	permissioning.AccountIngressAddress = readAddress(v, "security.accountIngressAddress", &discarded)
	permissioning.AccountRulesCacheMs = readInt(v, "security.accountRulesCacheMs", permissioning.AccountRulesCacheMs, 0, &discarded)

	return validation, permissioning, discarded
}

// readAddress lee una direccion. Una que no tiene forma de direccion se descarta: el default es
// vacio, o sea que el registro de permisos no se consulta, que es como se comporta el servicio hoy.
func readAddress(v *viper.Viper, key string, discarded *[]DiscardedKey) string {
	if !v.IsSet(key) {
		return ""
	}
	raw := v.Get(key)
	value, ok := raw.(string)
	if !ok {
		*discarded = append(*discarded, DiscardedKey{Key: key, Value: raw, Reason: "no es texto"})
		return ""
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if !common.IsHexAddress(value) {
		*discarded = append(*discarded, DiscardedKey{Key: key, Value: raw, Reason: "no es una direccion"})
		return ""
	}
	return value
}

// ExpirationFloor es la vigencia minima que el servicio exige de verdad: el minimo menos la
// tolerancia, acotado a cero.
//
// Una tolerancia mayor que el minimo no puede volverse un limite negativo, que aceptaria una metatx
// ya vencida por la puerta de atras. Ver design.md, D8.
func (validation ValidationConfig) ExpirationFloor() int {
	floor := validation.MinExpirationSeconds - validation.ExpirationToleranceSeconds
	if floor < 0 {
		return 0
	}
	return floor
}
