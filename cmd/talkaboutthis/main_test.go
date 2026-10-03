package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"talkaboutthis/internal/domain"
)

// Estos tests cubren el composition root. Se centran en el comportamiento
// observable del comando: que devuelve codigo de error cuando corresponde, que
// escribe donde debe y que no traga fallos de configuracion.

// ejecutar es un helper que invoca run capturando sus salidas.
func ejecutar(args ...string) (stdout, stderr string, err error) {
	var out, errOut bytes.Buffer
	err = run(args, &out, &errOut)
	return out.String(), errOut.String(), err
}

func TestRunVersion(t *testing.T) {
	stdout, _, err := ejecutar("-version")
	if err != nil {
		t.Fatalf("-version devolvio error inesperado: %v", err)
	}
	if !strings.Contains(stdout, programa) {
		t.Errorf("la salida deberia incluir el nombre del programa: %q", stdout)
	}
	if !strings.Contains(stdout, version) {
		t.Errorf("la salida deberia incluir la version: %q", stdout)
	}
}

// TestRunSchemaVerifica el contrato embebido, el unico chequeo real que la CLI
// puede hacer todavia.
func TestRunSchema(t *testing.T) {
	stdout, _, err := ejecutar("-schema")
	if err != nil {
		t.Fatalf("-schema devolvio error inesperado: %v", err)
	}
	if !strings.Contains(stdout, "OK") {
		t.Errorf("-schema deberia confirmar la validacion: %q", stdout)
	}
}

// TestRunSinFlagsMuestraUso comprueba que sin argumentos el usuario recibe
// instrucciones en lugar de una pantalla vacia o un panic.
func TestRunSinFlagsMuestraUso(t *testing.T) {
	stdout, stderr, err := ejecutar()

	if err != nil {
		t.Fatalf("sin flags no deberia haber error: %v", err)
	}
	if !strings.Contains(stdout, "Uso:") {
		t.Errorf("la salida deberia incluir el uso: %q", stdout)
	}
	// El aviso de "fase pendiente" va a stderr en formato log, no a stdout, para
	// no contaminar la salida que un futuro flag `--output table` o `--output json`
	// reservara para datos.
	if !strings.Contains(stderr, "CLI no operativa todavia") {
		t.Errorf("stderr deberia incluir el aviso de que la CLI aun no esta operativa: %q", stderr)
	}
	if !strings.Contains(stderr, "1 de 5") {
		t.Errorf("stderr deberia indicar la fase actual: %q", stderr)
	}
}

func TestRunNivelDeLogInvalido(t *testing.T) {
	_, _, err := ejecutar("-log-level", "verboso")

	if !errors.Is(err, domain.ErrConfigInvalida) {
		t.Errorf("se esperaba ErrConfigInvalida, se obtuvo %v", err)
	}
}

func TestRunFormatoDeLogInvalido(t *testing.T) {
	_, _, err := ejecutar("-log-format", "yaml")

	if !errors.Is(err, domain.ErrConfigInvalida) {
		t.Errorf("se esperaba ErrConfigInvalida, se obtuvo %v", err)
	}
}

func TestRunFlagDesconocido(t *testing.T) {
	_, _, err := ejecutar("--no-existe")

	if err == nil {
		t.Fatal("un flag desconocido debe producir error")
	}
	if !errors.Is(err, domain.ErrConfigInvalida) {
		t.Errorf("se esperaba ErrConfigInvalida, se obtuvo %v", err)
	}
}

// TestRunHelpNoEsError comprueba el caso especial de -h: flag lo reporta como
// error, pero la CLI debe salir con codigo 0 porque es una peticion legitima.
func TestRunHelpNoEsError(t *testing.T) {
	_, stderr, err := ejecutar("-h")

	if err != nil {
		t.Errorf("-h no debe considerarse error: %v", err)
	}
	if !strings.Contains(stderr, "Uso:") {
		t.Errorf("-h deberia imprimir el uso por stderr: %q", stderr)
	}
}

// TestConfigurarLoggerFormatos cubre la construccion del logger estructurado
// (RNF-06), incluido el formato JSON que usan las integraciones de CI.
func TestConfigurarLoggerFormatos(t *testing.T) {
	tests := []struct {
		formato string
		valida  bool
	}{
		{formato: "json", valida: true},
		{formato: "text", valida: true},
		{formato: "yaml", valida: false},
		{formato: "", valida: false},
	}

	for _, tt := range tests {
		t.Run(tt.formato, func(t *testing.T) {
			var buf bytes.Buffer
			logger, err := configurarLogger(&buf, "info", tt.formato)

			if tt.valida {
				if err != nil {
					t.Fatalf("el formato %q deberia ser valido: %v", tt.formato, err)
				}
				logger.Info("mensaje de prueba")
				if buf.Len() == 0 {
					t.Error("el logger no escribio nada")
				}
				return
			}

			if err == nil {
				t.Fatalf("el formato %q deberia ser invalido", tt.formato)
			}
			if !errors.Is(err, domain.ErrConfigInvalida) {
				t.Errorf("se esperaba ErrConfigInvalida, se obtuvo %v", err)
			}
		})
	}
}

// TestLogJSONEsParseable comprueba que el formato JSON produce una linea por
// evento, que es lo que necesitan los agregadores de logs para indexarlo.
func TestLogJSONEsParseable(t *testing.T) {
	var buf bytes.Buffer

	logger, err := configurarLogger(&buf, "info", "json")
	if err != nil {
		t.Fatalf("no se pudo crear el logger: %v", err)
	}

	logger.Info("evento de prueba")

	var evento map[string]any
	if err := json.Unmarshal(buf.Bytes(), &evento); err != nil {
		t.Fatalf("la salida del logger no es JSON valido: %v\nsalida: %q", err, buf.String())
	}

	if evento["msg"] != "evento de prueba" {
		t.Errorf("el mensaje no aparece en el JSON: %v", evento)
	}
	if _, ok := evento["time"]; !ok {
		t.Error("el evento JSON deberia incluir la marca de tiempo")
	}
}

func TestConfigurarLoggerNiveles(t *testing.T) {
	validos := []string{"debug", "info", "warn", "error"}

	for _, nivel := range validos {
		var buf bytes.Buffer
		if _, err := configurarLogger(&buf, nivel, "text"); err != nil {
			t.Errorf("el nivel %q deberia ser valido: %v", nivel, err)
		}
	}

	var buf bytes.Buffer
	if _, err := configurarLogger(&buf, "trace", "text"); !errors.Is(err, domain.ErrConfigInvalida) {
		t.Errorf("el nivel trace deberia ser invalido, se obtuvo %v", err)
	}
}
