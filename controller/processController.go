package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	log "github.com/LACNetNetworks/gas-relay-signer/audit"
	"github.com/LACNetNetworks/gas-relay-signer/rpc"
	"github.com/LACNetNetworks/gas-relay-signer/service"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
)

const PENDING = "PENDING"
const LATEST = "LATEST"

func processGetTransactionReceipt(ctx context.Context, relaySignerService *service.RelaySignerService, rpcMessage rpc.JsonrpcMessage, w http.ResponseWriter) {
	log.GeneralLogger.Println("Is getTransactionReceipt")
	var params []string
	err := json.Unmarshal(rpcMessage.Params, &params)
	if err != nil {
		log.GeneralLogger.Println(err)
		err := errors.New("internal error")
		data := handleError(ctx, rpcMessage.ID, err)
		w.Write(data)
		return
	}
	response := relaySignerService.GetTransactionReceipt(ctx, rpcMessage.ID, params[0][2:])
	data, err := json.Marshal(response)
	if err != nil {
		log.GeneralLogger.Println(err)
		err := errors.New("internal error")
		data := handleError(ctx, rpcMessage.ID, err)
		w.Write(data)
		return
	}
	w.Write(data)
}

func processTransactionCount(ctx context.Context, relaySignerService *service.RelaySignerService, rpcMessage rpc.JsonrpcMessage, w http.ResponseWriter) {
	log.GeneralLogger.Println("Is getTransactionCount")
	var params []string
	err := json.Unmarshal(rpcMessage.Params, &params)
	if err != nil {
		log.GeneralLogger.Println(err)
		err := errors.New("internal error")
		data := handleError(ctx, rpcMessage.ID, err)
		w.Write(data)
		return
	}

	var response *rpc.JsonrpcMessage

	if len(params) > 1 {
		if strings.ToUpper(params[1]) == PENDING {
			response = relaySignerService.GetTransactionCount(ctx, rpcMessage.ID, params[0], true)
		} else if strings.ToUpper(params[1]) == LATEST {
			response = relaySignerService.GetTransactionCount(ctx, rpcMessage.ID, params[0], false)
		} else {
			err := errors.New("parameter not defined, only pending or latest are allowed")
			data := handleError(ctx, rpcMessage.ID, err)
			w.Write(data)
		}
	} else {
		response = relaySignerService.GetTransactionCount(ctx, rpcMessage.ID, params[0], false)
	}

	data, err := json.Marshal(response)
	if err != nil {
		log.GeneralLogger.Println(err)
		err := errors.New("internal error")
		data := handleError(ctx, rpcMessage.ID, err)
		w.Write(data)
		return
	}
	w.Write(data)
}

func processGetMetaTxResult(ctx context.Context, relaySignerService *service.RelaySignerService, rpcMessage rpc.JsonrpcMessage, w http.ResponseWriter) {
	log.GeneralLogger.Println("Is getMetaTxResult")
	var params []string
	err := json.Unmarshal(rpcMessage.Params, &params)
	if err != nil || len(params) == 0 {
		data := handleError(ctx, rpcMessage.ID, errors.New("invalid params: expected [txHash]"))
		w.Write(data)
		return
	}
	response := relaySignerService.GetMetaTxResult(ctx, rpcMessage.ID, params[0][2:])
	data, err := json.Marshal(response)
	if err != nil {
		log.GeneralLogger.Println(err)
		data := handleError(ctx, rpcMessage.ID, errors.New("internal error"))
		w.Write(data)
		return
	}
	w.Write(data)
}

// decodedFields arma los campos de relay.decoded.
//
// Todo campo del contrato se emite siempre, incluso cuando no hay valor aplicable: la vista los
// lee por nombre y omitirlos la dejaria sin poder distinguir "no se pudo" de "no se emitio".
// Los cuatro que salen del sufijo del modelo de gas quedan sin valor si el sufijo no esta, y eso
// NO rechaza la metatx: se decodifica para registrar, nunca para validar. Ver design.md, D13.
func decodedFields(tx *types.Transaction, from common.Address, metaTxGasLimit uint64) map[string]interface{} {
	fields := map[string]interface{}{
		"from":             from.Hex(),
		"to":               nil,
		"isDeploy":         tx.To() == nil,
		"nonce":            tx.Nonce(),
		"userGasLimit":     tx.Gas(),
		"metaTxGasLimit":   metaTxGasLimit,
		"dataBytes":        len(tx.Data()),
		"nodeAddress":      nil,
		"expiration":       nil,
		"expiresInSeconds": nil,
		"selector":         nil,
	}
	if to := tx.To(); to != nil {
		fields["to"] = to.Hex()
	}

	gasModel := service.DecodeGasModelSuffix(tx.Data())
	if !gasModel.Decoded {
		return fields
	}
	fields["nodeAddress"] = gasModel.NodeAddress
	if gasModel.Selector != "" {
		fields["selector"] = gasModel.Selector
	}
	if expiration, ok := gasModel.ExpirationSeconds(); ok {
		fields["expiration"] = expiration
		fields["expiresInSeconds"] = int64(expiration) - time.Now().Unix()
	}
	return fields
}

// rejectMetaTx registra el rechazo de una metatx y responde el error JSON-RPC.
//
// Es el unico punto por el que sale un rechazo del camino de relay: el evento y la respuesta se
// arman del MISMO error, asi que no pueden indicar motivos distintos. `error` va siempre porque es
// el campo con el que la vista muestra el motivo; `code` y `errorType` solo cuando el error los
// trae. Ver design.md, D12.
func rejectMetaTx(ctx context.Context, id json.RawMessage, w http.ResponseWriter, err error) {
	log.Warn(ctx, "relay.rejected", log.ErrorFields(err))
	w.Write(handleError(ctx, id, err))
}

func processRawTransaction(ctx context.Context, relaySignerService *service.RelaySignerService, rpcMessage rpc.JsonrpcMessage, w http.ResponseWriter) {
	// El metaTxId se genera al entrar al camino de relay, no en el handler: una peticion puede
	// traer mas de una metatx, y sin un id propio por metatx no habria forma de saber cual
	// relay.sent corresponde a cual relay.received. Ver design.md de 01, D8.
	ctx = log.WithMetaTxID(ctx, log.NewMetaTxID())

	log.GeneralLogger.Println("Is a rawTransaction")
	var params []string
	err := json.Unmarshal(rpcMessage.Params, &params)
	if err != nil {
		rejectMetaTx(ctx, rpcMessage.ID, w, err)
		return
	}
	if len(params) == 0 {
		rejectMetaTx(ctx, rpcMessage.ID, w, errors.New("invalid params: expected [rawTx]"))
		return
	}

	// Decodificar y validar es lo mismo para las dos puertas: lo comparte `POST /relay`, asi que no
	// puede haber dos criterios sobre que metatx es aceptable. Ver design.md, D4.
	prepared, err := relaySignerService.PrepareMetaTx(ctx, params[0])
	if err != nil {
		rejectMetaTx(ctx, rpcMessage.ID, w, err)
		return
	}

	logMetaTx(prepared)

	// La reserva del cupo de gas y el envio son atomicos entre si, y el lock vive del lado del
	// servicio: quien espere un receipt lo hace despues de soltarlo. Ver D3.
	hash, err := relaySignerService.ReserveGasAndSend(ctx, prepared)
	if err != nil {
		rejectMetaTx(ctx, rpcMessage.ID, w, err)
		return
	}

	response := new(rpc.JsonrpcMessage)
	response.ID = rpcMessage.ID
	data, err := json.Marshal(response.Response(&hash))
	if err != nil {
		log.GeneralLogger.Println(err)
		rejectMetaTx(ctx, rpcMessage.ID, w, errors.New("internal error"))
		return
	}
	w.Write(data)
}

// logMetaTx conserva, tal cual, las entradas que el log de texto viene emitiendo por cada metatx.
// Hay operadores que las parsean.
func logMetaTx(prepared *service.PreparedMetaTx) {
	tx := prepared.Transaction
	log.GeneralLogger.Println("From:", prepared.From.Hex())
	if tx.To() != nil {
		log.GeneralLogger.Println("To:", tx.To().Hex())
	}
	log.GeneralLogger.Println("Data:", hexutil.Encode(tx.Data()))
	log.GeneralLogger.Println("GasLimit:", tx.Gas())
	log.GeneralLogger.Println("Nonce", tx.Nonce())
	log.GeneralLogger.Println("GasPrice:", tx.GasPrice())
	log.GeneralLogger.Println("Value:", tx.Value())
}
