package model

import "github.com/ethereum/go-ethereum/common"

type ApplicationConfig struct {
	NodeURL                 string          `mapstructure:"nodeURL"`
	WSURL                   string          `mapstructure:"wsURL"`
	ContractAddress         string          `mapstructure:"contractAddress"`
	RelayHubContractAddress *common.Address `mapstructure:"relayHubContractAddress"`
	NodeKeyPath             string          `mapstructure:"nodeKeyPath"`
	NodeAddressPath         string          `mapstructure:"nodeAddressPath"`
	Key                     string          `mapstructure:"key"`
	Port                    string          `mapstructure:"port"`
	// NonceCacheTTL: vida máxima (segundos) de una entrada del caché de nonces por sender.
	// 0 o ausente = default (300s).
	NonceCacheTTL int64 `mapstructure:"nonceCacheTTL"`
}

type KeyStoreConfig struct {
	Agent string `mapstructure:"agent"`
}

type PassphraseConfig struct {
	Agent string `mapstructure:"agent"`
}

type SecurityConfig struct {
	PermissionsEnabled     bool   `mapstructure:"permissionsEnabled"`
	AccountContractAddress string `mapstructure:"accountContractAddress"`
}

// ReorderConfig gobierna el reordenamiento de nonces. Apagado por defecto: mientras `Enabled` sea
// false ninguna metatx se retiene y el camino JSON-RPC se comporta como siempre.
type ReorderConfig struct {
	Enabled            bool
	WindowMs           int
	MaxInflightPerUser int
	ReceiptTimeoutMs   int
	// AutoNonce habilita el reparto de nonces: las consultas del mismo usuario se serializan y cada
	// una se lleva un numero distinto. Apagado, consultar el nonce no reserva nada y dos clientes
	// que preguntan a la vez se llevan el mismo, que es el comportamiento historico.
	AutoNonce bool
	// AutoNonceTicketMs es cuanto se espera la metatx que use un nonce entregado antes de que ese
	// mismo numero vuelva a entregarse.
	AutoNonceTicketMs int
}

// DashboardConfig gobierna el bus de eventos en memoria. `BufferSize` distingue la clave ausente
// -que toma el default- de un 0 explicito, que deja el bus sin capacidad, es decir inerte.
type DashboardConfig struct {
	Enabled    bool
	BufferSize int
}

// LogConfig gobierna el log estructurado. `Level` es el nivel minimo que se escribe en la salida;
// no afecta que eventos se publican en el bus. `RawTx` habilita el volcado de la transaccion
// firmada completa, que por defecto no se registra.
type LogConfig struct {
	Level string
	RawTx bool
}

// CorsConfig gobierna que origenes pueden llamar al servicio desde un navegador. Vacio -el
// default- significa no emitir ninguna cabecera de intercambio entre origenes, que es como se
// comporta el servicio desde siempre.
type CorsConfig struct {
	AllowedOrigins []string
}

// Config es la configuracion del servicio.
//
// Los tres bloques nuevos llevan `mapstructure:"-"` a proposito: NO se decodifican con el
// Unmarshal que carga el resto. Un valor no interpretable en cualquiera de sus claves haria
// fallar ese Unmarshal entero -no solo esa clave- y abortaria el arranque, que es justo lo que
// no debe pasar. Los llena LoadRuntimeBlocks, clave por clave. Ver design.md, D10.
type Config struct {
	Application ApplicationConfig `mapstructure:"application"`
	KeyStore    KeyStoreConfig    `mapstructure:"keystore"`
	Passphrase  PassphraseConfig  `mapstructure:"passphrase"`
	Security    SecurityConfig    `mapstructure:"security"`
	Reorder     ReorderConfig     `mapstructure:"-"`
	Dashboard   DashboardConfig   `mapstructure:"-"`
	Log         LogConfig         `mapstructure:"-"`
	CORS        CorsConfig        `mapstructure:"-"`
}
