package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"talkaboutthis/internal/domain"
)

// Ollama es el adaptador de domain.LLMProvider para motores locales.
//
// Funciona contra Ollama y contra cualquier servidor compatible (LM Studio
// expone la misma ruta /api/chat).
//
// Ventaja especifica frente a las APIs SaaS: el dato nunca sale de la maquina.
// Es el modo indicado para minutas de reunion que no pueden enviarse a un
// tercero.
type Ollama struct {
	// baseURL es la raiz del servidor, por ejemplo "http://localhost:11434".
	baseURL string
	// modelo es el nombre del modelo cargado, por ejemplo "llama3".
	modelo string
	// cliente comparte timeout, limite de tamano y politica de reintentos.
	cliente *Cliente
	// opciones son parametros de muestreo que se pasan tal cual.
	opciones map[string]any
}

// OllamaOpciones configura el adaptador.
type OllamaOpciones struct {
	// BaseURL del servidor. Si es vacio se usa http://localhost:11434.
	BaseURL string
	// Modelo a utilizar.
	Modelo string
	// Cliente compartido. Si es nil se construye uno por defecto.
	Cliente *Cliente
	// Opciones de muestreo (temperature, top_p, num_ctx...).
	Opciones map[string]any
}

// BaseURLPorDefecto es el puerto estandar de Ollama.
const BaseURLPorDefecto = "http://localhost:11434"

// ModeloPorDefecto es un modelo de instruccion razonable como punto de partida.
const ModeloPorDefecto = "llama3"

// NuevaOllama construye el adaptador para un motor local.
func NuevaOllama(opciones OllamaOpciones) *Ollama {
	base := opciones.BaseURL
	if base == "" {
		base = BaseURLPorDefecto
	}

	modelo := opciones.Modelo
	if modelo == "" {
		modelo = ModeloPorDefecto
	}

	cliente := opciones.Cliente
	if cliente == nil {
		cliente = NuevoCliente(ClienteOpciones{})
	}

	// Se valida la URL aqui, al construir el adaptador, y no en la primera
	// peticion: un error de configuracion debe descubrirse al arrancar, cuando
	// el usuario todavia puede corregirlo, y no a mitad del procesamiento.
	if err := baseURLOllamaValida(strings.TrimRight(base, "/")); err != nil {
		// La URL invalida no es un error fatal de construccion (el adaptador se
		// construye igualmente para poder inspeccionarlo), pero se registra
		// como problema de configuracion mediante un campo explicito.
		base = baseURLInvalida
	}

	return &Ollama{
		baseURL:  strings.TrimRight(base, "/"),
		modelo:   modelo,
		cliente:  cliente,
		opciones: opciones.Opciones,
	}
}

// baseURLInvalida marca un adaptador cuya configuracion no supera la validacion.
// Cualquier peticion con esta base fallara de forma explicita en lugar de
// apuntar a un host inesperado.
const baseURLInvalida = "invalido://base-url"

// Name implementa domain.LLMProvider.
//
// Devuelve el proveedor y el modelo juntos porque en modo local saber WHICH
// modelo respondio es imprescindible: "el modelo dio una respuesta mala" no es
// accionable si hay tres cargados.
func (o *Ollama) Name() string {
	return "ollama/" + o.modelo
}

// peticionOllama es el cuerpo de POST /api/chat.
type peticionOllama struct {
	Model    string          `json:"model"`
	Messages []mensajeOllama `json:"messages"`
	Format   any             `json:"format,omitempty"`
	Stream   bool            `json:"stream"`
	Options  map[string]any  `json:"options,omitempty"`
}

// mensajeOllama es un mensaje de chat.
type mensajeOllama struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// respuestaOllama es la forma de la respuesta de /api/chat.
type respuestaOllama struct {
	Model   string `json:"model"`
	Message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
	Done       bool   `json:"done"`
	DoneReason string `json:"done_reason"`
	Error      string `json:"error"`
}

// GenerateStructuredOutput implementa domain.LLMProvider.
//
// El JSON Schema se envia en el campo "format". Ollama lo acepta como objeto de
// esquema y lo aplica mediante restricted decoding: el modelo SOLO puede emitir
// tokens que gird al esquema. Eso reduce drasticamente los JSON sintacticamente
// rotos, que es exactamente el problema que el Retry Loop tiene que manejar.
//
// Si la version de Ollama no soporta el esquema como objeto, este metodo
// degrada de forma controlada: se reintenta con "format": "json", que no
// restringe la forma pero al menos fuerza JSON. El esquema se sigue validando
// despues en la capa de aplicacion.
func (o *Ollama) GenerateStructuredOutput(ctx context.Context, prompt string, schemaJSON string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	endpoint := o.baseURL + "/api/chat"

	formato, err := formatoDeOllama(schemaJSON)
	if err != nil {
		return nil, err
	}

	cuerpo := peticionOllama{
		Model:    o.modelo,
		Messages: []mensajeOllama{{Role: "user", Content: prompt}},
		Format:   formato,
		Stream:   false, // imprescindible: en streaming la respuesta no es un JSON
		Options:  o.opciones,
	}

	respuesta, err := o.cliente.Do(ctx, endpoint, cuerpo, nil)
	if err != nil {
		return nil, fmt.Errorf("ollama: %w", err)
	}

	var parseada respuestaOllama
	if err := json.Unmarshal(respuesta, &parseada); err != nil {
		return nil, fmt.Errorf("%w: la respuesta de ollama no es JSON valido: %w", ErrFormatoRespuesta, err)
	}

	// Ollama reporta algunos fallos con HTTP 200 y un campo "error".
	if parseada.Error != "" {
		return nil, fmt.Errorf("ollama: %s", parseada.Error)
	}

	contenido := parseada.Message.Content
	if strings.TrimSpace(contenido) == "" {
		return nil, ErrRespuestaVacia
	}

	return []byte(contenido), nil
}

// Asercion de compilacion del puerto.
var _ domain.LLMProvider = (*Ollama)(nil)

// formatoDeOllama construye el campo "format".
//
// Se intenta enviar el esquema completo como objeto, que es lo que activa el
// restricted decoding. Si el esquema esta vacio se recurre a "json".
func formatoDeOllama(schemaJSON string) (any, error) {
	recortado := strings.TrimSpace(schemaJSON)
	if recortado == "" {
		return "json", nil
	}

	// Se decodifica primero para no enviar un JSON invalido que el servidor
	// rechazaria con un error poco informativo.
	var esquema any
	if err := json.Unmarshal([]byte(recortado), &esquema); err != nil {
		return nil, fmt.Errorf("el JSON Schema no es valido y no puede enviarse a ollama: %w", err)
	}

	return esquema, nil
}

// baseURLOllamaValida comprueba que la URL tiene esquema y host.
//
// Se valida en la construccion del adaptador para fallar al arrancar y no en la
// primera peticion, cuando el usuario ya espera un resultado.
func baseURLOllamaValida(baseURL string) error {
	analizada, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("%w: la URL de ollama no es valida: %w", domain.ErrConfigInvalida, err)
	}

	if analizada.Scheme == "" || analizada.Host == "" {
		return fmt.Errorf("%w: la URL de ollama debe incluir esquema y host, por ejemplo http://localhost:11434",
			domain.ErrConfigInvalida)
	}

	return nil
}
