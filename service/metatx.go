package service

import (
	"encoding/hex"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	sha "golang.org/x/crypto/sha3"
)

// Datos de una metatx que solo sirven para registrarla. Nada de lo que hay aca valida ni rechaza:
// una raw tx que hoy se relaya tiene que seguir relayandose aunque estos datos no se puedan
// calcular. Ver design.md, D13.

// RawTxHash identifica la transaccion firmada sin volcar su contenido. Devuelve "" si la raw tx no
// es hexadecimal: identificarla es util, pero no a costa de rechazar lo que antes pasaba.
func RawTxHash(rawTx string) string {
	raw, err := hex.DecodeString(strings.TrimPrefix(rawTx, "0x"))
	if err != nil {
		return ""
	}
	digest := sha.NewLegacyKeccak256()
	digest.Write(raw)
	return "0x" + hex.EncodeToString(digest.Sum(nil))
}

// RawTxBytes es el tamano en bytes de la transaccion firmada. Junto con el hash alcanza para
// identificarla en el log sin registrar la transaccion entera.
func RawTxBytes(rawTx string) int {
	body := strings.TrimPrefix(rawTx, "0x")
	return len(body) / 2
}

// gasModelSuffixBytes es lo que el modelo de gas agrega al final del data de la metatx:
// abi.encode(address, uint256), o sea dos palabras de 32 bytes. En crudo son
// [12 ceros][20 bytes de address][32 bytes de uint256].
const gasModelSuffixBytes = 64

// GasModelFields son los datos que el modelo de gas agrega al final del data. Decoded indica si se
// pudieron leer: un data mas corto que el sufijo NO es un error, es una metatx que este servicio
// relaya igual y de la que simplemente no se puede informar esto.
type GasModelFields struct {
	Decoded     bool
	NodeAddress string
	Expiration  *big.Int
	InnerData   []byte
	Selector    string
}

// DecodeGasModelSuffix lee el sufijo del modelo de gas SOLO PARA REGISTRARLO.
//
// No valida y no rechaza: una raw tx que hoy se relaya tiene que seguir relayandose aunque el
// sufijo no este o venga corto. Validarlo es una brecha propia, de otra propuesta. Ver D13.
func DecodeGasModelSuffix(data []byte) GasModelFields {
	if len(data) < gasModelSuffixBytes {
		return GasModelFields{}
	}

	suffix := data[len(data)-gasModelSuffixBytes:]
	fields := GasModelFields{
		Decoded:     true,
		NodeAddress: common.BytesToAddress(suffix[12:32]).Hex(),
		Expiration:  new(big.Int).SetBytes(suffix[32:64]),
		InnerData:   data[:len(data)-gasModelSuffixBytes],
	}
	if len(fields.InnerData) >= 4 {
		fields.Selector = "0x" + hex.EncodeToString(fields.InnerData[:4])
	}
	return fields
}

// ExpirationSeconds devuelve la expiracion como segundos unix, y si es representable. Un uint256
// arbitrario no entra en un entero: en ese caso se informa que no hay valor aplicable en lugar de
// emitir un numero truncado.
func (fields GasModelFields) ExpirationSeconds() (uint64, bool) {
	if !fields.Decoded || fields.Expiration == nil || !fields.Expiration.IsUint64() {
		return 0, false
	}
	return fields.Expiration.Uint64(), true
}
