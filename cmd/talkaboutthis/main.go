// Command talkaboutthis es el punto de entrada de la CLI.
//
// Este archivo es el COMPOSITION ROOT: el unico lugar donde se permite construir
// dependencias concretas y conectarlas con los puertos del dominio. Contiene
// cableado, no logica de negocio: si aqui aparece una regla de negocio, la
// arquitectura esta mal.
//
// En la Fase 1 su unico proposito es dejar el binario compilando y verificando
// que el JSON Schema se carga correctamente. El CLI completo (subcomando
// ingest, flags --dry-run/--publish, salida tabular) llega en la Fase 5.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"

	"talkaboutthis/docs/specifications"
	"talkaboutthis/internal/domain"
)

const (
	version  = "0.1.0"
	programa = "talkaboutthis"
)

const descUso = `talkaboutthis - convierte minutas de reunion en items de backlog

Uso:
  talkaboutthis ingest --file <ruta> [--dry-run | --publish] [opciones]

El subcomando ingest se habilita en la Fase 5. Esta version expone --version y
--schema para verificar el binario y el contrato de extraccion embebido.

Opciones:
  -version      muestra la version y termina
  -schema       valida el JSON Schema embebido y termina
  -log-level    nivel de log: debug, info, warn, error (por defecto info)
  -log-format   formato de log: text o json (por defecto text)
`

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		// Los errores de configuracion son responsabilidad del operador: salen
		// por stderr con exit 1 y sin traza, porque el usuario puede corregirlos.
		fmt.Fprintf(os.Stderr, "%s: %v\n", programa, err)
		os.Exit(1)
	}
}

// run contiene la logica del comando, separada de main para que sea testeable
// sin manipular os.Exit ni los argumentos globales del proceso.
func run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet(programa, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, descUso) }

	var (
		versionFlag  = fs.Bool("version", false, "muestra la version y termina")
		schemaFlag   = fs.Bool("schema", false, "valida el JSON Schema embebido y termina")
		logLevelFlag = fs.String("log-level", "info", "nivel de log: debug, info, warn, error")
		logFormat    = fs.String("log-format", "text", "formato de log: text o json")
	)

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil // -h ya escribio el uso por stderr; no es un fallo
		}
		return fmt.Errorf("%w: %w", domain.ErrConfigInvalida, err)
	}

	if *versionFlag {
		fmt.Fprintf(stdout, "%s %s\n", programa, version)
		return nil
	}

	// El contrato se valida al arrancar, y no en cada peticion: si el schema
	// esta corrupto, el fallo debe aparecer en el primer uso y no a mitad de
	// una ejecucion con credenciales ya cargadas.
	if err := specifications.Validar(); err != nil {
		return fmt.Errorf("%w: %w", domain.ErrConfigInvalida, err)
	}

	if *schemaFlag {
		fmt.Fprintln(stdout, "OK: el JSON Schema embebido es valido")
		return nil
	}

	logger, err := configurarLogger(stderr, *logLevelFlag, *logFormat)
	if err != nil {
		return err
	}

	logger.Info("CLI no operativa todavia",
		slog.String("version", version),
		slog.String("fase", "1 de 5: dominio y contratos"),
		slog.String("siguiente", "la Fase 5 habilita el subcomando ingest"),
	)

	fmt.Fprint(stdout, descUso)
	return nil
}

// configurarLogger construye el logger estructurado con log/slog (RNF-06).
func configurarLogger(w io.Writer, nivel, formato string) (*slog.Logger, error) {
	var lv slog.Level

	switch nivel {
	case "debug":
		lv = slog.LevelDebug
	case "info":
		lv = slog.LevelInfo
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		return nil, fmt.Errorf("%w: nivel de log desconocido %q", domain.ErrConfigInvalida, nivel)
	}

	opts := &slog.HandlerOptions{Level: lv}

	var handler slog.Handler
	switch formato {
	case "json":
		handler = slog.NewJSONHandler(w, opts)
	case "text":
		handler = slog.NewTextHandler(w, opts)
	default:
		return nil, fmt.Errorf("%w: formato de log desconocido %q", domain.ErrConfigInvalida, formato)
	}

	return slog.New(handler), nil
}
