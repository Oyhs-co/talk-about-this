package logging_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"talkaboutthis/internal/infrastructure/logging"
)

// Estos tests cubren la configuracion del logger y los identificadores de
// correlacion (RNF-06).

func TestParseFormato(t *testing.T) {
	casos := []struct {
		entrada string
		quiere  logging.Formato
		falla   bool
	}{
		{entrada: "json", quiere: logging.FormatoJSON},
		{entrada: "text", quiere: logging.FormatoTexto},
		{entrada: "JSON", quiere: logging.FormatoJSON},
		{entrada: "  text  ", quiere: logging.FormatoTexto},
		{entrada: "yaml", falla: true},
		{entrada: "", falla: true},
	}

	for _, tt := range casos {
		t.Run(tt.entrada, func(t *testing.T) {
			got, err := logging.ParseFormato(tt.entrada)

			if tt.falla {
				if err == nil {
					t.Fatalf("se esperaba error para %q", tt.entrada)
				}
				if !errors.Is(err, logging.ErrFormato) {
					t.Errorf("se esperaba ErrFormato, se obtuvo %v", err)
				}
				return
			}

			if err != nil {
				t.Fatalf("error inesperado: %v", err)
			}
			if got != tt.quiere {
				t.Errorf("ParseFormato(%q) = %q, se esperaba %q", tt.entrada, got, tt.quiere)
			}
		})
	}
}

func TestNuevoNiveles(t *testing.T) {
	validos := []string{"debug", "info", "warn", "warning", "error", "DEBUG", ""}

	for _, nivel := range validos {
		t.Run(nivel, func(t *testing.T) {
			var buf bytes.Buffer

			if _, err := logging.Nuevo(logging.Configuracion{Nivel: nivel, Formato: logging.FormatoJSON}, &buf); err != nil {
				t.Errorf("el nivel %q deberia ser valido: %v", nivel, err)
			}
		})
	}

	var buf bytes.Buffer

	_, err := logging.Nuevo(logging.Configuracion{Nivel: "trace"}, &buf)
	if !errors.Is(err, logging.ErrFormato) {
		t.Errorf("un nivel desconocido debe fallar, se obtuvo %v", err)
	}
}

func TestNuevoFormatoInvalido(t *testing.T) {
	var buf bytes.Buffer

	_, err := logging.Nuevo(logging.Configuracion{Formato: "xml"}, &buf)

	if !errors.Is(err, logging.ErrFormato) {
		t.Errorf("un formato desconocido debe fallar, se obtuvo %v", err)
	}
}

// TestNivelFiltraEventos comprueba que el nivel configurado descarta los
// eventos por debajo del umbral, que es de lo que sirve.
func TestNivelFiltraEventos(t *testing.T) {
	var buf bytes.Buffer

	logger, err := logging.Nuevo(logging.Configuracion{Nivel: "warn", Formato: logging.FormatoJSON}, &buf)
	if err != nil {
		t.Fatalf("no se pudo crear el logger: %v", err)
	}

	logger.Debug("deberia filtrarse")
	logger.Info("tambien deberia filtrarse")
	logger.Warn("este si debe aparecer")

	salida := buf.String()

	if strings.Contains(salida, "filtrarse") {
		t.Errorf("los eventos por debajo del nivel deben descartarse:\n%s", salida)
	}
	if !strings.Contains(salida, "este si debe aparecer") {
		t.Errorf("el evento del nivel configurado debe aparecer:\n%s", salida)
	}
}

// TestJSONEsParseableYTraeElJobID es el contrato con los agregadores de logs.
func TestJSONEsParseableYTraeElJobID(t *testing.T) {
	var buf bytes.Buffer

	logger, err := logging.Nuevo(logging.Configuracion{Nivel: "info", Formato: logging.FormatoJSON}, &buf)
	if err != nil {
		t.Fatalf("no se pudo crear el logger: %v", err)
	}

	conJob := logging.ConJobID(logger, "job-123456")
	conJob.Info("procesando reunion")

	var evento map[string]any
	if err := json.Unmarshal(buf.Bytes(), &evento); err != nil {
		t.Fatalf("la salida deberia ser JSON valido: %v\n%s", err, buf.String())
	}

	if evento["msg"] != "procesando reunion" {
		t.Errorf("el mensaje no aparece: %v", evento)
	}
	if evento["job_id"] != "job-123456" {
		t.Errorf("el Job ID no aparece en cada evento: %v", evento)
	}
	if _, ok := evento["time"]; !ok {
		t.Error("deberia incluirse la marca de tiempo")
	}
}

func TestConJobIDToleraNil(t *testing.T) {
	// No debe entrar en panic: un logger ausente significa "usa el global".
	if got := logging.ConJobID(nil, "job-1"); got != nil {
		t.Errorf("ConJobID(nil) deberia devolver nil, devolvio %v", got)
	}

	var buf bytes.Buffer

	logger, _ := logging.Nuevo(logging.Configuracion{Formato: logging.FormatoJSON}, &buf)
	if got := logging.ConJobID(logger, ""); got != logger {
		t.Error("un Job ID vacio deberia devolver el logger sin modificar")
	}
}

// TestJobIDSonUnicos importa en CI: dos procesos paralelos no pueden compartir
// identificador o sus logs se mezclarian.
func TestJobIDSonUnicos(t *testing.T) {
	const total = 500

	vistos := make(map[string]bool, total)

	for i := 0; i < total; i++ {
		id := logging.NuevoJobID()

		if id == "" {
			t.Fatal("el Job ID no deberia estar vacio")
		}
		if vistos[id] {
			t.Fatalf("el Job ID %q se genero dos veces", id)
		}
		vistos[id] = true
	}
}

func TestJobIDTienePrefijo(t *testing.T) {
	id := logging.NuevoJobID()

	if !strings.HasPrefix(id, "job-") {
		t.Errorf("el Job ID deberia llevar prefijo legible, es %q", id)
	}
}

func TestJobIDEnContexto(t *testing.T) {
	ctx := context.Background()

	if got := logging.JobIDDeContexto(ctx); got != "" {
		t.Errorf("un contexto sin Job ID deberia devolver vacio, devolvio %q", got)
	}

	ctx = logging.ConJobIDEnContexto(ctx, "job-abc")

	if got := logging.JobIDDeContexto(ctx); got != "job-abc" {
		t.Errorf("JobIDDeContexto = %q, se esperaba job-abc", got)
	}
}

func TestJobIDDeContextoNil(t *testing.T) {
	// nil no debe entrar en panic: un contexto nil es un error de quien llama,
	// y esta funcion es defensiva por ser usada en rutas de error.
	if got := logging.JobIDDeContexto(nil); got != "" {
		t.Errorf("deberia devolver vacio, devolvio %q", got)
	}
}

func TestFormatosValidos(t *testing.T) {
	if len(logging.FormatosValidos) != 2 {
		t.Errorf("se esperaban 2 formatos validos, hay %d", len(logging.FormatosValidos))
	}
}
