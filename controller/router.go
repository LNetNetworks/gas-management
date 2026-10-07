package controller

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/LACNetNetworks/gas-relay-signer/dashboard"
)

// Ruteo por path del servicio.
//
// `POST /` sigue siendo el catch-all JSON-RPC y atiende cualquier path sin manejador propio: un
// cliente que apunta el RelaySigner como si fuera el RPC del nodo no nota ninguna diferencia.
//
// Las rutas se registran por su PATH y el metodo se comprueba dentro del manejador, en lugar de
// usar los patrones con metodo que acepta ServeMux desde Go 1.22. Con un catch-all esos patrones
// no sirven: ServeMux devuelve 405 solo cuando ninguna otra ruta coincide, y como "/" coincide con
// todo, un `POST /info` o un `GET /relay` se los lleva el camino JSON-RPC y el cliente recibe un
// 200 que no pidio. Ver design.md, D1.

// Routes registra las rutas del servicio en el mux indicado.
func (controller *RelayController) Routes(mux *http.ServeMux) {
	mux.HandleFunc("/", controller.SignTransaction)
	mux.HandleFunc("/info", onlyMethod(http.MethodGet, controller.Info))
	mux.HandleFunc("/relay", onlyMethod(http.MethodPost, controller.Relay))
	// Subarbol y no comodin: `{address}` no coincide con `/nonce/` a secas, asi que una peticion
	// sin direccion caeria al catch-all en lugar de dar el 400 que corresponde. Ver D2.
	mux.HandleFunc("/nonce/", onlyMethod(http.MethodGet, controller.Nonce))

	// Las rutas del monitor NO se registran con el dashboard apagado, en lugar de registrarse y
	// responder que estan deshabilitadas: asi no queda una superficie que informe que el monitor
	// existe, y esos paths se comportan como cualquier otro sin manejador propio. Ver D7 de
	// 03-add-relay-dashboard.
	if controller.Config != nil && controller.Config.Dashboard.Enabled {
		mux.HandleFunc("/dashboard", onlyMethod(http.MethodGet, dashboard.Page))
		mux.HandleFunc("/dashboard/stream", onlyMethod(http.MethodGet, dashboard.Stream))
	}
}

// onlyMethod deja pasar un solo metodo y responde 405 para el resto, sin que la peticion caiga al
// camino JSON-RPC.
func onlyMethod(method string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			w.Header().Set("Allow", method)
			writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{
				"error": "metodo no permitido: " + r.URL.Path + " solo acepta " + method,
			})
			return
		}
		next(w, r)
	}
}

// pathParam devuelve lo que sigue al prefijo de una ruta de subarbol.
func pathParam(r *http.Request, prefix string) string {
	return strings.Trim(strings.TrimPrefix(r.URL.Path, prefix), "/")
}

// writeJSON responde un objeto JSON con el codigo indicado.
//
// Un fallo al serializar no puede dejar al cliente esperando: se responde un error generico, que es
// preferible a una respuesta vacia con codigo de exito.
func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	encoded, err := json.Marshal(body)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"no se pudo serializar la respuesta"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(encoded)
}

// writeError responde un error REST con su motivo y, cuando lo hay, su codigo y su detalle.
func writeError(w http.ResponseWriter, status int, message string, code string, details interface{}) {
	body := map[string]interface{}{"error": message}
	if code != "" {
		body["code"] = code
	}
	if details != nil {
		body["details"] = details
	}
	writeJSON(w, status, body)
}

// Handler es el manejador completo del servicio: el ruteo con las cabeceras de intercambio entre
// origenes aplicadas encima.
//
// Las cabeceras van como envoltura y no dentro de cada manejador a proposito: si fueran por ruta,
// agregar una ruta nueva significaria acordarse de agregarlas, y olvidarse no produce ningun error
// visible hasta que un navegador falla. Ver design.md de 03-add-relay-dashboard, D6.
func (controller *RelayController) Handler() http.Handler {
	mux := http.NewServeMux()
	controller.Routes(mux)

	var origins []string
	if controller.Config != nil {
		origins = controller.Config.CORS.AllowedOrigins
	}
	return withCORS(mux, origins)
}

// Cabeceras que se anuncian como aceptadas cuando el navegador consulta por adelantado.
const (
	corsAllowedMethods = "GET, POST, OPTIONS"
	corsAllowedHeaders = "Content-Type, Last-Event-ID"
	corsMaxAge         = "600"
)

// withCORS agrega las cabeceras de intercambio entre origenes.
//
// Sin origenes configurados no agrega ninguna: el servicio se comporta exactamente como antes de
// esta capacidad. Es deliberado que el default sea cerrado, porque el servicio relaya con el cupo
// de gas del nodo y no pide autenticacion.
func withCORS(next http.Handler, origins []string) http.Handler {
	if len(origins) == 0 {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if allowed := matchOrigin(origins, origin); allowed != "" {
			w.Header().Set("Access-Control-Allow-Origin", allowed)
			// Sin esto, una cache intermedia podria servirle a un origen la respuesta que se
			// autorizo para otro.
			w.Header().Add("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", corsAllowedMethods)
			w.Header().Set("Access-Control-Allow-Headers", corsAllowedHeaders)
			w.Header().Set("Access-Control-Max-Age", corsMaxAge)
		}

		// La consulta previa del navegador se responde ACA y no llega al manejador. Sin esto, una
		// consulta previa sobre `POST /relay` terminaria relayando: el navegador pregunta antes de
		// mandar, y la pregunta no es la metatx.
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// Un origen no autorizado igual se procesa: la restriccion la aplica el navegador al leer
		// (o no leer) las cabeceras, no el servicio rechazando la peticion.
		next.ServeHTTP(w, r)
	})
}

// matchOrigin devuelve el valor a autorizar, o "" si ese origen no esta configurado.
func matchOrigin(origins []string, origin string) string {
	if origin == "" {
		return ""
	}
	for _, allowed := range origins {
		if allowed == "*" {
			return "*"
		}
		if strings.EqualFold(allowed, origin) {
			// Se devuelve el origen pedido y no el configurado: es lo que el navegador compara.
			return origin
		}
	}
	return ""
}
