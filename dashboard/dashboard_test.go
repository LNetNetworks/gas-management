package dashboard

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LACNetNetworks/gas-relay-signer/events"
)

// El stream se prueba contra un servidor real y no con un grabador de respuestas: un grabador no
// entrega nada hasta que el manejador retorna, y este manejador no retorna hasta que el cliente
// corta.

func withBus(t *testing.T, capacity int) {
	t.Helper()
	events.Init(true, capacity)
	t.Cleanup(func() { events.Init(false, 0) })
}

func streamServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/dashboard", Page)
	mux.HandleFunc("/dashboard/stream", Stream)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func line(name string) map[string]interface{} {
	return map[string]interface{}{"event": name, "level": "info", "ts": "2026-09-15T00:00:00.000Z"}
}

// sseClient es un observador conectado al stream, leido incrementalmente.
type sseClient struct {
	cancel   context.CancelFunc
	response *http.Response
	mutex    sync.Mutex
	events   []sseEvent
	comments int
	failed   error
}

type sseEvent struct {
	id   string
	data map[string]interface{}
}

// openStream conecta un observador y empieza a leer en segundo plano.
func openStream(t *testing.T, server *httptest.Server, query string, headers map[string]string) *sseClient {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/dashboard/stream"+query, nil)
	if err != nil {
		cancel()
		t.Fatalf("no se pudo armar la peticion: %v", err)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		cancel()
		t.Fatalf("no se pudo abrir el stream: %v", err)
	}

	client := &sseClient{cancel: cancel, response: response}
	if response.StatusCode != http.StatusOK {
		return client
	}

	go func() {
		defer response.Body.Close()
		scanner := bufio.NewScanner(response.Body)
		var id string
		for scanner.Scan() {
			text := scanner.Text()
			switch {
			case strings.HasPrefix(text, ":"):
				client.mutex.Lock()
				client.comments++
				client.mutex.Unlock()
			case strings.HasPrefix(text, "id: "):
				id = strings.TrimPrefix(text, "id: ")
			case strings.HasPrefix(text, "data: "):
				var decoded map[string]interface{}
				if err := json.Unmarshal([]byte(strings.TrimPrefix(text, "data: ")), &decoded); err != nil {
					client.mutex.Lock()
					client.failed = err
					client.mutex.Unlock()
					return
				}
				client.mutex.Lock()
				client.events = append(client.events, sseEvent{id: id, data: decoded})
				client.mutex.Unlock()
				id = ""
			}
		}
	}()

	t.Cleanup(client.close)
	return client
}

func (client *sseClient) close() { client.cancel() }

func (client *sseClient) received() []sseEvent {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	return append([]sseEvent(nil), client.events...)
}

func (client *sseClient) commentCount() int {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	return client.comments
}

// relayEventIDs son los identificadores de los eventos de operacion, sin los del propio monitor.
func relayEventIDs(client *sseClient) []string {
	var ids []string
	for _, event := range client.received() {
		name, _ := event.data["event"].(string)
		if strings.HasPrefix(name, "dashboard.") || event.id == "" {
			continue
		}
		ids = append(ids, event.id)
	}
	return ids
}

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("no se cumplio a tiempo: %s", what)
}

// namesOf son los nombres de los eventos de operacion recibidos.
//
// Se excluyen los del propio monitor -el saludo y el aviso de apertura-: abrir un stream publica un
// evento en el bus, que los streams ya conectados reciben. Es el mismo comportamiento que el
// relayer de referencia, y la pagina los ignora porque no llevan metaTxId, pero un test que cuente
// eventos tiene que saberlo.
func namesOf(client *sseClient) []string {
	var names []string
	for _, event := range client.received() {
		name, _ := event.data["event"].(string)
		if strings.HasPrefix(name, "dashboard.") {
			continue
		}
		names = append(names, name)
	}
	return names
}

// ------------------------------------------------------------------ pagina

// TestPageIsServedFromTheBinary cubre la tarea 3.2.
func TestPageIsServedFromTheBinary(t *testing.T) {
	withBus(t, 50)
	server := streamServer(t)

	response, err := server.Client().Get(server.URL + "/dashboard")
	if err != nil {
		t.Fatalf("no se pudo pedir la pagina: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /dashboard -> %d", response.StatusCode)
	}
	if tipo := response.Header.Get("Content-Type"); !strings.HasPrefix(tipo, "text/html") {
		t.Errorf("Content-Type = %q, se esperaba HTML", tipo)
	}
	// La pagina esta embebida: no hay archivo que leer, asi que basta con que el contenido este.
	if !strings.Contains(page, "<!doctype html>") || !strings.Contains(page, "EventSource") {
		t.Errorf("la pagina embebida no parece la del monitor")
	}
	if !strings.Contains(page, "/dashboard/stream") {
		t.Errorf("la pagina no apunta al stream de este servicio")
	}
}

// ------------------------------------------------------------------ stream

// TestStreamDeliversNewEvents cubre la tarea 4.1.
func TestStreamDeliversNewEvents(t *testing.T) {
	withBus(t, 50)
	server := streamServer(t)
	client := openStream(t, server, "", nil)

	waitFor(t, "llega el saludo", func() bool { return len(client.received()) >= 1 })

	events.Publish(line("relay.received"))
	events.Publish(line("relay.sent"))

	waitFor(t, "llegan los dos eventos", func() bool { return len(namesOf(client)) == 2 })

	recibidos := client.received()
	if recibidos[0].data["event"] != "dashboard.hello" {
		t.Errorf("el primer mensaje deberia ser el saludo, fue %v", recibidos[0].data["event"])
	}
	// Cada evento va identificado por su numero de secuencia, que es lo que el navegador devuelve.
	for _, event := range recibidos[1:] {
		if event.id == "" {
			t.Errorf("un evento llego sin identificador: %v", event.data)
		}
		if event.data["seq"] == nil {
			t.Errorf("un evento llego sin seq en su cuerpo: %v", event.data)
		}
	}
}

// TestStreamHeaders cubre parte de la tarea 4.5: las cabeceras que hacen que el flujo atraviese un
// intermediario con almacenamiento.
func TestStreamHeaders(t *testing.T) {
	withBus(t, 50)
	server := streamServer(t)
	client := openStream(t, server, "", nil)

	headers := client.response.Header
	if tipo := headers.Get("Content-Type"); !strings.HasPrefix(tipo, "text/event-stream") {
		t.Errorf("Content-Type = %q", tipo)
	}
	if cache := headers.Get("Cache-Control"); !strings.Contains(cache, "no-cache") || !strings.Contains(cache, "no-transform") {
		t.Errorf("Cache-Control = %q: tiene que pedir no almacenar ni transformar", cache)
	}
	if headers.Get("X-Accel-Buffering") != "no" {
		t.Errorf("falta la cabecera que evita el almacenamiento intermedio: un proxy retendria el flujo")
	}
}

// TestStreamHeartbeat cubre la otra parte de 4.5: un periodo sin eventos no cierra el stream.
func TestStreamHeartbeat(t *testing.T) {
	previous := heartbeat
	heartbeat = 20 * time.Millisecond
	t.Cleanup(func() { heartbeat = previous })

	withBus(t, 50)
	server := streamServer(t)
	client := openStream(t, server, "", nil)

	waitFor(t, "llegan varios latidos sin publicar nada", func() bool { return client.commentCount() >= 3 })

	// El stream sigue vivo: un evento publicado despues de los latidos llega igual.
	events.Publish(line("relay.received"))
	waitFor(t, "el stream sigue entregando", func() bool { return len(namesOf(client)) == 1 })
}

// TestStreamResumesFromQuery cubre la tarea 4.2 por el parametro de la peticion.
func TestStreamResumesFromQuery(t *testing.T) {
	withBus(t, 50)
	for i := 0; i < 5; i++ {
		events.Publish(line(fmt.Sprintf("relay.%d", i)))
	}

	server := streamServer(t)
	client := openStream(t, server, "?after=3", nil)

	waitFor(t, "llega lo posterior al seq 3", func() bool { return len(namesOf(client)) == 2 })

	if ids := relayEventIDs(client); len(ids) != 2 || ids[0] != "4" || ids[1] != "5" {
		t.Errorf("se reanudo desde los identificadores %v, se esperaban 4 y 5", ids)
	}
}

// TestStreamResumesFromLastEventID cubre la tarea 4.2 por el reintento automatico del navegador.
func TestStreamResumesFromLastEventID(t *testing.T) {
	withBus(t, 50)
	for i := 0; i < 5; i++ {
		events.Publish(line(fmt.Sprintf("relay.%d", i)))
	}

	server := streamServer(t)
	client := openStream(t, server, "", map[string]string{"Last-Event-ID": "4"})

	waitFor(t, "llega lo posterior al seq 4", func() bool { return len(namesOf(client)) == 1 })
	if ids := relayEventIDs(client); len(ids) != 1 || ids[0] != "5" {
		t.Errorf("se reanudo desde %v, se esperaba 5", ids)
	}
}

// TestStreamWithoutStartingPointDeliversEverything cubre parte de la tarea 4.4.
func TestStreamWithoutStartingPointDeliversEverything(t *testing.T) {
	withBus(t, 50)
	for i := 0; i < 4; i++ {
		events.Publish(line("relay.received"))
	}

	server := streamServer(t)
	client := openStream(t, server, "", nil)

	waitFor(t, "llega todo lo retenido", func() bool { return len(namesOf(client)) == 4 })
}

// TestStreamFromADiscardedPoint cubre la otra parte de 4.4: reanudar desde un punto que el bus ya
// no conserva devuelve lo que queda, sin error, y el salto de numeracion deja visible el hueco.
func TestStreamFromADiscardedPoint(t *testing.T) {
	withBus(t, 3)
	for i := 0; i < 10; i++ {
		events.Publish(line("relay.received"))
	}

	server := streamServer(t)
	client := openStream(t, server, "?after=1", nil)

	if client.response.StatusCode != http.StatusOK {
		t.Fatalf("reanudar desde un punto descartado dio %d, se esperaba una respuesta correcta",
			client.response.StatusCode)
	}
	waitFor(t, "llega lo que queda retenido", func() bool { return len(namesOf(client)) == 3 })

	// El primero que llega es el seq 8, no el 2: el salto deja visible que hubo un hueco.
	if ids := relayEventIDs(client); len(ids) == 0 || ids[0] != "8" {
		t.Errorf("el primer evento entregado tiene identificador %v, se esperaba 8", ids)
	}
}

// TestStreamDeliversAnEventPublishedWhileConnecting cubre la tarea 4.3 en el manejador: el evento
// que se publica en el instante de conectarse llega exactamente una vez.
func TestStreamDeliversAnEventPublishedWhileConnecting(t *testing.T) {
	withBus(t, 500)
	server := streamServer(t)

	const total = 120
	publicando := make(chan struct{})
	go func() {
		close(publicando)
		for i := 0; i < total; i++ {
			events.Publish(line("relay.received"))
		}
	}()
	<-publicando

	client := openStream(t, server, "?after=0", nil)
	waitFor(t, "llegan todos los eventos", func() bool { return len(namesOf(client)) >= total })

	visto := make(map[string]int)
	for _, id := range relayEventIDs(client) {
		visto[id]++
	}
	for seq := 1; seq <= total; seq++ {
		switch visto[fmt.Sprint(seq)] {
		case 1:
		case 0:
			t.Fatalf("se perdio el evento %d entre el historial y el flujo", seq)
		default:
			t.Fatalf("el evento %d llego %d veces", seq, visto[fmt.Sprint(seq)])
		}
	}
}

// ------------------------------------------------------------------ limite y liberacion

// TestStreamLimitsClients cubre la tarea 5.1.
func TestStreamLimitsClients(t *testing.T) {
	withBus(t, 50)
	server := streamServer(t)

	var conectados []*sseClient
	for i := 0; i < maxClients; i++ {
		client := openStream(t, server, "", nil)
		if client.response.StatusCode != http.StatusOK {
			t.Fatalf("el observador %d -> %d, se esperaba que entrara", i, client.response.StatusCode)
		}
		conectados = append(conectados, client)
	}
	waitFor(t, "el bus registra a todos", func() bool { return events.SubscriberCount() == maxClients })

	// El que sobra se rechaza, y no desplaza a ninguno.
	sobrante := openStream(t, server, "", nil)
	if sobrante.response.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("el observador que supera el limite -> %d, se esperaba 503", sobrante.response.StatusCode)
	}
	if events.SubscriberCount() != maxClients {
		t.Errorf("hay %d observadores, el rechazado no debe haber entrado", events.SubscriberCount())
	}

	// Y los que ya estaban siguen recibiendo.
	events.Publish(line("relay.received"))
	waitFor(t, "los conectados siguen recibiendo", func() bool { return len(namesOf(conectados[0])) == 1 })
}

// TestStreamReleasesItsSlot cubre las tareas 5.2 y 5.3: cerrar libera el lugar, y abrir y cerrar
// repetidamente no acumula observadores.
func TestStreamReleasesItsSlot(t *testing.T) {
	withBus(t, 50)
	server := streamServer(t)

	for i := 0; i < maxClients*3; i++ {
		client := openStream(t, server, "", nil)
		if client.response.StatusCode != http.StatusOK {
			t.Fatalf("en la vuelta %d el observador -> %d: se acumularon suscripciones",
				i, client.response.StatusCode)
		}
		client.close()
		waitFor(t, "se libera el lugar", func() bool { return events.SubscriberCount() == 0 })
	}

	if events.SubscriberCount() != 0 {
		t.Errorf("quedaron %d observadores tras cerrarlos todos", events.SubscriberCount())
	}
}
