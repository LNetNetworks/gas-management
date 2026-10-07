package service

import (
	"math/big"
	"testing"

	"github.com/LACNetNetworks/gas-relay-signer/model"
)

// Los limites de la ventana de vigencia, evaluados contra un instante fijo para que el test no
// dependa del reloj. Cubre los bordes de la tarea 2.4.
func TestExpirationBoundaries(t *testing.T) {
	const ahora = int64(1_000_000)
	validation := model.ValidationConfig{
		EnforceExpiration:          true,
		MinExpirationSeconds:       300,
		ExpirationToleranceSeconds: 2,
	}

	conExpiracion := func(expiration int64) GasModelFields {
		return GasModelFields{Decoded: true, Expiration: big.NewInt(expiration)}
	}

	casos := []struct {
		nombre     string
		expiration int64
		codigo     string
	}{
		{"vencida hace rato", ahora - 100, CodeExpired},
		{"vence justo ahora", ahora, CodeExpired},
		{"un segundo de vida", ahora + 1, CodeExpirationTooLow},
		{"justo debajo del limite efectivo", ahora + 297, CodeExpirationTooLow},
		{"en el limite efectivo (minimo menos tolerancia)", ahora + 298, ""},
		{"el minimo exacto", ahora + 300, ""},
		{"de sobra", ahora + 3600, ""},
	}

	for _, caso := range casos {
		err := checkExpiration(conExpiracion(caso.expiration), ahora, validation)
		if caso.codigo == "" {
			if err != nil {
				t.Errorf("%s: se esperaba que pasara, fue rechazada con %v", caso.nombre, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: se esperaba el rechazo %s, paso", caso.nombre, caso.codigo)
			continue
		}
		if code := CodeOf(err); code != caso.codigo {
			t.Errorf("%s: codigo %s, se esperaba %s", caso.nombre, code, caso.codigo)
		}
	}
}

// Una expiracion que no entra en un entero no se puede comparar con ningun instante: no rechaza.
// Cubre la mitad restante de la tarea 2.7.
func TestUnrepresentableExpirationDoesNotReject(t *testing.T) {
	enorme := new(big.Int).Lsh(big.NewInt(1), 200)
	fields := GasModelFields{Decoded: true, Expiration: enorme}
	validation := model.ValidationConfig{EnforceExpiration: true, MinExpirationSeconds: 300}

	if err := checkExpiration(fields, 1_000_000, validation); err != nil {
		t.Errorf("una expiracion no representable no puede rechazar: %v", err)
	}
}

// El catalogo de codigos es el del relayer de referencia: no se inventan codigos nuevos.
// Cubre la tarea 2.5.
func TestErrorCatalogMatchesTheReference(t *testing.T) {
	// Los trece del catalogo de Node, segun ENDPOINTS-GO-VS-NODE.md.
	catalogo := map[string]bool{
		"BAD_RAW_TX": true, "BAD_META_TX": true, "BAD_NONCE": true, "WRONG_NODE_ADDRESS": true,
		"EXPIRED": true, "EXPIRATION_TOO_LOW": true, "SENDER_NOT_PERMITTED": true,
		"PERMISSIONING_UNAVAILABLE": true, "TOO_MANY_INFLIGHT": true, "SIMULATION_FAILED": true,
		"SEND_FAILED": true, "NO_RECEIPT": true, "RECEIPT_TIMEOUT": true, "RELAY_ERROR": true,
	}

	producidos := []string{
		CodeBadRawTx, CodeBadMetaTx, CodeBadNonce, CodeTooManyInflight, CodeWrongNodeAddress,
		CodeExpired, CodeExpirationTooLow, CodeSenderNotPermitted, CodePermissioningUnavailable,
		CodeSendFailed, CodeReceiptTimeout, CodeRelayError,
	}
	for _, code := range producidos {
		if !catalogo[code] {
			t.Errorf("%s no pertenece al catalogo del relayer de referencia", code)
		}
	}
}
