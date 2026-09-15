package audit

import "context"

// Go no tiene AsyncLocalStorage: la correlacion viaja por context.Context, explicitamente por las
// firmas del camino de relay. Ver design.md, D1.
//
// El vocabulario vive aca, junto al emisor que lo lee, porque es el unico que necesita conocerlo:
// quien origina un evento solo tiene que arrastrar el ctx.
type contextKey int

const (
	requestIDKey contextKey = iota
	metaTxIDKey
)

// WithRequestID devuelve un contexto que lleva el identificador de la peticion HTTP. Todos los
// eventos originados por esa peticion lo incluyen, incluso los emitidos despues de responder.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestID es el identificador de la peticion en curso, o "" si el contexto no lo trae.
func RequestID(ctx context.Context) string { return contextText(ctx, requestIDKey) }

// WithMetaTxID devuelve un contexto que lleva el identificador de una metatx concreta. Hace falta
// ademas del reqId porque una peticion puede traer mas de una metatx: sin el, no hay forma de
// saber cual relay.sent corresponde a cual relay.received.
func WithMetaTxID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, metaTxIDKey, id)
}

// MetaTxID es el identificador de la metatx en curso, o "" si el contexto no lo trae.
func MetaTxID(ctx context.Context) string { return contextText(ctx, metaTxIDKey) }

func contextText(ctx context.Context, key contextKey) string {
	if ctx == nil {
		return ""
	}
	if value, ok := ctx.Value(key).(string); ok {
		return value
	}
	return ""
}
