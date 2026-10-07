package service

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LACNetNetworks/gas-relay-signer/model"
)

// Nodo simulado que despacha por metodo JSON-RPC, en lugar de por path como el mock historico.
// Es lo que hace falta para ejercitar el camino completo -resolver el hub, verificar el cupo,
// enviar y consultar el receipt- desde un solo extremo, igual que un nodo real.

// testNode es un nodo simulado con el que se puede cambiar lo que responde durante el test.
type testNode struct {
	*httptest.Server
	// receipt es lo que devuelve eth_getTransactionReceipt. Vacio significa que todavia no se mino.
	receipt atomic.Value
	// receiptCalls cuenta cuantas veces se consulto el receipt, para observar el sondeo.
	receiptCalls atomic.Int64
}

// zeroBloom son los 256 bytes de un Bloom vacio. Con menos, el receipt entero se descarta en
// silencio: no es un error que se vea, simplemente el receipt queda nulo.
const zeroBloom = "0x" + "0000000000000000000000000000000000000000000000000000000000000000" +
	"0000000000000000000000000000000000000000000000000000000000000000" +
	"0000000000000000000000000000000000000000000000000000000000000000" +
	"0000000000000000000000000000000000000000000000000000000000000000" +
	"0000000000000000000000000000000000000000000000000000000000000000" +
	"0000000000000000000000000000000000000000000000000000000000000000" +
	"0000000000000000000000000000000000000000000000000000000000000000" +
	"0000000000000000000000000000000000000000000000000000000000000000"

const testRelayHub = "ff6d55d01fb12695ea00c071ad8af3ce44cf3a91"

func abiWord(value string) string {
	return "0x" + strings.Repeat("0", 64-len(value)) + value
}

// newTestNode levanta el nodo simulado. `receipt` es lo que responde desde el arranque; con "" se
// comporta como una transaccion todavia sin minar.
func newTestNode(t *testing.T, receipt string) *testNode {
	t.Helper()
	node := new(testNode)
	node.receipt.Store(receipt)

	node.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		text := string(body)
		var request struct {
			Method string   `json:"method"`
			Params []string `json:"params"`
		}
		_ = json.Unmarshal(body, &request)
		w.Header().Set("Content-Type", "application/json")

		switch {
		case strings.Contains(text, DATA_CALL_RELAYHUB):
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"1000","result":"` + abiWord(testRelayHub) + `"}`))
		case strings.Contains(text, `"eth_call"`):
			// Cupo de gas generoso: la metatx tiene que llegar al envio.
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"` + abiWord("3b9aca00") + `"}`))
		case strings.Contains(text, `"eth_getTransactionCount"`):
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x6"}`))
		case strings.Contains(text, `"eth_sendRawTransaction"`):
			// Un nodo real responde el hash de la transaccion que acaba de recibir.
			hash := abiWord("1")
			if len(request.Params) > 0 {
				hash = RawTxHash(request.Params[0])
			}
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"` + hash + `"}`))
		case strings.Contains(text, `"eth_getTransactionReceipt"`):
			node.receiptCalls.Add(1)
			stored, _ := node.receipt.Load().(string)
			if stored == "" {
				_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":null}`))
				return
			}
			_, _ = w.Write([]byte(stored))
		default:
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":null}`))
		}
	}))
	t.Cleanup(node.Close)
	return node
}

// mine hace que a partir de ahora el nodo devuelva ese receipt.
func (node *testNode) mine(receipt string) { node.receipt.Store(receipt) }

// serviceAgainstNode arma un servicio inicializado contra el nodo simulado.
func serviceAgainstNode(t *testing.T, node *testNode) *RelaySignerService {
	t.Helper()
	dir := t.TempDir()
	createKeyMock(dir + "/keyMock")
	setKeyMock()

	config := &model.Config{Application: model.ApplicationConfig{
		NodeURL:         node.URL,
		ContractAddress: "0x0ae2Da68515Ef8DC4bBCa1fA1bcE00C508b2Af4B",
		NodeKeyPath:     dir + "/keyMock",
	}}
	config.Reorder = model.DefaultReorderConfig()

	relaySignerService := new(RelaySignerService)
	if err := relaySignerService.Init(config); err != nil {
		t.Fatalf("no se pudo inicializar el servicio: %v", err)
	}
	return relaySignerService
}

// resetGasWindow deja el cupo de gas del bloque en cero.
func resetGasWindow() {
	lock.Lock()
	GAS_LIMIT = 0
	lock.Unlock()
}

// gasWindow es el cupo comprometido en el bloque en curso.
func gasWindow() uint64 {
	lock.Lock()
	defer lock.Unlock()
	return GAS_LIMIT
}
