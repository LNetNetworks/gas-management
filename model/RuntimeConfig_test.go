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
	if reorder.MaxInflightPerUser != 16 {
		t.Errorf("reorder.maxInflightPerUser por defecto = %d, se esperaba 16", reorder.MaxInflightPerUser)
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
	if reorder.MaxInflightPerUser != 16 {
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
	if reorder.MaxInflightPerUser != 16 {
		t.Errorf("reorder.maxInflightPerUser = %d, se esperaba el default 16", reorder.MaxInflightPerUser)
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
