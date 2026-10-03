package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"talkaboutthis/internal/domain"
)

// OpenAI es el adaptador de domain.LLMProvider para la API de OpenAI.
//
// Funciona tambien con servidores compatibles con la misma interfaz (Together,
// Groq, LM Studio en modo servidor OpenAI), cambiando BaseURL.
type OpenAI struct {
	baseURL string
	modelo  string
	apiKey  string
	cliente *Cliente
}

// OpenAIOpciones configura el adaptador.
type OpenAIOpciones struct {
	// BaseURL. Si es vacio se usa https://api.openai.com/v1.
	BaseURL string
	// Modelo. Si es vacio se usa gpt-4o-mini, que es barato y suficiente para
	// extraccion estructurada.
	Modelo string
	// APIKey. Sin ella el adaptador devuelve ErrSinAPIKey al usarse.
	APIKey string
	// Cliente compartido.
	Cliente *Cliente
}

// Valores por defecto.
const (
	BaseURLOpenAIPorDefecto = "https://api.openai.com/v1"
	ModeloOpenAIPorDefecto  = "gpt-4o-mini"
)

// NuevaOpenAI construye el adaptador.
func NuevaOpenAI(opciones OpenAIOpciones) *OpenAI {
	base := opciones.BaseURL
	if base == "" {
		base = BaseURLOpenAIPorDefecto
	}

	modelo := opciones.Modelo
	if modelo == "" {
		modelo = ModeloOpenAIPorDefecto
	}

	cliente := opciones.Cliente
	if cliente == nil {
		cliente = NuevoCliente(ClienteOpciones{})
	}

	return &OpenAI{
		baseURL: strings.TrimRight(base, "/"),
		modelo:  modelo,
		apiKey:  opciones.APIKey,
		cliente: cliente,
	}
}

// Name implementa domain.LLMProvider.
func (o *OpenAI) Name() string {
	return "openai/" + o.modelo
}

// peticionChatCompletion es el cuerpo de POST /chat/completions.
type peticionChatCompletion struct {
	Model          string            `json:"model"`
	Messages       []mensajeOpenAI   `json:"messages"`
	ResponseFormat *formatoRespuesta `json:"response_format,omitempty"`
	Temperature    *float64          `json:"temperature,omitempty"`
}

// formatoRespuesta es el campo response_format.
//
// Con "json_schema" y strict=true, OpenAI garantiza que la salida cumpla el
// esquema: es el equivalente remoto de lo que Ollama hace con "format".
type formatoRespuesta struct {
	Type       string       `json:"type"`
	JSONSchema *esquemaJSON `json:"json_schema,omitempty"`
}

// esquemaJSON envuelve el esquema dentro de response_format.
type esquemaJSON struct {
	Name   string `json:"name"`
	Strict bool   `json:"strict"`
	Schema any    `json:"schema"`
}

// mensajeOpenAI es un mensaje de chat.
type mensajeOpenAI struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// respuestaChatCompletion es la forma de la respuesta.
type respuestaChatCompletion struct {
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// GenerateStructuredOutput implementa domain.LLMProvider.
func (o *OpenAI) GenerateStructuredOutput(ctx context.Context, prompt string, schemaJSON string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if strings.TrimSpace(o.apiKey) == "" {
		// Falla de forma explicita y ANTES de cualquier llamada de red: un
		// 401 del servidor seria un mensaje mucho menos util.
		return nil, ErrSinAPIKey
	}

	cuerpo := peticionChatCompletion{
		Model:    o.modelo,
		Messages: []mensajeOpenAI{{Role: "user", Content: prompt}},
	}

	formato, err := formatoDeOpenAI(schemaJSON)
	if err != nil {
		return nil, err
	}
	cuerpo.ResponseFormat = formato

	cabeceras := map[string]string{
		// La credencial viaja solo en la cabecera Authorization. Nunca en la
		// URL ni en el cuerpo, donde acabaria en los logs (RNF-04).
		"Authorization": "Bearer " + o.apiKey,
	}

	respuesta, err := o.cliente.Do(ctx, o.baseURL+"/chat/completions", cuerpo, cabeceras)
	if err != nil {
		return nil, fmt.Errorf("openai: %w", err)
	}

	var parseada respuestaChatCompletion
	if err := json.Unmarshal(respuesta, &parseada); err != nil {
		return nil, fmt.Errorf("%w: la respuesta de openai no es JSON valido: %w", ErrFormatoRespuesta, err)
	}

	if parseada.Error != nil {
		return nil, fmt.Errorf("openai: %s", parseada.Error.Message)
	}

	if len(parseada.Choices) == 0 {
		return nil, fmt.Errorf("%w: openai no devolvio ninguna eleccion", ErrFormatoRespuesta)
	}

	contenido := parseada.Choices[0].Message.Content
	if strings.TrimSpace(contenido) == "" {
		return nil, ErrRespuestaVacia
	}

	return []byte(contenido), nil
}

// Asercion de compilacion del puerto.
var _ domain.LLMProvider = (*OpenAI)(nil)

// formatoDeOpenAI construye response_format.
//
// Cuando hay esquema se usa el modo estricto. Si no, se recurre a "json_object",
// que al menos garantiza JSON bien formado aunque no la forma exacta.
func formatoDeOpenAI(schemaJSON string) (*formatoRespuesta, error) {
	recortado := strings.TrimSpace(schemaJSON)
	if recortado == "" {
		return &formatoRespuesta{Type: "json_object"}, nil
	}

	var esquema any
	if err := json.Unmarshal([]byte(recortado), &esquema); err != nil {
		return nil, fmt.Errorf("el JSON Schema no es valido y no puede enviarse a openai: %w", err)
	}

	return &formatoRespuesta{
		Type: "json_schema",
		JSONSchema: &esquemaJSON{
			// El nombre del esquema es obligatorio en la API. Es un
			// identificador, no aparece en la respuesta.
			Name:   "meeting_backlog_extraction",
			Strict: true,
			Schema: esquema,
		},
	}, nil
}
