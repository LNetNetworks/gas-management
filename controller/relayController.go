package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"

	log "github.com/LACNetNetworks/gas-relay-signer/audit"
	"github.com/LACNetNetworks/gas-relay-signer/model"
	"github.com/LACNetNetworks/gas-relay-signer/rpc"
	"github.com/LACNetNetworks/gas-relay-signer/service"
)

var lock sync.Mutex

// RelayController is the main controller
type RelayController struct {
	// The controller's configuration
	Config             *model.Config
	RelaySignerService *service.RelaySignerService
}

// Init controller
func (controller *RelayController) Init(config *model.Config, relaySignerService *service.RelaySignerService) {
	controller.Config = config
	controller.RelaySignerService = relaySignerService
}

// SignTransaction ...
func (controller *RelayController) SignTransaction(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	w.Header().Set("Content-Type", "application/json")

	//log.GeneralLogger.Println("Body:", r.Body)

	buf, err := io.ReadAll(r.Body)
	if err != nil {
		handleError(ctx, nil, err)
	}
	rdr1 := io.NopCloser(bytes.NewBuffer(buf))
	rdr2 := io.NopCloser(bytes.NewBuffer(buf))

	//	log.GeneralLogger.Println("Request body : ", rdr1)

	var rpcMessage rpc.JsonrpcMessage

	err = json.NewDecoder(rdr1).Decode(&rpcMessage)
	if err != nil {
		log.GeneralLogger.Println("Invalid params")
		log.GeneralLogger.Println(err)
		return
	}

	log.GeneralLogger.Println("JSON-RPC Method:", rpcMessage.Method)

	if rpcMessage.IsPrivTransaction() {
		r.Body = rdr2
		log.GeneralLogger.Println("Is a private Transaction, forward to Besu->Orion")

		serveReverseProxy(controller.Config.Application.NodeURL, w, r)
	} else if rpcMessage.IsPrivRawTransaction() {
		r.Body = rdr2
		log.GeneralLogger.Println("Is a private send Transaction, decrease gas used")

		controller.RelaySignerService.DecreaseGasUsed(ctx, rpcMessage.ID)

		log.GeneralLogger.Println("forward to Besu->Orion")
		serveReverseProxy(controller.Config.Application.NodeURL, w, r)
	} else if rpcMessage.IsRawTransaction() {
		processRawTransaction(ctx, controller.RelaySignerService, rpcMessage, w)
		return
	} else if rpcMessage.IsGetTransactionReceipt() {
		processGetTransactionReceipt(ctx, controller.RelaySignerService, rpcMessage, w)
		return
	} else if rpcMessage.IsGetTransactionCount() {
		processTransactionCount(ctx, controller.RelaySignerService, rpcMessage, w)
		return
	} else if rpcMessage.IsGetMetaTxResult() {
		processGetMetaTxResult(ctx, controller.RelaySignerService, rpcMessage, w)
		return
	} else {
		//	r.Body=rdr2
		err := errors.New("method is not supported")
		data := handleError(ctx, rpcMessage.ID, err)
		w.Write(data)
		return
		//	serveReverseProxy(controller.Config.Application.NodeURL,w,r)
	}
}

func serveReverseProxy(target string, res http.ResponseWriter, req *http.Request) {
	// parse the url
	url, err := url.Parse(target)
	if err != nil {
		handleError(req.Context(), nil, err)
	}
	// create the reverse proxy
	proxy := httputil.NewSingleHostReverseProxy(url)

	// Update the headers to allow for SSL redirection
	req.URL.Host = url.Host
	req.URL.Scheme = url.Scheme
	req.Header.Set("X-Forwarded-Host", req.Header.Get("Host"))
	req.Host = url.Host

	// Note that ServeHttp is non blocking and uses a go routine under the hood
	proxy.ServeHTTP(res, req)
}

func handleError(ctx context.Context, messageID json.RawMessage, err error) []byte {
	//	log.GeneralLogger.Println(err)
	data, err := json.Marshal(service.HandleError(ctx, messageID, err))
	if err != nil {
		log.GeneralLogger.Println("Error trying to marshall a response to client")
	}

	return data
}
