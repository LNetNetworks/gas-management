/*
Copyright © 2020 Adrian Pareja <adriancc5.5@gmail.com>
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package main

import (
	"net/http"
	"os"
	"time"

	log "github.com/LACNetNetworks/gas-relay-signer/audit"
	"github.com/LACNetNetworks/gas-relay-signer/controller"
	"github.com/LACNetNetworks/gas-relay-signer/events"
	"github.com/LACNetNetworks/gas-relay-signer/model"
	"github.com/LACNetNetworks/gas-relay-signer/service"
	"github.com/spf13/viper"
)

var config *model.Config
var relaySignerService *service.RelaySignerService
var relayController *controller.RelayController

func main() {
	// `gas-relay-signer --version` imprime la versión y sale, ANTES de cargar
	// config.toml (para que funcione sin fichero de configuración).
	if isVersionFlag(os.Args[1:]) {
		printVersion()
		return
	}

	config = getConfigFromFile()

	// El emisor estructurado y el bus se inicializan aca: con la configuracion ya leida y ANTES
	// de levantar el servidor, para que ningun evento salga con una capacidad o un nivel que
	// todavia no se leyeron. No va en un init() porque corre antes de que exista config.toml,
	// ni en una inicializacion perezosa, que esconderia el orden justo donde importa. Ver D5.
	log.InitStructured(config.Log.Level, config.Log.RawTx)
	events.Init(config.Dashboard.Enabled, config.Dashboard.BufferSize)

	relaySignerService = new(service.RelaySignerService)
	err := relaySignerService.Init(config)
	if err != nil {
		log.GeneralLogger.Fatal(err)
		return
	}

	relayController = new(controller.RelayController)
	relayController.Init(config, relaySignerService)
	done := make(chan interface{})
	go relaySignerService.ProcessNewBlocks(done)
	setupRoutes(config.Application.Port)
	close(done)
}

func getConfigFromFile() *model.Config {
	v := viper.New()
	v.SetConfigName("config")
	v.AddConfigPath(".")
	if err := v.ReadInConfig(); err != nil {
		log.GeneralLogger.Printf("couldn't load config: %s", err)
		os.Exit(1)
	}
	var c model.Config
	if err := v.Unmarshal(&c); err != nil {
		log.GeneralLogger.Printf("couldn't read config: %s", err)
		os.Exit(1)
	}
	// Los bloques [reorder], [dashboard] y [log] se leen aparte, clave por clave: un valor
	// invalido en cualquiera de ellos cae a su default y se registra, pero no aborta el arranque.
	var discarded []model.DiscardedKey
	c.Reorder, c.Dashboard, c.Log, discarded = model.LoadRuntimeBlocks(v)
	for _, key := range discarded {
		log.GeneralLogger.Printf("config: se descarto %s = %v (%s), se usa el valor por defecto",
			key.Key, key.Value, key.Reason)
	}
	log.GeneralLogger.Printf("smartContract=%s AgentKey=%s\n", c.Application.ContractAddress, c.KeyStore.Agent)
	return &c
}

func setupRoutes(port string) {
	log.GeneralLogger.Println("Init RelaySigner")
	mux := http.NewServeMux()
	mux.HandleFunc("/", relayController.SignTransaction)
	// http.Server con timeouts explícitos (evita Slowloris/DoS — gosec G114).
	server := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil {
		log.GeneralLogger.Fatal(err)
	}
}
