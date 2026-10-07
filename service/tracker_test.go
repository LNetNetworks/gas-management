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

// Una rafaga retenida y resuelta no deja rastro en el registro de retenidas: ni entradas sueltas ni
// una lista vacia por usuario. Sin esto el mapa crece con cada usuario que alguna vez espero turno.
func TestHeldRegistryLeavesNothingBehind(t *testing.T) {
	service := trackerService(3000)
	key := senderKey("0xa0f03c489a1bcd53883289d3c476100220b21b0b")

	primera := service.hold(key, 11)
	segunda := service.hold(key, 12)

	service.sendersLock.Lock()
	retenidas := service.waitingOf(key)
	service.sendersLock.Unlock()
	if retenidas != 2 {
		t.Fatalf("deberia haber 2 retenidas, hubo %d", retenidas)
	}

	service.unhold(key, primera)
	service.unhold(key, segunda)

	service.sendersLock.Lock()
	defer service.sendersLock.Unlock()
	if restantes := service.waitingOf(key); restantes != 0 {
		t.Errorf("no deberia quedar ninguna retenida, quedaron %d", restantes)
	}
	if _, existe := service.held[key]; existe {
		t.Error("el usuario no deberia seguir en el registro de retenidas: fuga por usuario")
	}
}

// unhold es idempotente: una desalojada ya salio del registro al marcarse, y su goroutine llama
// igual a unhold al despertar. El segundo llamado no puede descontar el lugar de otra.
func TestUnholdIsIdempotent(t *testing.T) {
	service := trackerService(3000)
	key := senderKey("0xa0f03c489a1bcd53883289d3c476100220b21b0b")

	primera := service.hold(key, 11)
	segunda := service.hold(key, 12)

	service.unhold(key, primera)
	service.unhold(key, primera)

	service.sendersLock.Lock()
	defer service.sendersLock.Unlock()
	if restantes := service.waitingOf(key); restantes != 1 {
		t.Errorf("solo deberia quedar la segunda retenida, quedaron %d", restantes)
	}
	if service.held[key][0] != segunda {
		t.Error("la que quedo tiene que ser la segunda: un unhold repetido no puede sacar a otra")
	}
}

// La retenida de nonce mas alto es la que sobra cuando el cupo se llena. Ante un empate gana la que
// llego despues: son duplicados y el hub solo aceptara una.
func TestHighestHeld(t *testing.T) {
	key := senderKey("0xa0f03c489a1bcd53883289d3c476100220b21b0b")

	casos := []struct {
		nombre   string
		nonces   []uint64
		esperado int // indice de la retenida esperada, -1 si no hay ninguna
	}{
		{"sin retenidas", nil, -1},
		{"una sola", []uint64{11}, 0},
		{"varias, la mas alta llego primero", []uint64{14, 11, 12}, 0},
		{"varias, la mas alta llego ultima", []uint64{11, 12, 14}, 2},
		{"empate en el maximo: gana la que llego despues", []uint64{12, 14, 11, 14}, 3},
		{"todas iguales: gana la ultima", []uint64{11, 11, 11}, 2},
	}

	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			service := trackerService(3000)
			var retenidas []*heldMetaTx
			for _, nonce := range caso.nonces {
				retenidas = append(retenidas, service.hold(key, nonce))
			}

			service.sendersLock.Lock()
			defer service.sendersLock.Unlock()
			highest := service.highestHeldLocked(key)

			if caso.esperado < 0 {
				if highest != nil {
					t.Fatalf("sin retenidas no deberia haber maximo, hubo la de nonce %d", highest.nonce)
				}
				return
			}
			if highest == nil {
				t.Fatal("con retenidas tiene que haber un maximo")
			}
			if highest != retenidas[caso.esperado] {
				t.Errorf("la mas alta deberia ser la de indice %d (nonce %d), fue la de nonce %d",
					caso.esperado, caso.nonces[caso.esperado], highest.nonce)
			}
		})
	}
}

// El lugar de una desalojada se descuenta al MARCARLA, no cuando su goroutine despierte: si se
// esperara a eso, el lugar liberado no estaria disponible enseguida y el desalojo no serviria.
func TestEvictFreesTheSlotAtMarkTime(t *testing.T) {
	service := trackerService(3000)
	key := senderKey("0xa0f03c489a1bcd53883289d3c476100220b21b0b")

	baja := service.hold(key, 11)
	alta := service.hold(key, 12)

	service.sendersLock.Lock()
	wake := service.waitTurnLocked(alta)
	service.evictHeldLocked(key, alta, service.inflightLocked(key))
	retenidas := service.waitingOf(key)
	service.sendersLock.Unlock()

	// Todavia nadie desperto y el lugar ya esta libre.
	if retenidas != 1 {
		t.Errorf("el lugar deberia descontarse al marcar: quedaban %d retenidas", retenidas)
	}
	if !alta.evicted {
		t.Error("la desalojada tiene que quedar marcada para que su goroutine sepa por que desperto")
	}

	select {
	case <-wake:
	case <-time.After(time.Second):
		t.Error("desalojar tiene que despertar a la desalojada")
	}

	// La goroutine desalojada llama igual a unhold al despertar, y eso no puede sacar a la otra.
	service.unhold(key, alta)
	service.sendersLock.Lock()
	defer service.sendersLock.Unlock()
	if restantes := service.waitingOf(key); restantes != 1 {
		t.Errorf("el unhold de la desalojada no puede descontar dos veces, quedaron %d", restantes)
	}
	if service.held[key][0] != baja {
		t.Error("la que sobrevive tiene que ser la de nonce mas bajo")
	}
}

// Una goroutine desalojada llama igual a unhold al despertar, concurrentemente con otras que entran
// y salen del registro. El cupo del usuario nunca puede quedar por debajo del real ni bajar de cero.
func TestEvictedDoesNotDoubleDiscount(t *testing.T) {
	service := trackerService(3000)
	key := senderKey("0xa0f03c489a1bcd53883289d3c476100220b21b0b")

	const retenidas = 32
	entries := make([]*heldMetaTx, retenidas)
	for index := range entries {
		entries[index] = service.hold(key, uint64(index))
	}

	var waiters sync.WaitGroup
	for _, entry := range entries {
		waiters.Add(2)
		// El desalojo, que descuenta al marcar.
		go func(entry *heldMetaTx) {
			defer waiters.Done()
			service.sendersLock.Lock()
			service.evictHeldLocked(key, entry, service.inflightLocked(key))
			service.sendersLock.Unlock()
		}(entry)
		// La goroutine desalojada, que al despertar llama igual a unhold.
		go func(entry *heldMetaTx) {
			defer waiters.Done()
			service.unhold(key, entry)
		}(entry)
	}
	waiters.Wait()

	service.sendersLock.Lock()
	defer service.sendersLock.Unlock()
	if restantes := service.waitingOf(key); restantes != 0 {
		t.Errorf("no deberia quedar ninguna retenida, quedaron %d", restantes)
	}
	if _, existe := service.held[key]; existe {
		t.Error("el usuario no deberia seguir en el registro")
	}
}
