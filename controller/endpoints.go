package controller

import (
	"math/big"
	"net/http"

	log "github.com/LACNetNetworks/gas-relay-signer/audit"
	"github.com/ethereum/go-ethereum/common"
)

// Info atiende `GET /info`: que direcciones esta usando este nodo, de donde salio cada una, y con
// que parametros esta operando.
//
// Nunca falla por una consulta a la cadena: el campo que no se pudo obtener va sin valor y el resto
// llega igual. Es la ruta a la que se acude cuando algo anda mal, asi que no puede ser la primera
// en caerse. Ver design.md, D6.
func (controller *RelayController) Info(w http.ResponseWriter, r *http.Request) {
	ctx := log.WithRequestID(r.Context(), log.NewRequestID())
	writeJSON(w, http.StatusOK, controller.RelaySignerService.Info(ctx))
}

// nonceResponse es lo que devuelve `GET /nonce/{address}`.
//
// Los nonces van en decimal y en hexadecimal porque quien firma los necesita en una forma y quien
// depura en la otra. Como texto y no como numero: es el mismo criterio que el resto de los valores
// de cadena, y lo que espera un cliente escrito contra el relayer de referencia.
type nonceResponse struct {
	Address      string `json:"address"`
	Nonce        string `json:"nonce"`
	NonceHex     string `json:"nonceHex"`
	NextNonce    string `json:"nextNonce"`
	NextNonceHex string `json:"nextNonceHex"`
	Pending      uint64 `json:"pending"`
}

// Nonce atiende `GET /nonce/{address}`: el nonce que tiene ese usuario en el RelayHub y el que hay
// que usar para firmar la proxima metatx.
func (controller *RelayController) Nonce(w http.ResponseWriter, r *http.Request) {
	ctx := log.WithRequestID(r.Context(), log.NewRequestID())

	address := pathParam(r, "/nonce/")
	if address == "" {
		writeError(w, http.StatusBadRequest, "se esperaba /nonce/{address}", "", nil)
		return
	}
	// La direccion se valida antes de consultar la cadena: una peticion mal formada no tiene por
	// que costar una llamada al nodo.
	if !common.IsHexAddress(address) {
		writeError(w, http.StatusBadRequest, "direccion invalida: "+address, "", nil)
		return
	}

	state, err := controller.RelaySignerService.NonceOf(ctx, common.HexToAddress(address), peekRequested(r))
	if err != nil {
		log.Warn(ctx, "nonce.failed", merge(map[string]interface{}{"address": address}, log.ErrorFields(err)))
		writeError(w, http.StatusBadRequest, err.Error(), "", nil)
		return
	}

	writeJSON(w, http.StatusOK, nonceResponse{
		// La direccion se devuelve normalizada: la misma direccion escrita de dos formas no puede
		// producir dos respuestas distintas.
		Address:      state.Address.Hex(),
		Nonce:        state.OnChain.String(),
		NonceHex:     hexOf(state.OnChain),
		NextNonce:    state.Next.String(),
		NextNonceHex: hexOf(state.Next),
		Pending:      state.Pending,
	})
}

// peekRequested indica si se pidio consultar sin reservar. Un valor que no se reconoce se trata
// como no pedido.
func peekRequested(r *http.Request) bool {
	peek := r.URL.Query().Get("peek")
	return peek == "true" || peek == "1"
}

func hexOf(value *big.Int) string {
	return "0x" + value.Text(16)
}

func merge(base, extra map[string]interface{}) map[string]interface{} {
	for key, value := range extra {
		base[key] = value
	}
	return base
}
