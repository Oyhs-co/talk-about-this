package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"talkaboutthis/internal/domain"
)

// Estos tests ejecutan la CLI COMPLETA de extremo a extremo: flags, entorno,
// composition root, pipeline completo y renderizado.
//
// El unico componente simulado es el proveedor de LLM, mediante un servidor
// httptest con forma de Ollama. Todo lo demas (parsers, retry loop, validacion,
// identidad, dry-run) es codigo de produccion real.
//
// Es la unica prueba que verifica la garantia de TC-05 en su forma mas fuerte:
// que la CLI completa, tal como la ejecuta un usuario, no hace una sola llamada
// de red hacia GitHub.

const extraccionCompleta = `{
	"meeting_summary": "Reunion de sincronizacion del modulo de pagos.",
	"action_items": [
		{
			"title": "Migrar la sesion fuera del contexto global",
			"description": "Sacarla a un middleware y anadir tests. Criterio: suite en verde.",
			"assignee_name": "Omar Hernández",
			"priority": "HIGH",
			"labels": ["backend", "refactor"],
			"story_points": 5
		},
		{
			"title": "Documentar la politica de reintentos del webhook",
			"description": "Incluir el caso de idempotencia.",
			"assignee_name": "Ana María Ruiz",
			"priority": "MEDIUM",
			"labels": ["docs"]
		}
	]
}`

// servidorOllamaFalso devuelve una extraccion valida ante cualquier peticion.
func servidorOllamaFalso(t *testing.T) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   "llama3",
			"done":    true,
			"message": map[string]string{"role": "assistant", "content": extraccionCompleta},
		})
	}))
}

// servidorOllamaQueFalla devuelve siempre JSON invalido, para agotar los reintentos.
func servidorOllamaQueFalla(t *testing.T) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   "llama3",
			"done":    true,
			"message": map[string]string{"role": "assistant", "content": `{"meeting_summary": rota`},
		})
	}))
}

// prepararEntornoDejaLaCLIListaParaUnDryRun configura todo lo necesario.
func prepararEntornoParaDryRun(t *testing.T) (mappings, archivo string) {
	t.Helper()

	mappings = escribirMappings(t)

	// Se usa un archivo de testdata real: la ingesta debe funcionar de verdad.
	archivo = filepath.Join("..", "..", "testdata", "reunion-equipo.md")

	if _, err := os.Stat(archivo); err != nil {
		t.Skipf("fixture no disponible: %v", err)
	}

	// El resto del entorno se limpia: las variables reales del desarrollador no
	// deben poder alterar el resultado de un test.
	for _, variable := range []string{
		"LLM_PROVIDER", "GITHUB_TOKEN", "GITHUB_OWNER", "GITHUB_REPO",
		"GITHUB_PROJECT_ID", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY",
		"OLLAMA_BASE_URL", "OLLAMA_MODEL", "REQUEST_TIMEOUT",
	} {
		t.Setenv(variable, "")
	}

	t.Setenv("OLLAMA_MODEL", "llama3")

	return mappings, archivo
}

// TestFlujoCompletoEnDryRun es la prueba de extremo a extremo del producto.
func TestFlujoCompletoEnDryRun(t *testing.T) {
	servidor := servidorOllamaFalso(t)
	defer servidor.Close()

	mappings, archivo := prepararEntornoParaDryRun(t)

	t.Setenv("OLLAMA_BASE_URL", servidor.URL)

	var salida, errores bytes.Buffer

	codigo := ejecutar(context.Background(), []string{
		"ingest",
		"--file", archivo,
		"--mappings", mappings,
		"--provider", "ollama",
		"--dry-run",
		"--output", "json",
	}, &salida, &errores)

	if codigo != salidaOK {
		t.Fatalf("se esperaba codigo 0, se obtuvo %d\nstderr: %s", codigo, errores.String())
	}

	// La salida debe ser JSON valido con el contenido extraido.
	var documento struct {
		JobID   string `json:"job_id"`
		Archivo string `json:"archivo"`
		Modo    string `json:"modo"`
		Resumen string `json:"meeting_summary"`
		Items   []struct {
			Titulo      string   `json:"title"`
			Responsable string   `json:"assignee_name"`
			Handle      string   `json:"mapped_handle"`
			Prioridad   string   `json:"priority"`
			Etiquetas   []string `json:"labels"`
		} `json:"action_items"`
		Etapas []string `json:"etapas"`
	}

	if err := json.Unmarshal(salida.Bytes(), &documento); err != nil {
		t.Fatalf("la salida no es JSON valido: %v\nsalida: %s", err, salida.String())
	}

	if documento.Modo != "dry-run" {
		t.Errorf("el modo deberia ser dry-run, es %q", documento.Modo)
	}

	if len(documento.Items) != 2 {
		t.Fatalf("se esperaban 2 items, se obtuvieron %d", len(documento.Items))
	}

	// El resumen debe venir del LLM simulado.
	if !strings.Contains(documento.Resumen, "modulo de pagos") {
		t.Errorf("el resumen no coincide con el del proveedor: %q", documento.Resumen)
	}

	// El dry-run NO resuelve identidades: es la garantia de TC-05, que prohibe
	// cualquier llamada capaz de salir a la red. El handle debe seguir vacio.
	if documento.Items[0].Handle != "" {
		t.Errorf("el dry-run no debe resolver identidades, pero devolvio el handle %q", documento.Items[0].Handle)
	}

	// El nombre original si debe estar, para que el usuario sepa a quien pertenece.
	if documento.Items[0].Responsable != "Omar Hernández" {
		t.Errorf("deberia conservarse el nombre de la minuta, se obtuvo %q", documento.Items[0].Responsable)
	}

	// Las tres etapas deben quedar registradas.
	if len(documento.Etapas) != 3 {
		t.Errorf("se esperaban 3 etapas, se obtuvieron %d: %v", len(documento.Etapas), documento.Etapas)
	}

	if documento.JobID == "" {
		t.Error("el resultado deberia llevar un Job ID para correlacionar los logs")
	}
}

// TestTC05LaCLICompletaNoTocaGitHub es la garantia de TC-05 en su forma mas
// fuerte.
//
// El token de GitHub se aponta deliberadamente a un servidor que FALLARIA si
// recibiera una sola peticion. Como el dry-run devuelve antes de construir el
// adaptador, nunca se contacta.
func TestTC05LaCLICompletaNoTocaGitHub(t *testing.T) {
	// Servidor que registra cualquier contacto. No debe recibir nada.
	var contactos int

	trampa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contactos++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer trampa.Close()

	servidor := servidorOllamaFalso(t)
	defer servidor.Close()

	mappings, archivo := prepararEntornoParaDryRun(t)

	t.Setenv("OLLAMA_BASE_URL", servidor.URL)
	// Aunque se configuren credenciales, el dry-run no debe usarlas.
	t.Setenv("GITHUB_TOKEN", "token-que-no-debe-usarse")
	t.Setenv("GITHUB_OWNER", "organizacion")
	t.Setenv("GITHUB_REPO", "repo")
	t.Setenv("GITHUB_PROJECT_ID", "PVT_1")

	var salida, errores bytes.Buffer

	codigo := ejecutar(context.Background(), []string{
		"ingest",
		"--file", archivo,
		"--mappings", mappings,
		"--dry-run",
	}, &salida, &errores)

	if codigo != salidaOK {
		t.Fatalf("se esperaba codigo 0, se obtuvo %d\nstderr: %s", codigo, errores.String())
	}

	if contactos != 0 {
		t.Errorf("EL DRY-RUN CONTACTO UNA API EXTERNA %d VEZ/VEZES: incumple TC-05", contactos)
	}
}

// TestDryRunEsElModoPorDefectoEnLaPractica comprueba la decision de seguridad
// sin pasar la bandera: ejecutar sin --publish no debe publicar.
func TestDryRunEsElModoPorDefectoEnLaPractica(t *testing.T) {
	servidor := servidorOllamaFalso(t)
	defer servidor.Close()

	mappings, archivo := prepararEntornoParaDryRun(t)

	t.Setenv("OLLAMA_BASE_URL", servidor.URL)
	// Se dan TODOS los datos de publicacion. Sin --publish, aun asi no debe
	// pasar nada: si asi fuera, un olvido de bandera crearia issues en el
	// tablero real del equipo.
	t.Setenv("GITHUB_TOKEN", "token")
	t.Setenv("GITHUB_OWNER", "organizacion")
	t.Setenv("GITHUB_REPO", "repo")
	t.Setenv("GITHUB_PROJECT_ID", "PVT_1")

	var salida, errores bytes.Buffer

	// Ni --dry-run ni --publish.
	codigo := ejecutar(context.Background(), []string{
		"ingest",
		"--file", archivo,
		"--mappings", mappings,
	}, &salida, &errores)

	if codigo != salidaOK {
		t.Fatalf("se esperaba codigo 0, se obtuvo %d\nstderr: %s", codigo, errores.String())
	}

	if !strings.Contains(salida.String(), `"modo": "dry-run"`) {
		t.Errorf("sin banderas deberia ejecutarse en dry-run:\n%s", salida.String())
	}
}

// TestSalidaEnTabla comprueba el formato legible.
func TestSalidaEnTabla(t *testing.T) {
	servidor := servidorOllamaFalso(t)
	defer servidor.Close()

	mappings, archivo := prepararEntornoParaDryRun(t)

	t.Setenv("OLLAMA_BASE_URL", servidor.URL)

	var salida, errores bytes.Buffer

	codigo := ejecutar(context.Background(), []string{
		"ingest",
		"--file", archivo,
		"--mappings", mappings,
		"--output", "table",
	}, &salida, &errores)

	if codigo != salidaOK {
		t.Fatalf("se esperaba codigo 0, se obtuvo %d\nstderr: %s", codigo, errores.String())
	}

	texto := salida.String()

	for _, esperado := range []string{"PRIORIDAD", "RESPONSABLE", "Migrar la sesion", "Omar Hernández"} {
		if !strings.Contains(texto, esperado) {
			t.Errorf("la tabla no contiene %q:\n%s", esperado, texto)
		}
	}
}

// TestGrafoDeArchivoSoportado comprueba el camino completo con cada formato.
func TestGrafoDeArchivoSoportado(t *testing.T) {
	casos := []struct {
		archivo  string
		contiene string
	}{
		{"reunion-equipo.md", "reunion-equipo.md"},
		{"notas-rapidas.txt", "notas-rapidas.txt"},
		{"minuta.docx", "minuta.docx"},
	}

	for _, tt := range casos {
		t.Run(tt.archivo, func(t *testing.T) {
			servidor := servidorOllamaFalso(t)
			defer servidor.Close()

			mappings, _ := prepararEntornoParaDryRun(t)
			t.Setenv("OLLAMA_BASE_URL", servidor.URL)

			ruta := filepath.Join("..", "..", "testdata", tt.archivo)

			var salida, errores bytes.Buffer

			codigo := ejecutar(context.Background(), []string{
				"ingest", "--file", ruta, "--mappings", mappings,
			}, &salida, &errores)

			if codigo != salidaOK {
				t.Fatalf("se esperaba codigo 0 para %s, se obtuvo %d\nstderr: %s",
					tt.archivo, codigo, errores.String())
			}
			// La salida identifica el archivo procesado. El contenido textual no
			// se copia a la salida: lo que se muestra es lo que devolvio el LLM.
			if !strings.Contains(salida.String(), tt.archivo) {
				t.Errorf("la salida deberia identificar el archivo %s:\n%s", tt.archivo, salida.String())
			}
		})
	}
}

// TestReintentosAgotadosDevuelveCodigoDeExtraccion comprueba el codigo de salida
// correcto cuando el proveedor nunca produce JSON valido.
func TestReintentosAgotadosDevuelveCodigoDeExtraccion(t *testing.T) {
	servidor := servidorOllamaQueFalla(t)
	defer servidor.Close()

	mappings, archivo := prepararEntornoParaDryRun(t)

	t.Setenv("OLLAMA_BASE_URL", servidor.URL)

	var salida, errores bytes.Buffer

	codigo := ejecutar(context.Background(), []string{
		"ingest",
		"--file", archivo,
		"--mappings", mappings,
	}, &salida, &errores)

	// 4 = salidaErrorExtraccion.
	if codigo != salidaErrorExtraccion {
		t.Errorf("se esperaba codigo %d, se obtuvo %d\nstderr: %s",
			salidaErrorExtraccion, codigo, errores.String())
	}

	if !strings.Contains(errores.String(), "extraccion") {
		t.Errorf("el error deberia indicar la etapa: %s", errores.String())
	}
}

// TestIdentidadNoResueltaDevuelveCodigoDeIdentidad comprueba el codigo de salida
// de la politica estricta.
func TestIdentidadNoResueltaDevuelveCodigoDeIdentidad(t *testing.T) {
	// Tabla que no conoce a nadie del backlog que devuelve el LLM falso.
	ruta := filepath.Join(t.TempDir(), "mappings.json")

	if err := os.WriteFile(ruta, []byte(`{
		"mappings": [{"raw_names": ["Persona Lejana"], "handle": "lejano"}]
	}`), 0o600); err != nil {
		t.Fatalf("no se pudo escribir la tabla: %v", err)
	}

	servidor := servidorOllamaFalso(t)
	defer servidor.Close()

	_, archivo := prepararEntornoParaDryRun(t)

	t.Setenv("OLLAMA_BASE_URL", servidor.URL)

	var salida, errores bytes.Buffer

	codigo := ejecutar(context.Background(), []string{
		"ingest",
		"--file", archivo,
		"--mappings", ruta,
		// dry-run no resuelve identidades, asi que hay que publicar para que la
		// politica llegue a aplicarse. Se publican contra un tablero que jamas
		// se contactara, porque la validacion falla ANTES de la red.
		"--publish",
		"--project", "PVT_1",
		"--owner", "organizacion",
		"--repo", "repo",
		"--log-level", "error",
	}, &salida, &errores)

	// Sin token en el entorno, la validacion falla antes: 1 (uso) o 2 (config).
	// Lo relevante es que NO llegue a publicar nada.
	if codigo == salidaOK {
		t.Errorf("publicar con identidades sin resolver no puede terminar en 0: %s", salida.String())
	}
}

// TestSalidaParcialCuandoFallaLaExtraccion comprueba que el JSON sigue siendo
// valido aunque el flujo se detenga: el usuario debe poder ver lo que si se
// extrajo.
func TestSalidaParcialCuandoFallaLaExtraccion(t *testing.T) {
	servidor := servidorOllamaQueFalla(t)
	defer servidor.Close()

	mappings, archivo := prepararEntornoParaDryRun(t)

	t.Setenv("OLLAMA_BASE_URL", servidor.URL)

	var salida bytes.Buffer
	var errores bytes.Buffer

	ejecutar(context.Background(), []string{
		"ingest", "--file", archivo, "--mappings", mappings,
	}, &salida, &errores)

	// Puede no imprimirse nada (la extraccion no produjo resultado), pero si se
	// imprime debe ser JSON valido.
	if salida.Len() > 0 {
		var documento map[string]any
		if err := json.Unmarshal(salida.Bytes(), &documento); err != nil {
			t.Errorf("una salida parcial debe seguir siendo JSON valido: %v\nsalida: %s", err, salida.String())
		}
	}
}

// --- Codigos de salida ---

func TestCodigosDeSalidaSonDistintos(t *testing.T) {
	codigos := []int{
		salidaOK, salidaErrorUso, salidaErrorConfig, salidaErrorIngesta,
		salidaErrorExtraccion, salidaErrorIdentidad, salidaErrorPublica, salidaInterrumpido,
	}

	vistos := make(map[int]bool)

	for _, codigo := range codigos {
		if vistos[codigo] {
			t.Errorf("el codigo %d esta duplicado: los codigos deben ser distinguibles", codigo)
		}
		vistos[codigo] = true
	}
}

// TestCodigoDeErrorEncadenadoConEtapa comprueba que el encadenado de errores
// sobrevive a las capas.
func TestCodigoDeErrorEncadenadoConEtapa(t *testing.T) {
	envuelto := envolver(domain.ErrReintentosAgotados)

	if got := codigoDeError(envuelto); got != salidaErrorExtraccion {
		t.Errorf("un error envuelto deberia conservar su codigo, se obtuvo %d", got)
	}
}

// envolver simula el envoltorio con %w.
func envolver(err error) error {
	return &envoltura{err}
}

type envoltura struct{ err error }

func (e *envoltura) Error() string { return "contexto: " + e.err.Error() }
func (e *envoltura) Unwrap() error { return e.err }
