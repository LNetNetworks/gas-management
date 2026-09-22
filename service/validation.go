package service

import (
	"context"
	"time"

	log "github.com/LACNetNetworks/gas-relay-signer/audit"
	"github.com/LACNetNetworks/gas-relay-signer/errors"
	"github.com/LACNetNetworks/gas-relay-signer/model"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// Validacion del sufijo del modelo de gas.
//
// El sufijo ya se decodifica para registrarlo -`DecodeGasModelSuffix`-; esto es el camino que ADEMAS
// lo valida. Los dos leen lo mismo desde el mismo lugar: una segunda lectura terminaria discrepando
// con la que se emite en `relay.decoded`, y el log diria una cosa y el rechazo otra.
//
// Lo que se valida es un sufijo PRESENTE Y LEGIBLE que dice algo inaceptable. Un sufijo ausente,
// corto o con una expiracion que no entra en un entero no rechaza nada: una raw tx que hoy se
// relaya tiene que seguir relayandose. Ver design.md, D2.

// validateGasModelSuffix rechaza la metatx que el hub no va a poder ejecutar por lo que dice su
// propio sufijo: la dirigida a otro nodo y la vencida o por vencer.
//
// Devuelve nil cuando no hay nada que objetar, incluido el caso de las exigencias apagadas: con las
// dos en false no se entra a ninguna comprobacion y se acepta exactamente lo mismo que antes de esta
// capacidad.
func (service *RelaySignerService) validateGasModelSuffix(ctx context.Context, tx *types.Transaction) error {
	validation := service.validationConfig()
	if !validation.EnforceNodeAddress && !validation.EnforceExpiration {
		return nil
	}

	fields := DecodeGasModelSuffix(tx.Data())
	if !fields.Decoded {
		// Sin sufijo legible no hay nada que validar. Se registra para que no parezca que se
		// valido: el operador que enciende la exigencia tiene que poder ver cuales pasaron sin
		// comprobarse.
		log.Debug(ctx, "relay.suffix_absent", map[string]interface{}{"dataBytes": len(tx.Data())})
		return nil
	}

	if validation.EnforceNodeAddress {
		if err := service.checkNodeAddress(fields); err != nil {
			return err
		}
	}

	if validation.EnforceExpiration {
		// UN solo instante para las dos comprobaciones. Releer el reloj entre una y otra abre una
		// ventana en la que la metatx esta vencida para una y no para la otra, y produce un mensaje
		// que no se puede reproducir. Ver design.md, D8.
		return checkExpiration(fields, time.Now().Unix(), validation)
	}
	return nil
}

// checkNodeAddress exige que el sufijo apunte a ESTE servicio.
//
// Una metatx dirigida a otro nodo no la puede ejecutar este: el hub la rechaza on-chain con la
// transaccion de este nodo ya gastada.
func (service *RelaySignerService) checkNodeAddress(fields GasModelFields) error {
	mine, err := service.NodeAddress()
	if err != nil {
		// No se puede afirmar que la metatx apunta a otro nodo si no se sabe cual es este. Se
		// rechaza igual, pero diciendo lo que realmente paso.
		return Reject(err, CodeRelayError)
	}

	target := common.HexToAddress(fields.NodeAddress)
	if target == mine {
		return nil
	}
	return RejectWithDetails(
		errors.New("The metatx targets node "+target.Hex()+" but this relayer is "+mine.Hex(), -32000),
		CodeWrongNodeAddress,
		map[string]interface{}{"nodeAddress": target.Hex(), "relayerNodeAddress": mine.Hex()},
	)
}

// checkExpiration exige que la metatx no haya vencido y que le quede la ventana minima.
//
// El minimo se aplica CON tolerancia: quien firma `ahora + 300` llega con 298 -se pierde la latencia
// de la peticion y el redondeo a segundos de cada lado-, asi que exigir el valor exacto rechazaria
// justo al cliente que hizo lo correcto.
func checkExpiration(fields GasModelFields, now int64, validation model.ValidationConfig) error {
	expiration, ok := fields.ExpirationSeconds()
	if !ok {
		// Una expiracion que no se puede representar como un momento no se puede comparar con
		// ninguno: no rechaza por la ventana de vigencia.
		return nil
	}

	if int64(expiration) <= now {
		return RejectWithDetails(
			errors.New("The metatx has expired (expiration="+decimalInt(int64(expiration))+
				", now="+decimalInt(now)+")", -32000),
			CodeExpired,
			map[string]interface{}{"expiration": expiration, "now": now},
		)
	}

	remaining := int64(expiration) - now
	floor := int64(validation.ExpirationFloor())
	if remaining >= floor {
		return nil
	}

	return RejectWithDetails(
		errors.New("expiration too low: the metatx expires in "+decimalInt(remaining)+
			" s and the minimum is "+decimalInt(int64(validation.MinExpirationSeconds))+
			" s ("+decimalInt(int64(validation.ExpirationToleranceSeconds))+
			" s tolerance for latency; expiration="+decimalInt(int64(expiration))+
			", now="+decimalInt(now)+")", -32000),
		CodeExpirationTooLow,
		map[string]interface{}{
			"expiration":       expiration,
			"remainingSeconds": remaining,
			"minimumSeconds":   validation.MinExpirationSeconds,
			"toleranceSeconds": validation.ExpirationToleranceSeconds,
		},
	)
}

// validationConfig es el bloque [validation] vigente.
func (service *RelaySignerService) validationConfig() model.ValidationConfig {
	if service.Config == nil {
		return model.ValidationConfig{}
	}
	return service.Config.Validation
}

// decimalInt formatea un entero con signo sin arrastrar fmt por un solo numero.
func decimalInt(value int64) string {
	if value < 0 {
		return "-" + decimal(uint64(-value))
	}
	return decimal(uint64(value))
}
