package model

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
)

// viperFor escribe un config.toml en un directorio temporal y lo lee, igual que hace el arranque.
func viperFor(t *testing.T, contents string) *viper.Viper {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(contents), 0o600); err != nil {
		t.Fatalf("no se pudo escribir el config.toml de prueba: %v", err)
	}
	v := viper.New()
	v.SetConfigName("config")
	v.AddConfigPath(dir)
	if err := v.ReadInConfig(); err != nil {
		t.Fatalf("no se pudo leer el config.toml de prueba: %v", err)
	}
	return v
}

// configExistente es un config.toml escrito antes de esta capacidad: no trae ninguno de los
// bloques nuevos.
const configExistente = `[application]
nodeURL = "http://localhost:4545"
port = 9001

[security]
permissionsEnabled = false
`

// TestRuntimeBlocksAbsentUseDefaults cubre el escenario "Los bloques estan ausentes": un
// config.toml sin [reorder], [dashboard] ni [log] carga, cada parametro toma su default y ambas
// capacidades quedan deshabilitadas.
func TestRuntimeBlocksAbsentUseDefaults(t *testing.T) {
	v := viperFor(t, configExistente)

	reorder, dashboard, logCfg, _, discarded := LoadRuntimeBlocks(v)

	if len(discarded) != 0 {
		t.Errorf("un config.toml sin los bloques nuevos no debe descartar ninguna clave, se descartaron %v", discarded)
	}
	if reorder.Enabled {
		t.Error("reorder.enabled debe quedar deshabilitado por defecto")
	}
	if dashboard.Enabled {
		t.Error("dashboard.enabled debe quedar deshabilitado por defecto")
	}
	if reorder.WindowMs != 3000 {
		t.Errorf("reorder.windowMs por defecto = %d, se esperaba 3000", reorder.WindowMs)
	}
	if reorder.MaxInflightPerUser != 5 {
		t.Errorf("reorder.maxInflightPerUser por defecto = %d, se esperaba 5", reorder.MaxInflightPerUser)
	}
	if reorder.ReceiptTimeoutMs != 60000 {
		t.Errorf("reorder.receiptTimeoutMs por defecto = %d, se esperaba 60000", reorder.ReceiptTimeoutMs)
	}
	if dashboard.BufferSize != 500 {
		t.Errorf("dashboard.bufferSize por defecto = %d, se esperaba 500", dashboard.BufferSize)
	}
	if logCfg.Level != "info" {
		t.Errorf("log.level por defecto = %q, se esperaba \"info\"", logCfg.Level)
	}
	if logCfg.RawTx {
		t.Error("log.rawTx debe estar apagado por defecto")
	}
}

// TestRuntimeBlocksExplicitValues cubre el escenario "Los bloques traen valores explicitos": se
// usan esos valores y las claves no especificadas conservan su default.
func TestRuntimeBlocksExplicitValues(t *testing.T) {
	v := viperFor(t, configExistente+`
[reorder]
enabled = true
windowMs = 5000

[dashboard]
bufferSize = 200
`)

	reorder, dashboard, _, _, discarded := LoadRuntimeBlocks(v)

	if len(discarded) != 0 {
		t.Errorf("no se esperaba ninguna clave descartada, se descartaron %v", discarded)
	}
	if !reorder.Enabled {
		t.Error("reorder.enabled = true no se aplico")
	}
	if reorder.WindowMs != 5000 {
		t.Errorf("reorder.windowMs = %d, se esperaba 5000", reorder.WindowMs)
	}
	if dashboard.BufferSize != 200 {
		t.Errorf("dashboard.bufferSize = %d, se esperaba 200", dashboard.BufferSize)
	}
	if reorder.MaxInflightPerUser != 5 {
		t.Errorf("una clave no especificada debe conservar su default, maxInflightPerUser = %d", reorder.MaxInflightPerUser)
	}
	if dashboard.Enabled {
		t.Error("dashboard.enabled no se especifico, debe quedar en false")
	}
}

// TestInvalidValueDoesNotBreakSharedUnmarshal es la razon de ser de D10: un valor no interpretable
// en un bloque nuevo no puede impedir que se cargue el resto de la configuracion. Si los bloques
// nuevos se decodificaran con el Unmarshal compartido, este Unmarshal fallaria entero.
func TestInvalidValueDoesNotBreakSharedUnmarshal(t *testing.T) {
	v := viperFor(t, configExistente+`
[dashboard]
enabled = "si"
bufferSize = "muchos"

[reorder]
windowMs = "pronto"
`)

	var config Config
	if err := v.Unmarshal(&config); err != nil {
		t.Fatalf("el Unmarshal compartido no debe fallar por una clave nueva invalida: %v", err)
	}
	if config.Application.NodeURL != "http://localhost:4545" {
		t.Errorf("el resto de la configuracion debe cargar igual, nodeURL = %q", config.Application.NodeURL)
	}

	reorder, dashboard, _, _, discarded := LoadRuntimeBlocks(v)
	if len(discarded) != 3 {
		t.Errorf("se esperaban 3 claves descartadas, se descartaron %d: %v", len(discarded), discarded)
	}
	if dashboard.BufferSize != 500 || dashboard.Enabled || reorder.WindowMs != 3000 {
		t.Errorf("las claves invalidas deben caer a su default, quedo reorder=%+v dashboard=%+v", reorder, dashboard)
	}
}

// TestDiscardedByType cubre los escenarios "Un buffer de dashboard no numerico" y "Un nivel de log
// no reconocido": se aplica el default, se registra la clave y el arranque no se interrumpe.
func TestDiscardedByType(t *testing.T) {
	v := viperFor(t, configExistente+`
[dashboard]
bufferSize = "muchos"

[log]
level = "verboso"
`)

	_, dashboard, logCfg, _, discarded := LoadRuntimeBlocks(v)

	if dashboard.BufferSize != 500 {
		t.Errorf("dashboard.bufferSize = %d, se esperaba el default 500", dashboard.BufferSize)
	}
	if logCfg.Level != "info" {
		t.Errorf("log.level = %q, se esperaba el default \"info\"", logCfg.Level)
	}
	if !discardedHas(discarded, "dashboard.bufferSize") {
		t.Errorf("no se registro el descarte de dashboard.bufferSize, se registro %v", discarded)
	}
	if !discardedHas(discarded, "log.level") {
		t.Errorf("no se registro el descarte de log.level, se registro %v", discarded)
	}
}

// TestDiscardedByRange cubre el escenario "Una ventana de reorden negativa". Es un fallo distinto
// al de tipo: el decodificador acepta -1 sin objetar, asi que solo se detecta comprobando el rango.
func TestDiscardedByRange(t *testing.T) {
	v := viperFor(t, configExistente+`
[reorder]
windowMs = -1
maxInflightPerUser = 0
`)

	reorder, _, _, _, discarded := LoadRuntimeBlocks(v)

	if reorder.WindowMs != 3000 {
		t.Errorf("reorder.windowMs = %d, se esperaba el default 3000", reorder.WindowMs)
	}
	if reorder.MaxInflightPerUser != 5 {
		t.Errorf("reorder.maxInflightPerUser = %d, se esperaba el default 5", reorder.MaxInflightPerUser)
	}
	if !discardedHas(discarded, "reorder.windowMs") {
		t.Errorf("no se registro el descarte de reorder.windowMs, se registro %v", discarded)
	}
}

// TestBufferSizeZeroIsAnExplicitChoice: la clave ausente toma el default, y un 0 explicito deja el
// bus sin capacidad. Son dos cosas distintas y el 0 no es un valor invalido.
func TestBufferSizeZeroIsAnExplicitChoice(t *testing.T) {
	ausente := viperFor(t, configExistente+`
[dashboard]
enabled = true
`)
	_, dashboard, _, _, discarded := LoadRuntimeBlocks(ausente)
	if dashboard.BufferSize != 500 {
		t.Errorf("con bufferSize ausente se esperaba 500, quedo %d", dashboard.BufferSize)
	}
	if len(discarded) != 0 {
		t.Errorf("una clave ausente no se descarta, se registro %v", discarded)
	}

	cero := viperFor(t, configExistente+`
[dashboard]
enabled = true
bufferSize = 0
`)
	_, dashboard, _, _, discarded = LoadRuntimeBlocks(cero)
	if dashboard.BufferSize != 0 {
		t.Errorf("un 0 explicito deja el bus sin capacidad, quedo %d", dashboard.BufferSize)
	}
	if len(discarded) != 0 {
		t.Errorf("un 0 explicito es una eleccion valida, no un descarte: %v", discarded)
	}
}

func discardedHas(discarded []DiscardedKey, key string) bool {
	for _, entry := range discarded {
		if entry.Key == key {
			return true
		}
	}
	return false
}

// Las claves del reparto de nonces: ausentes toman su default -apagado- y un valor invalido se
// descarta sin habilitar nada. Cubre parte de la tarea 6.1 de 04-add-nonce-reordering.
func TestAutoNonceKeys(t *testing.T) {
	t.Run("ausentes", func(t *testing.T) {
		v := viperFor(t, "")
		reorder, _, _, _, discarded := LoadRuntimeBlocks(v)

		if reorder.AutoNonce {
			t.Error("reorder.autoNonce debe quedar apagado por defecto")
		}
		if reorder.AutoNonceTicketMs != DefaultReorderAutoNonceTicketMs {
			t.Errorf("reorder.autoNonceTicketMs por defecto = %d, se esperaba %d",
				reorder.AutoNonceTicketMs, DefaultReorderAutoNonceTicketMs)
		}
		if len(discarded) != 0 {
			t.Errorf("no se tenia que descartar ninguna clave: %v", discarded)
		}
	})

	t.Run("valores explicitos", func(t *testing.T) {
		v := viperFor(t, "[reorder]\nautoNonce = true\nautoNonceTicketMs = 500\n")
		reorder, _, _, _, _ := LoadRuntimeBlocks(v)

		if !reorder.AutoNonce || reorder.AutoNonceTicketMs != 500 {
			t.Errorf("no se tomaron los valores explicitos: %+v", reorder)
		}
	})

	t.Run("valor invalido", func(t *testing.T) {
		v := viperFor(t, `[reorder]
autoNonce = "si"
autoNonceTicketMs = -1
`)
		reorder, _, _, _, discarded := LoadRuntimeBlocks(v)

		if reorder.AutoNonce {
			t.Error("un valor invalido no puede habilitar el reparto")
		}
		if reorder.AutoNonceTicketMs != DefaultReorderAutoNonceTicketMs {
			t.Errorf("un valor invalido tiene que caer al default, quedo %d", reorder.AutoNonceTicketMs)
		}
		if len(discarded) != 2 {
			t.Errorf("se esperaban dos claves descartadas, hubo %v", discarded)
		}
	})
}

// El bloque [validation] y las claves de permisionado: ausentes toman su default -las dos
// exigencias apagadas- y un valor invalido se descarta sin habilitar ninguna. Cubre las tareas 1.1
// y 1.2 de 05-add-metatx-validation.
func TestValidationBlocks(t *testing.T) {
	t.Run("ausentes", func(t *testing.T) {
		validation, permissioning, discarded := LoadValidationBlocks(viperFor(t, ""))

		if validation.EnforceNodeAddress || validation.EnforceExpiration {
			t.Errorf("las dos exigencias tienen que quedar apagadas por defecto: %+v", validation)
		}
		if validation.MinExpirationSeconds != DefaultMinExpirationSeconds ||
			validation.ExpirationToleranceSeconds != DefaultExpirationToleranceSeconds {
			t.Errorf("los defaults de expiracion no son los esperados: %+v", validation)
		}
		if permissioning.AccountIngressAddress != "" {
			t.Errorf("el registro de permisos no tiene default, quedo %q", permissioning.AccountIngressAddress)
		}
		if permissioning.AccountRulesCacheMs != DefaultAccountRulesCacheMs {
			t.Errorf("la vigencia por defecto = %d, se esperaba %d",
				permissioning.AccountRulesCacheMs, DefaultAccountRulesCacheMs)
		}
		if len(discarded) != 0 {
			t.Errorf("no se tenia que descartar ninguna clave: %v", discarded)
		}
	})

	t.Run("valores explicitos", func(t *testing.T) {
		v := viperFor(t, `[validation]
enforceNodeAddress = true
enforceExpiration = true
minExpirationSeconds = 120
expirationToleranceSeconds = 5

[security]
accountIngressAddress = "0x0000000000000000000000000000000000008888"
accountRulesCacheMs = 1000
`)
		validation, permissioning, discarded := LoadValidationBlocks(v)

		if !validation.EnforceNodeAddress || !validation.EnforceExpiration ||
			validation.MinExpirationSeconds != 120 || validation.ExpirationToleranceSeconds != 5 {
			t.Errorf("no se tomaron los valores explicitos: %+v", validation)
		}
		if permissioning.AccountIngressAddress != "0x0000000000000000000000000000000000008888" ||
			permissioning.AccountRulesCacheMs != 1000 {
			t.Errorf("no se tomaron los valores de permisionado: %+v", permissioning)
		}
		if len(discarded) != 0 {
			t.Errorf("no se tenia que descartar ninguna clave: %v", discarded)
		}
	})

	t.Run("valores invalidos", func(t *testing.T) {
		v := viperFor(t, `[validation]
enforceExpiration = "si"
minExpirationSeconds = -3

[security]
accountIngressAddress = "no-es-una-direccion"
accountRulesCacheMs = "mucho"
`)
		validation, permissioning, discarded := LoadValidationBlocks(v)

		if validation.EnforceExpiration {
			t.Error("un valor invalido no puede habilitar una exigencia")
		}
		if validation.MinExpirationSeconds != DefaultMinExpirationSeconds {
			t.Errorf("el minimo invalido tiene que caer al default, quedo %d", validation.MinExpirationSeconds)
		}
		if permissioning.AccountIngressAddress != "" {
			t.Errorf("una direccion invalida se descarta, quedo %q", permissioning.AccountIngressAddress)
		}
		if permissioning.AccountRulesCacheMs != DefaultAccountRulesCacheMs {
			t.Errorf("la vigencia invalida tiene que caer al default, quedo %d", permissioning.AccountRulesCacheMs)
		}
		if len(discarded) != 4 {
			t.Errorf("se esperaban cuatro claves descartadas, hubo %v", discarded)
		}
	})
}

// El limite efectivo de vigencia es el minimo menos la tolerancia, acotado a cero: una tolerancia
// mayor que el minimo no puede volverse un limite negativo. Cubre la tarea 1.3.
func TestExpirationFloorIsNeverNegative(t *testing.T) {
	casos := []struct {
		minimo, tolerancia, esperado int
	}{
		{300, 2, 298},
		{300, 0, 300},
		{2, 5, 0},
		{0, 10, 0},
	}
	for _, caso := range casos {
		validation := ValidationConfig{MinExpirationSeconds: caso.minimo, ExpirationToleranceSeconds: caso.tolerancia}
		if floor := validation.ExpirationFloor(); floor != caso.esperado {
			t.Errorf("minimo %d con tolerancia %d -> %d, se esperaba %d",
				caso.minimo, caso.tolerancia, floor, caso.esperado)
		}
	}
}
