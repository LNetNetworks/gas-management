/*
	RelaySigner Service
	version 0.9
	author: Adrian Pareja Abarca
	email: adriancc5.5@gmail.com
*/

package service

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"strings"
	"sync"
	"time"

	log "github.com/LACNetNetworks/gas-relay-signer/audit"
	bl "github.com/LACNetNetworks/gas-relay-signer/blockchain"
	"github.com/LACNetNetworks/gas-relay-signer/errors"
	"github.com/LACNetNetworks/gas-relay-signer/model"
	"github.com/LACNetNetworks/gas-relay-signer/rpc"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	sha "golang.org/x/crypto/sha3"
)

const RelayABI = "[{\"inputs\":[{\"internalType\":\"uint8\",\"name\":\"_blocksFrequency\",\"type\":\"uint8\"},{\"internalType\":\"address\",\"name\":\"_accountIngress\",\"type\":\"address\"}],\"stateMutability\":\"nonpayable\",\"type\":\"constructor\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":false,\"internalType\":\"address\",\"name\":\"admin\",\"type\":\"address\"},{\"indexed\":false,\"internalType\":\"address\",\"name\":\"newAddress\",\"type\":\"address\"}],\"name\":\"AccountIngressChanged\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":false,\"internalType\":\"address\",\"name\":\"node\",\"type\":\"address\"},{\"indexed\":false,\"internalType\":\"address\",\"name\":\"originalSender\",\"type\":\"address\"},{\"indexed\":false,\"internalType\":\"enumIRelayHub.ErrorCode\",\"name\":\"errorCode\",\"type\":\"uint8\"}],\"name\":\"BadTransactionSent\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":false,\"internalType\":\"address\",\"name\":\"admin\",\"type\":\"address\"},{\"indexed\":false,\"internalType\":\"uint8\",\"name\":\"blocksFrequency\",\"type\":\"uint8\"}],\"name\":\"BlockFrequencyChanged\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":true,\"internalType\":\"address\",\"name\":\"relay\",\"type\":\"address\"},{\"indexed\":true,\"internalType\":\"address\",\"name\":\"from\",\"type\":\"address\"},{\"indexed\":false,\"internalType\":\"address\",\"name\":\"contractDeployed\",\"type\":\"address\"}],\"name\":\"ContractDeployed\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":false,\"internalType\":\"address\",\"name\":\"node\",\"type\":\"address\"},{\"indexed\":false,\"internalType\":\"uint256\",\"name\":\"blockNumber\",\"type\":\"uint256\"},{\"indexed\":false,\"internalType\":\"uint8\",\"name\":\"countExceeded\",\"type\":\"uint8\"}],\"name\":\"GasLimitExceeded\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":false,\"internalType\":\"uint256\",\"name\":\"blockNumber\",\"type\":\"uint256\"},{\"indexed\":false,\"internalType\":\"uint256\",\"name\":\"gasUsedLastBlocks\",\"type\":\"uint256\"},{\"indexed\":false,\"internalType\":\"uint256\",\"name\":\"averageLastBlocks\",\"type\":\"uint256\"},{\"indexed\":false,\"internalType\":\"uint256\",\"name\":\"newGasLimit\",\"type\":\"uint256\"}],\"name\":\"GasLimitSet\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":false,\"internalType\":\"address\",\"name\":\"node\",\"type\":\"address\"},{\"indexed\":false,\"internalType\":\"uint256\",\"name\":\"blockNumber\",\"type\":\"uint256\"},{\"indexed\":false,\"internalType\":\"uint256\",\"name\":\"gasUsed\",\"type\":\"uint256\"},{\"indexed\":false,\"internalType\":\"uint256\",\"name\":\"gasLimit\",\"type\":\"uint256\"},{\"indexed\":false,\"internalType\":\"uint256\",\"name\":\"gasUsedLastBlocks\",\"type\":\"uint256\"}],\"name\":\"GasUsedByTransaction\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":false,\"internalType\":\"address\",\"name\":\"admin\",\"type\":\"address\"},{\"indexed\":false,\"internalType\":\"uint256\",\"name\":\"gasUsedRelayHub\",\"type\":\"uint256\"}],\"name\":\"GasUsedRelayHubChanged\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":false,\"internalType\":\"uint256\",\"name\":\"blockNumber\",\"type\":\"uint256\"},{\"indexed\":false,\"internalType\":\"address\",\"name\":\"admin\",\"type\":\"address\"},{\"indexed\":false,\"internalType\":\"uint256\",\"name\":\"maxGasBlockLimit\",\"type\":\"uint256\"}],\"name\":\"MaxGasBlockLimitChanged\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":false,\"internalType\":\"address\",\"name\":\"newNode\",\"type\":\"address\"}],\"name\":\"NodeAdded\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":false,\"internalType\":\"address\",\"name\":\"node\",\"type\":\"address\"},{\"indexed\":false,\"internalType\":\"uint256\",\"name\":\"blockNumber\",\"type\":\"uint256\"}],\"name\":\"NodeBlocked\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":false,\"internalType\":\"address\",\"name\":\"oldNode\",\"type\":\"address\"}],\"name\":\"NodeDeleted\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":false,\"internalType\":\"uint256\",\"name\":\"nonce\",\"type\":\"uint256\"},{\"indexed\":false,\"internalType\":\"uint256\",\"name\":\"gasLimit\",\"type\":\"uint256\"},{\"indexed\":false,\"internalType\":\"address\",\"name\":\"to\",\"type\":\"address\"},{\"indexed\":false,\"internalType\":\"bytes\",\"name\":\"decodedFunction\",\"type\":\"bytes\"}],\"name\":\"Parameters\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":false,\"internalType\":\"bool\",\"name\":\"result\",\"type\":\"bool\"}],\"name\":\"Recalculated\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":true,\"internalType\":\"address\",\"name\":\"sender\",\"type\":\"address\"},{\"indexed\":true,\"internalType\":\"address\",\"name\":\"from\",\"type\":\"address\"}],\"name\":\"Relayed\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":true,\"internalType\":\"bytes32\",\"name\":\"role\",\"type\":\"bytes32\"},{\"indexed\":true,\"internalType\":\"bytes32\",\"name\":\"previousAdminRole\",\"type\":\"bytes32\"},{\"indexed\":true,\"internalType\":\"bytes32\",\"name\":\"newAdminRole\",\"type\":\"bytes32\"}],\"name\":\"RoleAdminChanged\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":true,\"internalType\":\"bytes32\",\"name\":\"role\",\"type\":\"bytes32\"},{\"indexed\":true,\"internalType\":\"address\",\"name\":\"account\",\"type\":\"address\"},{\"indexed\":true,\"internalType\":\"address\",\"name\":\"sender\",\"type\":\"address\"}],\"name\":\"RoleGranted\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":true,\"internalType\":\"bytes32\",\"name\":\"role\",\"type\":\"bytes32\"},{\"indexed\":true,\"internalType\":\"address\",\"name\":\"account\",\"type\":\"address\"},{\"indexed\":true,\"internalType\":\"address\",\"name\":\"sender\",\"type\":\"address\"}],\"name\":\"RoleRevoked\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":true,\"internalType\":\"address\",\"name\":\"relay\",\"type\":\"address\"},{\"indexed\":true,\"internalType\":\"address\",\"name\":\"from\",\"type\":\"address\"},{\"indexed\":true,\"internalType\":\"address\",\"name\":\"to\",\"type\":\"address\"},{\"indexed\":false,\"internalType\":\"bool\",\"name\":\"executed\",\"type\":\"bool\"},{\"indexed\":false,\"internalType\":\"bytes\",\"name\":\"output\",\"type\":\"bytes\"}],\"name\":\"TransactionRelayed\",\"type\":\"event\"},{\"inputs\":[],\"name\":\"DEFAULT_ADMIN_ROLE\",\"outputs\":[{\"internalType\":\"bytes32\",\"name\":\"\",\"type\":\"bytes32\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"newNode\",\"type\":\"address\"}],\"name\":\"addNode\",\"outputs\":[{\"internalType\":\"bool\",\"name\":\"\",\"type\":\"bool\"}],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"node\",\"type\":\"address\"}],\"name\":\"deleteNode\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[],\"name\":\"getGasLimit\",\"outputs\":[{\"internalType\":\"uint256\",\"name\":\"\",\"type\":\"uint256\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[],\"name\":\"getGasUsedLastBlocks\",\"outputs\":[{\"internalType\":\"uint256\",\"name\":\"\",\"type\":\"uint256\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[],\"name\":\"getNodes\",\"outputs\":[{\"internalType\":\"uint256\",\"name\":\"\",\"type\":\"uint256\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"bytes32\",\"name\":\"role\",\"type\":\"bytes32\"}],\"name\":\"getRoleAdmin\",\"outputs\":[{\"internalType\":\"bytes32\",\"name\":\"\",\"type\":\"bytes32\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"bytes32\",\"name\":\"role\",\"type\":\"bytes32\"},{\"internalType\":\"uint256\",\"name\":\"index\",\"type\":\"uint256\"}],\"name\":\"getRoleMember\",\"outputs\":[{\"internalType\":\"address\",\"name\":\"\",\"type\":\"address\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"bytes32\",\"name\":\"role\",\"type\":\"bytes32\"}],\"name\":\"getRoleMemberCount\",\"outputs\":[{\"internalType\":\"uint256\",\"name\":\"\",\"type\":\"uint256\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"bytes32\",\"name\":\"role\",\"type\":\"bytes32\"},{\"internalType\":\"address\",\"name\":\"account\",\"type\":\"address\"}],\"name\":\"grantRole\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"bytes32\",\"name\":\"role\",\"type\":\"bytes32\"},{\"internalType\":\"address\",\"name\":\"account\",\"type\":\"address\"}],\"name\":\"hasRole\",\"outputs\":[{\"internalType\":\"bool\",\"name\":\"\",\"type\":\"bool\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"bytes32\",\"name\":\"role\",\"type\":\"bytes32\"},{\"internalType\":\"address\",\"name\":\"account\",\"type\":\"address\"}],\"name\":\"renounceRole\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"bytes32\",\"name\":\"role\",\"type\":\"bytes32\"},{\"internalType\":\"address\",\"name\":\"account\",\"type\":\"address\"}],\"name\":\"revokeRole\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"_accountIngress\",\"type\":\"address\"}],\"name\":\"setAccounIngress\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"uint8\",\"name\":\"_blocksFrequency\",\"type\":\"uint8\"}],\"name\":\"setBlocksFrequency\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"uint256\",\"name\":\"newGasUsed\",\"type\":\"uint256\"}],\"name\":\"setGasUsedLastBlocks\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"uint256\",\"name\":\"_gasUsedRelayHub\",\"type\":\"uint256\"}],\"name\":\"setGasUsedRelayHub\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"uint256\",\"name\":\"_maxGasBlockLimit\",\"type\":\"uint256\"}],\"name\":\"setMaxGasBlockLimit\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"bytes\",\"name\":\"signingData\",\"type\":\"bytes\"},{\"internalType\":\"uint8\",\"name\":\"v\",\"type\":\"uint8\"},{\"internalType\":\"bytes32\",\"name\":\"r\",\"type\":\"bytes32\"},{\"internalType\":\"bytes32\",\"name\":\"s\",\"type\":\"bytes32\"}],\"name\":\"relayMetaTx\",\"outputs\":[{\"internalType\":\"bool\",\"name\":\"success\",\"type\":\"bool\"}],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"bytes\",\"name\":\"signingData\",\"type\":\"bytes\"},{\"internalType\":\"uint8\",\"name\":\"v\",\"type\":\"uint8\"},{\"internalType\":\"bytes32\",\"name\":\"r\",\"type\":\"bytes32\"},{\"internalType\":\"bytes32\",\"name\":\"s\",\"type\":\"bytes32\"}],\"name\":\"deployMetaTx\",\"outputs\":[{\"internalType\":\"bool\",\"name\":\"success\",\"type\":\"bool\"},{\"internalType\":\"address\",\"name\":\"deployedAddress\",\"type\":\"address\"}],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"address\",\"name\":\"from\",\"type\":\"address\"}],\"name\":\"getNonce\",\"outputs\":[{\"internalType\":\"uint256\",\"name\":\"\",\"type\":\"uint256\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[],\"name\":\"getMsgSender\",\"outputs\":[{\"internalType\":\"address\",\"name\":\"\",\"type\":\"address\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"uint256\",\"name\":\"gasUsed\",\"type\":\"uint256\"}],\"name\":\"increaseGasUsed\",\"outputs\":[{\"internalType\":\"bool\",\"name\":\"\",\"type\":\"bool\"}],\"stateMutability\":\"nonpayable\",\"type\":\"function\"}]"

const ENVIRONMENT_KEY_NAME = "WRITER_KEY"

var GAS_LIMIT uint64 = 0

var lock sync.Mutex

// nonceEntry es una entrada del caché de nonces por sender: el PRÓXIMO nonce a usar y cuándo se
// actualizó. `updatedAt` permite expirar entradas obsoletas (TTL) y volver a leer el nonce real
// on-chain (getNonce del RelayHub) si el caché quedara desincronizado por cualquier causa.
type nonceEntry struct {
	next      uint64
	updatedAt time.Time
}

// RelaySignerService is the main service
type RelaySignerService struct {
	// The service's configuration
	Config      *model.Config
	senders     map[string]*nonceEntry
	sendersLock sync.Mutex
	// metaTx recuerda, por hash de la transaccion enviada, a que metatx pertenece. Sin esto un
	// receipt consultado en otra peticion no se podria asociar a la metatx que lo origino.
	metaTx     map[common.Hash]*metaTxEntry
	metaTxLock sync.Mutex
}

// Init configuration parameters
func (service *RelaySignerService) Init(_config *model.Config) error {
	service.Config = _config

	key, exist := os.LookupEnv(ENVIRONMENT_KEY_NAME)
	if !exist {
		return errors.FailedReadEnv.New("Environment variable WRITER_KEY not set", -32602)
	}

	privateKey, err := crypto.HexToECDSA(string(key[2:66]))
	if err != nil {
		return errors.FailedKeyConfig.New("Invalid ECDSA Key", -32602)
	}

	publicKey := privateKey.Public()
	_, ok := publicKey.(*ecdsa.PublicKey)
	if !ok {
		return errors.FailedKeyConfig.New("Invalid ECDSA Public Key", -32602)
	}

	service.Config.Application.Key = string(key[2:66])

	service.senders = make(map[string]*nonceEntry)
	service.metaTx = make(map[common.Hash]*metaTxEntry)

	if service.Config.Security.PermissionsEnabled {
		if !(common.IsHexAddress(service.Config.Security.AccountContractAddress)) {
			return errors.InvalidAddress.New("Invalid Account Smart Contract Address", -32608)
		}
	}

	service.Config.Application.RelayHubContractAddress, err = getRelayHubContractAddress(service.Config.Application.NodeURL, "1000", service.Config.Application.ContractAddress, 10)
	if err != nil {
		return errors.FailedKeyConfig.New("Can't get relayHub smart contract address from Proxy", -32610)
	}

	return nil
}

// SendMetatransaction to blockchain
func (service *RelaySignerService) SendMetatransaction(ctx context.Context, id json.RawMessage, to *common.Address, gasLimit uint64, signingData []byte, v uint8, r, s [32]byte, sender string, nonce uint64) *rpc.JsonrpcMessage {
	hash, err := service.sendPrepared(ctx, &PreparedMetaTx{
		From:           common.HexToAddress(sender),
		To:             to,
		SigningData:    signingData,
		V:              v,
		R:              r,
		S:              s,
		Nonce:          nonce,
		MetaTxGasLimit: gasLimit,
		IsDeploy:       to == nil,
		SenderKey:      sender,
	})
	if err != nil {
		return HandleError(ctx, id, err)
	}

	result := new(rpc.JsonrpcMessage)
	result.ID = id
	return result.Response(&hash)
}

// sendPrepared envuelve la metatx, la firma con la clave del nodo y la difunde. Devuelve el hash de
// la transaccion envolvente.
//
// Es el unico punto de envio: lo comparten el camino JSON-RPC y el sincronico, asi que los dos
// emiten el mismo relay.sent y recuerdan la correlacion de la misma forma.
func (service *RelaySignerService) sendPrepared(ctx context.Context, prepared *PreparedMetaTx) (common.Hash, error) {
	client := new(bl.Client)
	err := client.Connect(service.Config.Application.NodeURL)
	if err != nil {
		return common.Hash{}, err
	}
	defer client.Close()

	privateKey, err := crypto.HexToECDSA(service.Config.Application.Key)
	if err != nil {
		return common.Hash{}, err
	}

	optionsSendTransaction, err := client.ConfigTransaction(privateKey, prepared.MetaTxGasLimit, true)
	if err != nil {
		return common.Hash{}, err
	}
	tx, err := client.SendMetatransaction(*service.Config.Application.RelayHubContractAddress,
		optionsSendTransaction, prepared.To, prepared.SigningData, prepared.V, prepared.R, prepared.S)
	if err != nil {
		return common.Hash{}, err
	}

	// La respuesta al cliente sigue siendo el hash y nada mas: el cliente recibe exactamente lo
	// mismo que antes de que este metodo tuviera la transaccion entera a mano.
	transactionHash := tx.Hash()

	// Se recuerda a que metatx pertenece este hash: el receipt llega en otra peticion, donde no
	// existe ningun metaTxId del que partir. Ver design.md, D11.
	service.rememberMetaTx(ctx, transactionHash)

	log.GeneralLogger.Println("transaction", &transactionHash)

	service.incrementTransactionCount(prepared.SenderKey, prepared.Nonce)

	log.Info(ctx, "relay.sent", map[string]interface{}{
		"transactionHash": transactionHash.Hex(),
		// El nonce del hub para este usuario, que es el que trae firmado la metatx.
		"hubNonce": prepared.Nonce,
		// El nonce de la CUENTA del writer node: es el que traba el txpool si algo se pierde, y
		// el unico dato con el que se puede desatascar la cola desde el nodo.
		"writerNodeNonce": tx.Nonce(),
		"metaTxGasLimit":  prepared.MetaTxGasLimit,
		// Campos del contrato que este servicio todavia no calcula. Se emiten sin valor en lugar
		// de omitirse, para que la vista distinga "no aplica" de "no se emitio". Ver D13.
		"simulated":              nil,
		"simulatedErrorCodeName": nil,
		"pendingForUser":         nil,
	})

	return transactionHash, nil
}

// GetTransactionReceipt from blockchain
func (service *RelaySignerService) GetTransactionReceipt(ctx context.Context, id json.RawMessage, transactionID string) *rpc.JsonrpcMessage {
	// Los eventos que salgan de aca pertenecen a la metatx que produjo este hash, no a la
	// peticion que vino a consultar el receipt. Ver design.md, D11.
	ctx = service.recallMetaTx(ctx, common.HexToHash(transactionID))

	client := new(bl.Client)
	err := client.Connect(service.Config.Application.NodeURL)
	if err != nil {
		return HandleError(ctx, id, err)
	}
	defer client.Close()

	receipt, err := client.GetTransactionReceipt(common.HexToHash(transactionID))
	if err != nil {
		HandleError(ctx, id, err)
	}

	var receiptReverted map[string]interface{}

	if receipt != nil {
		d := sha.NewLegacyKeccak256()
		e := sha.NewLegacyKeccak256()
		f := sha.NewLegacyKeccak256()
		g := sha.NewLegacyKeccak256()

		d.Write([]byte("ContractDeployed(address,address,address)"))
		eventContractDeployed := hex.EncodeToString(d.Sum(nil))

		e.Write([]byte("TransactionRelayed(address,address,address,bool,bytes)"))
		eventTransactionRelayed := hex.EncodeToString(e.Sum(nil))

		f.Write([]byte("BadTransactionSent(address,address,uint8)"))
		eventBadTransaction := hex.EncodeToString(f.Sum(nil))

		g.Write([]byte("Relayed(address,address)"))
		eventRelayed := hex.EncodeToString(g.Sum(nil))

		var sawContractDeployed, sawTransactionRelayed, sawBadTransaction, sawRelayed bool

		for _, lg := range receipt.Logs {
			if len(lg.Topics) == 0 {
				continue
			}
			switch lg.Topics[0].Hex() {
			case "0x" + eventContractDeployed:
				sawContractDeployed = true
				receipt.ContractAddress = common.BytesToAddress(lg.Data)
			case "0x" + eventTransactionRelayed:
				sawTransactionRelayed = true
				executed, output := transactionRelayedFailed(ctx, id, lg.Data)
				if !executed {
					receipt.Status = uint64(0)
					reason := decodeRevertReason(output)

					jsonReceipt, err := json.Marshal(receipt)
					if err != nil {
						HandleError(ctx, id, err)
					}

					json.Unmarshal(jsonReceipt, &receiptReverted)
					receiptReverted["revertReason"] = reason
				}
			case "0x" + eventBadTransaction:
				sawBadTransaction = true
				errorCode, badSender := badTransactionErrorCode(ctx, id, lg.Data)
				// La meta-tx no consumió nonce en el RelayHub: descartar el contador local del
				// sender para que su próxima lectura "pending" vuelva al nonce real on-chain.
				service.invalidateNonce(badSender.Hex())
				hubRejected(ctx, transactionID, badSender, errorCode)
				receipt.Status = uint64(0)

				jsonReceipt, err := json.Marshal(receipt)
				if err != nil {
					HandleError(ctx, id, err)
				}

				json.Unmarshal(jsonReceipt, &receiptReverted)
				receiptReverted["revertReason"] = "BadTransactionSent: " + errorCodeName(errorCode)
			case "0x" + eventRelayed:
				sawRelayed = true
			}
		}

		// Fallo silencioso en DEPLOY: la verificación pasó (evento Relayed) pero el CREATE interno
		// revirtió en el constructor → no hay ContractDeployed, ni TransactionRelayed, ni
		// BadTransactionSent. El RelayHub retorna OK y la tx externa mina con status=1; lo exponemos
		// como fallo para que el cliente se entere (no quedaría reflejado de otra forma).
		if sawRelayed && !sawContractDeployed && !sawTransactionRelayed && !sawBadTransaction {
			receipt.Status = uint64(0)

			jsonReceipt, err := json.Marshal(receipt)
			if err != nil {
				HandleError(ctx, id, err)
			}

			json.Unmarshal(jsonReceipt, &receiptReverted)
			receiptReverted["revertReason"] = "deploy reverted: contract constructor failed (no code created)"
		}
	}
	result := new(rpc.JsonrpcMessage)

	result.ID = id
	if receiptReverted != nil {
		return result.Response(receiptReverted)
	}
	return result.Response(receipt)

}

// GetMetaTxResult devuelve el resultado parseado de un meta-tx relayado, a partir de los eventos del
// RelayHub en el receipt: { mined, success, executed, errorCode, revertReason, deployedAddress }.
func (service *RelaySignerService) GetMetaTxResult(ctx context.Context, id json.RawMessage, transactionID string) *rpc.JsonrpcMessage {
	ctx = service.recallMetaTx(ctx, common.HexToHash(transactionID))

	client := new(bl.Client)
	err := client.Connect(service.Config.Application.NodeURL)
	if err != nil {
		return HandleError(ctx, id, err)
	}
	defer client.Close()

	receipt, err := client.GetTransactionReceipt(common.HexToHash(transactionID))
	if err != nil {
		return HandleError(ctx, id, err)
	}

	out := map[string]interface{}{
		"transactionHash": "0x" + transactionID,
		"mined":           receipt != nil,
		"success":         true,
		"executed":        true,
		"errorCode":       nil,
		"revertReason":    nil,
		"deployedAddress": nil,
	}

	if receipt != nil {
		d := sha.NewLegacyKeccak256()
		e := sha.NewLegacyKeccak256()
		f := sha.NewLegacyKeccak256()
		g := sha.NewLegacyKeccak256()
		d.Write([]byte("ContractDeployed(address,address,address)"))
		e.Write([]byte("TransactionRelayed(address,address,address,bool,bytes)"))
		f.Write([]byte("BadTransactionSent(address,address,uint8)"))
		g.Write([]byte("Relayed(address,address)"))
		eventContractDeployed := "0x" + hex.EncodeToString(d.Sum(nil))
		eventTransactionRelayed := "0x" + hex.EncodeToString(e.Sum(nil))
		eventBadTransaction := "0x" + hex.EncodeToString(f.Sum(nil))
		eventRelayed := "0x" + hex.EncodeToString(g.Sum(nil))

		var sawContractDeployed, sawTransactionRelayed, sawBadTransaction, sawRelayed bool

		for _, lg := range receipt.Logs {
			if len(lg.Topics) == 0 {
				continue
			}
			switch lg.Topics[0].Hex() {
			case eventContractDeployed:
				sawContractDeployed = true
				out["deployedAddress"] = common.BytesToAddress(lg.Data).Hex()
			case eventTransactionRelayed:
				sawTransactionRelayed = true
				executed, output := transactionRelayedFailed(ctx, id, lg.Data)
				out["executed"] = executed
				if !executed {
					out["success"] = false
					out["revertReason"] = decodeRevertReason(output)
				}
			case eventBadTransaction:
				sawBadTransaction = true
				code, badSender := badTransactionErrorCode(ctx, id, lg.Data)
				service.invalidateNonce(badSender.Hex())
				hubRejected(ctx, transactionID, badSender, code)
				out["success"] = false
				out["executed"] = false
				out["errorCode"] = errorCodeName(code)
				out["revertReason"] = "BadTransactionSent: " + errorCodeName(code)
			case eventRelayed:
				sawRelayed = true
			}
		}

		// Fallo silencioso en DEPLOY: la verificación pasó (Relayed) pero el CREATE interno revirtió
		// en el constructor → no hay ContractDeployed/TransactionRelayed/BadTransactionSent y el RelayHub
		// retornó OK. Sin esto se reportaría success:true, deployedAddress:null (el cliente no se entera).
		if sawRelayed && !sawContractDeployed && !sawTransactionRelayed && !sawBadTransaction {
			out["success"] = false
			out["executed"] = false
			out["revertReason"] = "deploy reverted: contract constructor failed (no code created)"
		}
	}

	result := new(rpc.JsonrpcMessage)
	result.ID = id
	return result.Response(out)
}

// GetTransactionCount of account
func (service *RelaySignerService) GetTransactionCount(ctx context.Context, id json.RawMessage, from string, isPending bool) *rpc.JsonrpcMessage {
	var count *big.Int
	if isPending {
		if next, ok := service.cachedNonce(from); ok {
			count = new(big.Int).SetUint64(next)
		}
	}
	if count == nil {
		client := new(bl.Client)
		err := client.Connect(service.Config.Application.NodeURL)
		if err != nil {
			return HandleError(ctx, id, err)
		}
		defer client.Close()

		privateKey, err := crypto.HexToECDSA(service.Config.Application.Key)
		if err != nil {
			HandleError(ctx, id, err)
		}

		publicKey := privateKey.Public()
		publicKeyECDSA, ok := publicKey.(*ecdsa.PublicKey)
		if !ok {
			err := errors.New("error casting public key to ECDSA", -32602)
			HandleError(ctx, id, err)
		}

		nodeAddress := crypto.PubkeyToAddress(*publicKeyECDSA)

		address := common.HexToAddress(from)

		count, err = client.GetTransactionCount(*service.Config.Application.RelayHubContractAddress, address, nodeAddress)
		if err != nil {
			HandleError(ctx, id, err)
		}
	}

	result := new(rpc.JsonrpcMessage)

	result.ID = id
	return result.Response(fmt.Sprintf("0x%x", count))
}

// VerifyGasLimit sent a transaction
func (service *RelaySignerService) VerifyGasLimit(ctx context.Context, gasLimit uint64, id json.RawMessage) (bool, error) {
	client := new(bl.Client)
	err := client.Connect(service.Config.Application.NodeURL)
	if err != nil {
		return false, err
	}
	defer client.Close()

	privateKey, err := crypto.HexToECDSA(service.Config.Application.Key)
	if err != nil {
		HandleError(ctx, id, err)
	}

	publicKey := privateKey.Public()
	publicKeyECDSA, ok := publicKey.(*ecdsa.PublicKey)
	if !ok {
		err := errors.New("error casting public key to ECDSA", -32602)
		HandleError(ctx, id, err)
	}

	nodeAddress := crypto.PubkeyToAddress(*publicKeyECDSA)

	currentGasLimit, err := client.GetNodeGasLimit(*service.Config.Application.RelayHubContractAddress, nodeAddress)
	if err != nil {
		return false, err
	}

	if currentGasLimit != nil {
		log.GeneralLogger.Println("current gasLimit assigned:", currentGasLimit.Uint64())
	}
	if increment(gasLimit) > currentGasLimit.Uint64() {
		return false, nil
	}

	return true, nil
}

// VerifySender sent a transaction
func (service *RelaySignerService) VerifySender(ctx context.Context, sender common.Address, id json.RawMessage) (bool, error) {
	client := new(bl.Client)
	err := client.Connect(service.Config.Application.NodeURL)
	if err != nil {
		return false, err
	}
	defer client.Close()

	contractAddress := common.HexToAddress(service.Config.Security.AccountContractAddress)

	isPermitted, err := client.AccountPermitted(contractAddress, sender)
	if err != nil {
		return false, err
	}

	log.GeneralLogger.Println("sender is permitted:", isPermitted)

	return isPermitted, nil
}

// DecreaseGasUsed by node
func (service *RelaySignerService) DecreaseGasUsed(ctx context.Context, id json.RawMessage) bool {
	client := new(bl.Client)
	err := client.Connect(service.Config.Application.NodeURL)
	if err != nil {
		HandleError(ctx, id, err)
		return false
	}
	defer client.Close()

	privateKey, err := crypto.HexToECDSA(service.Config.Application.Key)
	if err != nil {
		log.GeneralLogger.Fatal(err)
	}

	options, err := client.ConfigTransaction(privateKey, 30000, false)
	if err != nil {
		HandleError(ctx, id, err)
	}

	_, err = client.DecreaseGasUsed(*service.Config.Application.RelayHubContractAddress, options, new(big.Int).SetUint64(25000))
	if err != nil {
		HandleError(ctx, id, err)
	}

	return true
}

func transactionRelayedFailed(ctx context.Context, id json.RawMessage, data []byte) (bool, []byte) {
	var transactionRelayedEvent struct {
		Relay    common.Address
		From     common.Address
		To       common.Address
		Executed bool
		Output   []byte
	}

	relayHubAbi, err := abi.JSON(strings.NewReader(RelayABI))
	if err != nil {
		HandleError(ctx, id, err)
	}

	err = relayHubAbi.Unpack(&transactionRelayedEvent, "TransactionRelayed", data)

	if err != nil {
		HandleError(ctx, id, err)
	}

	return transactionRelayedEvent.Executed, transactionRelayedEvent.Output
}

// badTransactionErrorCode decodifica el evento BadTransactionSent y devuelve su ErrorCode y el
// originalSender afectado (para invalidar su entrada en el caché de nonces).
func badTransactionErrorCode(ctx context.Context, id json.RawMessage, data []byte) (uint8, common.Address) {
	var badTransactionEvent struct {
		Node           common.Address
		OriginalSender common.Address
		ErrorCode      uint8
	}

	relayHubAbi, err := abi.JSON(strings.NewReader(RelayABI))
	if err != nil {
		HandleError(ctx, id, err)
	}

	err = relayHubAbi.Unpack(&badTransactionEvent, "BadTransactionSent", data)
	if err != nil {
		HandleError(ctx, id, err)
	}

	return badTransactionEvent.ErrorCode, badTransactionEvent.OriginalSender
}

// decodeRevertReason traduce el `output` de un revert a un string legible.
// Soporta Error(string) y Panic(uint256); para custom errors devuelve el selector + hex.
func decodeRevertReason(output []byte) string {
	if len(output) == 0 {
		return "execution reverted (sin motivo)"
	}
	if len(output) < 4 {
		return "execution reverted: " + hexutil.Encode(output)
	}

	selector := hexutil.Encode(output[:4])
	switch selector {
	case "0x08c379a0": // Error(string)
		// layout: [4:36]=offset, [36:68]=length, [68:68+length]=string
		if len(output) >= 68 {
			length := new(big.Int).SetBytes(output[36:68]).Uint64()
			if uint64(len(output)) >= 68+length {
				return "execution reverted: " + string(output[68:68+length])
			}
		}
	case "0x4e487b71": // Panic(uint256)
		if len(output) >= 36 {
			code := new(big.Int).SetBytes(output[4:36])
			return fmt.Sprintf("execution reverted: panic(0x%x)", code)
		}
	}
	// Custom errors conocidos (OZ v5): se decodifican por catálogo de selectores.
	if decoded := decodeKnownCustomError(selector, output); decoded != "" {
		return "execution reverted: " + decoded
	}
	// Custom error desconocido: no se puede decodificar sin su ABI; se muestra el selector.
	return "execution reverted (custom error " + selector + "): " + hexutil.Encode(output)
}

// knownCustomErrors mapea selectores de errores comunes (OpenZeppelin v5) a su nombre y tipos de args.
// Tipos soportados para formatear: "address" y "uint256" (cada arg ocupa una palabra de 32 bytes).
var knownCustomErrors = map[string]struct {
	name string
	args []string
}{
	"0xe450d38c": {"ERC20InsufficientBalance", []string{"address", "uint256", "uint256"}},
	"0xfb8f41b2": {"ERC20InsufficientAllowance", []string{"address", "uint256", "uint256"}},
	"0x96c6fd1e": {"ERC20InvalidSender", []string{"address"}},
	"0xec442f05": {"ERC20InvalidReceiver", []string{"address"}},
	"0xe602df05": {"ERC20InvalidApprover", []string{"address"}},
	"0x94280d62": {"ERC20InvalidSpender", []string{"address"}},
	"0x118cdaa7": {"OwnableUnauthorizedAccount", []string{"address"}},
	"0x1e4fbdf7": {"OwnableInvalidOwner", []string{"address"}},
}

// decodeKnownCustomError formatea un custom error conocido como "Nombre(arg0, arg1, ...)".
// Devuelve "" si el selector no está catalogado o los datos no alcanzan.
func decodeKnownCustomError(selector string, output []byte) string {
	def, ok := knownCustomErrors[selector]
	if !ok {
		return ""
	}
	args := output[4:]
	if len(args) < len(def.args)*32 {
		return ""
	}
	parts := make([]string, 0, len(def.args))
	for i, t := range def.args {
		word := args[i*32 : (i+1)*32]
		switch t {
		case "address":
			parts = append(parts, common.BytesToAddress(word).Hex())
		default: // uint256
			parts = append(parts, new(big.Int).SetBytes(word).String())
		}
	}
	return def.name + "(" + strings.Join(parts, ", ") + ")"
}

// hubRejected registra que el RelayHub rechazo una metatx ya enviada.
//
// Es el escenario que rompe la cadena de nonces: el hub NO consumio el nonce del usuario, asi que
// todas las metatx que se encadenaron despues quedaron invalidas. El contexto trae el metaTxId de
// la metatx original aunque este receipt se este procesando en otra peticion. Ver D11.
func hubRejected(ctx context.Context, transactionID string, from common.Address, errorCode uint8) {
	log.Warn(ctx, "relay.hub_rejected", map[string]interface{}{
		"transactionHash": "0x" + strings.TrimPrefix(transactionID, "0x"),
		"from":            from.Hex(),
		"errorCode":       errorCode,
		"errorCodeName":   errorCodeName(errorCode),
		"note":            "el nonce reservado se descarta: las metatx encadenadas despues de esta van a fallar",
	})
}

// errorCodeName traduce el enum ErrorCode de IRelayHub a un nombre legible.
func errorCodeName(code uint8) string {
	names := []string{
		"MaxBlockGasLimit", "BadOriginalSender", "BadNonce", "NotEnoughGas",
		"IsNotContract", "EmptyCode", "InvalidSignature", "InvalidDestination", "OK",
	}
	if int(code) < len(names) {
		return names[code]
	}
	return fmt.Sprintf("Unknown(%d)", code)
}

// ProcessNewBlocks se suscribe por WebSocket a las nuevas cabeceras y, ante caídas del nodo o del
// socket, RECONECTA con backoff exponencial en vez de terminar el proceso. Solo sale por `done`.
func (service *RelaySignerService) ProcessNewBlocks(done <-chan interface{}) {
	fmt.Println("Initiating process BLOCKSSS")

	const (
		initialBackoff = 1 * time.Second
		maxBackoff     = 30 * time.Second
	)
	backoff := initialBackoff

	for { // bucle externo: (re)conexión
		if isDone(done) {
			return
		}

		client := new(bl.Client)
		if err := client.Connect(service.Config.Application.WSURL); err != nil {
			log.GeneralLogger.Println("WS connect failed, retrying in", backoff, ":", err)
			if waitOrDone(done, backoff) {
				return
			}
			backoff = nextBackoff(backoff, maxBackoff)
			continue
		}

		headers := make(chan *types.Header)
		sub, err := client.GetEthclient().SubscribeNewHead(context.Background(), headers)
		if err != nil {
			log.GeneralLogger.Println("WS subscribe failed, retrying in", backoff, ":", err)
			client.Close()
			if waitOrDone(done, backoff) {
				return
			}
			backoff = nextBackoff(backoff, maxBackoff)
			continue
		}

		log.GeneralLogger.Println("subscribed to new heads (WS connected)")
		backoff = initialBackoff // reset tras una suscripción exitosa
		// Nota: la ventana de gas (GAS_LIMIT) se resincroniza sola con el próximo bloque (decrement()).

		reconnect := false
		for !reconnect { // bucle interno: procesar cabeceras hasta que el socket falle
			select {
			case err := <-sub.Err():
				log.GeneralLogger.Println("WebSocket failed, will reconnect:", err)
				reconnect = true
			case header := <-headers:
				log.GeneralLogger.Println("new block generated:", header.Hash().Hex())
				decrement()
			case <-done:
				log.GeneralLogger.Println("quit signal received...exiting from processing blocks")
				sub.Unsubscribe()
				client.Close()
				return
			}
		}

		sub.Unsubscribe()
		client.Close()
		if waitOrDone(done, backoff) {
			return
		}
		backoff = nextBackoff(backoff, maxBackoff)
	}
}

// isDone indica si ya se recibió la señal de apagado.
func isDone(done <-chan interface{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}

// waitOrDone duerme d, o retorna true si llega la señal de apagado durante la espera.
func waitOrDone(done <-chan interface{}, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

// nextBackoff duplica el backoff hasta un tope.
func nextBackoff(current, max time.Duration) time.Duration {
	next := current * 2
	if next > max {
		next = max
	}
	return next
}

func increment(gasLimit uint64) uint64 {
	lock.Lock()
	defer lock.Unlock()
	GAS_LIMIT = GAS_LIMIT + gasLimit
	log.GeneralLogger.Println("gasLimit used in currently block:", GAS_LIMIT)
	return GAS_LIMIT
}

func decrement() {
	lock.Lock()
	defer lock.Unlock()
	GAS_LIMIT = 0
	log.GeneralLogger.Println("gas limit was reseted to 0")
}

// senderKey normaliza la clave del caché de nonces. La misma address llega en distinto case según
// el camino: `eth_getTransactionCount` trae la string cruda del cliente (ethers la manda lowercase)
// y la escritura interna usa `message.From().Hex()` (checksum EIP-55); sin normalizar se crean
// entradas duplicadas que leen/escriben estados distintos.
func senderKey(from string) string {
	return strings.ToLower(from)
}

// nonceCacheTTL es la vida máxima de una entrada del caché (config `nonceCacheTTL` en segundos,
// default 300). Pasado el TTL la entrada se descarta y se vuelve al nonce real on-chain: es la red
// de seguridad si el caché diverge y el cliente nunca consulta el receipt de la tx fallida.
func (service *RelaySignerService) nonceCacheTTL() time.Duration {
	if service.Config != nil && service.Config.Application.NonceCacheTTL > 0 {
		return time.Duration(service.Config.Application.NonceCacheTTL) * time.Second
	}
	return 300 * time.Second
}

// cachedNonce devuelve el próximo nonce cacheado para `from`, si existe y no expiró.
func (service *RelaySignerService) cachedNonce(from string) (uint64, bool) {
	service.sendersLock.Lock()
	defer service.sendersLock.Unlock()
	key := senderKey(from)
	entry := service.senders[key]
	if entry == nil {
		return 0, false
	}
	if time.Since(entry.updatedAt) > service.nonceCacheTTL() {
		delete(service.senders, key)
		return 0, false
	}
	return entry.next, true
}

// invalidateNonce borra la entrada cacheada de `from`: la próxima lectura "pending" releerá el
// nonce real on-chain. Se llama al detectar BadTransactionSent en el receipt — esa tx NO consumió
// nonce en el RelayHub, así que el contador local quedó por delante del real y no puede corregirse
// solo (cada reintento lo alejaría +1 más, dejando la address bloqueada hasta reiniciar el servicio).
func (service *RelaySignerService) invalidateNonce(from string) {
	service.sendersLock.Lock()
	defer service.sendersLock.Unlock()
	delete(service.senders, senderKey(from))
	log.GeneralLogger.Println("nonce cache invalidated for sender:", from)
}

// incrementTransactionCount registra que `from` acaba de relayar una tx firmada con `nonce`: el
// próximo a usar es al menos nonce+1. Si el caché ya iba más adelante (otras tx en vuelo) se
// conserva; NO se suma +1 a ciegas — dos envíos que firmaron el MISMO nonce (colisión) solo pueden
// consumir uno on-chain, y el +1 incondicional dejaba el contador por delante del real para siempre.
func (service *RelaySignerService) incrementTransactionCount(from string, nonce uint64) {
	service.sendersLock.Lock()
	defer service.sendersLock.Unlock()
	key := senderKey(from)
	next := nonce + 1
	if entry := service.senders[key]; entry != nil && time.Since(entry.updatedAt) <= service.nonceCacheTTL() && entry.next > next {
		next = entry.next
	}
	service.senders[key] = &nonceEntry{next: next, updatedAt: time.Now()}
}

// HandleError
func HandleError(ctx context.Context, id json.RawMessage, err error) *rpc.JsonrpcMessage {
	log.GeneralLogger.Println(err.Error())
	result := new(rpc.JsonrpcMessage)
	result.ID = id
	return result.ErrorResponse(err)
}
