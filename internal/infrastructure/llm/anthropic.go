package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"talkaboutthis/internal/domain"
)

// Anthropic es el adaptador de domain.LLMProvider para la API de Claude.
//
// # DIFERENCIA TECNICA IMPORTANTE RESPECTO A OPENAI
//
// La API de Anthropic no expone response_format con json_schema. La via
// soportada para obtener salida estructurada es el uso de TOOLS: se declara una
// herramienta cuyos "input_schema" es nuestro JSON Schema, y el modelo responde
// llenando sus argumentos.
//
// Por eso la extraccion se pide explicitamente en el prompt para que el modelo
// invoque la herramienta, y el resultado se lee de tool_use y no de text. Si el
// modelo respondiera con texto plano en vez de llamar a la herramienta, eso es
// un fallo de formato y el Retry Loop lo corregira.
type Anthropic struct {
	baseURL string
	modelo  string
	apiKey  string
	cliente *Cliente

	// maxTokens acota la respuesta. Claude no tiene limite de salida
	// implícito: sin este tope, una extraccion larga podria generar una
	// respuesta que exceda el contexto de la ventana.
	maxTokens int
}

// AnthropicOpciones configura el adaptador.
type AnthropicOpciones struct {
	BaseURL string
	Modelo  string
	APIKey  string
	Cliente *Cliente
	// MaxTokens limita la longitud de la respuesta. Por defecto 4096.
	MaxTokens int
}

// Valores por defecto.
const (
	BaseURLAnthropicPorDefecto = "https://api.anthropic.com"
	ModeloAnthropicPorDefecto  = "claude-3-5-sonnet-latest"
	VersionAnthropicAPI        = "2023-06-01"
	MaxTokensAnthropicDefecto  = 4096
	nombreHerramienta          = "registrar_backlog"
)

// NuevaAnthropic construye el adaptador.
func NuevaAnthropic(opciones AnthropicOpciones) *Anthropic {
	base := opciones.BaseURL
	if base == "" {
		base = BaseURLAnthropicPorDefecto
	}

	modelo := opciones.Modelo
	if modelo == "" {
		modelo = ModeloAnthropicPorDefecto
	}

	maxTokens := opciones.MaxTokens
	if maxTokens == 0 {
		maxTokens = MaxTokensAnthropicDefecto
	}

	cliente := opciones.Cliente
	if cliente == nil {
		cliente = NuevoCliente(ClienteOpciones{})
	}

	return &Anthropic{
		baseURL:   strings.TrimRight(base, "/"),
		modelo:    modelo,
		apiKey:    opciones.APIKey,
		cliente:   cliente,
		maxTokens: maxTokens,
	}
}

// Name implementa domain.LLMProvider.
func (a *Anthropic) Name() string {
	return "anthropic/" + a.modelo
}

// peticionAnthropic es el cuerpo de POST /v1/messages.
type peticionAnthropic struct {
	Model     string                 `json:"model"`
	MaxTokens int                    `json:"max_tokens"`
	Message   mensajeAnthropic       `json:"messages"`
	Tools     []herramientaAnthropic `json:"tools,omitempty"`
}

// mensajeAnthropic es un mensaje de la conversación.
type mensajeAnthropic struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// herramientaAnthropic declara una herramienta con su esquema de entrada.
type herramientaAnthropic struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"input_schema"`
}

// respuestaAnthropic es la forma de la respuesta.
type respuestaAnthropic struct {
	Content []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Error      *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// GenerateStructuredOutput implementa domain.LLMProvider.
func (a *Anthropic) GenerateStructuredOutput(ctx context.Context, prompt string, schemaJSON string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if strings.TrimSpace(a.apiKey) == "" {
		return nil, ErrSinAPIKey
	}

	esquema, err := esquemaParaHerramienta(schemaJSON)
	if err != nil {
		return nil, err
	}

	cuerpo := peticionAnthropic{
		Model:     a.modelo,
		MaxTokens: a.maxTokens,
		Message: mensajeAnthropic{
			Role: "user",
			// La instruccion de llamar a la herramienta es obligatoria: sin ella
			// el modelo podria responder con prosa, que es justo lo que no
			// queremos.
			Content: prompt + "\n\nInvoca la herramienta `" + nombreHerramienta +
				"` con los datos extraidos. No respondas con texto plano.",
		},
	}

	if esquema != nil {
		cuerpo.Tools = []herramientaAnthropic{{
			Name:        nombreHerramienta,
			Description: "Registra los items de backlog extraidos de la transcripcion.",
			InputSchema: esquema,
		}}
	}

	cabeceras := map[string]string{
		"x-api-key":         a.apiKey,
		"anthropic-version": VersionAnthropicAPI,
	}

	respuesta, err := a.cliente.Do(ctx, a.baseURL+"/v1/messages", cuerpo, cabeceras)
	if err != nil {
		return nil, fmt.Errorf("anthropic: %w", err)
	}

	var parseada respuestaAnthropic
	if err := json.Unmarshal(respuesta, &parseada); err != nil {
		return nil, fmt.Errorf("%w: la respuesta de anthropic no es JSON valido: %w", ErrFormatoRespuesta, err)
	}

	if parseada.Error != nil {
		return nil, fmt.Errorf("anthropic: %s", parseada.Error.Message)
	}

	// Se prefiere el bloque tool_use, que es la salida estructurada.
	for _, bloque := range parseada.Content {
		if bloque.Type == "tool_use" && bloque.Name == nombreHerramienta {
			if len(bloque.Input) == 0 {
				return nil, ErrRespuestaVacia
			}
			return []byte(bloque.Input), nil
		}
	}

	// El modelo no uso la herramienta. Se devuelve su texto para que la capa de
	// aplicacion pueda intentar corregirlo: puede que hayałodade la forma y solo
	// falle el formato, que el Retry Loop sabe arreglar adjuntando el error.
	for _, bloque := range parseada.Content {
		if bloque.Type == "text" && strings.TrimSpace(bloque.Text) != "" {
			return []byte(bloque.Text), nil
		}
	}

	return nil, ErrRespuestaVacia
}

// Asercion de compilacion del puerto.
var _ domain.LLMProvider = (*Anthropic)(nil)

// esquemaParaHerramienta decodifica el JSON Schema para usarlo como input_schema.
func esquemaParaHerramienta(schemaJSON string) (any, error) {
	recortado := strings.TrimSpace(schemaJSON)
	if recortado == "" {
		return nil, nil
	}

	var esquema any
	if err := json.Unmarshal([]byte(recortado), &esquema); err != nil {
		return nil, fmt.Errorf("el JSON Schema no es valido y no puede enviarse a anthropic: %w", err)
	}

	return esquema, nil
}
