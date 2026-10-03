package application_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"talkaboutthis/internal/application"
	"talkaboutthis/internal/domain"
	"talkaboutthis/internal/infrastructure/llm"
)

// TestMain silencia los logs estructurados.
//
// El caso de uso usa slog (RNF-06) y sus trazas son utiles en produccion, pero
// durante los tests saturan la salida y hacen dificil leer un fallo. Se
// redirigen a io.Discard; los asserts siguen siendo validos porque el log no
// participa en ninguna decision.
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

// Estos tests ejercitan el Retry Loop con autocorreccion (RF-07) usando un
// proveedor FALSO. Ninguno sale a la red: el proveedor simula las respuestas,
// que es la unica forma de probar el reintento de forma determinista.
//
// El proveedor real (httptest) ya esta cubierto en internal/infrastructure/llm.

func schemaReal(t *testing.T) []byte {
	t.Helper()

	ruta := filepath.Join("..", "..", "docs", "specifications", "backlog_schema.json")

	contenido, err := os.ReadFile(ruta)
	if err != nil {
		t.Fatalf("no se pudo leer el schema real: %v", err)
	}

	return contenido
}

const extraccionValida = `{
	"meeting_summary": "Reunion de arquitectura del modulo de pagos.",
	"action_items": [{
		"title": "Migrar la sesion",
		"description": "Sacarla del contexto global a un middleware.",
		"assignee_name": "Omar Hernandez",
		"priority": "HIGH",
		"labels": ["backend"],
		"story_points": 3
	}]
}`

// proveedorFalso es un domain.LLMProvider que devuelve respuestas preparadas.
type proveedorFalso struct {
	mu sync.Mutex
	// respuestas se consume una por llamada. La ultima se repite si se agotan.
	respuestas []string
	// errores se consume a la par; un error no vacio aborta la llamada.
	errores []error
	// llamadas registra cada prompt recibido, para verificar que el segundo
	// intento lleva los errores del primero.
	prompts []string
}

func (p *proveedorFalso) Name() string { return "falso/modelo-test" }

func (p *proveedorFalso) GenerateStructuredOutput(ctx context.Context, prompt string, schemaJSON string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.prompts = append(p.prompts, prompt)

	indice := len(p.prompts) - 1

	if indice < len(p.errores) && p.errores[indice] != nil {
		return nil, p.errores[indice]
	}

	if len(p.respuestas) == 0 {
		return nil, errors.New("el proveedor falso no tiene respuestas preparadas")
	}

	if indice >= len(p.respuestas) {
		indice = len(p.respuestas) - 1
	}

	return []byte(p.respuestas[indice]), nil
}

func (p *proveedorFalso) numLlamadas() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.prompts)
}

func (p *proveedorFalso) promptEn(i int) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i >= len(p.prompts) {
		return ""
	}
	return p.prompts[i]
}

// constructorDePromptFalso registra los prompts que construye el caso de uso.
type constructorDePromptFalso struct {
	iniciales    []string
	correcciones [][]string
}

func (c *constructorDePromptFalso) Construir(transcript, schemaJSON string) string {
	c.iniciales = append(c.iniciales, transcript)
	return "PROMPT_INICIAL: " + transcript
}

func (c *constructorDePromptFalso) ConstruirCorreccion(transcript, schemaJSON string, errores []string) string {
	c.correcciones = append(c.correcciones, errores)
	return "PROMPT_CORRECCION: " + strings.Join(errores, " | ")
}

// extraer construye el caso de uso con el proveedor y prompt indicados.
func extraer(t *testing.T, proveedor domain.LLMProvider, prompt application.ConstructorDePrompt) *application.ExtractBacklog {
	t.Helper()

	caso, err := application.NuevoExtractBacklog(application.ExtractOpciones{
		Proveedor: proveedor,
		Prompt:    prompt,
		Schema:    schemaReal(t),
	})
	if err != nil {
		t.Fatalf("no se pudo construir el caso de uso: %v", err)
	}

	return caso
}

// --- TC-02: extraccion correcta ---

// TestExtraccionCorrectaEnUnIntento es el camino feliz.
func TestExtraccionCorrectaEnUnIntento(t *testing.T) {
	proveedor := &proveedorFalso{respuestas: []string{extraccionValida}}
	caso := extraer(t, proveedor, llm.NuevoPromptBuilder())

	extraccion, err := caso.Ejecutar(context.Background(), "transcripcion de la reunion")
	if err != nil {
		t.Fatalf("la extraccion fallo: %v", err)
	}

	if len(extraccion.ActionItems) != 1 {
		t.Fatalf("se esperaba 1 item, se obtuvieron %d", len(extraccion.ActionItems))
	}

	item := extraccion.ActionItems[0]
	if item.Title != "Migrar la sesion" {
		t.Errorf("titulo inesperado: %q", item.Title)
	}
	if item.RawAssignee != "Omar Hernandez" {
		t.Errorf("assignee inesperado: %q", item.RawAssignee)
	}
	if item.Priority != domain.PriorityHigh {
		t.Errorf("prioridad inesperada: %q", item.Priority)
	}

	// Un resultado valido no debe gastar reintentos.
	if proveedor.numLlamadas() != 1 {
		t.Errorf("se esperaba 1 llamada al proveedor, se hicieron %d", proveedor.numLlamadas())
	}
}

// --- TC-03: Retry Loop con autocorreccion ---

// TestTC03ReintentaAnteJSONCorrupto es el caso de evaluacion TC-03.
//
// Criterio: ante una respuesta con sintaxis JSON corrupta, el sistema reintenta
// automaticamente con el contexto del error antes de rendirse.
func TestTC03ReintentaAnteJSONCorrupto(t *testing.T) {
	// El primer intento devuelve JSON truncado: sintaxis rota.
	proveedor := &proveedorFalso{
		respuestas: []string{
			`{"meeting_summary": "Reunion", "action_items": [{"title": "Migrar"`,
			extraccionValida,
		},
	}

	constructor := &constructorDePromptFalso{}
	caso := extraer(t, proveedor, constructor)

	extraccion, err := caso.Ejecutar(context.Background(), "transcripcion")
	if err != nil {
		t.Fatalf("el reintento no deberia haber fallado: %v", err)
	}

	if len(extraccion.ActionItems) != 1 {
		t.Errorf("se esperaba 1 item tras la correccion, se obtuvieron %d", len(extraccion.ActionItems))
	}

	// Deben hubo exactamente 2 llamadas: la fallida y la corregida.
	if proveedor.numLlamadas() != 2 {
		t.Errorf("se esperaban 2 llamadas al proveedor, se hicieron %d", proveedor.numLlamadas())
	}

	// El segundo prompt debe llevar el error de sintaxis del primer intento.
	if len(constructor.correcciones) != 1 {
		t.Fatalf("se esperaba 1 prompt de correccion, se obtuvieron %d", len(constructor.correcciones))
	}

	errores := constructor.correcciones[0]
	if len(errores) == 0 {
		t.Fatal("el prompt de correccion debe adjuntar los errores detectados")
	}
	if !strings.Contains(strings.ToLower(errores[0]), "json") {
		t.Errorf("el error de correccion deberia mencionar el problema de JSON: %q", errores[0])
	}
}

// TestReintentaAnteViolacionDeSchema comprueba el otro camino: JSON bien formado
// pero con valores que violan el contrato.
func TestReintentaAnteViolacionDeSchema(t *testing.T) {
	// priority fuera del enum: el LLM se inventa una prioridad.
	invalida := `{
		"meeting_summary": "Reunion",
		"action_items": [{
			"title": "Migrar la sesion",
			"description": "Sacarla del contexto global.",
			"assignee_name": "Omar",
			"priority": "URGENTE",
			"labels": []
		}]
	}`

	proveedor := &proveedorFalso{respuestas: []string{invalida, extraccionValida}}
	constructor := &constructorDePromptFalso{}

	caso := extraer(t, proveedor, constructor)

	if _, err := caso.Ejecutar(context.Background(), "transcripcion"); err != nil {
		t.Fatalf("el reintento no deberia haber fallado: %v", err)
	}

	if proveedor.numLlamadas() != 2 {
		t.Errorf("se esperaban 2 llamadas, se hicieron %d", proveedor.numLlamadas())
	}

	// El error debe nombrar el campo y las opciones validas, para que el modelo
	// pueda autocorregirse.
	errores := strings.Join(constructor.correcciones[0], " ")
	if !strings.Contains(errores, "priority") {
		t.Errorf("el error deberia señalar el campo priority: %s", errores)
	}
	if !strings.Contains(errores, "HIGH") {
		t.Errorf("el error deberia listar los valores admitidos: %s", errores)
	}
}

// TestAgotaIntentosYTieneExitoEsElMaximo: si nunca se corrige, falla tras 3.
func TestAgotaIntentos(t *testing.T) {
	siempreInvalida := `{"meeting_summary": "", "action_items": []}`

	proveedor := &proveedorFalso{respuestas: []string{siempreInvalida}}
	caso := extraer(t, proveedor, llm.NuevoPromptBuilder())

	_, err := caso.Ejecutar(context.Background(), "transcripcion")

	if !errors.Is(err, domain.ErrReintentosAgotados) {
		t.Fatalf("se esperaba ErrReintentosAgotados, se obtuvo %v", err)
	}

	// Exactamente 3 intentos, no mas: es el limite del contrato.
	if proveedor.numLlamadas() != application.MaxIntentosExtraccion {
		t.Errorf("se esperaban %d intentos, se hicieron %d",
			application.MaxIntentosExtraccion, proveedor.numLlamadas())
	}

	// El error final debe incluir la causa, no solo "fallo".
	if !strings.Contains(err.Error(), "meeting_summary") {
		t.Errorf("el error final deberia incluir la causa concreta: %v", err)
	}
}

// TestErroresSeAcumulanEntreIntentos comprueba que el tercer intento conoce los
// problemas del primero Y del segundo.
func TestErroresSeAcumulanEntreIntentos(t *testing.T) {
	proveedor := &proveedorFalso{
		respuestas: []string{
			`{roto`,                    // intento 1: sintaxis
			`{"meeting_summary": "x"}`, // intento 2: faltan claves
			extraccionValida,           // intento 3: correcto
		},
	}

	constructor := &constructorDePromptFalso{}
	caso := extraer(t, proveedor, constructor)

	if _, err := caso.Ejecutar(context.Background(), "transcripcion"); err != nil {
		t.Fatalf("deberia haber tenido exito en el tercer intento: %v", err)
	}

	if len(constructor.correcciones) != 2 {
		t.Fatalf("se esperaban 2 correcciones, se obtuvieron %d", len(constructor.correcciones))
	}

	// La segunda correccion debe conocer los errores de ambos intentos previos.
	segunda := strings.Join(constructor.correcciones[1], " ")
	if !strings.Contains(segunda, "action_items") {
		t.Errorf("la segunda correccion deberia conocer el error del intento 2: %s", segunda)
	}
}

// TestFalloDelProveedorNoSeReintenta es una decision importante: un error de
// red o de credencial NO se arregla cambiando el prompt.
func TestFalloDelProveedorNoSeReintenta(t *testing.T) {
	proveedor := &proveedorFalso{
		respuestas: []string{extraccionValida},
		errores:    []error{errors.New("credencial invalida")},
	}

	caso := extraer(t, proveedor, llm.NuevoPromptBuilder())

	_, err := caso.Ejecutar(context.Background(), "transcripcion")

	if err == nil {
		t.Fatal("se esperaba error")
	}
	if errors.Is(err, domain.ErrReintentosAgotados) {
		t.Errorf("un fallo del proveedor no es un problema de formato: %v", err)
	}
	if proveedor.numLlamadas() != 1 {
		t.Errorf("un fallo del proveedor no debe reintentarse con el mismo prompt, hubo %d llamadas",
			proveedor.numLlamadas())
	}
}

// TestContextoCanceladoDetieneElReintento comprueba RNF-03 en el Retry Loop.
func TestContextoCanceladoDetieneElReintento(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	proveedor := &proveedorFalso{respuestas: []string{extraccionValida}}
	caso := extraer(t, proveedor, llm.NuevoPromptBuilder())

	_, err := caso.Ejecutar(ctx, "transcripcion")

	if !errors.Is(err, context.Canceled) {
		t.Errorf("se esperaba context.Canceled, se obtuvo %v", err)
	}
	if proveedor.numLlamadas() != 0 {
		t.Error("no deberia haberse llamado al proveedor con el contexto ya cancelado")
	}
}

// TestInvarianteDelDominioSeAplica tras el schema.
//
// El schema no puede expresar que un titulo en blanco no es una tarea, asi que
// la tercera capa de validacion debe rechazarlo.
func TestInvarianteDelDominioSeAplica(t *testing.T) {
	// Cumple el schema (title es string, no tiene minLength) pero el titulo
	// esta vacio, lo cual el dominio rechaza.
	conTituloVacio := `{
		"meeting_summary": "Reunion",
		"action_items": [{
			"title": "",
			"description": "Tarea sin titulo.",
			"assignee_name": "Omar",
			"priority": "HIGH",
			"labels": []
		}]
	}`

	proveedor := &proveedorFalso{respuestas: []string{conTituloVacio}}
	constructor := &constructorDePromptFalso{}

	caso := extraer(t, proveedor, constructor)

	_, err := caso.Ejecutar(context.Background(), "transcripcion")

	if !errors.Is(err, domain.ErrReintentosAgotados) {
		t.Fatalf("un titulo vacio debe agotar los reintentos, se obtuvo %v", err)
	}

	// El error de correccion debe explicar el problema del titulo. La clave
	// aparece en ingles ("title") porque es la del JSON Schema; el mensaje que
	// la rodea esta en espanol.
	errores := strings.Join(constructor.correcciones[0], " ")
	if !strings.Contains(errores, "action_items[0].title") {
		t.Errorf("el error deberia señalar el titulo vacio: %s", errores)
	}
}

// TestClaveExtraEsRechazada comprueba additionalProperties:false.
//
// El LLM tiende a "mejorar" la respuesta con campos propios ("confidence",
// "notes"). Si se aceptaran, se publicarian en el tablero sin que nadie sepa
// interpretarlos.
func TestClaveExtraEsRechazada(t *testing.T) {
	conExtra := `{
		"meeting_summary": "Reunion",
		"action_items": [{
			"title": "Migrar la sesion",
			"description": "Sacarla del contexto global.",
			"assignee_name": "Omar",
			"priority": "HIGH",
			"labels": []
		}],
		"confidence": 0.87
	}`

	proveedor := &proveedorFalso{respuestas: []string{conExtra}}
	caso := extraer(t, proveedor, llm.NuevoPromptBuilder())

	_, err := caso.Ejecutar(context.Background(), "transcripcion")

	if !errors.Is(err, domain.ErrReintentosAgotados) {
		t.Fatalf("una clave no declarada debe rechazarse, se obtuvo %v", err)
	}
}

// TestConfiguracionInvalida comprueba los errores de construccion.
func TestConfiguracionInvalida(t *testing.T) {
	casos := []struct {
		nombre   string
		opciones application.ExtractOpciones
	}{
		{
			nombre:   "sin proveedor",
			opciones: application.ExtractOpciones{Schema: []byte(`{"type":"object"}`)},
		},
		{
			nombre:   "sin schema",
			opciones: application.ExtractOpciones{Proveedor: &proveedorFalso{}},
		},
		{
			nombre: "schema no JSON",
			opciones: application.ExtractOpciones{
				Proveedor: &proveedorFalso{},
				Schema:    []byte(`{roto`),
			},
		},
	}

	for _, tt := range casos {
		t.Run(tt.nombre, func(t *testing.T) {
			_, err := application.NuevoExtractBacklog(tt.opciones)

			if !errors.Is(err, domain.ErrConfigInvalida) {
				t.Errorf("se esperaba ErrConfigInvalida, se obtuvo %v", err)
			}
		})
	}
}

// TestExtraccionRespetaElContextoEnCadaIntento verifica que un contexto con
// deadline no se ignora entre reintentos.
func TestExtraccionRespetaElContextoEnCadaIntento(t *testing.T) {
	// Un deadline corto con un proveedor que siempre falla: el caso de uso debe
	// cortarlo, no completar los 3 intentos a cualquier coste.
	proveedor := &proveedorFalso{respuestas: []string{`{roto`}}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	caso := extraer(t, proveedor, llm.NuevoPromptBuilder())

	inicio := time.Now()
	_, err := caso.Ejecutar(ctx, "transcripcion")
	transcurrido := time.Since(inicio)

	if err == nil {
		t.Fatal("se esperaba error")
	}
	// Las esperas entre intentos son de 400 ms: con un deadline de 10 ms el caso
	// de uso debe abortar mucho antes de completar los 3 intentos.
	if transcurrido > 300*time.Millisecond {
		t.Errorf("el deadline no se respeto entre reintentos: tardo %v", transcurrido)
	}
}
