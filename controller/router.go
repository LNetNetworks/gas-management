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
