package service

import (
	"context"
	"crypto/ecdsa"
	"math/big"

	bl "github.com/LACNetNetworks/gas-relay-signer/blockchain"
	"github.com/LACNetNetworks/gas-relay-signer/errors"
	"github.com/LACNetNetworks/gas-relay-signer/model"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// ServiceInfo es lo que informa `GET /info`: que direcciones esta usando este nodo, de donde salio
// cada una, y con que parametros esta operando.
//
// Los numeros grandes van como texto, igual que en el relayer de referencia: el balance en wei
// excede lo que un entero de JavaScript representa sin perder precision, y un cliente escrito
// contra aquel espera leerlos asi.
//
// Los campos que pueden no tener valor son punteros a proposito: se emiten como nulos en lugar de
// omitirse, para que quien consume la respuesta distinga "no se pudo obtener" de "esta version no
// lo informa". Ver design.md, D6.
type ServiceInfo struct {
	NodeAddress          string  `json:"nodeAddress"`
	RelayHubAddress      *string `json:"relayHubAddress"`
	RelayHubSource       string  `json:"relayHubSource"`
	RelayHubProxyAddress *string `json:"relayHubProxyAddress"`
	ChainID              *string `json:"chainId"`
	RPCURL               string  `json:"rpcUrl"`
	NodeBalance          *string `json:"nodeBalance"`
	CurrentGasLimit      *string `json:"currentGasLimit"`

	AccountRulesAddress *string `json:"accountRulesAddress"`
	AccountRulesSource  *string `json:"accountRulesSource"`
	NodePermitted       *bool   `json:"nodePermitted"`
	EnforceAccountRules bool    `json:"enforceAccountRules"`

	// Este servicio no valida la expiracion del modelo de gas, asi que informa que no exige
	// ninguna ventana minima. No se omiten: un cliente que los lee tiene que poder saberlo.
	MinExpirationSeconds       int `json:"minExpirationSeconds"`
	ExpirationToleranceSeconds int `json:"expirationToleranceSeconds"`

	ReorderEnabled     bool `json:"reorderEnabled"`
	ReorderWindowMs    int  `json:"reorderWindowMs"`
	MaxInflightPerUser int  `json:"maxInflightPerUser"`
	ReceiptTimeoutMs   int  `json:"receiptTimeoutMs"`

	// El reparto de nonces: si el numero que el servicio entrega es de ese cliente o compartido, y
	// cuanto se espera la metatx que lo use.
	AutoNonce         bool `json:"autoNonce"`
	AutoNonceTicketMs int  `json:"autoNonceTicketMs"`
}

// Origen de cada direccion resuelta. Una direccion equivocada es indistinguible de una correcta si
// no se sabe de donde salio.
const (
	sourceProxy  = "proxy"
	sourceConfig = "config"
)

// NodeAddress es la direccion de la cuenta con la que este servicio firma las transacciones
// envolventes, derivada de la clave del writer node.
func (service *RelaySignerService) NodeAddress() (common.Address, error) {
	privateKey, err := crypto.HexToECDSA(service.Config.Application.Key)
	if err != nil {
		return common.Address{}, err
	}
	publicKey, ok := privateKey.Public().(*ecdsa.PublicKey)
	if !ok {
		return common.Address{}, errors.FailedKeyConfig.New("error casting public key to ECDSA", -32602)
	}
	return crypto.PubkeyToAddress(*publicKey), nil
}

// Info reune lo que informa `GET /info`.
//
// Ninguna consulta a la cadena que falle interrumpe la respuesta: ese campo queda sin valor y el
// resto llega igual. Es la ruta a la que se acude cuando algo anda mal, asi que no puede ser la
// primera en caerse.
func (service *RelaySignerService) Info(ctx context.Context) ServiceInfo {
	config := service.Config

	info := ServiceInfo{
		RelayHubSource:      sourceProxy,
		RPCURL:              config.Application.NodeURL,
		EnforceAccountRules: config.Security.PermissionsEnabled,
		ReorderEnabled:      config.Reorder.Enabled,
		ReorderWindowMs:     config.Reorder.WindowMs,
		MaxInflightPerUser:  config.Reorder.MaxInflightPerUser,
		ReceiptTimeoutMs:    config.Reorder.ReceiptTimeoutMs,
		AutoNonce:           config.Reorder.AutoNonce,
		AutoNonceTicketMs:   ticketMsInEffect(config.Reorder),
	}
	if config.Application.ContractAddress != "" {
		proxy := config.Application.ContractAddress
		info.RelayHubProxyAddress = &proxy
	}
	if config.Application.RelayHubContractAddress != nil {
		hub := config.Application.RelayHubContractAddress.Hex()
		info.RelayHubAddress = &hub
	}
	if config.Security.PermissionsEnabled && config.Security.AccountContractAddress != "" {
		rules := config.Security.AccountContractAddress
		source := sourceConfig
		info.AccountRulesAddress = &rules
		info.AccountRulesSource = &source
	}

	nodeAddress, err := service.NodeAddress()
	if err != nil {
		// Sin la direccion del nodo no hay nada que consultar en la cadena, pero lo que sale de la
		// configuracion se informa igual.
		return info
	}
	info.NodeAddress = nodeAddress.Hex()

	client := new(bl.Client)
	if err := client.Connect(config.Application.NodeURL); err != nil {
		return info
	}
	defer client.Close()

	if chainID, err := client.ChainID(ctx); err == nil {
		info.ChainID = text(chainID)
	}
	if balance, err := client.BalanceOf(ctx, nodeAddress); err == nil {
		info.NodeBalance = text(balance)
	}
	if config.Application.RelayHubContractAddress != nil {
		hub := *config.Application.RelayHubContractAddress
		if gasLimit, err := client.GetCurrentGasLimit(hub); err == nil {
			info.CurrentGasLimit = text(gasLimit)
		}
	}
	if info.AccountRulesAddress != nil {
		rules := common.HexToAddress(*info.AccountRulesAddress)
		if permitted, err := client.AccountPermitted(rules, nodeAddress); err == nil {
			info.NodePermitted = &permitted
		}
	}

	return info
}

func text(value *big.Int) *string {
	if value == nil {
		return nil
	}
	rendered := value.String()
	return &rendered
}

// ticketMsInEffect es el plazo del ticket que el servicio esta aplicando de verdad.
//
// Con el reparto apagado no hay ticket que vencer, y se informa cero. Publicar el plazo configurado
// cuando no rige haria que un cliente cuente con una serializacion que no existe, y ademas cambiaria
// la respuesta de `GET /info` respecto de la del binario anterior con la capacidad apagada.
func ticketMsInEffect(reorder model.ReorderConfig) int {
	if !reorder.AutoNonce {
		return 0
	}
	return reorder.AutoNonceTicketMs
}
