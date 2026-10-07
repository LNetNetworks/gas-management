package service

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

// relayHubAddressServerMock levanta un nodo falso que responde el body indicado a la
// llamada eth_call que hace getRelayHubContractAddress.
func relayHubAddressServerMock(body string) *httptest.Server {
	handler := http.NewServeMux()
	handler.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-type", "application/json")
		_, _ = w.Write([]byte(body))
	})

	return httptest.NewServer(handler)
}

// TestGetRelayHubContractAddress cubre los escenarios de la capability
// relayhub-address-resolution: un resultado válido devuelve la address, y toda respuesta
// inválida devuelve error sin hacer panic.
func TestGetRelayHubContractAddress(t *testing.T) {
	const wantAddress = "0x8e34aBE3D6c8Cf0e6D5Cc38d9A2e2c6c3D53E33f"

	// La address viene ABI-encoded: una palabra de 32 bytes con la address en los 20 finales.
	validResult := "0x" + strings.Repeat("0", 24) + strings.ToLower(strings.TrimPrefix(wantAddress, "0x"))

	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{
			name: "address valida",
			body: `{"jsonrpc":"2.0","id":"1000","result":"` + validResult + `"}`,
		},
		{
			name:    "error json-rpc",
			body:    `{"jsonrpc":"2.0","id":"1000","error":{"code":-32000,"message":"execution reverted"}}`,
			wantErr: true,
		},
		{
			name:    "result vacio",
			body:    `{"jsonrpc":"2.0","id":"1000","result":""}`,
			wantErr: true,
		},
		{
			name:    "result malformado",
			body:    `{"jsonrpc":"2.0","id":"1000","result":"0xdeadbeef"}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := relayHubAddressServerMock(tt.body)
			defer srv.Close()

			address, err := getRelayHubContractAddress(srv.URL, "1000", wantAddress, 10)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("se esperaba error, se obtuvo address %v", address)
				}
				if address != nil {
					t.Errorf("con error se esperaba address nula, se obtuvo %v", address)
				}
				return
			}

			if err != nil {
				t.Fatalf("no se esperaba error: %v", err)
			}
			if address == nil {
				t.Fatal("se esperaba una address, se obtuvo nil")
			}
			if got := address.Hex(); got != common.HexToAddress(wantAddress).Hex() {
				t.Errorf("address = %s, se esperaba %s", got, wantAddress)
			}
		})
	}
}

// TestGetRelayHubContractAddressNodeDown verifica que un nodo inalcanzable degrade en
// error y no en panic.
func TestGetRelayHubContractAddressNodeDown(t *testing.T) {
	srv := relayHubAddressServerMock(`{}`)
	url := srv.URL
	srv.Close() // el nodo deja de responder antes de la llamada

	address, err := getRelayHubContractAddress(url, "1000", "0x8e34aBE3D6c8Cf0e6D5Cc38d9A2e2c6c3D53E33f", 1)
	if err == nil {
		t.Fatalf("se esperaba error con el nodo caido, se obtuvo address %v", address)
	}
	if address != nil {
		t.Errorf("con error se esperaba address nula, se obtuvo %v", address)
	}
}
