package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LACNetNetworks/gas-relay-signer/events"
	"github.com/LACNetNetworks/gas-relay-signer/model"
	"github.com/ethereum/go-ethereum/common"
)

// Selectores del permisionado.
const (
	selectorGetContractAddress = "0d2020dd"
	selectorAccountPermitted   = "0f68f0b3"
)

// Direcciones de este escenario.
const (
	ingressAddr    = "0x0000000000000000000000000000000000008888"
	rulesFromChain = "0x00000000000000000000000000000000000000AA"
	rulesInConfig  = "0x4683519EF834572017Cb583246B717449A4B752c"
)

// permissioningNode es un nodo falso que responde el registro de permisos y el contrato de reglas.
type permissioningNode struct {
	server *httptest.Server

	// rulesPublished es lo que el registro publica bajo el nombre "rules". Vacio significa que la
	// red tiene registro pero sin contrato de reglas.
	rulesPublished string
	// withoutCode enumera las direcciones que no tienen codigo en esta red.
	withoutCode map[string]bool

	permitted atomic.Bool
	failRules atomic.Bool

	mutex            sync.Mutex
	permittedQueries int
	ingressQueries   int
}

func newPermissioningNode(rulesPublished string, withoutCode ...string) *permissioningNode {
	node := &permissioningNode{rulesPublished: rulesPublished, withoutCode: map[string]bool{}}
	for _, address := range withoutCode {
		node.withoutCode[strings.ToLower(address)] = true
	}
	node.permitted.Store(true)
	node.server = httptest.NewServer(http.HandlerFunc(node.handle))
	return node
}

func (node *permissioningNode) close() { node.server.Close() }

func (node *permissioningNode) counts() (permitted, ingress int) {
	node.mutex.Lock()
	defer node.mutex.Unlock()
	return node.permittedQueries, node.ingressQueries
}

func (node *permissioningNode) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	text := string(body)
	var request struct {
		Method string        `json:"method"`
		Params []interface{} `json:"params"`
	}
	_ = json.Unmarshal(body, &request)
	w.Header().Set("Content-Type", "application/json")

	word := func(value string) string { return "0x" + strings.Repeat("0", 64-len(value)) + value }

	switch {
	case request.Method == "eth_getCode":
		address, _ := request.Params[0].(string)
		if node.withoutCode[strings.ToLower(address)] {
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"0x"}`)
			return
		}
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"0x60606040"}`)

	case strings.Contains(text, selectorGetContractAddress):
		node.mutex.Lock()
		node.ingressQueries++
		node.mutex.Unlock()
		published := strings.TrimPrefix(strings.ToLower(node.rulesPublished), "0x")
		if published == "" {
			published = strings.Repeat("0", 40)
		}
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":"%s"}`, word(published))

	case strings.Contains(text, selectorAccountPermitted):
		node.mutex.Lock()
		node.permittedQueries++
		node.mutex.Unlock()
		if node.failRules.Load() {
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"execution reverted"}}`)
			return
		}
		if node.permitted.Load() {
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":"%s"}`, word("1"))
			return
		}
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":"%s"}`, word("0"))

	default:
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":null}`)
	}
}

// permissioningService arma un servicio contra ese nodo, con la configuracion de permisionado dada.
func permissioningService(node *permissioningNode, configured, ingress string, enabled bool) *RelaySignerService {
	service := new(RelaySignerService)
	service.Config = &model.Config{Application: model.ApplicationConfig{
		NodeURL: node.server.URL,
		Key:     "b3e7374dca5ca90c3899dbb2c978051437fb15534c945bf59df16d6c80be27c0",
	}}
	service.Config.Security = model.SecurityConfig{PermissionsEnabled: enabled, AccountContractAddress: configured}
	service.Config.Permissioning = model.PermissioningConfig{
		AccountIngressAddress: ingress,
		AccountRulesCacheMs:   model.DefaultAccountRulesCacheMs,
	}
	return service
}

func unaCuenta() common.Address {
	return common.HexToAddress("0xa0f03c489a1bcd53883289d3c476100220b21b0b")
}

// Sin direccion configurada, el contrato de reglas se resuelve del registro de la red. Cubre 3.1.
func TestRulesResolvedFromTheIngress(t *testing.T) {
	node := newPermissioningNode(rulesFromChain)
	defer node.close()
	service := permissioningService(node, "", ingressAddr, true)

	service.ResolveAccountRules(context.Background())

	if address := service.AccountRulesAddress(); address == nil || !strings.EqualFold(*address, rulesFromChain) {
		t.Errorf("direccion resuelta = %v, se esperaba %s", address, rulesFromChain)
	}
	if source := service.AccountRulesSource(); source == nil || *source != rulesFromIngress {
		t.Errorf("fuente = %v, se esperaba %s", source, rulesFromIngress)
	}
}

// La direccion configurada gana, y el registro ni se consulta. Cubre 3.2.
func TestConfiguredAddressTakesPrecedence(t *testing.T) {
	node := newPermissioningNode(rulesFromChain)
	defer node.close()
	service := permissioningService(node, rulesInConfig, ingressAddr, true)

	service.ResolveAccountRules(context.Background())

	if address := service.AccountRulesAddress(); address == nil || !strings.EqualFold(*address, rulesInConfig) {
		t.Errorf("direccion resuelta = %v, se esperaba la configurada %s", address, rulesInConfig)
	}
	if source := service.AccountRulesSource(); source == nil || *source != rulesFromConfig {
		t.Errorf("fuente = %v, se esperaba %s", source, rulesFromConfig)
	}
	if _, ingress := node.counts(); ingress != 0 {
		t.Errorf("con direccion configurada no hay que consultar el registro, se consulto %d veces", ingress)
	}
}

// Una red sin permisionado no es un error: el servicio opera y relaya. Cubre 3.3.
func TestNetworkWithoutPermissioning(t *testing.T) {
	t.Run("sin registro desplegado", func(t *testing.T) {
		node := newPermissioningNode(rulesFromChain, ingressAddr)
		defer node.close()
		service := permissioningService(node, "", ingressAddr, false)

		service.ResolveAccountRules(context.Background())

		if service.AccountRulesAddress() != nil || service.AccountRulesSource() != nil {
			t.Error("sin registro desplegado no hay contrato de reglas que informar")
		}
		if permitted, err := service.AccountPermitted(context.Background(), unaCuenta()); err != nil || !permitted {
			t.Errorf("sin permisionado hay que relayar con normalidad: %v %v", permitted, err)
		}
	})

	t.Run("registro sin contrato publicado", func(t *testing.T) {
		node := newPermissioningNode("")
		defer node.close()
		service := permissioningService(node, "", ingressAddr, false)

		service.ResolveAccountRules(context.Background())

		if service.AccountRulesAddress() != nil {
			t.Errorf("un registro sin contrato publicado no resuelve nada, resolvio %v", service.AccountRulesAddress())
		}
	})
}

// Si alguien PIDIO el chequeo y no hay allowlist que aplicar, no se deja pasar.
func TestEnabledCheckWithoutRulesRejects(t *testing.T) {
	node := newPermissioningNode("")
	defer node.close()
	service := permissioningService(node, "", ingressAddr, true)

	service.ResolveAccountRules(context.Background())

	permitted, err := service.AccountPermitted(context.Background(), unaCuenta())
	if permitted || err == nil {
		t.Fatalf("con el chequeo pedido y sin reglas no se puede dejar pasar: %v %v", permitted, err)
	}
	if !strings.Contains(err.Error(), "accountContractAddress") {
		t.Errorf("el motivo tiene que decir como resolverlo: %v", err)
	}
}

// Una direccion configurada sin codigo se conserva para informarla, pero no da por permitida a
// nadie. Cubre 3.4.
func TestConfiguredAddressWithoutCode(t *testing.T) {
	node := newPermissioningNode(rulesFromChain, rulesInConfig)
	defer node.close()
	service := permissioningService(node, rulesInConfig, "", true)

	service.ResolveAccountRules(context.Background())

	if address := service.AccountRulesAddress(); address == nil || !strings.EqualFold(*address, rulesInConfig) {
		t.Errorf("la direccion configurada tiene que seguir informandose: %v", address)
	}
	// El nodo simulado responde a accountPermitted igual, asi que lo que se comprueba es que la
	// situacion quedo registrada y la direccion no se cambio por otra.
	if source := service.AccountRulesSource(); source == nil || *source != rulesFromConfig {
		t.Errorf("fuente = %v", source)
	}
}

// El registro se resuelve UNA sola vez: atender metatx no lo vuelve a consultar. Cubre 3.5.
func TestIngressIsResolvedOnlyOnce(t *testing.T) {
	node := newPermissioningNode(rulesFromChain)
	defer node.close()
	service := permissioningService(node, "", ingressAddr, true)

	service.ResolveAccountRules(context.Background())
	_, despuesDeArrancar := node.counts()

	for i := 0; i < 5; i++ {
		if _, err := service.AccountPermitted(context.Background(), unaCuenta()); err != nil {
			t.Fatalf("la consulta de permiso fallo: %v", err)
		}
	}

	if _, ahora := node.counts(); ahora != despuesDeArrancar {
		t.Errorf("el registro se consulto %d veces de mas al atender metatx", ahora-despuesDeArrancar)
	}
}

// El resultado se cachea por cuenta durante la vigencia configurada. Cubre 4.1 y 4.2.
func TestPermissionCacheHonoursItsTTL(t *testing.T) {
	node := newPermissioningNode(rulesFromChain)
	defer node.close()
	service := permissioningService(node, rulesInConfig, "", true)
	service.Config.Permissioning.AccountRulesCacheMs = 80
	service.ResolveAccountRules(context.Background())

	antes, _ := node.counts()
	for i := 0; i < 3; i++ {
		if _, err := service.AccountPermitted(context.Background(), unaCuenta()); err != nil {
			t.Fatalf("la consulta fallo: %v", err)
		}
	}
	dentroDeLaVigencia, _ := node.counts()
	if dentroDeLaVigencia-antes != 1 {
		t.Errorf("dentro de la vigencia la cadena se consulta una sola vez, se consulto %d",
			dentroDeLaVigencia-antes)
	}

	// Pasada la vigencia se vuelve a leer, y un alta reciente se refleja.
	node.permitted.Store(false)
	time.Sleep(120 * time.Millisecond)

	permitted, err := service.AccountPermitted(context.Background(), unaCuenta())
	if err != nil {
		t.Fatalf("la consulta fallo: %v", err)
	}
	if permitted {
		t.Error("pasada la vigencia hay que reflejar lo que dice la cadena ahora")
	}
	if despues, _ := node.counts(); despues == dentroDeLaVigencia {
		t.Error("pasada la vigencia hay que volver a consultar la cadena")
	}
}

// Un fallo al leer el allowlist no deja pasar, y no se sirve un valor cacheado vencido. Cubre 4.3.
func TestUnreadableAllowlistDoesNotLetThrough(t *testing.T) {
	node := newPermissioningNode(rulesFromChain)
	defer node.close()
	service := permissioningService(node, rulesInConfig, "", true)
	service.Config.Permissioning.AccountRulesCacheMs = 50
	service.ResolveAccountRules(context.Background())

	if permitted, err := service.AccountPermitted(context.Background(), unaCuenta()); err != nil || !permitted {
		t.Fatalf("la primera consulta deberia responder permitido: %v %v", permitted, err)
	}

	// La cadena deja de responder y lo cacheado vence.
	node.failRules.Store(true)
	time.Sleep(80 * time.Millisecond)

	permitted, err := service.AccountPermitted(context.Background(), unaCuenta())
	if err == nil {
		t.Fatal("un allowlist ilegible tiene que devolver error, no un valor viejo")
	}
	if permitted {
		t.Error("un allowlist ilegible no puede dejar pasar")
	}
}

// La cache soporta consultas concurrentes. Corre con -race. Cubre 4.4.
func TestPermissionCacheUnderConcurrency(t *testing.T) {
	node := newPermissioningNode(rulesFromChain)
	defer node.close()
	service := permissioningService(node, rulesInConfig, "", true)
	service.ResolveAccountRules(context.Background())

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if _, err := service.AccountPermitted(context.Background(), unaCuenta()); err != nil {
				t.Errorf("consulta concurrente fallo: %v", err)
			}
		}()
		go func(i int) {
			defer wg.Done()
			otra := common.BigToAddress(common.Big1)
			otra[19] = byte(i)
			if _, err := service.AccountPermitted(context.Background(), otra); err != nil {
				t.Errorf("consulta concurrente fallo: %v", err)
			}
		}(i)
	}
	wg.Wait()
}

// El nodo que relaya se comprueba al arrancar y queda informado. Cubre 5.1 y 5.2.
func TestNodePermittedIsCheckedAtStartup(t *testing.T) {
	t.Run("permitido", func(t *testing.T) {
		node := newPermissioningNode(rulesFromChain)
		defer node.close()
		service := permissioningService(node, rulesInConfig, "", true)

		service.ResolveAccountRules(context.Background())

		if permitted := service.NodePermitted(); permitted == nil || !*permitted {
			t.Errorf("nodePermitted = %v, se esperaba true", permitted)
		}
	})

	t.Run("no permitido no impide arrancar", func(t *testing.T) {
		node := newPermissioningNode(rulesFromChain)
		defer node.close()
		node.permitted.Store(false)
		service := permissioningService(node, rulesInConfig, "", true)

		service.ResolveAccountRules(context.Background())

		if permitted := service.NodePermitted(); permitted == nil || *permitted {
			t.Errorf("nodePermitted = %v, se esperaba false informado", permitted)
		}
		if service.AccountRulesAddress() == nil {
			t.Error("el servicio tiene que seguir operativo y con su contrato de reglas resuelto")
		}
	})

	t.Run("comprobacion que falla deja el dato sin valor", func(t *testing.T) {
		node := newPermissioningNode(rulesFromChain)
		defer node.close()
		node.failRules.Store(true)
		service := permissioningService(node, rulesInConfig, "", true)

		service.ResolveAccountRules(context.Background())

		if permitted := service.NodePermitted(); permitted != nil {
			t.Errorf("nodePermitted = %v: si no se pudo comprobar, va sin valor", *permitted)
		}
	})
}

// El nodo no permitido se registra de forma accionable: el mensaje dice que hace falta para darlo
// de alta, porque el sintoma -todas las metatx fallando on-chain- no lo sugiere. Cubre 5.3.
func TestNotPermittedNodeIsLoggedActionably(t *testing.T) {
	events.Init(true, 50)
	defer events.Init(false, 0)
	node := newPermissioningNode(rulesFromChain)
	defer node.close()
	node.permitted.Store(false)
	service := permissioningService(node, rulesInConfig, "", true)

	service.ResolveAccountRules(context.Background())

	var aviso events.Event
	var encontrado bool
	for _, event := range events.Replay(0) {
		if event.Name() == "permissioning.node_not_permitted" {
			aviso, encontrado = event, true
		}
	}
	if !encontrado {
		t.Fatal("un nodo no permitido tiene que dejar aviso")
	}
	nota, _ := aviso.Field("note").(string)
	for _, esperado := range []string{"addAccount", "addNode"} {
		if !strings.Contains(nota, esperado) {
			t.Errorf("el aviso tiene que decir que hace falta %s: %q", esperado, nota)
		}
	}
	if aviso.Field("nodeAddress") == nil || aviso.Field("accountRules") == nil {
		t.Errorf("el aviso tiene que indicar el nodo y el contrato de reglas: %v", aviso.Line)
	}
	if aviso.Level() != "warn" {
		t.Errorf("level = %q, un nodo no permitido es informacion de operacion", aviso.Level())
	}
}

// Con el chequeo pedido, el arranque acepta que la direccion del contrato de reglas venga del
// registro de permisos en lugar de la configuracion. Sin ninguna de las dos sigue fallando, como
// siempre.
func TestStartupAcceptsRulesFromTheIngress(t *testing.T) {
	previous, hadKey := os.LookupEnv("WRITER_KEY")
	os.Setenv("WRITER_KEY", "0xb3e7374dca5ca90c3899dbb2c978051437fb15534c945bf59df16d6c80be27c0")
	t.Cleanup(func() {
		if hadKey {
			os.Setenv("WRITER_KEY", previous)
			return
		}
		os.Unsetenv("WRITER_KEY")
	})

	node := newPermissioningNode(rulesFromChain)
	defer node.close()

	configFor := func(configured, ingress string) *model.Config {
		config := &model.Config{Application: model.ApplicationConfig{NodeURL: node.server.URL}}
		config.Security = model.SecurityConfig{PermissionsEnabled: true, AccountContractAddress: configured}
		config.Permissioning = model.PermissioningConfig{AccountIngressAddress: ingress}
		return config
	}

	t.Run("con registro y sin direccion configurada", func(t *testing.T) {
		err := new(RelaySignerService).Init(configFor("", ingressAddr))
		// Falla despues, al resolver el RelayHub contra este nodo simulado: lo que importa es que
		// NO falle por la direccion del contrato de reglas.
		if err != nil && strings.Contains(err.Error(), "Account Smart Contract Address") {
			t.Errorf("con registro configurado el arranque no puede exigir la direccion: %v", err)
		}
	})

	t.Run("sin ninguna de las dos", func(t *testing.T) {
		err := new(RelaySignerService).Init(configFor("", ""))
		if err == nil || !strings.Contains(err.Error(), "Account Smart Contract Address") {
			t.Errorf("sin direccion ni registro el arranque tiene que fallar como siempre: %v", err)
		}
	})
}
