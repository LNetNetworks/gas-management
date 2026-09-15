package controller

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	log "github.com/LACNetNetworks/gas-relay-signer/audit"
	"github.com/LACNetNetworks/gas-relay-signer/model"
	"github.com/LACNetNetworks/gas-relay-signer/rpc"
	"github.com/LACNetNetworks/gas-relay-signer/service"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rlp"
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
	// relay.sent corresponde a cual relay.received. Ver design.md, D8.
	ctx = log.WithMetaTxID(ctx, log.NewMetaTxID())

	log.GeneralLogger.Println("Is a rawTransaction")
	var params []string
	err := json.Unmarshal(rpcMessage.Params, &params)
	if err != nil {
		rejectMetaTx(ctx, rpcMessage.ID, w, err)
		return
	}

	// relay.received se emite ANTES de decodificar, para que una raw tx malformada deje rastro
	// con su metaTxId en lugar de desaparecer. Ver design.md, D8.
	if len(params) > 0 {
		received := map[string]interface{}{
			"rawTxHash":  service.RawTxHash(params[0]),
			"rawTxBytes": service.RawTxBytes(params[0]),
		}
		if log.ShouldLogRawTx() {
			received["rawTx"] = params[0]
		}
		log.Info(ctx, "relay.received", received)
	}

	decodeTransaction, err := service.GetTransaction(params[0][2:])
	if err != nil {
		rejectMetaTx(ctx, rpcMessage.ID, w, err)
		return
	}

	v, rInt, sInt := decodeTransaction.RawSignatureValues()
	if (v == nil) || (rInt == nil) || (sInt == nil) {
		err := errors.New("bad signature ECDSA")
		rejectMetaTx(ctx, rpcMessage.ID, w, err)
		return
	}

	// El RelayHub espera una firma pre-EIP155 (chainId=0 => v=27/28). Si el cliente
	// firmó con EIP-155, el valor se truncaría (gosec G115) y la meta-tx revertiría
	// on-chain. Lo rechazamos temprano con un mensaje claro.
	if vUint := v.Uint64(); vUint != 27 && vUint != 28 {
		err := errors.New("transaction must be signed pre-EIP155 (chainId=0, v=27 or 28)")
		rejectMetaTx(ctx, rpcMessage.ID, w, err)
		return
	}

	message, err := decodeTransaction.AsMessage(types.NewEIP155Signer(decodeTransaction.ChainId()))
	if err != nil {
		rejectMetaTx(ctx, rpcMessage.ID, w, err)
		return
	}

	if relaySignerService.Config.Security.PermissionsEnabled {
		isSenderPermitted, err := relaySignerService.VerifySender(ctx, message.From(), rpcMessage.ID)
		if err != nil {
			data := handleError(ctx, rpcMessage.ID, err)
			w.Write(data)
			return
		}
		if !isSenderPermitted {
			err := errors.New("account sender is not permitted to send transactions")
			data := handleError(ctx, rpcMessage.ID, err)
			w.Write(data)
			return
		}
	}

	var metaTxGasLimit uint64 = uint64((len(decodeTransaction.Data())*105)+300000) + decodeTransaction.Gas()

	lock.Lock()
	defer lock.Unlock()
	isCorrectGasLimit, err := relaySignerService.VerifyGasLimit(ctx, metaTxGasLimit, rpcMessage.ID)
	if err != nil {
		rejectMetaTx(ctx, rpcMessage.ID, w, err)
		return
	}
	if !isCorrectGasLimit {
		err := errors.New("transaction gas limit exceeds block gas limit")
		rejectMetaTx(ctx, rpcMessage.ID, w, err)
		return
	}

	log.GeneralLogger.Println("From:", message.From().Hex())
	if decodeTransaction.To() != nil {
		log.GeneralLogger.Println("To:", decodeTransaction.To().Hex())
	}
	log.GeneralLogger.Println("Data:", hexutil.Encode(decodeTransaction.Data()))
	log.GeneralLogger.Println("GasLimit:", decodeTransaction.Gas())
	log.GeneralLogger.Println("Nonce", decodeTransaction.Nonce())
	log.GeneralLogger.Println("GasPrice:", decodeTransaction.GasPrice())
	log.GeneralLogger.Println("Value:", decodeTransaction.Value())

	var r [32]byte
	var s [32]byte
	rBytes, _ := hex.DecodeString(fmt.Sprintf("%064x", rInt))
	sBytes, _ := hex.DecodeString(fmt.Sprintf("%064x", sInt))

	copy(r[:], rBytes)
	copy(s[:], sBytes)

	var signingDataTx *model.RawTransaction

	if decodeTransaction.To() != nil {
		signingDataTx = model.NewTransaction(decodeTransaction.Nonce(), *decodeTransaction.To(), decodeTransaction.Value(), decodeTransaction.Gas(), decodeTransaction.GasPrice(), decodeTransaction.Data())
	} else {
		signingDataTx = model.NewContractCreation(decodeTransaction.Nonce(), decodeTransaction.Value(), decodeTransaction.Gas(), decodeTransaction.GasPrice(), decodeTransaction.Data())
	}

	signingDataRLP, err := rlp.EncodeToBytes(signingDataTx.Data)
	if err != nil {
		err := errors.New("internal error")
		rejectMetaTx(ctx, rpcMessage.ID, w, err)
		return
	}

	response := relaySignerService.SendMetatransaction(ctx, rpcMessage.ID, decodeTransaction.To(), metaTxGasLimit, signingDataRLP, uint8(v.Uint64()), r, s, message.From().Hex(), decodeTransaction.Nonce())
	data, err := json.Marshal(response)
	if err != nil {
		log.GeneralLogger.Println(err)
		err := errors.New("internal error")
		rejectMetaTx(ctx, rpcMessage.ID, w, err)
		return
	}
	w.Write(data)
}
