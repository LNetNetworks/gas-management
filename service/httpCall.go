package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/LACNetNetworks/gas-relay-signer/rpc"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

const DATA_CALL_RELAYHUB = "0x7bdf2ec7"

// getRelayHubContractAddress resuelve la address del RelayHub consultando su proxy vía eth_call.
// Se ejecuta en el arranque del servicio: toda ruta de fallo (red, error json-rpc, resultado
// vacío o malformado, decodificación ABI) retorna error para que el llamador lo traduzca a
// FailedKeyConfig (-32610); nunca debe hacer panic ni continuar con datos inválidos.
func getRelayHubContractAddress(rpcURL string, id string, relayHubProxyAddress string, _timeout int) (*common.Address, error) {
	data := fmt.Sprintf(`{"jsonrpc":"2.0","method":"eth_call","params":[{"to":"%s","data":"%s"},"latest"], "id":"%s"}`, relayHubProxyAddress, DATA_CALL_RELAYHUB, id)

	requestBody := []byte(data)

	timeout := time.Duration(time.Duration(_timeout) * time.Second)
	client := http.Client{
		Timeout: timeout,
	}

	request, err := http.NewRequest("POST", rpcURL, bytes.NewBuffer(requestBody))
	if err != nil {
		return nil, err
	}

	request.Header.Set("Content-type", "application/json")

	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}

	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)

	if err != nil {
		return nil, err
	}

	rdr1 := io.NopCloser(bytes.NewBuffer(body))

	var rpcMessage rpc.JsonrpcMessage

	err = json.NewDecoder(rdr1).Decode(&rpcMessage)
	if err != nil {
		return nil, err
	}

	if rpcMessage.Error != nil {
		return nil, fmt.Errorf("node returned a json-rpc error resolving the relayHub address: %s", rpcMessage.Error.Error())
	}

	var resultHex string
	if err := json.Unmarshal(rpcMessage.Result, &resultHex); err != nil {
		return nil, fmt.Errorf("can't decode eth_call result as a hex string: %w", err)
	}

	resultHex = strings.TrimPrefix(resultHex, "0x")

	// Una address codificada en ABI es exactamente una palabra de 32 bytes (64 chars hex).
	if len(resultHex) != 64 {
		return nil, fmt.Errorf("unexpected eth_call result length resolving the relayHub address: got %d hex chars, want 64", len(resultHex))
	}

	responseData := common.Hex2Bytes(resultHex)

	addressPacked, err := abi.NewType("address", "", nil)
	if err != nil {
		return nil, err
	}

	resultPayloadPacked := abi.Arguments{
		{Type: addressPacked},
	}

	addressUnpacked, err := resultPayloadPacked.UnpackValues(responseData)
	if err != nil {
		return nil, err
	}

	if len(addressUnpacked) == 0 {
		return nil, fmt.Errorf("eth_call result unpacked to no values resolving the relayHub address")
	}

	relayHubAddress, ok := addressUnpacked[0].(common.Address)
	if !ok {
		return nil, fmt.Errorf("unexpected type %T resolving the relayHub address, want common.Address", addressUnpacked[0])
	}

	return &relayHubAddress, nil
}
