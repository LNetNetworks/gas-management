package service

import (
	"encoding/hex"
	"strings"

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
