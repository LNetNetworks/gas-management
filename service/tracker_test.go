package service

import (
	"context"
	"encoding/json"
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LACNetNetworks/gas-relay-signer/model"
	"github.com/ethereum/go-ethereum/common"
)

// trackerService arma un servicio con el tracker vacio y la ventana indicada.
func trackerService(windowMs int) *RelaySignerService {
	service := new(RelaySignerService)
	service.Config = &model.Config{}
	service.Config.Reorder = model.ReorderConfig{Enabled: true, WindowMs: windowMs, MaxInflightPerUser: 16}
	service.senders = make(map[string]*nonceEntry)
	return service
}

// reserveNext hace lo que hace el camino de envio: leer el esperado y reservar sobre el, en UNA
// seccion critica. Devuelve el nonce que le toco y la cadena sobre la que quedo.
func reserveNext(service *RelaySignerService, key string, base uint64) (uint64, *nonceEntry) {
	service.sendersLock.Lock()
	defer service.sendersLock.Unlock()

	next := base
	if entry := service.chainLocked(key); entry != nil {
		next = entry.next
	}
	return next, service.reserveLocked(key, next, true)
}

// Reservar, liberar y descartar desde varias goroutines a la vez no puede perder ni duplicar una
// posicion: cada una se tiene que llevar un nonce distinto y consecutivo.
func TestTrackerReservesEachPositionOnce(t *testing.T) {
	service := trackerService(3000)
	key := senderKey("0xa0f03c489a1bcd53883289d3c476100220b21b0b")

	const reservas = 64
	const base = 100

	taken := make([]uint64, reservas)
	chains := make([]*nonceEntry, reservas)
	var wg sync.WaitGroup
	for i := 0; i < reservas; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			taken[i], chains[i] = reserveNext(service, key, base)
		}(i)
	}
	wg.Wait()

	seen := make(map[uint64]bool, reservas)
	for _, nonce := range taken {
		if seen[nonce] {
			t.Fatalf("el nonce %d se entrego dos veces", nonce)
		}
		seen[nonce] = true
	}
	for nonce := uint64(base); nonce < base+reservas; nonce++ {
		if !seen[nonce] {
			t.Fatalf("falta el nonce %d: la secuencia tiene un hueco", nonce)
		}
	}

	if pending := service.pendingOf(key); pending != reservas {
		t.Errorf("en vuelo esperado %d, fue %d", reservas, pending)
	}

	// Liberar concurrentemente tampoco puede descontar de mas ni de menos.
	for _, chain := range chains {
		wg.Add(1)
		go func(chain *nonceEntry) { defer wg.Done(); service.releaseChain(key, chain) }(chain)
	}
	wg.Wait()

	if pending := service.pendingOf(key); pending != 0 {
		t.Errorf("tras liberar todo, en vuelo esperado 0, fue %d", pending)
	}
}

// Descartar la cadena mientras se reserva y se libera no puede corromper el tracker ni trabarlo.
// El detector de carreras es parte de la comprobacion: este test corre con -race.
func TestTrackerConcurrentReserveReleaseAndForget(t *testing.T) {
	service := trackerService(50)
	key := senderKey("0xa0f03c489a1bcd53883289d3c476100220b21b0b")

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			_, chain := reserveNext(service, key, 1)
			service.releaseChain(key, chain)
		}()
		go func() { defer wg.Done(); service.forgetChain(key) }()
		go func() { defer wg.Done(); service.pendingOf(key) }()
	}
	wg.Wait()

	// Tras el ultimo descarte el usuario tiene que poder reservar de nuevo: nada quedo trabado.
	nonce, chain := reserveNext(service, key, 7)
	if chain == nil {
		t.Fatal("no se pudo reservar despues de descartar la cadena")
	}
	if nonce < 1 {
		t.Errorf("nonce reservado inesperado: %d", nonce)
	}
	service.releaseChain(key, chain)
}

// Un resultado que llega tarde y pertenece a una cadena YA DESCARTADA no puede descontar sobre la
// que la reemplazo: si lo hiciera, el conteo de en vuelo quedaria corrido para siempre.
func TestTrackerLateReleaseDoesNotTouchNewChain(t *testing.T) {
	service := trackerService(3000)
	key := senderKey("0xa0f03c489a1bcd53883289d3c476100220b21b0b")

	_, vieja := reserveNext(service, key, 10)
	service.forgetChain(key)

	_, nueva := reserveNext(service, key, 50)
	if vieja == nueva {
		t.Fatal("la cadena nueva tiene que ser otra identidad que la descartada")
	}

	service.releaseChain(key, vieja) // el resultado atrasado de la cadena vieja

	if pending := service.pendingOf(key); pending != 1 {
		t.Errorf("la cadena nueva deberia seguir con 1 en vuelo, tiene %d", pending)
	}
	service.sendersLock.Lock()
	next := service.senders[key].next
	service.sendersLock.Unlock()
	if next != 51 {
		t.Errorf("el proximo nonce de la cadena nueva deberia ser 51, es %d", next)
	}
}

// La gracia: al quedarse sin metatx en vuelo la cadena sobrevive un rato, y despues se olvida.
func TestTrackerGraceBeforeForgetting(t *testing.T) {
	service := trackerService(60)
	key := senderKey("0xa0f03c489a1bcd53883289d3c476100220b21b0b")

	_, chain := reserveNext(service, key, 10)
	service.releaseChain(key, chain)

	// Durante la gracia el proximo nonce sigue siendo el reservado.
	service.sendersLock.Lock()
	entry := service.senders[key]
	service.sendersLock.Unlock()
	if entry == nil || entry.next != 11 {
		t.Fatalf("durante la gracia la cadena tiene que seguir en 11, fue %v", entry)
	}

	time.Sleep(150 * time.Millisecond)

	service.sendersLock.Lock()
	entry = service.senders[key]
	service.sendersLock.Unlock()
	if entry != nil {
		t.Errorf("pasada la gracia la cadena tiene que olvidarse, quedo %v", entry)
	}
}

// Una metatx nueva durante la gracia continua la cadena en curso, no empieza una nueva: si
// empezara una nueva, el proximo nonce caeria al de la cadena de bloques en medio de una rafaga.
func TestTrackerReserveDuringGraceContinuesTheChain(t *testing.T) {
	service := trackerService(200)
	key := senderKey("0xa0f03c489a1bcd53883289d3c476100220b21b0b")

	_, chain := reserveNext(service, key, 10)
	service.releaseChain(key, chain) // arranca la gracia

	nonce, continuada := reserveNext(service, key, 0)
	if nonce != 11 {
		t.Errorf("la metatx nueva deberia tomar el 11, tomo %d", nonce)
	}
	if continuada != chain {
		t.Error("deberia continuar la MISMA cadena, no empezar una nueva")
	}

	time.Sleep(300 * time.Millisecond)

	// La gracia se cancelo al continuar la rafaga: la cadena sigue viva con su metatx en vuelo.
	if pending := service.pendingOf(key); pending != 1 {
		t.Errorf("la cadena deberia seguir viva con 1 en vuelo, tiene %d", pending)
	}
}

// Las dos puertas del nonce -el camino JSON-RPC y GET /nonce/{address}- tienen que responder el
// MISMO proximo nonce para la misma direccion en el mismo momento. Si difieren, un cliente que use
// una y otra firma con nonces incompatibles.
func TestBothNonceDoorsAgree(t *testing.T) {
	srv := serverMock()
	defer srv.Close()

	relayHubAddress := common.HexToAddress("0xdD37c69fF29C4b93A346Ed6dF184f48A71800b7E")
	config := model.Config{Application: model.ApplicationConfig{
		NodeURL:         srv.URL + "/getTransactionCount",
		ContractAddress: relayHubAddress.Hex(),
		Key:             "b3e7374dca5ca90c3899dbb2c978051437fb15534c945bf59df16d6c80be27c0",
	}}
	service := new(RelaySignerService)
	service.Config = &config
	service.Config.Application.RelayHubContractAddress = &relayHubAddress
	service.senders = make(map[string]*nonceEntry)

	address := common.HexToAddress("0xa0f03c489a1bcd53883289d3c476100220b21b0b")

	// Sin nada en vuelo: las dos leen la cadena.
	assertDoorsAgree(t, service, address, "sin metatx en vuelo")

	// Con una metatx relayada: las dos tienen que contar lo reservado.
	service.incrementTransactionCount(address.Hex(), 400)
	assertDoorsAgree(t, service, address, "con una metatx relayada")
}

func assertDoorsAgree(t *testing.T, service *RelaySignerService, address common.Address, caso string) {
	t.Helper()

	state, err := service.NonceOf(context.Background(), address, false)
	if err != nil {
		t.Fatalf("%s: GET /nonce fallo: %v", caso, err)
	}

	response := service.GetTransactionCount(context.Background(), json.RawMessage(`1`), address.Hex(), true)
	var decoded struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal([]byte(response.String()), &decoded); err != nil {
		t.Fatalf("%s: no se pudo leer la respuesta JSON-RPC: %v", caso, err)
	}
	porRPC, ok := new(big.Int).SetString(strings.TrimPrefix(decoded.Result, "0x"), 16)
	if !ok {
		t.Fatalf("%s: el nonce JSON-RPC no es hexadecimal: %q", caso, decoded.Result)
	}

	if porRPC.Cmp(state.Next) != 0 {
		t.Errorf("%s: las dos puertas difieren: JSON-RPC %s, GET /nonce %s", caso, porRPC, state.Next)
	}
}
