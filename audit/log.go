package audit

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
)

// GeneralLogger exported
var GeneralLogger *log.Logger

// ErrorLogger exported
var ErrorLogger *log.Logger

func init() {
	out := openLogWriter()
	GeneralLogger = log.New(out, "General Logger:\t", log.Ldate|log.Ltime|log.Lshortfile)
	ErrorLogger = log.New(out, "Error Logger:\t", log.Ldate|log.Ltime|log.Lshortfile)
}

// openLogWriter abre ./log/idbServiceLog.log para el logging de auditoría. Si no puede
// (cwd sin permisos de escritura, p. ej. `gas-relay-signer --version`, o dir no creable),
// cae a stderr en vez de abortar el proceso — así el binario no muere en el init del
// paquete y flags como --version siguen funcionando desde cualquier directorio.
func openLogWriter() io.Writer {
	absPath, err := filepath.Abs("./log")
	if err != nil {
		fmt.Fprintln(os.Stderr, "audit: no se pudo resolver ./log, log a stderr:", err)
		return os.Stderr
	}
	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		if errDir := os.MkdirAll(absPath, 0750); errDir != nil {
			fmt.Fprintln(os.Stderr, "audit: no se pudo crear", absPath, "-> log a stderr:", errDir)
			return os.Stderr
		}
	}
	f, err := os.OpenFile(absPath+"/idbServiceLog.log", os.O_RDWR|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		fmt.Fprintln(os.Stderr, "audit: no se pudo abrir el log -> stderr:", err)
		return os.Stderr
	}
	return f
}