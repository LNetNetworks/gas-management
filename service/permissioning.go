package service

import (
	"context"
	"sync"
	"time"

	log "github.com/LACNetNetworks/gas-relay-signer/audit"
	bl "github.com/LACNetNetworks/gas-relay-signer/blockchain"
	"github.com/LACNetNetworks/gas-relay-signer/errors"
	"github.com/LACNetNetworks/gas-relay-signer/model"
	"github.com/ethereum/go-ethereum/common"
)

// De donde sale el contrato de reglas del permisionado.
//
// Hasta ahora salia de una direccion fija en la configuracion. Si el contrato de reglas de la red
// cambia, el servicio le sigue preguntando al viejo y contesta que si sobre un allowlist que ya no
// rige. Ahora, cuando no hay direccion configurada, se resuelve del registro de permisos de la red.
//
// La direccion configurada conserva la PRECEDENCIA: es lo que hace que un despliegue actual -que la
// tiene escrita- no cambie de comportamiento al actualizar el binario. Ver design.md, D4.

// Origen de la direccion del contrato de reglas.
const (
	rulesFromConfig  = "config"
	rulesFromIngress = "ingress"
)

// accountRules es el contrato de reglas resuelto, con su origen y lo que se sabe de cada cuenta.
//
// La resolucion es una propiedad de la RED, no de la peticion: se hace una vez al arrancar y se
// guarda. El permiso de cada cuenta si cambia, y por eso se cachea con vigencia. Ver design.md, D5.
type accountRules struct {
	address common.Address
	source  string

	mutex sync.Mutex
	cache map[common.Address]permissionEntry
}

// permissionEntry es lo que se sabe de una cuenta y hasta cuando vale.
type permissionEntry struct {
	permitted bool
	expiresAt time.Time
}

// ResolveAccountRules deja resuelto de donde sale el contrato de reglas, y comprueba si el nodo que
// relaya esta permitido.
//
// No devuelve error ni impide arrancar: una red sin permisionado es un caso valido -una devnet-, y
// un nodo no permitido se diagnostica mejor viendolo en `GET /info` que con un binario que no
// levanta. Ver design.md, D6.
func (service *RelaySignerService) ResolveAccountRules(ctx context.Context) {
	// Con el chequeo apagado no se resuelve NADA y no se toca la cadena: `GET /info` ya define que
	// con el permisionado deshabilitado se responde sin consultar el contrato de reglas, y resolver
	// igual agregaria consultas al arranque de un despliegue que hoy no las hace.
	if service.Config == nil || !service.Config.Security.PermissionsEnabled {
		return
	}

	rules := service.resolveRules(ctx)
	service.rulesMutex.Lock()
	service.rules = rules
	service.rulesMutex.Unlock()

	if rules == nil {
		log.Info(ctx, "permissioning.absent", map[string]interface{}{
			"note": "esta red no expone contrato de reglas: no se aplica permisionado de cuentas",
		})
		return
	}

	log.Info(ctx, "permissioning.resolved", map[string]interface{}{
		"accountRules":       rules.address.Hex(),
		"accountRulesSource": rules.source,
	})
	service.checkNodePermitted(ctx)
}

// resolveRules aplica la precedencia de D4: la direccion configurada primero, el registro despues.
func (service *RelaySignerService) resolveRules(ctx context.Context) *accountRules {
	configured := service.configuredRulesAddress()
	ingress := service.ingressAddress()

	if configured == nil && ingress == nil {
		return nil
	}

	client := new(bl.Client)
	if err := client.Connect(service.Config.Application.NodeURL); err != nil {
		log.Warn(ctx, "permissioning.resolve_failed", log.ErrorFields(err))
		// Con una direccion configurada se conserva: es lo que este servicio usaba hasta ahora, y no
		// poder hablar con el nodo al arrancar no la invalida. Si sigue sin responder al relayar, el
		// chequeo falla cerrado ahi.
		if configured != nil {
			return newAccountRules(*configured, rulesFromConfig)
		}
		return nil
	}
	defer client.Close()

	if configured != nil {
		if hasCode, err := client.HasCode(*configured); err == nil && !hasCode {
			// Se conserva la direccion configurada: el operador la escribio y tiene que verla en
			// `GET /info`. Lo que NO se hace es dar por permitida ninguna cuenta, porque preguntarle
			// a una direccion sin codigo no responde nada.
			log.Warn(ctx, "permissioning.no_code", map[string]interface{}{
				"accountRules": configured.Hex(),
				"note":         "la direccion configurada no tiene codigo en esta red",
			})
		}
		return newAccountRules(*configured, rulesFromConfig)
	}

	hasCode, err := client.HasCode(*ingress)
	if err != nil {
		log.Warn(ctx, "permissioning.resolve_failed", log.ErrorFields(err))
		return nil
	}
	if !hasCode {
		// Red sin permisionado: el registro no esta desplegado.
		return nil
	}

	resolved, err := client.ResolveAccountRules(*ingress)
	if err != nil {
		log.Warn(ctx, "permissioning.resolve_failed", log.ErrorFields(err))
		return nil
	}
	if resolved == (common.Address{}) {
		// El registro existe pero no tiene contrato de reglas publicado.
		return nil
	}
	return newAccountRules(resolved, rulesFromIngress)
}

func newAccountRules(address common.Address, source string) *accountRules {
	return &accountRules{address: address, source: source, cache: make(map[common.Address]permissionEntry)}
}

// configuredRulesAddress es la direccion escrita en la configuracion, si la hay.
func (service *RelaySignerService) configuredRulesAddress() *common.Address {
	if service.Config == nil || service.Config.Security.AccountContractAddress == "" {
		return nil
	}
	if !common.IsHexAddress(service.Config.Security.AccountContractAddress) {
		return nil
	}
	address := common.HexToAddress(service.Config.Security.AccountContractAddress)
	return &address
}

// ingressAddress es el registro de permisos configurado, si lo hay.
func (service *RelaySignerService) ingressAddress() *common.Address {
	if service.Config == nil || service.Config.Permissioning.AccountIngressAddress == "" {
		return nil
	}
	address := common.HexToAddress(service.Config.Permissioning.AccountIngressAddress)
	return &address
}

// accountRulesOf es el contrato de reglas vigente, o nil si esta red no tiene.
func (service *RelaySignerService) accountRulesOf() *accountRules {
	service.rulesMutex.Lock()
	defer service.rulesMutex.Unlock()
	return service.rules
}

// rulesCacheTTL es cuanto vale lo que se sabe de una cuenta.
func (service *RelaySignerService) rulesCacheTTL() time.Duration {
	if service.Config != nil && service.Config.Permissioning.AccountRulesCacheMs > 0 {
		return time.Duration(service.Config.Permissioning.AccountRulesCacheMs) * time.Millisecond
	}
	return time.Duration(model.DefaultAccountRulesCacheMs) * time.Millisecond
}

// AccountPermitted responde si esa cuenta esta dada de alta en el contrato de reglas, cacheando el
// resultado.
//
// Un fallo al consultar NO deja pasar: un allowlist que no se puede leer no se puede aplicar, y
// dejar pasar seria abrir la puerta creyendo lo contrario. Tampoco se sirve un valor cacheado
// vencido para tapar el fallo. Ver design.md, D7.
func (service *RelaySignerService) AccountPermitted(ctx context.Context, account common.Address) (bool, error) {
	rules := service.accountRulesOf()
	if rules == nil {
		// Sin contrato de reglas no hay allowlist que aplicar. Lo que se hace depende de si alguien
		// lo pidio: con el chequeo apagado no habia nada que comprobar, y con el chequeo encendido
		// no se puede dejar pasar, porque seria abrir la puerta creyendo lo contrario.
		if service.Config != nil && service.Config.Security.PermissionsEnabled {
			return false, errors.New(
				"the sender permission check is enabled but no account rules contract could be resolved: "+
					"set security.accountContractAddress, or security.accountIngressAddress on a chain that "+
					"publishes one, or turn permissionsEnabled off", -32603)
		}
		return true, nil
	}

	if permitted, fresh := rules.cached(account, time.Now()); fresh {
		return permitted, nil
	}

	client := new(bl.Client)
	if err := client.Connect(service.Config.Application.NodeURL); err != nil {
		return false, err
	}
	defer client.Close()

	permitted, err := client.AccountPermitted(rules.address, account)
	if err != nil {
		return false, err
	}

	rules.remember(account, permitted, time.Now().Add(service.rulesCacheTTL()))
	return permitted, nil
}

// cached devuelve lo que se sabe de una cuenta, y si sigue vigente.
func (rules *accountRules) cached(account common.Address, now time.Time) (bool, bool) {
	rules.mutex.Lock()
	defer rules.mutex.Unlock()
	entry, known := rules.cache[account]
	if !known || !entry.expiresAt.After(now) {
		return false, false
	}
	return entry.permitted, true
}

// remember guarda lo que se acaba de leer de la cadena.
func (rules *accountRules) remember(account common.Address, permitted bool, expiresAt time.Time) {
	rules.mutex.Lock()
	defer rules.mutex.Unlock()
	if rules.cache == nil {
		rules.cache = make(map[common.Address]permissionEntry)
	}
	rules.cache[account] = permissionEntry{permitted: permitted, expiresAt: expiresAt}
}

// checkNodePermitted comprueba si el nodo que relaya esta permitido y lo deja informado.
//
// Un nodo no permitido relaya sin error aparente y TODAS sus metatx fallan on-chain: el sintoma es
// opaco y el diagnostico se resuelve mirando este dato.
func (service *RelaySignerService) checkNodePermitted(ctx context.Context) {
	nodeAddress, err := service.NodeAddress()
	if err != nil {
		return
	}

	permitted, err := service.AccountPermitted(ctx, nodeAddress)
	if err != nil {
		// No se pudo comprobar: se informa sin valor en lugar de afirmar que esta permitido.
		log.Warn(ctx, "permissioning.node_check_failed", log.ErrorFields(err))
		return
	}

	service.rulesMutex.Lock()
	service.nodePermitted = &permitted
	service.rulesMutex.Unlock()

	if permitted {
		return
	}
	rules := service.accountRulesOf()
	address := ""
	if rules != nil {
		address = rules.address.Hex()
	}
	log.Warn(ctx, "permissioning.node_not_permitted", map[string]interface{}{
		"nodeAddress":  nodeAddress.Hex(),
		"accountRules": address,
		"note": "este nodo no esta dado de alta en AccountRules: todas sus metatx van a fallar. " +
			"Darlo de alta con addAccount en el contrato de reglas, y con addNode en el hub",
	})
}

// NodePermitted es lo que se sabe sobre si el nodo que relaya esta permitido, o nil si no se pudo
// comprobar.
func (service *RelaySignerService) NodePermitted() *bool {
	service.rulesMutex.Lock()
	defer service.rulesMutex.Unlock()
	return service.nodePermitted
}

// AccountRulesAddress y AccountRulesSource son lo que informa `GET /info`.
func (service *RelaySignerService) AccountRulesAddress() *string {
	rules := service.accountRulesOf()
	if rules == nil {
		return nil
	}
	address := rules.address.Hex()
	return &address
}

func (service *RelaySignerService) AccountRulesSource() *string {
	rules := service.accountRulesOf()
	if rules == nil {
		return nil
	}
	source := rules.source
	return &source
}
