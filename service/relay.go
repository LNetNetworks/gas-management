package service

import (
	"context"
	"encoding/hex"
	"math/big"
	"strings"
	"time"

	bl "github.com/LACNetNetworks/gas-relay-signer/blockchain"
	"github.com/LACNetNetworks/gas-relay-signer/errors"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	sha "golang.org/x/crypto/sha3"
)

// El relay sincronico: enviar la metatx y esperar a saber como termino, en una sola llamada.

const (
	// receiptPollInterval es cada cuanto se le pregunta al nodo por el receipt. Conservador a
	// proposito: el plazo maximo ya lo acota `reorder.receiptTimeoutMs`, y el numero de metatx
	// esperando a la vez lo acota el cupo de gas por bloque.
	receiptPollInterval = 500 * time.Millisecond

	// defaultReceiptTimeout se usa si la configuracion no trae un plazo utilizable.
	defaultReceiptTimeout = 60 * time.Second
)

// RelayResult es como termino una metatx, tal como lo devuelve `POST /relay`.
//
// Los numeros grandes van como texto, igual que en el relayer de referencia. Los campos que pueden
// no tener valor son punteros: se emiten como nulos en lugar de omitirse.
type RelayResult struct {
	TransactionHash string   `json:"transactionHash"`
	IsDeploy        bool     `json:"isDeploy"`
	DeployedAddress *string  `json:"deployedAddress"`
	BlockNumber     *uint64  `json:"blockNumber"`
	GasUsed         string   `json:"gasUsed"`
	ErrorCode       *uint8   `json:"errorCode"`
	ErrorCodeName   *string  `json:"errorCodeName"`
	Executed        *bool    `json:"executed"`
	Output          *string  `json:"output"`
	From            string   `json:"from"`
	To              *string  `json:"to"`
	Nonce           uint64   `json:"nonce"`
	MetaTxGasLimit  string   `json:"metaTxGasLimit"`
	Events          []string `json:"events"`

	// Este servicio no hace pre-chequeo por simulacion, asi que ninguna metatx se envia simulada.
	Simulated bool `json:"simulated"`
}

// RelayAndWait relaya una metatx y responde recien cuando se sabe como termino en la cadena.
//
// Usa el mismo camino de validacion y envio que `POST /`: las dos puertas no pueden divergir en que
// metatx aceptan. La espera ocurre DESPUES de que ReserveGasAndSend solto el lock del cupo de gas,
// asi que un relay sincronico no bloquea a los demas envios. Ver design.md, D3 y D4.
func (service *RelaySignerService) RelayAndWait(ctx context.Context, rawTx string) (*RelayResult, error) {
	prepared, err := service.PrepareMetaTx(ctx, rawTx)
	if err != nil {
		return nil, err
	}

	hash, err := service.ReserveGasAndSend(ctx, prepared)
	if err != nil {
		return nil, err
	}

	receipt, err := service.waitForReceipt(ctx, hash)
	if err != nil {
		// La metatx SE ENVIO: el vencimiento no es un rechazo. Se devuelve el hash para que el
		// cliente la consulte en lugar de reenviarla, que produciria un nonce repetido.
		return nil, Reject(err, CodeReceiptTimeout)
	}

	return service.decodeRelayResult(ctx, hash, prepared, receipt), nil
}

// waitForReceipt sondea el receipt hasta que aparezca o se agote el plazo configurado.
func (service *RelaySignerService) waitForReceipt(ctx context.Context, hash common.Hash) (*types.Receipt, error) {
	client := new(bl.Client)
	if err := client.Connect(service.Config.Application.NodeURL); err != nil {
		return nil, err
	}
	defer client.Close()

	timeout := defaultReceiptTimeout
	if service.Config.Reorder.ReceiptTimeoutMs > 0 {
		timeout = time.Duration(service.Config.Reorder.ReceiptTimeoutMs) * time.Millisecond
	}
	deadline := time.After(timeout)
	ticker := time.NewTicker(receiptPollInterval)
	defer ticker.Stop()

	// El ultimo error del sondeo se conserva para el mensaje del vencimiento. Un fallo aislado no
	// interrumpe la espera -el nodo puede tardar o parpadear-, pero si la espera vence habiendo
	// fallado siempre, decirlo distingue "todavia no se mino" de "el nodo responde algo ilegible".
	var lastErr error

	for {
		receipt, err := client.GetTransactionReceipt(hash)
		if err == nil && receipt != nil {
			return receipt, nil
		}
		if err != nil {
			lastErr = err
		}

		select {
		case <-deadline:
			message := "the metatx was sent but its result was not known within " + timeout.String() +
				": consult the receipt for " + hash.Hex()
			if lastErr != nil {
				message += " (last error: " + lastErr.Error() + ")"
			}
			return nil, errors.New(message, -32603)
		case <-ctx.Done():
			return nil, errors.New("the client stopped waiting for "+hash.Hex(), -32603)
		case <-ticker.C:
		}
	}
}

// hubEvents son los eventos del RelayHub que describen como termino una metatx.
type hubEvents struct {
	contractDeployed  string
	transactionRelay  string
	badTransaction    string
	relayed           string
	gasUsedByRelayHub string
}

func topicsOfHub() hubEvents {
	topic := func(signature string) string {
		digest := sha.NewLegacyKeccak256()
		digest.Write([]byte(signature))
		return "0x" + hex.EncodeToString(digest.Sum(nil))
	}
	return hubEvents{
		contractDeployed:  topic("ContractDeployed(address,address,address)"),
		transactionRelay:  topic("TransactionRelayed(address,address,address,bool,bytes)"),
		badTransaction:    topic("BadTransactionSent(address,address,uint8)"),
		relayed:           topic("Relayed(address,address)"),
		gasUsedByRelayHub: topic("GasUsedByTransaction(address,uint256,uint256,uint256,uint256)"),
	}
}

// decodeRelayResult traduce el receipt a como termino la metatx, leyendo los eventos del hub.
//
// NOTA: `GetMetaTxResult` decodifica lo mismo para su propia respuesta. No se unificaron porque ese
// camino no tiene cobertura y refactorizarlo a ciegas dentro de este change arriesga cambiarlo en
// silencio; unificarlos pide primero un test de caracterizacion sobre el.
func (service *RelaySignerService) decodeRelayResult(ctx context.Context, hash common.Hash, prepared *PreparedMetaTx, receipt *types.Receipt) *RelayResult {
	result := &RelayResult{
		TransactionHash: hash.Hex(),
		IsDeploy:        prepared.IsDeploy,
		From:            prepared.From.Hex(),
		Nonce:           prepared.Nonce,
		MetaTxGasLimit:  new(big.Int).SetUint64(prepared.MetaTxGasLimit).String(),
		GasUsed:         "0",
		Events:          []string{},
		Simulated:       false,
	}
	if prepared.To != nil {
		to := prepared.To.Hex()
		result.To = &to
	}
	if receipt == nil {
		return result
	}

	result.GasUsed = new(big.Int).SetUint64(receipt.GasUsed).String()
	if receipt.BlockNumber != nil {
		blockNumber := receipt.BlockNumber.Uint64()
		result.BlockNumber = &blockNumber
	}

	outcome := service.readHubEvents(ctx, hash, receipt)
	result.Events = outcome.events
	result.Executed = outcome.executed
	result.ErrorCode = outcome.errorCode
	result.ErrorCodeName = outcome.errorCodeName
	result.Output = outcome.output
	result.DeployedAddress = outcome.deployedAddress

	service.settle(ctx, hash, result.BlockNumber, result.GasUsed, outcome)

	return result
}

// ExpectedRawTxField es lo que `POST /relay` espera en el cuerpo, y el alias que tambien acepta.
const (
	FieldRawTx = "rawTx"
	FieldAlias = "signedTransaction"
)

// IsHexData indica si el valor tiene FORMA de transaccion firmada en hexadecimal.
//
// Comprueba la forma y no que se pueda decodificar, a proposito: si rechazara aca una raw tx de
// longitud impar, esta puerta daria un motivo distinto del que da el camino JSON-RPC para la misma
// entrada. El veredicto sobre si se puede decodificar es del camino compartido, que es lo que hace
// que las dos puertas coincidan. Ver design.md, D4.
func IsHexData(value string) bool {
	body := strings.TrimPrefix(strings.TrimPrefix(value, "0x"), "0X")
	if body == "" || body == value {
		return false
	}
	for _, character := range body {
		if !strings.ContainsRune("0123456789abcdefABCDEF", character) {
			return false
		}
	}
	return true
}
