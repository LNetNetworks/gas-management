package main

import (
	"fmt"
	"runtime"
)

// Metadatos de versión. Se inyectan en tiempo de compilación con
//
//	-ldflags "-X main.version=... -X main.commit=... -X main.date=..."
//
// (ver el Makefile, target `build`). Sin ldflags el binario reporta "dev".
// La fuente de verdad de `version` es el tag git (p. ej. v1.1.0), derivado con
// `git describe --tags`.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// isVersionFlag indica si algún argumento pide imprimir la versión.
func isVersionFlag(args []string) bool {
	for _, a := range args {
		switch a {
		case "--version", "-version", "-v", "version":
			return true
		}
	}
	return false
}

// printVersion escribe la versión y metadatos de build en stdout.
func printVersion() {
	fmt.Printf("gas-relay-signer %s (commit %s, built %s, %s)\n", version, commit, date, runtime.Version())
}
