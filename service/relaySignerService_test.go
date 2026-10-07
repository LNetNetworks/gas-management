package service

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	audit "github.com/LACNetNetworks/gas-relay-signer/audit"
	"github.com/LACNetNetworks/gas-relay-signer/events"
	"github.com/LACNetNetworks/gas-relay-signer/model"
	"github.com/LACNetNetworks/gas-relay-signer/rpc"
	"github.com/ethereum/go-ethereum/common"
)

var sequence uint8 = 0

// TestMain sube el nivel de log para que los eventos de operacion no ensucien la salida de los
// tests. No afecta a lo que se verifica: los tests leen el bus, y el nivel regula la consola y no
// la observabilidad.
func TestMain(m *testing.M) {
	audit.InitStructured("error", false)
	os.Exit(m.Run())
}

func TestInit(t *testing.T) {
	srv := serverMock()
	defer srv.Close()

	dir, _ := os.Getwd()

	createKeyMock(dir + "/keyMock")
	setKeyMock()

	applicationConfig := model.ApplicationConfig{NodeURL: srv.URL + "/getRelayHubContract", NodeKeyPath: dir + "/keyMock"}
	applicationConfig.ContractAddress = "0xdD37c69fF29C4b93A346Ed6dF184f48A71800b7E"
	config := model.Config{Application: applicationConfig}
	relaySignerService := new(RelaySignerService)

	relaySignerService.Init(&config)

	err := os.RemoveAll(dir + "/log")
	if err != nil {
		log.Fatal(err)
	}

	err = os.Remove("keyMock")
	if err != nil {
		log.Fatal(err)
	}

	if relaySignerService.Config.Application.Key != "b3e7374dca5ca90c3899dbb2c978051437fb15534c945bf59df16d6c80be27c0" {
		t.Errorf("Private Key wasn't loaded from file")
	}

	if relaySignerService.Config.Application.RelayHubContractAddress.Hex() != "0xfF6D55d01FB12695EA00c071aD8aF3CE44cF3A91" {
		t.Errorf("RelayHub Smart Contract can't be loaded from Proxy")
	}
}

func TestFailInit(t *testing.T) {
	applicationConfig := model.ApplicationConfig{NodeKeyPath: "./keyMock"}
	config := model.Config{Application: applicationConfig}
	relaySignerService := new(RelaySignerService)

	os.Unsetenv("WRITER_KEY")

	err := relaySignerService.Init(&config)

	if err.Error() != "Environment variable WRITER_KEY not set" {
		t.Errorf("A environment variable shouldn't be set")
	}
}

func TestGetTransactionCount(t *testing.T) {
	srv := serverMock()
	defer srv.Close()

	contents := []byte(`{"jsonrpc":"2.0","method":"eth_getTransactionCount","params":["0x92c9885663f6e84127c857d3137936c424b7e07555d2bc7d8bd781b3f0847ac8"],"id":53}`)

	var rpcMessage rpc.JsonrpcMessage
	_ = json.Unmarshal(contents, &rpcMessage)

	var params []string
	_ = json.Unmarshal(rpcMessage.Params, &params)

	applicationConfig := model.ApplicationConfig{NodeURL: srv.URL + "/getTransactionCount", Key: "b3e7374dca5ca90c3899dbb2c978051437fb15534c945bf59df16d6c80be27c0"}
	config := model.Config{Application: applicationConfig}

	relaySignerService := new(RelaySignerService)
	_ = relaySignerService.Init(&config)
	relayHubAddress := common.HexToAddress("0xdD37c69fF29C4b93A346Ed6dF184f48A71800b7E")
	relaySignerService.Config.Application.RelayHubContractAddress = &relayHubAddress
	relaySignerService.Config.Application.ContractAddress = "0xdD37c69fF29C4b93A346Ed6dF184f48A71800b7E"
	jsonResponse := relaySignerService.GetTransactionCount(context.Background(), rpcMessage.ID, params[0], false)

	fmt.Println(jsonResponse)

	if jsonResponse.String() != `{"jsonrpc":"2.0","id":53,"result":"0x159"}` {
		t.Errorf("Incorrect nonce was gotten")
	}
}

func TestGetTransactionCountLatest(t *testing.T) {
	srv := serverMock()
	defer srv.Close()

	contents := []byte(`{"jsonrpc":"2.0","method":"eth_getTransactionCount","params":["0x92c9885663f6e84127c857d3137936c424b7e07555d2bc7d8bd781b3f0847ac8","latest"],"id":53}`)

	var rpcMessage rpc.JsonrpcMessage
	_ = json.Unmarshal(contents, &rpcMessage)

	var params []string
	_ = json.Unmarshal(rpcMessage.Params, &params)

	applicationConfig := model.ApplicationConfig{NodeURL: srv.URL + "/getTransactionCount", Key: "b3e7374dca5ca90c3899dbb2c978051437fb15534c945bf59df16d6c80be27c0"}
	config := model.Config{Application: applicationConfig}

	relaySignerService := new(RelaySignerService)
	_ = relaySignerService.Init(&config)
	relayHubAddress := common.HexToAddress("0xdD37c69fF29C4b93A346Ed6dF184f48A71800b7E")
	relaySignerService.Config.Application.RelayHubContractAddress = &relayHubAddress
	relaySignerService.Config.Application.ContractAddress = "0xdD37c69fF29C4b93A346Ed6dF184f48A71800b7E"
	jsonResponse := relaySignerService.GetTransactionCount(context.Background(), rpcMessage.ID, params[0], false)

	fmt.Println(jsonResponse)

	if jsonResponse.String() != `{"jsonrpc":"2.0","id":53,"result":"0x159"}` {
		t.Errorf("Incorrect nonce was gotten")
	}
}

func TestGetTransactionCountPending(t *testing.T) {
	srv := serverMock()
	defer srv.Close()

	contents := []byte(`{"jsonrpc":"2.0","method":"eth_getTransactionCount","params":["0x92c9885663f6e84127c857d3137936c424b7e07555d2bc7d8bd781b3f0847ac8","pending"],"id":53}`)

	var rpcMessage rpc.JsonrpcMessage
	_ = json.Unmarshal(contents, &rpcMessage)

	var params []string
	_ = json.Unmarshal(rpcMessage.Params, &params)

	applicationConfig := model.ApplicationConfig{NodeURL: srv.URL + "/getTransactionCount", ContractAddress: "0xdD37c69fF29C4b93A346Ed6dF184f48A71800b7E"}
	config := model.Config{Application: applicationConfig}
	relaySignerService := new(RelaySignerService)
	_ = relaySignerService.Init(&config)
	relaySignerService.senders = make(map[string]*nonceEntry)
	relaySignerService.senders["0x92c9885663f6e84127c857d3137936c424b7e07555d2bc7d8bd781b3f0847ac8"] = &nonceEntry{next: 200, updatedAt: time.Now()}
	jsonResponse := relaySignerService.GetTransactionCount(context.Background(), rpcMessage.ID, params[0], true)

	if jsonResponse.String() != `{"jsonrpc":"2.0","id":53,"result":"0xc8"}` {
		t.Errorf("Incorrect nonce was gotten")
	}
}

func TestGetTransactionCountPendingNoValue(t *testing.T) {
	srv := serverMock()
	defer srv.Close()

	contents := []byte(`{"jsonrpc":"2.0","method":"eth_getTransactionCount","params":["0x92c9885663f6e84127c857d3137936c424b7e07555d2bc7d8bd781b3f0847ac8","pending"],"id":53}`)

	var rpcMessage rpc.JsonrpcMessage
	_ = json.Unmarshal(contents, &rpcMessage)

	var params []string
	_ = json.Unmarshal(rpcMessage.Params, &params)

	applicationConfig := model.ApplicationConfig{NodeURL: srv.URL + "/getTransactionCount", ContractAddress: "0xdD37c69fF29C4b93A346Ed6dF184f48A71800b7E", Key: "b3e7374dca5ca90c3899dbb2c978051437fb15534c945bf59df16d6c80be27c0"}
	config := model.Config{Application: applicationConfig}
	relaySignerService := new(RelaySignerService)
	_ = relaySignerService.Init(&config)
	relayHubAddress := common.HexToAddress("0xdD37c69fF29C4b93A346Ed6dF184f48A71800b7E")
	relaySignerService.Config.Application.RelayHubContractAddress = &relayHubAddress
	relaySignerService.Config.Application.ContractAddress = "0xdD37c69fF29C4b93A346Ed6dF184f48A71800b7E"
	relaySignerService.senders = make(map[string]*nonceEntry)
	jsonResponse := relaySignerService.GetTransactionCount(context.Background(), rpcMessage.ID, params[0], true)

	if jsonResponse.String() != `{"jsonrpc":"2.0","id":53,"result":"0x159"}` {
		t.Errorf("Incorrect nonce was gotten")
	}
}

func TestGetTransactionReceipt(t *testing.T) {
	srv := serverMock()
	defer srv.Close()

	contents := []byte(`{"jsonrpc":"2.0","method":"eth_getTransactionReceipt","params":["0x504ce587a65bdbdb6414a0c6c16d86a04dd79bfcc4f2950eec9634b30ce5370f"],"id":53}`)

	var rpcMessage rpc.JsonrpcMessage
	_ = json.Unmarshal(contents, &rpcMessage)

	var params []string
	_ = json.Unmarshal(rpcMessage.Params, &params)

	applicationConfig := model.ApplicationConfig{NodeURL: srv.URL + "/getReceipt"}
	config := model.Config{Application: applicationConfig}
	relaySignerService := new(RelaySignerService)
	_ = relaySignerService.Init(&config)
	jsonResponse := relaySignerService.GetTransactionReceipt(context.Background(), rpcMessage.ID, params[0])

	var result map[string]interface{}
	json.Unmarshal([]byte(jsonResponse.String()), &result)

	blockHash := result["result"].(map[string]interface{})

	if blockHash["blockHash"] != "0x6e3aa24e261e61832624749b64049104c6105ba870d3375484548ffdb133eeea" {
		t.Errorf("Incorrect blockHash was gotten")
	}
}

func TestGetTransactionReceiptRevertReason(t *testing.T) {
	srv := serverMock()
	defer srv.Close()

	contents := []byte(`{"jsonrpc":"2.0","method":"eth_getTransactionReceipt","params":["0x504ce587a65bdbdb6414a0c6c16d86a04dd79bfcc4f2950eec9634b30ce5370f"],"id":53}`)

	var rpcMessage rpc.JsonrpcMessage
	_ = json.Unmarshal(contents, &rpcMessage)

	var params []string
	_ = json.Unmarshal(rpcMessage.Params, &params)

	applicationConfig := model.ApplicationConfig{NodeURL: srv.URL + "/getReceiptRevertReason"}
	config := model.Config{Application: applicationConfig}
	relaySignerService := new(RelaySignerService)
	_ = relaySignerService.Init(&config)
	jsonResponse := relaySignerService.GetTransactionReceipt(context.Background(), rpcMessage.ID, params[0])

	var result map[string]interface{}
	json.Unmarshal([]byte(jsonResponse.String()), &result)

	blockHash := result["result"].(map[string]interface{})

	// El revertReason se devuelve DECODIFICADO a texto legible (Error(string)), no en hex crudo.
	if blockHash["revertReason"] != "execution reverted: nabucodos" {
		t.Errorf("Incorrect revert reason was gotten: %v", blockHash["revertReason"])
	}
}

// TestGetTransactionReceiptFailedDeploy verifica la detección del fallo silencioso en DEPLOY:
// el receipt trae el evento Relayed (verificación OK) pero NO ContractDeployed/TransactionRelayed/
// BadTransactionSent (el CREATE interno revirtió en el constructor) → se expone como fallo.
func TestGetTransactionReceiptFailedDeploy(t *testing.T) {
	srv := serverMock()
	defer srv.Close()

	contents := []byte(`{"jsonrpc":"2.0","method":"eth_getTransactionReceipt","params":["0x504ce587a65bdbdb6414a0c6c16d86a04dd79bfcc4f2950eec9634b30ce5370f"],"id":53}`)

	var rpcMessage rpc.JsonrpcMessage
	_ = json.Unmarshal(contents, &rpcMessage)

	var params []string
	_ = json.Unmarshal(rpcMessage.Params, &params)

	applicationConfig := model.ApplicationConfig{NodeURL: srv.URL + "/getReceiptFailedDeploy"}
	config := model.Config{Application: applicationConfig}
	relaySignerService := new(RelaySignerService)
	_ = relaySignerService.Init(&config)
	jsonResponse := relaySignerService.GetTransactionReceipt(context.Background(), rpcMessage.ID, params[0])

	var result map[string]interface{}
	json.Unmarshal([]byte(jsonResponse.String()), &result)

	blockHash := result["result"].(map[string]interface{})

	if blockHash["revertReason"] != "deploy reverted: contract constructor failed (no code created)" {
		t.Errorf("Expected failed-deploy revertReason, got: %v", blockHash["revertReason"])
	}
	if blockHash["status"] != "0x0" {
		t.Errorf("Expected status 0x0 for failed deploy, got: %v", blockHash["status"])
	}
}

func TestSendMetatransaction(t *testing.T) {
	srv := serverMock()
	defer srv.Close()

	contents := []byte(`{"id":2914410858336929,"jsonrpc":"2.0","params":["0xf8840180831e8480946e6bbf31aa45042d53128339383fcd1c377b42c780a46057361d00000000000000000000000000000000000000000000000000000000000001591ba028934b543809922b277e85f6bcf7b1f25e937de05c5138e17fdfa480ba74e84ba055a2a611763ffcb748547408093551928c9549f95a0a9cabd3b1f1f2e166cc16"],"method":"eth_sendRawTransaction"}`)

	var rpcMessage rpc.JsonrpcMessage
	_ = json.Unmarshal(contents, &rpcMessage)

	var params []string
	_ = json.Unmarshal(rpcMessage.Params, &params)

	dir, _ := os.Getwd()

	createKeyMock(dir + "/keyMock")
	setKeyMock()

	applicationConfig := model.ApplicationConfig{NodeURL: srv.URL + "/getRelayHubContract", ContractAddress: "0x0ae2Da68515Ef8DC4bBCa1fA1bcE00C508b2Af4B", NodeKeyPath: dir + "/keyMock"}
	config := model.Config{Application: applicationConfig}
	relaySignerService := new(RelaySignerService)
	_ = relaySignerService.Init(&config)

	relaySignerService.Config.Application.NodeURL = srv.URL + "/sendMetatransaction"

	to := common.HexToAddress("0x82a978b3f5962a5b0957d9ee9eef472ee55b42f1")

	encodedFunction, _ := hex.DecodeString("0xf861808082ea6094fd32cfc2e71611626d6368a41f915d0077a306a180b8446057361d000000000000000000000000000000000000000000000000000000000000003c000000000000000000000000173cf75f0905338597fcd38f5ce13e6840b230e9")

	var gasLimit uint64
	var r [32]byte
	var s [32]byte

	gasLimit = 200000

	sender := "0x92c9885663f6e84127c857d3137936c424b7e07555d2bc7d8bd781b3f0847ac8"

	jsonResponse := relaySignerService.SendMetatransaction(context.Background(), rpcMessage.ID, &to, gasLimit, encodedFunction, 27, r, s, sender, 34)

	err := os.Remove("keyMock")
	if err != nil {
		log.Fatal(err)
	}

	log.Println(jsonResponse.String())

	if jsonResponse.String() != `{"jsonrpc":"2.0","id":2914410858336929,"result":"0x9c2fb4956ce18491021a534106fe50e7cfe86bcc373b1626623fa0366f4cc3bc"}` {
		t.Errorf("Incorrect transactionHash was gotten")
	}
}

func TestNonceAfterTransactions(t *testing.T) {
	srv := serverMock()
	defer srv.Close()

	contents := []byte(`{"id":2914410858336929,"jsonrpc":"2.0","params":["0xf8840180831e8480946e6bbf31aa45042d53128339383fcd1c377b42c780a46057361d00000000000000000000000000000000000000000000000000000000000001591ba028934b543809922b277e85f6bcf7b1f25e937de05c5138e17fdfa480ba74e84ba055a2a611763ffcb748547408093551928c9549f95a0a9cabd3b1f1f2e166cc16"],"method":"eth_sendRawTransaction"}`)

	var rpcMessage rpc.JsonrpcMessage
	_ = json.Unmarshal(contents, &rpcMessage)

	var params []string
	_ = json.Unmarshal(rpcMessage.Params, &params)

	dir, _ := os.Getwd()

	createKeyMock(dir + "/keyMock")
	setKeyMock()

	applicationConfig := model.ApplicationConfig{NodeURL: srv.URL + "/getRelayHubContract", ContractAddress: "0x0ae2Da68515Ef8DC4bBCa1fA1bcE00C508b2Af4B", NodeKeyPath: dir + "/keyMock"}
	config := model.Config{Application: applicationConfig}
	relaySignerService := new(RelaySignerService)
	_ = relaySignerService.Init(&config)

	relaySignerService.Config.Application.NodeURL = srv.URL + "/sendMetatransaction"

	to := common.HexToAddress("0x82a978b3f5962a5b0957d9ee9eef472ee55b42f1")

	encodedFunction, _ := hex.DecodeString("0xf861808082ea6094fd32cfc2e71611626d6368a41f915d0077a306a180b8446057361d000000000000000000000000000000000000000000000000000000000000003c000000000000000000000000173cf75f0905338597fcd38f5ce13e6840b230e9")

	var gasLimit uint64
	var r [32]byte
	var s [32]byte

	gasLimit = 200000

	sender := "0x92c9885663f6e84127c857d3137936c424b7e07555d2bc7d8bd781b3f0847ac8"

	// Tras relayar una tx firmada con nonce=i, la lectura "pending" debe devolver el PRÓXIMO
	// nonce a usar (i+1) — no el recién consumido (comportamiento antiguo, que provocaba colisión
	// inmediata del siguiente envío).
	for i := 34; i < 45; i++ {
		jsonResponse := relaySignerService.SendMetatransaction(context.Background(), rpcMessage.ID, &to, gasLimit, encodedFunction, 27, r, s, sender, uint64(i))
		if jsonResponse.String() != `{"jsonrpc":"2.0","id":2914410858336929,"result":"0x9c2fb4956ce18491021a534106fe50e7cfe86bcc373b1626623fa0366f4cc3bc"}` {
			t.Errorf("Incorrect transactionHash was gotten")
		}

		jsonResponseNonce := relaySignerService.GetTransactionCount(context.Background(), rpcMessage.ID, sender, true)

		responseNonce := fmt.Sprintf(`{"jsonrpc":"2.0","id":2914410858336929,"result":"0x%x"}`, i+1)

		if jsonResponseNonce.String() != responseNonce {
			t.Errorf("Incorrect nonce was gotten: %s, expected %s", jsonResponseNonce.String(), responseNonce)
		}
	}

	err := os.Remove("keyMock")
	if err != nil {
		log.Fatal(err)
	}
}

// Dos envíos que firmaron el MISMO nonce (colisión concurrente) solo pueden consumir uno on-chain:
// el caché debe quedar en nonce+1, no avanzar +1 por cada envío (eso lo dejaba por delante del real
// para siempre → BadNonce permanente para esa address).
func TestNonceCollisionDoesNotDoubleIncrement(t *testing.T) {
	service := new(RelaySignerService)
	service.Config = &model.Config{}
	service.senders = make(map[string]*nonceEntry)

	sender := "0xa0f03c489a1bcd53883289d3c476100220b21b0b"
	service.incrementTransactionCount(sender, 23)
	service.incrementTransactionCount(sender, 23) // colisión: mismo nonce firmado

	next, ok := service.cachedNonce(sender)
	if !ok || next != 24 {
		t.Errorf("expected cached nonce 24 after collision, got %d (ok=%v)", next, ok)
	}
}

// Al detectar BadTransactionSent la entrada del sender debe borrarse: la próxima lectura "pending"
// vuelve al nonce real on-chain.
func TestInvalidateNonce(t *testing.T) {
	service := new(RelaySignerService)
	service.Config = &model.Config{}
	service.senders = make(map[string]*nonceEntry)

	sender := "0xa0f03c489a1bcd53883289d3c476100220b21b0b"
	service.incrementTransactionCount(sender, 10)
	if _, ok := service.cachedNonce(sender); !ok {
		t.Fatal("expected cache entry before invalidation")
	}

	// como llega desde el evento: address en checksum EIP-55
	service.invalidateNonce(common.HexToAddress(sender).Hex())

	if _, ok := service.cachedNonce(sender); ok {
		t.Error("expected cache entry to be removed after invalidateNonce")
	}
}

// Una entrada más vieja que el TTL debe descartarse (fallback on-chain) en lectura y no ancla el
// incremento siguiente.
func TestNonceCacheTTLExpiry(t *testing.T) {
	service := new(RelaySignerService)
	service.Config = &model.Config{}
	service.Config.Application.NonceCacheTTL = 1 // 1s para el test
	service.senders = make(map[string]*nonceEntry)

	sender := "0xa0f03c489a1bcd53883289d3c476100220b21b0b"
	service.senders[sender] = &nonceEntry{next: 99, updatedAt: time.Now().Add(-2 * time.Second)}

	if _, ok := service.cachedNonce(sender); ok {
		t.Error("expected expired entry to be discarded")
	}

	// tras expirar, el incremento re-siembra desde el nonce firmado (no desde la entrada vieja)
	service.senders[sender] = &nonceEntry{next: 99, updatedAt: time.Now().Add(-2 * time.Second)}
	service.incrementTransactionCount(sender, 5)
	next, ok := service.cachedNonce(sender)
	if !ok || next != 6 {
		t.Errorf("expected cached nonce 6 after expired entry reseed, got %d (ok=%v)", next, ok)
	}
}

// La misma address en lowercase (lecturas de ethers) y en checksum EIP-55 (escritura interna,
// message.From().Hex()) debe caer en la MISMA entrada del caché.
func TestNonceKeyCaseInsensitive(t *testing.T) {
	service := new(RelaySignerService)
	service.Config = &model.Config{}
	service.senders = make(map[string]*nonceEntry)

	checksum := "0xA0F03C489a1bcd53883289d3c476100220b21B0b"
	lower := "0xa0f03c489a1bcd53883289d3c476100220b21b0b"

	service.incrementTransactionCount(checksum, 7)
	next, ok := service.cachedNonce(lower)
	if !ok || next != 8 {
		t.Errorf("expected cached nonce 8 via lowercase key, got %d (ok=%v)", next, ok)
	}
}

// Accesos concurrentes al caché no deben corromperlo ni disparar el detector de carreras (-race).
func TestNonceCacheConcurrentAccess(t *testing.T) {
	service := new(RelaySignerService)
	service.Config = &model.Config{}
	service.senders = make(map[string]*nonceEntry)

	sender := "0xa0f03c489a1bcd53883289d3c476100220b21b0b"
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(3)
		go func(n uint64) { defer wg.Done(); service.incrementTransactionCount(sender, n) }(uint64(i))
		go func() { defer wg.Done(); service.cachedNonce(sender) }()
		go func() { defer wg.Done(); service.invalidateNonce(sender) }()
	}
	wg.Wait()
}

func serverMock() *httptest.Server {
	handler := http.NewServeMux()
	handler.HandleFunc("/getTransactionCount", mockGetNonce)
	handler.HandleFunc("/getReceipt", mockGetReceipt)
	handler.HandleFunc("/getReceiptRevertReason", mockGetReceiptRevertReason)
	handler.HandleFunc("/getReceiptFailedDeploy", mockGetReceiptFailedDeploy)
	handler.HandleFunc("/sendMetatransaction", mockSendMetatransaction)
	handler.HandleFunc("/getRelayHubContract", mockGetRelayHubContract)

	srv := httptest.NewServer(handler)

	return srv
}

func mockGetNonce(w http.ResponseWriter, r *http.Request) {
	_, _ = w.Write([]byte(`{"jsonrpc" : "2.0","id" : 53,"result" : "0x0000000000000000000000000000000000000000000000000000000000000159"}`))
}

func mockSendMetatransaction(w http.ResponseWriter, r *http.Request) {
	switch sequence {
	case 0, 1, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 16, 17, 18, 19, 20, 21, 22, 23:
		_, _ = w.Write([]byte(`{"jsonrpc" : "2.0","id" : 53,"result" : "0x6"}`))
	case 2:
		_, _ = w.Write([]byte(`{"jsonrpc" : "2.0","id" : 53,"result" : "0x6"}`))
	case 15:
		_, _ = w.Write([]byte(`{"jsonrpc" : "2.0","id" : 53,"result" : "0x6"}`))
	default:
		// Sin esto el mock deja de responder pasadas 24 llamadas y el siguiente test que envie
		// una metatx falla por EOF, segun cuantas hayan enviado los anteriores. Las tres ramas de
		// arriba escriben lo mismo, asi que el default no cambia lo que responde ninguna.
		_, _ = w.Write([]byte(`{"jsonrpc" : "2.0","id" : 53,"result" : "0x6"}`))
	}
	sequence++
}

func mockGetReceipt(w http.ResponseWriter, r *http.Request) {
	_, _ = w.Write([]byte(`{
		"jsonrpc" : "2.0",
		"id" : 53,
		"result" : {
		  "blockHash" : "0x6e3aa24e261e61832624749b64049104c6105ba870d3375484548ffdb133eeea",
		  "blockNumber" : "0xaae545",
		  "contractAddress" : null,
		  "cumulativeGasUsed" : "0x309f0",
		  "from" : "0xd00e6624a73f88b39f82ab34e8bf2b4d226fd768",
		  "gasUsed" : "0x309f0",
		  "logs" : [ {
			"address" : "0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91",
			"topics" : [ "0xa37b1b27143f61d990cfcf145e7f5d21c4419700613094ab29654b7ac6c08724" ],
			"data" : "0x0000000000000000000000000000000000000000000000000000000000000001",
			"blockNumber" : "0xaae545",
			"transactionHash" : "0x41167872ab8e13bf7ea5ea366786da656b3f32181410523b97ffecf0ee9cd945",
			"transactionIndex" : "0x0",
			"blockHash" : "0x6e3aa24e261e61832624749b64049104c6105ba870d3375484548ffdb133eeea",
			"logIndex" : "0x0",
			"removed" : false
		  }, {
			"address" : "0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91",
			"topics" : [ "0x1ecdaca0ae98a95eed765c0622982b0f7691f9a345988f8fca91c1c016ce5ee7" ],
			"data" : "0x0000000000000000000000000000000000000000000000000000000000aae545000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000009896800",
			"blockNumber" : "0xaae545",
			"transactionHash" : "0x41167872ab8e13bf7ea5ea366786da656b3f32181410523b97ffecf0ee9cd945",
			"transactionIndex" : "0x0",
			"blockHash" : "0x6e3aa24e261e61832624749b64049104c6105ba870d3375484548ffdb133eeea",
			"logIndex" : "0x1",
			"removed" : false
		  }, {
			"address" : "0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91",
			"topics" : [ "0x79f72f9dacecfa9af3cfe946364971d0ef4826ffd35451658b283d58a382c20f", "0x000000000000000000000000a20aa371a9d05bba5d087bfee8fdf47ffe1088da", "0x000000000000000000000000d00e6624a73f88b39f82ab34e8bf2b4d226fd768" ],
			"data" : "0x",
			"blockNumber" : "0xaae545",
			"transactionHash" : "0x41167872ab8e13bf7ea5ea366786da656b3f32181410523b97ffecf0ee9cd945",
			"transactionIndex" : "0x0",
			"blockHash" : "0x6e3aa24e261e61832624749b64049104c6105ba870d3375484548ffdb133eeea",
			"logIndex" : "0x2",
			"removed" : false
		  }, {
			"address" : "0x91402a50b130cb6ee76b1c85704faf94361cc233",
			"topics" : [ "0xeaf540d6ee39a98c4ab8d5d07d678c306272e18b51a3c93b026c4a2ce84a7afd" ],
			"data" : "0x000000000000000000000000ff6d55d01fb12695ea00c071ad8af3ce44cf3a9100000000000000000000000000000000000000000000000000000000000000430000000000000000000000000000000000000000000000000000000000000043",
			"blockNumber" : "0xaae545",
			"transactionHash" : "0x41167872ab8e13bf7ea5ea366786da656b3f32181410523b97ffecf0ee9cd945",
			"transactionIndex" : "0x0",
			"blockHash" : "0x6e3aa24e261e61832624749b64049104c6105ba870d3375484548ffdb133eeea",
			"logIndex" : "0x3",
			"removed" : false
		  }, {
			"address" : "0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91",
			"topics" : [ "0xfed6f0abc4f5e1923377ee51313db072532b591ea23ea4b4c44a4457e7e5f417", "0x000000000000000000000000d00e6624a73f88b39f82ab34e8bf2b4d226fd768", "0x000000000000000000000000a20aa371a9d05bba5d087bfee8fdf47ffe1088da", "0x00000000000000000000000091402a50b130cb6ee76b1c85704faf94361cc233" ],
			"data" : "0x0000000000000000000000000000000000000000000000000000000000000001",
			"blockNumber" : "0xaae545",
			"transactionHash" : "0x41167872ab8e13bf7ea5ea366786da656b3f32181410523b97ffecf0ee9cd945",
			"transactionIndex" : "0x0",
			"blockHash" : "0x6e3aa24e261e61832624749b64049104c6105ba870d3375484548ffdb133eeea",
			"logIndex" : "0x4",
			"removed" : false
		  }, {
			"address" : "0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91",
			"topics" : [ "0x260359eeed8459102359245337088f93b15364b134b4be9092d508e741bbdee1" ],
			"data" : "0x000000000000000000000000d00e6624a73f88b39f82ab34e8bf2b4d226fd7680000000000000000000000000000000000000000000000000000000000aae5450000000000000000000000000000000000000000000000000000000000004085000000000000000000000000000000000000000000000000000000000989277b0000000000000000000000000000000000000000000000000000000000004085",
			"blockNumber" : "0xaae545",
			"transactionHash" : "0x41167872ab8e13bf7ea5ea366786da656b3f32181410523b97ffecf0ee9cd945",
			"transactionIndex" : "0x0",
			"blockHash" : "0x6e3aa24e261e61832624749b64049104c6105ba870d3375484548ffdb133eeea",
			"logIndex" : "0x5",
			"removed" : false
		  } ],
		  "logsBloom" : "0x0000000000400000000000000000000000000000080000000000000000000000000000000000000000020000010000000000000000000000400000000000000002000000000001000000008000000000100000000020010000100000000000000800000000000000000000000000000000000000000000000000000000000000000000000010000000000000080000000000000000000000000000010000000000004800040000000000000001800000001001400000000000000010000000000000000a000000004008000000000000000000000000000000000000000000000000020000000000000000000000100000000020000000200000000000000000",
		  "status" : "0x1",
		  "to" : "0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91",
		  "transactionHash" : "0x41167872ab8e13bf7ea5ea366786da656b3f32181410523b97ffecf0ee9cd945",
		  "transactionIndex" : "0x0"
		}
	  }`))
}

func mockGetReceiptRevertReason(w http.ResponseWriter, r *http.Request) {
	_, _ = w.Write([]byte(`{
		"jsonrpc" : "2.0",
		"id" : 53,
		"result" : {
		  "blockHash" : "0x6e3aa24e261e61832624749b64049104c6105ba870d3375484548ffdb133eeea",
		  "blockNumber" : "0xaae545",
		  "contractAddress" : null,
		  "cumulativeGasUsed" : "0x309f0",
		  "from" : "0xd00e6624a73f88b39f82ab34e8bf2b4d226fd768",
		  "gasUsed" : "0x309f0",
		  "logs" : [ {
			"address" : "0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91",
			"topics" : [ "0xa37b1b27143f61d990cfcf145e7f5d21c4419700613094ab29654b7ac6c08724" ],
			"data" : "0x0000000000000000000000000000000000000000000000000000000000000001",
			"blockNumber" : "0xaae545",
			"transactionHash" : "0x41167872ab8e13bf7ea5ea366786da656b3f32181410523b97ffecf0ee9cd945",
			"transactionIndex" : "0x0",
			"blockHash" : "0x6e3aa24e261e61832624749b64049104c6105ba870d3375484548ffdb133eeea",
			"logIndex" : "0x0",
			"removed" : false
		  }, {
			"address" : "0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91",
			"topics" : [ "0x1ecdaca0ae98a95eed765c0622982b0f7691f9a345988f8fca91c1c016ce5ee7" ],
			"data" : "0x0000000000000000000000000000000000000000000000000000000000aae545000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000009896800",
			"blockNumber" : "0xaae545",
			"transactionHash" : "0x41167872ab8e13bf7ea5ea366786da656b3f32181410523b97ffecf0ee9cd945",
			"transactionIndex" : "0x0",
			"blockHash" : "0x6e3aa24e261e61832624749b64049104c6105ba870d3375484548ffdb133eeea",
			"logIndex" : "0x1",
			"removed" : false
		  }, {
			"address" : "0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91",
			"topics" : [ "0x79f72f9dacecfa9af3cfe946364971d0ef4826ffd35451658b283d58a382c20f", "0x000000000000000000000000a20aa371a9d05bba5d087bfee8fdf47ffe1088da", "0x000000000000000000000000d00e6624a73f88b39f82ab34e8bf2b4d226fd768" ],
			"data" : "0x",
			"blockNumber" : "0xaae545",
			"transactionHash" : "0x41167872ab8e13bf7ea5ea366786da656b3f32181410523b97ffecf0ee9cd945",
			"transactionIndex" : "0x0",
			"blockHash" : "0x6e3aa24e261e61832624749b64049104c6105ba870d3375484548ffdb133eeea",
			"logIndex" : "0x2",
			"removed" : false
		  }, {
			"address" : "0x91402a50b130cb6ee76b1c85704faf94361cc233",
			"topics" : [ "0xeaf540d6ee39a98c4ab8d5d07d678c306272e18b51a3c93b026c4a2ce84a7afd" ],
			"data" : "0x000000000000000000000000ff6d55d01fb12695ea00c071ad8af3ce44cf3a9100000000000000000000000000000000000000000000000000000000000000430000000000000000000000000000000000000000000000000000000000000043",
			"blockNumber" : "0xaae545",
			"transactionHash" : "0x41167872ab8e13bf7ea5ea366786da656b3f32181410523b97ffecf0ee9cd945",
			"transactionIndex" : "0x0",
			"blockHash" : "0x6e3aa24e261e61832624749b64049104c6105ba870d3375484548ffdb133eeea",
			"logIndex" : "0x3",
			"removed" : false
		  }, {
			"address" : "0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91",
			"topics" : [ "0xfed6f0abc4f5e1923377ee51313db072532b591ea23ea4b4c44a4457e7e5f417", "0x000000000000000000000000d00e6624a73f88b39f82ab34e8bf2b4d226fd768", "0x000000000000000000000000a20aa371a9d05bba5d087bfee8fdf47ffe1088da", "0x00000000000000000000000091402a50b130cb6ee76b1c85704faf94361cc233" ],
			"data" : "0x0000000000000000000000000000000000000000000000000000000000000001",
			"blockNumber" : "0xaae545",
			"transactionHash" : "0x41167872ab8e13bf7ea5ea366786da656b3f32181410523b97ffecf0ee9cd945",
			"transactionIndex" : "0x0",
			"blockHash" : "0x6e3aa24e261e61832624749b64049104c6105ba870d3375484548ffdb133eeea",
			"logIndex" : "0x4",
			"removed" : false
		  }, {
			"address" : "0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91",   
		    "topics": [
			"0x548af85d7bc344f47cbfacdfce1ffea1ecd862e5e235ca9ec919e767c14049a8",
			"0x00000000000000000000000063949701cd0e1cc04dfea0afbf410968f10ff4b6",
			"0x000000000000000000000000bceda2ba9af65c18c7992849c312d1db77cf008e",
			"0x000000000000000000000000938144efd1b3943c3b6658f4f7b72fcd980c55a1"
		    ],
			"data": "0x00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000040000000000000000000000000000000000000000000000000000000000000006408c379a0000000000000000000000000000000000000000000000000000000000000002000000000000000000000000000000000000000000000000000000000000000096e616275636f646f73000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000",		    
			"blockNumber" : "0xaae545",
			"transactionHash" : "0x41167872ab8e13bf7ea5ea366786da656b3f32181410523b97ffecf0ee9cd945",
			"transactionIndex" : "0x0",
			"blockHash" : "0x6e3aa24e261e61832624749b64049104c6105ba870d3375484548ffdb133eeea",
			"logIndex" : "0x5",
			"removed" : false
		  }, {
			"address" : "0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91",
			"topics" : [ "0x260359eeed8459102359245337088f93b15364b134b4be9092d508e741bbdee1" ],
			"data" : "0x000000000000000000000000d00e6624a73f88b39f82ab34e8bf2b4d226fd7680000000000000000000000000000000000000000000000000000000000aae5450000000000000000000000000000000000000000000000000000000000004085000000000000000000000000000000000000000000000000000000000989277b0000000000000000000000000000000000000000000000000000000000004085",
			"blockNumber" : "0xaae545",
			"transactionHash" : "0x41167872ab8e13bf7ea5ea366786da656b3f32181410523b97ffecf0ee9cd945",
			"transactionIndex" : "0x0",
			"blockHash" : "0x6e3aa24e261e61832624749b64049104c6105ba870d3375484548ffdb133eeea",
			"logIndex" : "0x5",
			"removed" : false
		  } ],
		  "logsBloom" : "0x0000000000400000000000000000000000000000080000000000000000000000000000000000000000020000010000000000000000000000400000000000000002000000000001000000008000000000100000000020010000100000000000000800000000000000000000000000000000000000000000000000000000000000000000000010000000000000080000000000000000000000000000010000000000004800040000000000000001800000001001400000000000000010000000000000000a000000004008000000000000000000000000000000000000000000000000020000000000000000000000100000000020000000200000000000000000",
		  "status" : "0x1",
		  "to" : "0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91",
		  "transactionHash" : "0x41167872ab8e13bf7ea5ea366786da656b3f32181410523b97ffecf0ee9cd945",
		  "transactionIndex" : "0x0",
		  "revertReason" : "0x08c379a0000000000000000000000000000000000000000000000000000000000000002000000000000000000000000000000000000000000000000000000000000000096e616275636f646f730000000000000000000000000000000000000000000000"	
		}
	  }`))
}

// mockGetReceiptFailedDeploy: receipt de un DEPLOY fallido — solo el evento Relayed
// (0x79f72f9d...), SIN ContractDeployed/TransactionRelayed/BadTransactionSent. status externo 0x1.
func mockGetReceiptFailedDeploy(w http.ResponseWriter, r *http.Request) {
	_, _ = w.Write([]byte(`{
		"jsonrpc" : "2.0",
		"id" : 53,
		"result" : {
		  "blockHash" : "0x6e3aa24e261e61832624749b64049104c6105ba870d3375484548ffdb133eeea",
		  "blockNumber" : "0xaae545",
		  "contractAddress" : null,
		  "cumulativeGasUsed" : "0x309f0",
		  "from" : "0xd00e6624a73f88b39f82ab34e8bf2b4d226fd768",
		  "gasUsed" : "0x309f0",
		  "logs" : [ {
			"address" : "0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91",
			"topics" : [ "0x79f72f9dacecfa9af3cfe946364971d0ef4826ffd35451658b283d58a382c20f", "0x000000000000000000000000a20aa371a9d05bba5d087bfee8fdf47ffe1088da", "0x000000000000000000000000d00e6624a73f88b39f82ab34e8bf2b4d226fd768" ],
			"data" : "0x",
			"blockNumber" : "0xaae545",
			"transactionHash" : "0x41167872ab8e13bf7ea5ea366786da656b3f32181410523b97ffecf0ee9cd945",
			"transactionIndex" : "0x0",
			"blockHash" : "0x6e3aa24e261e61832624749b64049104c6105ba870d3375484548ffdb133eeea",
			"logIndex" : "0x0",
			"removed" : false
		  } ],
		  "logsBloom" : "0x0000000000400000000000000000000000000000080000000000000000000000000000000000000000020000010000000000000000000000400000000000000002000000000001000000008000000000100000000020010000100000000000000800000000000000000000000000000000000000000000000000000000000000000000000010000000000000080000000000000000000000000000010000000000004800040000000000000001800000001001400000000000000010000000000000000a000000004008000000000000000000000000000000000000000000000000020000000000000000000000100000000020000000200000000000000000",
		  "status" : "0x1",
		  "to" : "0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91",
		  "transactionHash" : "0x41167872ab8e13bf7ea5ea366786da656b3f32181410523b97ffecf0ee9cd945",
		  "transactionIndex" : "0x0"
		}
	  }`))
}

func mockGetRelayHubContract(w http.ResponseWriter, r *http.Request) {
	_, _ = w.Write([]byte(`{"jsonrpc" : "2.0","id" : 53,"result" : "0x000000000000000000000000ff6d55d01fb12695ea00c071ad8af3ce44cf3a91"}`))
}

func createKeyMock(path string) {
	d1 := []byte("0xb3e7374dca5ca90c3899dbb2c978051437fb15534c945bf59df16d6c80be27c0")
	err := ioutil.WriteFile(path, d1, 0644)
	if err != nil {
		fmt.Println("err:", err)
	}
}

func setKeyMock() {
	os.Setenv("WRITER_KEY", "0xb3e7374dca5ca90c3899dbb2c978051437fb15534c945bf59df16d6c80be27c0")
}

// TestSentEventCarriesContractFields cubre la tarea 5.5: relay.sent lleva los campos del contrato,
// incluidos los que este servicio todavia no calcula, que se emiten sin valor en lugar de omitirse.
func TestSentEventCarriesContractFields(t *testing.T) {
	srv := serverMock()
	defer srv.Close()

	events.Init(true, 50)
	defer events.Init(false, 0)

	contents := []byte(`{"id":2914410858336929,"jsonrpc":"2.0","params":["0xf8840180831e8480946e6bbf31aa45042d53128339383fcd1c377b42c780a46057361d00000000000000000000000000000000000000000000000000000000000001591ba028934b543809922b277e85f6bcf7b1f25e937de05c5138e17fdfa480ba74e84ba055a2a611763ffcb748547408093551928c9549f95a0a9cabd3b1f1f2e166cc16"],"method":"eth_sendRawTransaction"}`)
	var rpcMessage rpc.JsonrpcMessage
	_ = json.Unmarshal(contents, &rpcMessage)

	dir, _ := os.Getwd()
	createKeyMock(dir + "/keyMock")
	setKeyMock()
	defer func() { _ = os.Remove("keyMock") }()

	applicationConfig := model.ApplicationConfig{NodeURL: srv.URL + "/getRelayHubContract", ContractAddress: "0x0ae2Da68515Ef8DC4bBCa1fA1bcE00C508b2Af4B", NodeKeyPath: dir + "/keyMock"}
	config := model.Config{Application: applicationConfig}
	relaySignerService := new(RelaySignerService)
	_ = relaySignerService.Init(&config)
	relaySignerService.Config.Application.NodeURL = srv.URL + "/sendMetatransaction"

	to := common.HexToAddress("0x82a978b3f5962a5b0957d9ee9eef472ee55b42f1")
	encodedFunction, _ := hex.DecodeString("0xf861808082ea6094fd32cfc2e71611626d6368a41f915d0077a306a180b8446057361d000000000000000000000000000000000000000000000000000000000000003c000000000000000000000000173cf75f0905338597fcd38f5ce13e6840b230e9")
	var r, s [32]byte
	sender := "0x92c9885663f6e84127c857d3137936c424b7e07555d2bc7d8bd781b3f0847ac8"

	ctx := audit.WithMetaTxID(audit.WithRequestID(context.Background(), "req-1"), "meta-1")
	response := relaySignerService.SendMetatransaction(ctx, rpcMessage.ID, &to, 200000, encodedFunction, 27, r, s, sender, 34)

	// La respuesta al cliente no cambio por haber pasado a manejar la transaccion entera.
	if response.String() != `{"jsonrpc":"2.0","id":2914410858336929,"result":"0x9c2fb4956ce18491021a534106fe50e7cfe86bcc373b1626623fa0366f4cc3bc"}` {
		t.Errorf("la respuesta cambio de forma: %s", response.String())
	}

	var sent events.Event
	for _, event := range events.Replay(0) {
		if event.Name() == "relay.sent" {
			sent = event
		}
	}
	if sent.Seq == 0 {
		t.Fatal("no se emitio relay.sent")
	}

	for _, field := range []string{
		"transactionHash", "hubNonce", "writerNodeNonce", "metaTxGasLimit",
		"simulated", "simulatedErrorCodeName", "pendingForUser",
	} {
		if _, present := sent.Line[field]; !present {
			t.Errorf("falta el campo %q del contrato: %v", field, sent.Line)
		}
	}
	if sent.Field("transactionHash") != "0x9c2fb4956ce18491021a534106fe50e7cfe86bcc373b1626623fa0366f4cc3bc" {
		t.Errorf("transactionHash = %v", sent.Field("transactionHash"))
	}
	if sent.Field("hubNonce") != uint64(34) {
		t.Errorf("hubNonce = %v, se esperaba 34 (el nonce que trae firmado la metatx)", sent.Field("hubNonce"))
	}
	if sent.Field("metaTxGasLimit") != uint64(200000) {
		t.Errorf("metaTxGasLimit = %v, se esperaba 200000", sent.Field("metaTxGasLimit"))
	}
	if _, ok := sent.Field("writerNodeNonce").(uint64); !ok {
		t.Errorf("writerNodeNonce = %v (%T), se esperaba el nonce de la cuenta del writer node",
			sent.Field("writerNodeNonce"), sent.Field("writerNodeNonce"))
	}
	for _, field := range []string{"simulated", "simulatedErrorCodeName", "pendingForUser"} {
		if sent.Field(field) != nil {
			t.Errorf("%q = %v, este servicio todavia no lo calcula", field, sent.Field(field))
		}
	}
	if sent.Field("metaTxId") != "meta-1" || sent.Field("reqId") != "req-1" {
		t.Errorf("relay.sent perdio la correlacion: %v", sent.Line)
	}
}
