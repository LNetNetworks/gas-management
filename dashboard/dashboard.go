// Package dashboard es el monitor en vivo: una pagina que se sirve desde el binario y un flujo de
// eventos enviados por el servidor que empuja el mismo log estructurado que sale por la salida
// estandar.
//
// Por que eventos enviados por el servidor y no WebSocket: el flujo es de una sola direccion -el
// navegador no manda nada-, va sobre HTTP comun porque el puerto ya esta abierto, el navegador
// reconecta solo, y la reanudacion por identificador de evento es exactamente lo que el `seq` del
// bus necesita. Ademas el WebSocket de este proceso ya se usa hacia el nodo, para resetear el cupo
// de gas por bloque.
//
// Lo que se ve es lo que vio ESTA instancia: el bus vive en memoria, igual que el cache de nonces.
//
// El monitor no autentica, como el resto del servicio: publica quien mando cada metatx, con que
// nonce y contra que contrato. Por eso esta apagado por defecto.
package dashboard

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	log "github.com/LACNetNetworks/gas-relay-signer/audit"
	"github.com/LACNetNetworks/gas-relay-signer/events"
)

// page viaja DENTRO del binario y no como archivo al lado.
//
// Es una decision de operacion: un despliegue aca es reemplazar un ejecutable, y una pagina servida
// desde el sistema de archivos se rompe en cuanto alguien copia solo el binario. El fallo aparecia
// recien al abrir el monitor, que es justo cuando algo ya anda mal.
//
//go:embed index.html
var page string

const (
	// maxClients es el techo de pestanas abiertas a la vez. Cada una es una conexion sostenida por
	// el proceso: sin tope, una pagina que reconecta en bucle agota el servicio que observa.
	maxClients = 8

	// retryHint es lo que se le pide al navegador que espere antes de reconectar.
	retryHint = 2000
)

// heartbeat mantiene viva la conexion a traves de intermediarios que cortan por inactividad. Es
// variable y no constante para que un test pueda acortarlo: comprobar el latido con el valor real
// costaria quince segundos por test.
var heartbeat = 15 * time.Second

// Page atiende la pagina del monitor.
func Page(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = fmt.Fprint(w, page)
}

// Stream atiende el flujo de eventos en vivo.
//
// Solo LEE el bus: no toca el camino de la metatx. Un observador lento ya lo cubre el descarte del
// bus, asi que aca no hace falta ninguna proteccion extra.
func Stream(w http.ResponseWriter, r *http.Request) {
	ctx := log.WithRequestID(r.Context(), log.NewRequestID())

	// El limite se comprueba contra el bus y no contra un contador propio: dos cuentas separadas
	// pueden discrepar, y el sintoma seria un monitor que dice estar lleno sin estarlo. Ver D5.
	if events.SubscriberCount() >= maxClients {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = fmt.Fprintf(w, `{"error":"el monitor ya tiene %d flujos abiertos"}`, maxClients)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprint(w, `{"error":"esta conexion no permite enviar eventos en vivo"}`)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	// Sin esto, un intermediario que almacena respuestas retiene el flujo y a la pagina no llega
	// nada, aunque todo lo demas funcione. Es el fallo mas confuso de diagnosticar: el servicio se
	// ve sano y la pagina esta vacia.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "retry: %d\n\n", retryHint)
	flusher.Flush()

	// Un canal propio entre la entrega del bus y esta goroutine: el bus llama a `notify` desde la
	// goroutine del suscriptor, y escribir la respuesta HTTP tiene que hacerse desde una sola.
	stream := make(chan events.Event, 64)

	// Reanudar y suscribirse es UNA operacion: hacerlo en dos pasos deja una ventana por la que un
	// evento publicado en ese instante no sale en el historial ni llega por el flujo. Ver D1.
	retained, cancel := events.ReplayAndSubscribe(startingPoint(r), func(event events.Event) {
		select {
		case stream <- event:
		default:
			// Este observador se quedo atras. Se descarta para el, como hace el bus: el `seq`
			// monotonico deja el hueco visible y la pagina lo cierra al reconectar.
		}
	})
	defer cancel()

	send(w, flusher, map[string]interface{}{
		"event":      "dashboard.hello",
		"instanceId": log.InstanceID(),
	}, 0)
	for _, event := range retained {
		send(w, flusher, event.Line, event.Seq)
	}

	log.Debug(ctx, "dashboard.stream_open", map[string]interface{}{
		"after":   startingPoint(r),
		"clients": events.SubscriberCount(),
	})

	beat := time.NewTicker(heartbeat)
	defer beat.Stop()

	for {
		select {
		case event := <-stream:
			send(w, flusher, event.Line, event.Seq)
		case <-beat.C:
			// Comentario del protocolo: no es un evento, solo mantiene viva la conexion.
			fmt.Fprint(w, ": hb\n\n")
			flusher.Flush()
		case <-ctx.Done():
			// El cliente corto. Al retornar, el `defer cancel()` libera la suscripcion y el lugar:
			// sin eso, abrir y cerrar el monitor llenaria el cupo con observadores que ya no
			// existen.
			return
		}
	}
}

// startingPoint es desde donde el observador quiere continuar: lo que pide en la peticion, o lo que
// manda el reintento automatico del navegador. Sin ninguno de los dos, desde el principio.
func startingPoint(r *http.Request) uint64 {
	raw := r.URL.Query().Get("after")
	if raw == "" {
		raw = r.Header.Get("Last-Event-ID")
	}
	after, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0
	}
	return after
}

// send escribe un evento en el flujo. El identificador es el `seq`, que es lo que el navegador
// devuelve al reconectar.
func send(w http.ResponseWriter, flusher http.Flusher, line map[string]interface{}, seq uint64) {
	encoded, err := json.Marshal(line)
	if err != nil {
		// Un evento que no se puede serializar no corta el flujo: se omite y se sigue.
		return
	}
	if seq > 0 {
		fmt.Fprintf(w, "id: %d\n", seq)
	}
	fmt.Fprintf(w, "data: %s\n\n", encoded)
	flusher.Flush()
}
