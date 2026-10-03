package llm_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"talkaboutthis/internal/domain"
	"talkaboutthis/internal/infrastructure/llm"
)

// NINGUN test de este archivo toca la red real. Todos usan httptest.NewServer,
// que abre un servidor local efimero. Es un requisito de AGENTS.md: los tests no
// pueden depender de una API externa, de una credencial ni de la cuota de nadie.

// serverOllama crea un servidor que imita la respuesta de /api/chat.
func serverOllama(t *testing.T, contenido string, capturar *peticion) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		if capturar != nil {
			capturar.ruta = r.URL.Path
			capturar.cuerpo = string(body)
			capturar.metodo = r.Method
		}

		if r.URL.Path != "/api/chat" {
			t.Errorf("ruta inesperada: %s", r.URL.Path)
		}

		respuesta := map[string]any{
			"model":   "llama3",
			"done":    true,
			"message": map[string]string{"role": "assistant", "content": contenido},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(respuesta)
	}))
}

type peticion struct {
	metodo string
	ruta   string
	cuerpo string
}

// --- TC-02: adaptador Ollama ---

// TestTC02OllamaDeserializaSinErrores es el caso de evaluacion TC-02.
//
// El criterio es que la extraccion se deserialice sin errores de json.Unmarshal
// a partir de lo que devuelve un servidor con forma de Ollama.
func TestTC02OllamaDeserializaSinErrores(t *testing.T) {
	payload := `{
		"meeting_summary": "Reunion de arquitectura del modulo de pagos.",
		"action_items": [{
			"title": "Migrar la sesion",
			"description": "Sacarla del contexto global.",
			"assignee_name": "Omar Hernandez",
			"priority": "HIGH",
			"labels": ["backend"],
			"story_points": 3
		}]
	}`

	var capturada peticion
	srv := serverOllama(t, payload, &capturada)
	defer srv.Close()

	ollama := llm.NuevaOllama(llm.OllamaOpciones{
		BaseURL: srv.URL,
		Modelo:  "llama3",
	})

	crudo, err := ollama.GenerateStructuredOutput(context.Background(), "prompt de prueba", "{}")
	if err != nil {
		t.Fatalf("GenerateStructuredOutput fallo: %v", err)
	}

	// Deserializacion sin error: es el criterio literal de TC-02.
	var extraccion domain.MeetingBacklogExtraction
	if err := json.Unmarshal(crudo, &extraccion); err != nil {
		t.Fatalf("json.Unmarshal fallo, incumpliendo TC-02: %v", err)
	}

	if len(extraccion.ActionItems) != 1 {
		t.Fatalf("se esperaba 1 item, se obtuvieron %d", len(extraccion.ActionItems))
	}
	if extraccion.ActionItems[0].Title != "Migrar la sesion" {
		t.Errorf("titulo inesperado: %q", extraccion.ActionItems[0].Title)
	}
	if extraccion.ActionItems[0].Priority != domain.PriorityHigh {
		t.Errorf("prioridad inesperada: %q", extraccion.ActionItems[0].Priority)
	}
}

// TestOllamaEnviaElSchemaVerifica que el JSON Schema viaja en la peticion, que
// es lo que activa el restricted decoding en Ollama.
func TestOllamaEnviaElSchema(t *testing.T) {
	schema := `{"type":"object","properties":{"titulo":{"type":"string"}}}`

	var capturada peticion
	srv := serverOllama(t, `{"ok":true}`, &capturada)
	defer srv.Close()

	ollama := llm.NuevaOllama(llm.OllamaOpciones{BaseURL: srv.URL, Modelo: "llama3"})

	if _, err := ollama.GenerateStructuredOutput(context.Background(), "prompt", schema); err != nil {
		t.Fatalf("fallo la llamada: %v", err)
	}

	var cuerpo map[string]any
	if err := json.Unmarshal([]byte(capturada.cuerpo), &cuerpo); err != nil {
		t.Fatalf("el cuerpo enviado no es JSON: %v", err)
	}

	formato, ok := cuerpo["format"].(map[string]any)
	if !ok {
		t.Fatalf("se esperaba el campo format como objeto de esquema, se obtuvo: %v", cuerpo["format"])
	}
	if formato["type"] != "object" {
		t.Errorf("el schema enviado no es el recibido: %v", formato)
	}

	// stream debe ser false: en streaming la respuesta no es un JSON.
	if cuerpo["stream"] != false {
		t.Error("stream debe enviarse en false para obtener una respuesta completa")
	}
}

// TestOllamaSinSchemaUsaFormatoJSON comprueba la degradacion controlada.
func TestOllamaSinSchema(t *testing.T) {
	var capturada peticion
	srv := serverOllama(t, `{"ok":true}`, &capturada)
	defer srv.Close()

	ollama := llm.NuevaOllama(llm.OllamaOpciones{BaseURL: srv.URL})

	if _, err := ollama.GenerateStructuredOutput(context.Background(), "prompt", ""); err != nil {
		t.Fatalf("fallo la llamada: %v", err)
	}

	if !strings.Contains(capturada.cuerpo, `"format":"json"`) {
		t.Errorf("sin schema deberia usarse format=json, se envió: %s", capturada.cuerpo)
	}
}

func TestOllamaSchemaInvalido(t *testing.T) {
	ollama := llm.NuevaOllama(llm.OllamaOpciones{BaseURL: "http://localhost:11434"})

	_, err := ollama.GenerateStructuredOutput(context.Background(), "prompt", "{roto:")

	if err == nil {
		t.Fatal("se esperaba error con un schema invalido")
	}
	if !strings.Contains(err.Error(), "Schema") {
		t.Errorf("el error deberia mencionar el schema: %v", err)
	}
}

func TestOllamaRespuestaVacia(t *testing.T) {
	srv := serverOllama(t, "   ", nil)
	defer srv.Close()

	ollama := llm.NuevaOllama(llm.OllamaOpciones{BaseURL: srv.URL})

	_, err := ollama.GenerateStructuredOutput(context.Background(), "prompt", "{}")

	if !errors.Is(err, llm.ErrRespuestaVacia) {
		t.Errorf("se esperaba ErrRespuestaVacia, se obtuvo %v", err)
	}
}

// TestOllamaErrorEnCampoJSON cubre el comportamiento peculiar de Ollama: puede
// reportar un fallo con HTTP 200 y un campo "error".
func TestOllamaErrorEnCampoJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"model": "llama3",
			"error": "model 'llama3' not found, try pulling it first",
		})
	}))
	defer srv.Close()

	ollama := llm.NuevaOllama(llm.OllamaOpciones{BaseURL: srv.URL, Modelo: "llama3"})

	_, err := ollama.GenerateStructuredOutput(context.Background(), "prompt", "{}")

	if err == nil {
		t.Fatal("se esperaba error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("el error deberia incluir el mensaje del servidor: %v", err)
	}
}

func TestOllamaNombre(t *testing.T) {
	ollama := llm.NuevaOllama(llm.OllamaOpciones{Modelo: "qwen2.5"})

	if ollama.Name() != "ollama/qwen2.5" {
		t.Errorf("Name() = %q, se esperaba ollama/qwen2.5", ollama.Name())
	}
}

// TestOllamaRespetaContextoCancelado comprueba RNF-03 en la frontera.
func TestOllamaRespetaContextoCancelado(t *testing.T) {
	ollama := llm.NuevaOllama(llm.OllamaOpciones{BaseURL: "http://localhost:11434"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := ollama.GenerateStructuredOutput(ctx, "prompt", "{}"); !errors.Is(err, context.Canceled) {
		t.Errorf("se esperaba context.Canceled, se obtuvo %v", err)
	}
}

// --- OpenAI ---

func TestOpenAIEnviaCredencialYSchema(t *testing.T) {
	var (
		capturado    peticion
		autorizacion string
		tipoFormato  string
		estricto     bool
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		capturado.cuerpo = string(body)
		autorizacion = r.Header.Get("Authorization")

		var cuerpo struct {
			ResponseFormat struct {
				Type       string `json:"type"`
				JSONSchema *struct {
					Strict bool `json:"strict"`
				} `json:"json_schema"`
			} `json:"response_format"`
		}
		_ = json.Unmarshal(body, &cuerpo)
		tipoFormato = cuerpo.ResponseFormat.Type
		if cuerpo.ResponseFormat.JSONSchema != nil {
			estricto = cuerpo.ResponseFormat.JSONSchema.Strict
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{
				map[string]any{
					"message": map[string]string{"role": "assistant", "content": `{"ok":true}`},
				},
			},
		})
	}))
	defer srv.Close()

	openai := llm.NuevaOpenAI(llm.OpenAIOpciones{
		BaseURL: srv.URL,
		APIKey:  "sk-secreto-de-prueba",
	})

	crudo, err := openai.GenerateStructuredOutput(context.Background(), "prompt", `{"type":"object"}`)
	if err != nil {
		t.Fatalf("fallo la llamada: %v", err)
	}

	if string(crudo) != `{"ok":true}` {
		t.Errorf("contenido inesperado: %s", crudo)
	}

	if autorizacion != "Bearer sk-secreto-de-prueba" {
		t.Errorf("la cabecera Authorization no es correcta: %q", autorizacion)
	}

	if tipoFormato != "json_schema" {
		t.Errorf("se esperaba response_format=json_schema, se obtuvo %q", tipoFormato)
	}
	if !estricto {
		t.Error("se esperaba strict=true para forzar el cumplimiento del esquema")
	}
}

// TestOpenAISinCredencialFallaSinLlamar comprueba que la falta de API key se
// detecta ANTES de hacer la peticion: un 401 del servidor seria menos util.
func TestOpenAISinCredencialFallaSinLlamar(t *testing.T) {
	var llamadas atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		llamadas.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	openai := llm.NuevaOpenAI(llm.OpenAIOpciones{BaseURL: srv.URL})

	_, err := openai.GenerateStructuredOutput(context.Background(), "prompt", "{}")

	if !errors.Is(err, llm.ErrSinAPIKey) {
		t.Errorf("se esperaba ErrSinAPIKey, se obtuvo %v", err)
	}
	if llamadas.Load() != 0 {
		t.Error("no deberia haberse hecho ninguna peticion de red sin credencial")
	}
}

func TestOpenAISinOpciones(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{}})
	}))
	defer srv.Close()

	openai := llm.NuevaOpenAI(llm.OpenAIOpciones{BaseURL: srv.URL, APIKey: "k"})

	_, err := openai.GenerateStructuredOutput(context.Background(), "prompt", "{}")

	if !errors.Is(err, llm.ErrFormatoRespuesta) {
		t.Errorf("una respuesta sin choices deberia dar ErrFormatoRespuesta, se obtuvo %v", err)
	}
}

// --- Anthropic ---

// TestAnthropicLeeToolUse comprueba el camino especifico de Anthropic: la salida
// estructurada llega en el bloque tool_use, no en el texto.
func TestAnthropicLeeToolUse(t *testing.T) {
	var enviada string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		enviada = string(body)

		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []any{
				map[string]any{
					"type":  "tool_use",
					"name":  "registrar_backlog",
					"input": map[string]any{"meeting_summary": "Resumen", "action_items": []any{}},
				},
			},
		})
	}))
	defer srv.Close()

	claude := llm.NuevaAnthropic(llm.AnthropicOpciones{
		BaseURL: srv.URL,
		APIKey:  "sk-ant-prueba",
	})

	crudo, err := claude.GenerateStructuredOutput(context.Background(), "prompt", `{"type":"object"}`)
	if err != nil {
		t.Fatalf("fallo la llamada: %v", err)
	}

	if !strings.Contains(string(crudo), "meeting_summary") {
		t.Errorf("se esperaba el contenido de tool_use, se obtuvo: %s", crudo)
	}

	// La peticion debe declarar la herramienta con el schema.
	if !strings.Contains(enviada, "registrar_backlog") {
		t.Errorf("la peticion no declara la herramienta: %s", enviada)
	}
	if !strings.Contains(enviada, "input_schema") {
		t.Errorf("la herramienta deberia llevar input_schema: %s", enviada)
	}
}

func TestAnthropicSinCredencial(t *testing.T) {
	claude := llm.NuevaAnthropic(llm.AnthropicOpciones{BaseURL: "http://localhost"})

	_, err := claude.GenerateStructuredOutput(context.Background(), "prompt", "{}")

	if !errors.Is(err, llm.ErrSinAPIKey) {
		t.Errorf("se esperaba ErrSinAPIKey, se obtuvo %v", err)
	}
}

// --- Gemini ---

// TestGeminiSimplificaElSchema comprueba la conversion al subconjunto que
// Gemini acepta: si se reenviara tal cual, el servidor rechazaria palabras
// clave como additionalProperties.
func TestGeminiSimplificaElSchema(t *testing.T) {
	var enviada string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		enviada = string(body)

		_ = json.NewEncoder(w).Encode(map[string]any{
			"candidates": []any{
				map[string]any{
					"content": map[string]any{
						"parts": []any{map[string]string{"text": `{"ok":true}`}},
					},
				},
			},
		})
	}))
	defer srv.Close()

	gemini := llm.NuevaGemini(llm.GeminiOpciones{
		BaseURL: srv.URL,
		APIKey:  "clave-prueba",
		Modelo:  "gemini-1.5-flash",
	})

	crudo, err := gemini.GenerateStructuredOutput(context.Background(), "prompt",
		`{"type":"object","additionalProperties":false,"properties":{"titulo":{"type":"string"}}}`)
	if err != nil {
		t.Fatalf("fallo la llamada: %v", err)
	}

	if string(crudo) != `{"ok":true}` {
		t.Errorf("contenido inesperado: %s", crudo)
	}

	if strings.Contains(enviada, "additionalProperties") {
		t.Errorf("additionalProperties no es admitido por Gemini y deberia haberse eliminado: %s", enviada)
	}
	if !strings.Contains(enviada, "application/json") {
		t.Errorf("deberia declararse responseMimeType: %s", enviada)
	}
}

// TestGeminiNoFiltraLaCredencialEnErrores es una comprobacion de RNF-04: la
// API de Gemini autentica por query string, y ese valor no debe aparecer en los
// errores ni en los logs.
func TestGeminiNoFiltraLaCredencialEnErrores(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	const secreto = "CLAVE_MUY_SECRETA"

	gemini := llm.NuevaGemini(llm.GeminiOpciones{
		BaseURL: srv.URL,
		APIKey:  secreto,
		// Espera minima: el 500 dispara reintentos y este test no verifica el
		// backoff, sino que la credencial no se filtre al mensaje de error.
		Cliente: llm.NuevoCliente(llm.ClienteOpciones{EsperaBase: time.Millisecond}),
	})

	_, err := gemini.GenerateStructuredOutput(context.Background(), "prompt", "{}")

	if err == nil {
		t.Fatal("se esperaba error")
	}
	if strings.Contains(err.Error(), secreto) {
		t.Errorf("LA CREDENCIAL APARECIO EN EL ERROR: %v", err)
	}
}

func TestGeminiSinCredencial(t *testing.T) {
	gemini := llm.NuevaGemini(llm.GeminiOpciones{BaseURL: "http://localhost"})

	_, err := gemini.GenerateStructuredOutput(context.Background(), "prompt", "{}")

	if !errors.Is(err, llm.ErrSinAPIKey) {
		t.Errorf("se esperaba ErrSinAPIKey, se obtuvo %v", err)
	}
}

// --- Cliente compartido: reintentos y timeouts ---

// TestClienteReintentaErroresTransitorios comprueba RNF-05.
func TestClienteReintentaErroresTransitorios(t *testing.T) {
	var intentos atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if intentos.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	// Se reduce la espera base a 1 ms: el backoff es la mecanica probada aqui,
	// pero su DURACION no es lo que interesa en una suite. Con el valor por
	// defecto (500 ms) este test tardaria 1,5 s de reloj sin verificar nada mas.
	cliente := llm.NuevoCliente(llm.ClienteOpciones{EsperaBase: time.Millisecond})

	crudo, err := cliente.Do(context.Background(), srv.URL, map[string]string{"a": "b"}, nil)
	if err != nil {
		t.Fatalf("el cliente deberia haber reintentado hasta tener exito: %v", err)
	}
	if !strings.Contains(string(crudo), "ok") {
		t.Errorf("respuesta inesperada: %s", crudo)
	}
	if intentos.Load() != 3 {
		t.Errorf("se esperaban 3 intentos, se hicieron %d", intentos.Load())
	}
}

// TestClienteNoReintentaErroresNoTransitorios: un 400 se repetiria igual.
func TestClienteNoReintentaErroresNoTransitorios(t *testing.T) {
	var intentos atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		intentos.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"peticion invalida"}`))
	}))
	defer srv.Close()

	cliente := llm.NuevoCliente(llm.ClienteOpciones{})

	if _, err := cliente.Do(context.Background(), srv.URL, map[string]string{}, nil); err == nil {
		t.Fatal("se esperaba error con un 400")
	}

	if intentos.Load() != 1 {
		t.Errorf("un 400 no debe reintentarse, se hicieron %d intentos", intentos.Load())
	}
}

// TestClienteRespetaTimeout es la comprobacion de RNF-03: un servidor lento
// produce un error de timeout, no una espera indefinida.
func TestClienteRespetaTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Se bloquea mas que el timeout configurado.
		time.Sleep(300 * time.Millisecond)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	cliente := llm.NuevoCliente(llm.ClienteOpciones{Timeout: 50 * time.Millisecond})

	inicio := time.Now()
	_, err := cliente.Do(context.Background(), srv.URL, map[string]string{}, nil)
	transcurrido := time.Since(inicio)

	if err == nil {
		t.Fatal("se esperaba error de timeout")
	}
	if transcurrido > 400*time.Millisecond {
		t.Errorf("el timeout no se respeto: tardo %v con un timeout de 50ms", transcurrido)
	}
}

// TestClienteCancelaConContexto verifica que un Ctrl+C detiene la espera del
// backoff inmediatamente.
func TestClienteCancelaConContexto(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	cliente := llm.NuevoCliente(llm.ClienteOpciones{EsperaBase: time.Millisecond})

	inicio := time.Now()
	_, err := cliente.Do(ctx, srv.URL, map[string]string{}, nil)
	transcurrido := time.Since(inicio)

	if err == nil {
		t.Fatal("se esperaba error")
	}
	// El backoff del primer reintento es de 500ms: si el contexto lo respeta,
	// se aborta mucho antes.
	if transcurrido > 400*time.Millisecond {
		t.Errorf("el backoff no respeta la cancelacion: tardo %v", transcurrido)
	}
}

func TestClienteTruncaCuerpoDeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		// Cuerpo enorme, como el que devolveria un proxy con su pagina de error.
		_, _ = w.Write([]byte(strings.Repeat("E", 5000)))
	}))
	defer srv.Close()

	cliente := llm.NuevoCliente(llm.ClienteOpciones{EsperaBase: time.Millisecond})

	_, err := cliente.Do(context.Background(), srv.URL, map[string]string{}, nil)

	if err == nil {
		t.Fatal("se esperaba error")
	}
	if len(err.Error()) > 800 {
		t.Errorf("el mensaje de error deberia estar truncado, ocupa %d caracteres", len(err.Error()))
	}
}

// --- Prompts ---

// TestPromptIncluyeSchemaYTranscript comprueba lo esencial del prompt.
func TestPromptIncluyeSchemaYTranscript(t *testing.T) {
	builder := llm.NuevoPromptBuilder()

	prompt := builder.Construir("transcripcion de la reunion", `{"type":"object"}`)

	for _, esperado := range []string{
		"transcripcion de la reunion",
		`{"type":"object"}`,
		llm.VersionPrompt,
		"assignee_name",
	} {
		if !strings.Contains(prompt, esperado) {
			t.Errorf("el prompt deberia contener %q", esperado)
		}
	}
}

// TestPromptDeCorreccionIncluyeErrores es el mecanismo central de RF-07: sin el
// error literal en el prompt, la autocorreccion no funciona.
func TestPromptDeCorreccionIncluyeErrores(t *testing.T) {
	builder := llm.NuevoPromptBuilder()

	errores := []string{
		"action_items[0].priority: el valor \"URGENTE\" no es valido; se esperaba uno de: HIGH, MEDIUM, LOW",
		"falta la clave obligatoria \"description\"",
	}

	prompt := builder.ConstruirCorreccion("transcripcion", `{"type":"object"}`, errores)

	for _, err := range errores {
		if !strings.Contains(prompt, err) {
			t.Errorf("el prompt de correccion deberia incluir el error %q", err)
		}
	}
	if !strings.Contains(prompt, "transcripcion") {
		t.Error("el prompt de correccion debe reenviar la transcripcion")
	}
}

// TestPromptEsDeterminista: el mismo input debe producir el mismo prompt, o el
// Retry Loop no seria comparable entre intentos.
func TestPromptEsDeterminista(t *testing.T) {
	builder := llm.NuevoPromptBuilder()

	uno := builder.Construir("texto", "schema")
	dos := builder.Construir("texto", "schema")

	if uno != dos {
		t.Error("el prompt deberia ser determinista")
	}
}

// TestResumirErroresRecorta verifica que los mensajes no crece sin limite.
func TestResumirErroresRecorta(t *testing.T) {
	largo := strings.Repeat("x", 1000)

	resumidos := llm.ResumirErrores([]string{largo, "corto", "   "}, 100)

	if len(resumidos) != 2 {
		t.Fatalf("se esperaban 2 errores tras descartar el vacio, se obtuvieron %d: %v", len(resumidos), resumidos)
	}
	if len(resumidos[0]) > 110 {
		t.Errorf("el error largo deberia haberse recortado, ocupa %d", len(resumidos[0]))
	}
	if resumidos[1] != "corto" {
		t.Errorf("el error corto no deberia cambiar: %q", resumidos[1])
	}
}
