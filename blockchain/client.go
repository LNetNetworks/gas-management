package blockchain

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"strings"

	log "github.com/LACNetNetworks/gas-relay-signer/audit"
	relay "github.com/LACNetNetworks/gas-relay-signer/blockchain/contracts"
	"github.com/LACNetNetworks/gas-relay-signer/errors"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

const (
	relayMetaTxMethod  = "relayMetaTx"
	deployMetaTxMethod = "deployMetaTx"
)

// Client to manage connection to Ethereum
type Client struct {
	client *ethclient.Client
}

// GetEthclient ...
func (ec *Client) GetEthclient() *ethclient.Client {
	return ec.client
}

// Connect to Ethereum
func (ec *Client) Connect(nodeURL string) error {
	client, err := ethclient.Dial(nodeURL)
	if err != nil {
		msg := fmt.Sprintf("Can't connect to node %s", nodeURL)
		err = errors.FailedConnection.Wrapf(err, msg, -32100)
		return err
	}

	log.GeneralLogger.Println("Connected to Ethereum Node:", nodeURL)
	ec.client = client
	return nil
}

// Close ethereum connection
func (ec *Client) Close() {
	ec.client.Close()
}

// ConfigTransaction from ethereum address contract
func (ec *Client) ConfigTransaction(key *ecdsa.PrivateKey, gasLimit uint64, pending bool) (*bind.TransactOpts, error) {
	auth := bind.NewKeyedTransactor(key)

	var nonce uint64
	var err error

	if pending {
		nonce, err = ec.client.PendingNonceAt(context.Background(), auth.From)
		if err != nil {
			msg := fmt.Sprintf("can't get pending nonce for:%s", auth.From)
			err = errors.FailedConfigTransaction.Wrapf(err, msg, -32603)
			return nil, err
		}
	} else {
		nonce, err = ec.client.NonceAt(context.Background(), auth.From, nil)
		if err != nil {
			msg := fmt.Sprintf("can't get latest nonce for:%s", auth.From)
			err = errors.FailedConfigTransaction.Wrapf(err, msg, -32604)
			return nil, err
		}
	}

	auth.Nonce = big.NewInt(int64(nonce))
	auth.Value = big.NewInt(0) // in wei
	auth.GasLimit = gasLimit   // in units
	auth.GasPrice = big.NewInt(0)

	log.GeneralLogger.Printf("OptionsTransaction=[From:0x%x,nonce:%d,gasPrice:%s,gasLimit:%d", auth.From, nonce, auth.GasPrice, auth.GasLimit)

	return auth, nil
}

// SendMetatransaction into blockchain
// SendMetatransaction envuelve la metatx en una llamada al RelayHub, la firma con la clave del
// nodo y la difunde.
//
// Devuelve la transaccion enviada y no solo su hash: el nonce de la CUENTA del writer node vive
// ahi, es el que traba el txpool si algo se pierde, y el unico dato con el que se puede desatascar
// la cola desde el nodo. El llamador sigue respondiendo el hash al cliente. Ver design.md, D13.
func (ec *Client) SendMetatransaction(contractAddress common.Address, options *bind.TransactOpts, to *common.Address, signingData []byte, v uint8, r [32]byte, s [32]byte) (*types.Transaction, error) {
	contract, err := relay.NewRelay(contractAddress, ec.client)
	if err != nil {
		msg := fmt.Sprintf("can't instance RelayHub contract %s", contractAddress)
		err = errors.FailedContract.Wrapf(err, msg, -32603)
		return nil, err
	}

	log.GeneralLogger.Println("RelayHub Contract instanced:", contractAddress.Hex())
	log.GeneralLogger.Println("Metatransaction signingData:", hexutil.Encode(signingData))

	var tx *types.Transaction

	if to != nil {
		tx, err = contract.RelayMetaTx(options, new(big.Int).SetUint64(options.GasLimit), signingData, v, r, s)
	} else {
		tx, err = contract.DeployMetaTx(options, new(big.Int).SetUint64(options.GasLimit), signingData, v, r, s)
	}
	if err != nil {
		msg := "failed executing smart contract"
		err = errors.FailedTransaction.Wrapf(err, msg, -32603)
		return nil, err
	}

	if len(tx.Hash()) == 0 {
		msg := "failed executing transaction relay Metatransaction"
		err = errors.FailedTransaction.Wrapf(err, msg, -32603)
		return nil, err
	}
	log.GeneralLogger.Printf("MetaTransaction sent: %s", tx.Hash().Hex())

	return tx, nil
}

func (ec *Client) GenerateTransaction(options *bind.TransactOpts, to *common.Address, relayAddress common.Address, signingData []byte, v uint8, r, s [32]byte) (*types.Transaction, error) {
	testabi, err := abi.JSON(strings.NewReader(relay.RelayABI))
	if err != nil {
		msg := "Error decoding ABI"
		err = errors.FailedTransaction.Wrapf(err, msg, -32603)
		return nil, err
	}

	var bytesData []byte

	if to != nil {
		bytesData, err = testabi.Pack(relayMetaTxMethod, signingData, v, r, s)
	} else {
		fmt.Println("DEPLOYYYY")
		bytesData, err = testabi.Pack(deployMetaTxMethod, signingData, v, r, s)
	}

	if err != nil {
		msg := "Error encoding transaction"
		err = errors.FailedTransaction.Wrapf(err, msg, -32603)
		return nil, err
	}

	tx := types.NewTransaction(options.Nonce.Uint64(), relayAddress, big.NewInt(0), options.GasLimit, big.NewInt(0), bytesData)
	return tx, nil
}

// GetTransactionReceipt ...
func (ec *Client) GetTransactionReceipt(transactionHash common.Hash) (*types.Receipt, error) {
	receipt, err := ec.client.TransactionReceipt(context.Background(), transactionHash)
	if err != nil {
		msg := fmt.Sprintf("failed get transaction receipt %s", transactionHash.Hex())
		err = errors.CallBlockchainFailed.Wrapf(err, msg, -32603)
		return nil, err
	}

	receipt.PostState = []byte{0x0}

	log.GeneralLogger.Printf("Receipt of Tx:%s was returned with Status:%d in blockNumber:%s", transactionHash.Hex(), receipt.Status, receipt.BlockNumber)

	return receipt, nil
}

// GetTransactionCount ...
func (ec *Client) GetTransactionCount(contractAddress common.Address, address common.Address, nodeAddress common.Address) (*big.Int, error) {
	contract, err := relay.NewRelay(contractAddress, ec.client)
	if err != nil {
		msg := fmt.Sprintf("can't instance RelayHub contract %s", contractAddress)
		err = errors.FailedContract.Wrapf(err, msg, -32603)
		return nil, err
	}

	log.GeneralLogger.Println("RelayHub Contract instanced:", contractAddress.Hex())

	count, err := contract.GetNonce(&bind.CallOpts{From: nodeAddress}, address)

	if err != nil {
		msg := fmt.Sprintf("failed get transaction count for %s", address.Hex())
		err = errors.CallBlockchainFailed.Wrapf(err, msg, -32603)
		return nil, err
	}

	return count, nil
}

// DecreaseGasUsed into blockchain
func (ec *Client) DecreaseGasUsed(contractAddress common.Address, options *bind.TransactOpts, gasUsed *big.Int) (*common.Hash, error) {
	contract, err := relay.NewRelay(contractAddress, ec.client)
	if err != nil {
		msg := fmt.Sprintf("can't instance RelayHub contract %s", contractAddress)
		err = errors.FailedContract.Wrapf(err, msg, -32603)
		return nil, err
	}

	log.GeneralLogger.Println("RelayHub Contract instanced:", contractAddress.Hex())

	var tx *types.Transaction

	tx, err = contract.IncreaseGasUsed(options, gasUsed)

	if err != nil {
		msg := "failed executing contract"
		err = errors.FailedTransaction.Wrapf(err, msg, -32603)
		return nil, err
	}

	if len(tx.Hash()) == 0 {
		msg := "failed executing transaction decrease gas"
		err = errors.FailedTransaction.Wrapf(err, msg, -32603)
		return nil, err
	}
	log.GeneralLogger.Printf("Decreased Gas Tx sent: %s", tx.Hash().Hex())

	transactionHash := tx.Hash()

	return &transactionHash, nil
}

// GetBlockByNumber ...
func (ec *Client) GetBlockByNumber(contractAddress common.Address, blockNumber *big.Int) (*types.Header, uint64, error) {
	block, err := ec.client.HeaderByNumber(context.Background(), blockNumber)
	if err != nil {
		msg := fmt.Sprintf("failed get block by number %d", blockNumber.Uint64())
		err = errors.CallBlockchainFailed.Wrapf(err, msg, -32603)
		return nil, 0, err
	}

	gasLimit, err := ec.GetMaxBlockGasLimit(contractAddress)
	if err != nil {
		return nil, 0, err
	}

	return block, gasLimit.Uint64(), nil
}

// GetGasLimit ...
func (ec *Client) GetGasLimit(contractAddress, nodeAddress common.Address) (*big.Int, error) {
	contract, err := relay.NewRelay(contractAddress, ec.client)
	if err != nil {
		msg := fmt.Sprintf("can't instance RelayHub contract %s", contractAddress)
		err = errors.FailedContract.Wrapf(err, msg, -32603)
		return nil, err
	}

	log.GeneralLogger.Println("RelayHub Contract instanced:", contractAddress.Hex())

	gasLimit, err := contract.GetGasLimit(&bind.CallOpts{Pending: true, From: nodeAddress})

	if err != nil {
		msg := fmt.Sprintf("failed get gasLimit from %s", contractAddress.Hex())
		err = errors.CallBlockchainFailed.Wrapf(err, msg, -32603)
		return nil, err
	}

	return gasLimit, nil
}

// GetMaxBlockGasLimit ...
func (ec *Client) GetMaxBlockGasLimit(contractAddress common.Address) (*big.Int, error) {
	contract, err := relay.NewRelay(contractAddress, ec.client)
	if err != nil {
		msg := fmt.Sprintf("can't instance RelayHub contract %s", contractAddress)
		err = errors.FailedContract.Wrapf(err, msg, -32603)
		return nil, err
	}

	log.GeneralLogger.Println("RelayHub Contract instanced:", contractAddress.Hex())

	gasLimit, err := contract.GetMaxGasBlockLimit(&bind.CallOpts{})

	if err != nil {
		msg := fmt.Sprintf("failed get max block gasLimit from %s", contractAddress.Hex())
		err = errors.CallBlockchainFailed.Wrapf(err, msg, -32603)
		return nil, err
	}

	return gasLimit, nil
}

// GetNodeGasLimit ...
func (ec *Client) GetNodeGasLimit(contractAddress, nodeAddress common.Address) (*big.Int, error) {
	contract, err := relay.NewRelay(contractAddress, ec.client)
	if err != nil {
		msg := fmt.Sprintf("can't instance RelayHub contract %s", contractAddress)
		err = errors.FailedContract.Wrapf(err, msg, -32603)
		return nil, err
	}

	log.GeneralLogger.Println("RelayHub Contract instanced:", contractAddress.Hex())

	nodeGasLimit, err := contract.GetNodeGasLimit(&bind.CallOpts{}, nodeAddress)

	if err != nil {
		msg := fmt.Sprintf("failed get node gasLimit from %s", contractAddress.Hex())
		err = errors.CallBlockchainFailed.Wrapf(err, msg, -32603)
		return nil, err
	}

	return nodeGasLimit, nil
}

// GetMaxBlockGasLimit ...
func (ec *Client) GetCurrentGasLimit(contractAddress common.Address) (*big.Int, error) {
	contract, err := relay.NewRelay(contractAddress, ec.client)
	if err != nil {
		msg := fmt.Sprintf("can't instance RelayHub contract %s", contractAddress)
		err = errors.FailedContract.Wrapf(err, msg, -32603)
		return nil, err
	}

	log.GeneralLogger.Println("RelayHub Contract instanced:", contractAddress.Hex())

	gasLimit, err := contract.GetCurrentGasLimit(&bind.CallOpts{})

	if err != nil {
		msg := fmt.Sprintf("failed get current gasLimit from %s", contractAddress.Hex())
		err = errors.CallBlockchainFailed.Wrapf(err, msg, -32603)
		return nil, err
	}

	return gasLimit, nil
}

// AccountPermitted ...
func (ec *Client) AccountPermitted(contractAddress, senderAddress common.Address) (bool, error) {
	contract, err := relay.NewAccount(contractAddress, ec.client)
	if err != nil {
		msg := fmt.Sprintf("can't instance RelayHub contract %s", contractAddress)
		err = errors.FailedContract.Wrapf(err, msg, -32603)
		return false, err
	}

	log.GeneralLogger.Println("AccountPermissioning Contract instanced:", contractAddress.Hex())

	isPermitted, err := contract.AccountPermitted(&bind.CallOpts{}, senderAddress)

	if err != nil {
		msg := fmt.Sprintf("failed to know if account is permitted from %s", contractAddress.Hex())
		err = errors.CallBlockchainFailed.Wrapf(err, msg, -32603)
		return false, err
	}

	return isPermitted, nil
}
