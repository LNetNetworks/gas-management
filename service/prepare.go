package service

import (
	"context"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	log "github.com/LACNetNetworks/gas-relay-signer/audit"
	"github.com/LACNetNetworks/gas-relay-signer/errors"
	"github.com/LACNetNetworks/gas-relay-signer/model"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rlp"
)

// El camino que va de una raw tx a una metatx lista para enviar, compartido por las dos puertas.
//
// `POST /` y `POST /relay` tienen que aceptar y rechazar exactamente las mismas metatx: la unica
// forma de garantizarlo es que compartan el codigo. Nada de lo que hay aca escribe respuestas ni
// conoce HTTP; devuelve un resultado o un rechazo, y cada puerta decide como presentarlo.
// Ver design.md, D4.

// Codigos de rechazo del catalogo del relayer de referencia. Son los que este servicio puede
// producir; los demas corresponden a validaciones que todavia no tiene o al tracker de nonces.
// Lo que no tiene codigo propio usa RELAY_ERROR: no se inventan codigos nuevos. Ver D5.
const (
	CodeBadRawTx                 = "BAD_RAW_TX"
	CodeBadMetaTx                = "BAD_META_TX"
	CodeSenderNotPermitted       = "SENDER_NOT_PERMITTED"
	CodePermissioningUnavailable = "PERMISSIONING_UNAVAILABLE"
	CodeBadNonce                 = "BAD_NONCE"
	CodeTooManyInflight          = "TOO_MANY_INFLIGHT"
	CodeSendFailed               = "SEND_FAILED"
	CodeReceiptTimeout           = "RECEIPT_TIMEOUT"
	CodeRelayError               = "RELAY_ERROR"
)

// Rejection es un rechazo expresado en los dos vocabularios a la vez: el codigo numerico que espera
// un cliente JSON-RPC y el simbolico que espera uno REST.
//
// Los dos salen del MISMO error, asi que las dos puertas no pueden dar motivos distintos para el
// mismo rechazo.
type Rejection struct {
	cause   error
	code    string
	details map[string]interface{}
}

// Reject construye un rechazo a partir del error que lo origino.
func Reject(cause error, code string) *Rejection {
	return &Rejection{cause: cause, code: code}
}

// RejectWithDetails construye un rechazo que ademas lleva datos con los que el cliente puede
// decidir sin parsear el texto del mensaje: el nonce esperado, cuantas metatx hay en vuelo.
func RejectWithDetails(cause error, code string, details map[string]interface{}) *Rejection {
	return &Rejection{cause: cause, code: code, details: details}
}

// Details son los datos adicionales del rechazo, o nil si no tiene.
func (rejection *Rejection) Details() map[string]interface{} { return rejection.details }

// DetailsOf expone el detalle de un error cuando es un rechazo que lo trae.
func DetailsOf(err error) map[string]interface{} {
	if rejection, ok := err.(*Rejection); ok {
		return rejection.details
	}
	return nil
}

func (rejection *Rejection) Error() string { return rejection.cause.Error() }

// Unwrap expone el error original, para que el camino JSON-RPC lo presente como siempre.
func (rejection *Rejection) Unwrap() error { return rejection.cause }

// Code es el codigo simbolico del catalogo.
func (rejection *Rejection) Code() string { return rejection.code }

// ErrorCode delega en el error original, que es de donde `rpc/json.go` saca el codigo numerico de
// la respuesta JSON-RPC.
func (rejection *Rejection) ErrorCode() int {
	if coded, ok := rejection.cause.(interface{ ErrorCode() int }); ok {
		return coded.ErrorCode()
	}
	return 0
}

// CodeOf devuelve el codigo simbolico de un error, o el generico si no tiene uno propio.
func CodeOf(err error) string {
	if rejection, ok := err.(*Rejection); ok && rejection.code != "" {
		return rejection.code
	}
	return CodeRelayError
}

// PreparedMetaTx es una metatx decodificada y validada, lista para enviarse al hub.
type PreparedMetaTx struct {
	Transaction    *types.Transaction
	From           common.Address
	To             *common.Address
	SigningData    []byte
	V              uint8
	R              [32]byte
	S              [32]byte
	Nonce          uint64
	MetaTxGasLimit uint64
	IsDeploy       bool
	// SenderKey es la clave con la que se indexa el cache de nonces. Se guarda tal como la
	// recibio el llamador, sin normalizar a direccion: hay callers que pasan una cadena que no es
	// una address, y truncarla les cambiaria la entrada del cache bajo los pies.
	SenderKey string
}

// relayLock serializa la reserva del cupo de gas y el envio frente a otros envios.
//
// Vive aca y no en el controller porque es el envio lo que tiene que ser atomico, no el manejo de
// la peticion: `POST /relay` espera el receipt DESPUES de soltarlo. Si la espera quedara adentro,
// un solo relay sincronico bloquearia todos los envios durante el plazo completo. Ver D3.
var relayLock sync.Mutex

// PrepareMetaTx decodifica y valida una raw tx, y emite los eventos de recepcion y decodificacion.
//
// Emitirlos aca y no en cada puerta es lo que hace que una metatx relayada por `POST /relay` deje
// exactamente la misma traza que una relayada por `POST /`.
func (service *RelaySignerService) PrepareMetaTx(ctx context.Context, rawTx string) (*PreparedMetaTx, error) {
	// relay.received se emite ANTES de decodificar, para que una raw tx malformada deje rastro con
	// su metaTxId en lugar de desaparecer.
	received := map[string]interface{}{
		"rawTxHash":  nil,
		"rawTxBytes": RawTxBytes(rawTx),
	}
	if hash := RawTxHash(rawTx); hash != "" {
		received["rawTxHash"] = hash
	}
	if log.ShouldLogRawTx() {
		received["rawTx"] = rawTx
	}
	log.Info(ctx, "relay.received", received)

	decodeTransaction, err := GetTransaction(trimHexPrefix(rawTx))
	if err != nil {
		return nil, Reject(err, CodeBadRawTx)
	}

	v, rInt, sInt := decodeTransaction.RawSignatureValues()
	if v == nil || rInt == nil || sInt == nil {
		return nil, Reject(errors.New("bad signature ECDSA", -32000), CodeBadMetaTx)
	}

	// El RelayHub espera una firma pre-EIP155 (chainId=0 => v=27/28). Si el cliente firmo con
	// EIP-155, el valor se truncaria y la meta-tx revertiria on-chain sin diagnostico.
	if vUint := v.Uint64(); vUint != 27 && vUint != 28 {
		return nil, Reject(
			errors.New("transaction must be signed pre-EIP155 (chainId=0, v=27 or 28)", -32000),
			CodeBadMetaTx)
	}

	message, err := decodeTransaction.AsMessage(types.NewEIP155Signer(decodeTransaction.ChainId()))
	if err != nil {
		return nil, Reject(err, CodeBadMetaTx)
	}

	metaTxGasLimit := uint64((len(decodeTransaction.Data())*105)+300000) + decodeTransaction.Gas()

	log.Info(ctx, "relay.decoded", DecodedFields(decodeTransaction, message.From(), metaTxGasLimit))

	if service.Config.Security.PermissionsEnabled {
		permitted, err := service.VerifySender(ctx, message.From(), nil)
		if err != nil {
			return nil, Reject(err, CodePermissioningUnavailable)
		}
		if !permitted {
			return nil, Reject(
				errors.New("account sender is not permitted to send transactions", -32000),
				CodeSenderNotPermitted)
		}
	}

	var r, s [32]byte
	rBytes, _ := hex.DecodeString(fmt.Sprintf("%064x", rInt))
	sBytes, _ := hex.DecodeString(fmt.Sprintf("%064x", sInt))
	copy(r[:], rBytes)
	copy(s[:], sBytes)

	var signingDataTx *model.RawTransaction
	if decodeTransaction.To() != nil {
		signingDataTx = model.NewTransaction(decodeTransaction.Nonce(), *decodeTransaction.To(),
			decodeTransaction.Value(), decodeTransaction.Gas(), decodeTransaction.GasPrice(), decodeTransaction.Data())
	} else {
		signingDataTx = model.NewContractCreation(decodeTransaction.Nonce(), decodeTransaction.Value(),
			decodeTransaction.Gas(), decodeTransaction.GasPrice(), decodeTransaction.Data())
	}
	signingDataRLP, err := rlp.EncodeToBytes(signingDataTx.Data)
	if err != nil {
		return nil, Reject(errors.New("internal error", -32603), CodeRelayError)
	}

	return &PreparedMetaTx{
		Transaction:    decodeTransaction,
		From:           message.From(),
		To:             decodeTransaction.To(),
		SigningData:    signingDataRLP,
		V:              uint8(v.Uint64()),
		R:              r,
		S:              s,
		Nonce:          decodeTransaction.Nonce(),
		MetaTxGasLimit: metaTxGasLimit,
		IsDeploy:       decodeTransaction.To() == nil,
		SenderKey:      message.From().Hex(),
	}, nil
}

// ReserveGasAndSend verifica el cupo de gas del bloque y envia la metatx, de forma atomica frente a
// otros envios. Devuelve el hash de la transaccion envolvente.
//
// Con el reordenamiento encendido, el envio pasa antes por el turno y la validacion del nonce; con
// el apagado se entra derecho al camino de siempre, que es byte a byte el de antes de esta
// capacidad. Ver design.md, D8.
func (service *RelaySignerService) ReserveGasAndSend(ctx context.Context, prepared *PreparedMetaTx) (common.Hash, error) {
	if service.reorderEnabled() {
		return service.sendReordered(ctx, prepared)
	}
	return service.reserveGasAndSend(ctx, prepared)
}

// reserveGasAndSend es la seccion critica global: reservar el cupo de gas del bloque y enviar.
//
// El lock se toma y se suelta ACA, no en el manejador: quien espere el receipt lo hace afuera. Con
// el reordenamiento encendido, el candado del usuario ya esta tomado cuando se llega aca -ese es el
// orden fijo usuario -> global de D2-.
func (service *RelaySignerService) reserveGasAndSend(ctx context.Context, prepared *PreparedMetaTx) (common.Hash, error) {
	relayLock.Lock()
	defer relayLock.Unlock()

	enoughGas, err := service.VerifyGasLimit(ctx, prepared.MetaTxGasLimit, nil)
	if err != nil {
		return common.Hash{}, Reject(err, CodeSendFailed)
	}
	if !enoughGas {
		return common.Hash{}, Reject(
			errors.New("transaction gas limit exceeds block gas limit", -32000), CodeRelayError)
	}

	hash, err := service.sendPrepared(ctx, prepared)
	if err != nil {
		return common.Hash{}, Reject(err, CodeSendFailed)
	}
	return hash, nil
}

// DecodedFields arma los campos de relay.decoded.
//
// Todo campo del contrato se emite siempre, incluso sin valor aplicable: la vista los lee por
// nombre y omitirlos la dejaria sin poder distinguir "no se pudo" de "no se emitio". Los cuatro que
// salen del sufijo del modelo de gas quedan sin valor si el sufijo no esta, y eso NO rechaza la
// metatx: se decodifica para registrar, nunca para validar. Ver D13 de 01-add-relay-event-bus.
func DecodedFields(tx *types.Transaction, from common.Address, metaTxGasLimit uint64) map[string]interface{} {
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

	gasModel := DecodeGasModelSuffix(tx.Data())
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

func trimHexPrefix(value string) string {
	if len(value) >= 2 && value[0] == '0' && (value[1] == 'x' || value[1] == 'X') {
		return value[2:]
	}
	return value
}
