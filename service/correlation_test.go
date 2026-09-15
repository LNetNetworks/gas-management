package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	audit "github.com/LACNetNetworks/gas-relay-signer/audit"
	"github.com/LACNetNetworks/gas-relay-signer/events"
	"github.com/LACNetNetworks/gas-relay-signer/model"
	"github.com/ethereum/go-ethereum/common"
)

// El hash de la metatx enviada, y el mismo por el que despues se consulta el receipt.
const relayedHash = "0x41167872ab8e13bf7ea5ea366786da656b3f32181410523b97ffecf0ee9cd945"

// badTransactionReceipt es un receipt con el evento BadTransactionSent del RelayHub: el hub
// rechazo la metatx al ejecutarla y NO consumio el nonce del usuario.
const badTransactionReceipt = `{
  "jsonrpc": "2.0",
  "id": 53,
  "result": {
    "blockHash": "0x6e3aa24e261e61832624749b64049104c6105ba870d3375484548ffdb133eeea",
    "blockNumber": "0xaae545",
    "contractAddress": null,
    "cumulativeGasUsed": "0x309f0",
    "from": "0xd00e6624a73f88b39f82ab34e8bf2b4d226fd768",
    "gasUsed": "0x309f0",
    "logs": [{
      "address": "0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91",
      "topics": ["0xc62bb53370aadcfe652881fc57ef9ca04a7c473e83b963413f2cf2b5d66c3ef3"],
      "data": "0x000000000000000000000000173cf75f0905338597fcd38f5ce13e6840b230e900000000000000000000000082a978b3f5962a5b0957d9ee9eef472ee55b42f10000000000000000000000000000000000000000000000000000000000000002",
      "blockNumber": "0xaae545",
      "transactionHash": "0x41167872ab8e13bf7ea5ea366786da656b3f32181410523b97ffecf0ee9cd945",
      "transactionIndex": "0x0",
      "blockHash": "0x6e3aa24e261e61832624749b64049104c6105ba870d3375484548ffdb133eeea",
      "logIndex": "0x0",
      "removed": false
    }],
    "logsBloom": "0x00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000",
    "status": "0x1",
    "to": "0xff6d55d01fb12695ea00c071ad8af3ce44cf3a91",
    "transactionHash": "0x41167872ab8e13bf7ea5ea366786da656b3f32181410523b97ffecf0ee9cd945",
    "transactionIndex": "0x0"
  }
}`

// badTransactionServer responde siempre ese receipt.
func badTransactionServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(badTransactionReceipt))
	}))
}

func serviceAgainst(url string) *RelaySignerService {
	relaySignerService := new(RelaySignerService)
	relaySignerService.Config = &model.Config{Application: model.ApplicationConfig{NodeURL: url}}
	relaySignerService.senders = make(map[string]*nonceEntry)
	relaySignerService.metaTx = make(map[common.Hash]*metaTxEntry)
	return relaySignerService
}

func hubRejectedEvent() (events.Event, bool) {
	for _, event := range events.Replay(0) {
		if event.Name() == "relay.hub_rejected" {
			return event, true
		}
	}
	return events.Event{}, false
}

// TestCloseEventKeepsTheOriginalCorrelation cubre las tareas 4.6 y 5.7. Es el escenario que en Go
// no se resuelve solo: el receipt llega en una peticion HTTP distinta de la que relayo la metatx,
// y el evento de cierre tiene que seguir perteneciendo a esa metatx.
func TestCloseEventKeepsTheOriginalCorrelation(t *testing.T) {
	events.Init(true, 50)
	defer events.Init(false, 0)
	srv := badTransactionServer()
	defer srv.Close()

	relaySignerService := serviceAgainst(srv.URL)

	// Peticion 1: se relaya la metatx y se recuerda a que metatx pertenece el hash.
	relayCtx := audit.WithMetaTxID(audit.WithRequestID(context.Background(), "req-relay"), "meta-1")
	relaySignerService.rememberMetaTx(relayCtx, common.HexToHash(relayedHash))

	// Peticion 2, mas tarde y con su propio reqId: el cliente consulta el receipt.
	receiptCtx := audit.WithRequestID(context.Background(), "req-receipt")
	relaySignerService.GetTransactionReceipt(receiptCtx, json.RawMessage(`1`), relayedHash)

	event, found := hubRejectedEvent()
	if !found {
		t.Fatal("no se emitio relay.hub_rejected al detectar BadTransactionSent")
	}
	if event.Field("metaTxId") != "meta-1" {
		t.Errorf("metaTxId = %v, se esperaba el de la metatx original (meta-1)", event.Field("metaTxId"))
	}
	if event.Field("reqId") != "req-relay" {
		t.Errorf("reqId = %v, se esperaba el de la peticion que relayo (req-relay), no el de la que consulto el receipt",
			event.Field("reqId"))
	}
	if event.Field("errorCodeName") != "BadNonce" {
		t.Errorf("errorCodeName = %v, se esperaba BadNonce", event.Field("errorCodeName"))
	}
	if event.Field("errorCode") != uint8(2) {
		t.Errorf("errorCode = %v, se esperaba 2", event.Field("errorCode"))
	}
	if event.Field("from") != "0x82A978B3f5962A5b0957d9ee9eEf472EE55B42F1" {
		t.Errorf("from = %v, se esperaba el sender original de la metatx", event.Field("from"))
	}
	if event.Field("transactionHash") != relayedHash {
		t.Errorf("transactionHash = %v, se esperaba %s", event.Field("transactionHash"), relayedHash)
	}
	if event.Level() != "warn" {
		t.Errorf("level = %q, un rechazo del hub es informacion de operacion (warn)", event.Level())
	}
}

// TestForgottenHashDoesNotBreakTheReceiptPath cubre la tarea 4.7: un receipt cuyo hash ya no esta
// recordado no rompe nada. El evento sale sin metaTxId -la vista lo descartara- y la peticion se
// responde exactamente igual.
func TestForgottenHashDoesNotBreakTheReceiptPath(t *testing.T) {
	events.Init(true, 50)
	defer events.Init(false, 0)
	srv := badTransactionServer()
	defer srv.Close()

	recordada := serviceAgainst(srv.URL)
	recordada.rememberMetaTx(audit.WithMetaTxID(context.Background(), "meta-1"), common.HexToHash(relayedHash))
	conMemoria := recordada.GetTransactionReceipt(context.Background(), json.RawMessage(`1`), relayedHash)

	olvidada := serviceAgainst(srv.URL)
	sinMemoria := olvidada.GetTransactionReceipt(context.Background(), json.RawMessage(`1`), relayedHash)

	if conMemoria.String() != sinMemoria.String() {
		t.Errorf("la respuesta cambia segun si el hash estaba recordado:\ncon:  %s\nsin: %s",
			conMemoria.String(), sinMemoria.String())
	}

	var sinCorrelacion bool
	for _, event := range events.Replay(0) {
		if event.Name() != "relay.hub_rejected" {
			continue
		}
		if _, present := event.Line["metaTxId"]; !present {
			sinCorrelacion = true
		}
	}
	if !sinCorrelacion {
		t.Error("se esperaba un relay.hub_rejected sin metaTxId para el hash no recordado")
	}
}

// TestMetaTxMemoryExpires cubre la mitad del TTL de la tarea 4.5: una entrada vencida se libera y
// deja de correlacionar.
func TestMetaTxMemoryExpires(t *testing.T) {
	relaySignerService := serviceAgainst("")
	hash := common.HexToHash(relayedHash)

	ctx := audit.WithMetaTxID(context.Background(), "meta-1")
	relaySignerService.rememberMetaTx(ctx, hash)

	if audit.MetaTxID(relaySignerService.recallMetaTx(context.Background(), hash)) != "meta-1" {
		t.Fatal("una entrada recien anotada debe correlacionar")
	}

	// Se envejece la entrada mas alla del TTL.
	relaySignerService.metaTx[hash].rememberedAt = time.Now().Add(-metaTxMemoryTTL - time.Minute)

	if id := audit.MetaTxID(relaySignerService.recallMetaTx(context.Background(), hash)); id != "" {
		t.Errorf("una entrada vencida no debe correlacionar, devolvio %q", id)
	}
	if _, present := relaySignerService.metaTx[hash]; present {
		t.Error("una entrada vencida debe liberarse, no quedar ocupando memoria")
	}
}

// TestMetaTxMemoryIsBounded cubre la otra mitad de la tarea 4.5: al llegar al tope se descarta la
// entrada mas antigua, asi que una rafaga sostenida no hace crecer el mapa sin limite.
func TestMetaTxMemoryIsBounded(t *testing.T) {
	relaySignerService := serviceAgainst("")
	ctx := audit.WithMetaTxID(context.Background(), "meta-1")

	primera := common.BigToHash(common.Big1)
	relaySignerService.rememberMetaTx(ctx, primera)
	relaySignerService.metaTx[primera].rememberedAt = time.Now().Add(-time.Minute)

	for i := 2; i <= metaTxMemoryMax+10; i++ {
		relaySignerService.rememberMetaTx(ctx, common.BigToHash(common.Big1.SetInt64(int64(i))))
	}

	if len(relaySignerService.metaTx) > metaTxMemoryMax {
		t.Errorf("el mapa tiene %d entradas, el tope es %d", len(relaySignerService.metaTx), metaTxMemoryMax)
	}
	if _, present := relaySignerService.metaTx[primera]; present {
		t.Error("al llegar al tope se debe descartar la entrada mas antigua")
	}
}

// TestRememberIgnoresContextWithoutMetaTx: no se anota lo que no se puede correlacionar.
func TestRememberIgnoresContextWithoutMetaTx(t *testing.T) {
	relaySignerService := serviceAgainst("")
	relaySignerService.rememberMetaTx(context.Background(), common.HexToHash(relayedHash))
	if len(relaySignerService.metaTx) != 0 {
		t.Errorf("sin metaTxId no hay nada que recordar, quedaron %d entradas", len(relaySignerService.metaTx))
	}
}
