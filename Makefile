# Makefile — build del RelaySigner (gas-relay-signer) con versión inyectada.
#
# La versión sale del tag git (git describe): al compilar en el tag v1.1.0 el
# binario reporta "v1.1.0"; en develop sin tag, algo como "v1.0.1-9-g5b7a3a7".
# Se puede forzar con:  make build VERSION=v1.1.0

BINARY  := gas-relay-signer
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

.PHONY: build version test clean

## build: compila el binario con la versión inyectada (host)
build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY)

## build-linux: cross-compila para el nodo (linux/amd64) con la versión inyectada
build-linux:
	GOOS=linux GOARCH=amd64 GOTOOLCHAIN=auto go build -ldflags "$(LDFLAGS)" -o $(BINARY)-linux-amd64

## version: compila e imprime la versión (verificación rápida)
version: build
	./$(BINARY) --version

## test: corre los tests
test:
	go test ./...

## clean: borra el binario
clean:
	rm -f $(BINARY)
