package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"talkaboutthis/internal/domain"
)

// Gemini es el adaptador de domain.LLMProvider para la API de Google Gemini.
//
// # DIFERENCIA TECNICA RESPECTO A LOS OTROS PROVEEDORES
//
// Gemini no solo acepta el esquema: lo NORMALIZA. responseSchema admite un
// subconjunto reducido de JSON Schema (no entiende $ref, additionalProperties ni
// varios tipos), y descarta en silencio lo que no reconoce.
//
// Por eso aqui no se reenvia el schema tal cual, sino que se convierte al
// subconjunto que Gemini entiende mediante simplificarSchemaParaGemini. Sin esa
// conversion, una palabra clave ignorada produciria una validacion en el
// servidor mas permisiva de lo que el contrato exige.
type Gemini struct {
	baseURL string
	modelo  string
	apiKey  string
	cliente *Cliente
}

// GeminiOpciones configura el adaptador.
type GeminiOpciones struct {
	// BaseURL. Si es vacio se usa https://generativelanguage.googleapis.com
	BaseURL string
	// Modelo. Si es vacio se usa gemini-1.5-flash.
	Modelo string
	APIKey string
	// Cliente compartido.
	Cliente *Cliente
}

// Valores por defecto.
const (
	BaseURLGeminiPorDefecto = "https://generativelanguage.googleapis.com"
	ModeloGeminiPorDefecto  = "gemini-1.5-flash"
	// VersionGemini es la version de la API en la ruta.
	VersionGemini = "v1beta"
)

// NuevaGemini construye el adaptador.
func NuevaGemini(opciones GeminiOpciones) *Gemini {
	base := opciones.BaseURL
	if base == "" {
		base = BaseURLGeminiPorDefecto
	}

	modelo := opciones.Modelo
	if modelo == "" {
		modelo = ModeloGeminiPorDefecto
	}

	cliente := opciones.Cliente
	if cliente == nil {
		cliente = NuevoCliente(ClienteOpciones{})
	}

	return &Gemini{
		baseURL: strings.TrimRight(base, "/"),
		modelo:  modelo,
		apiKey:  opciones.APIKey,
		cliente: cliente,
	}
}

// Name implementa domain.LLMProvider.
func (g *Gemini) Name() string {
	return "gemini/" + g.modelo
}

// peticionGemini es el cuerpo de generateContent.
type peticionGemini struct {
	Contents []contenidoGemini `json:"contents"`
	// GenerationConfig lleva las restricciones de forma.
	GenerationConfig configuracionGemini `json:"generationConfig"`
}

// contenidoGemini es un turno de la conversacion.
type contenidoGemini struct {
	Role  string        `json:"role,omitempty"`
	Parts []parteGemini `json:"parts"`
}

// parteGemini es un fragmento de mensaje.
type parteGemini struct {
	Text string `json:"text"`
}

// configuracionGemini restringe la forma de la respuesta.
type configuracionGemini struct {
	ResponseMimeType string         `json:"responseMimeType"`
	ResponseSchema   map[string]any `json:"responseSchema,omitempty"`
}

// respuestaGemini es la forma de la respuesta.
type respuestaGemini struct {
	Candidates []struct {
		Content      contenidoGemini `json:"content"`
		FinishReason string          `json:"finishReason"`
	} `json:"candidates"`
	Error *struct {
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}

// GenerateStructuredOutput implementa domain.LLMProvider.
func (g *Gemini) GenerateStructuredOutput(ctx context.Context, prompt string, schemaJSON string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if strings.TrimSpace(g.apiKey) == "" {
		return nil, ErrSinAPIKey
	}

	config := configuracionGemini{ResponseMimeType: "application/json"}

	if esquema := esquemaParaGemini(schemaJSON); esquema != nil {
		config.ResponseSchema = esquema
	}

	cuerpo := peticionGemini{
		Contents: []contenidoGemini{{
			Role:  "user",
			Parts: []parteGemini{{Text: prompt}},
		}},
		GenerationConfig: config,
	}

	// La API de Gemini autentica por query string. Es una excepcion al principio
	// de no llevar credenciales en la URL (RNF-04), impuesta por el diseno del
	// servicio: no admite cabecera Authorization. La URL completa NUNCA se
	// escribe en los logs por esa razon.
	endpoint := fmt.Sprintf("%s/%s/models/%s:generateContent?key=%s",
		g.baseURL, VersionGemini, url.PathEscape(g.modelo), url.QueryEscape(g.apiKey))

	respuesta, err := g.cliente.Do(ctx, endpoint, cuerpo, nil)
	if err != nil {
		// El error se envuelve sin la URL para no filtrar la credencial.
		return nil, fmt.Errorf("gemini: %w", err)
	}

	var parseada respuestaGemini
	if err := json.Unmarshal(respuesta, &parseada); err != nil {
		return nil, fmt.Errorf("%w: la respuesta de gemini no es JSON valido: %w", ErrFormatoRespuesta, err)
	}

	if parseada.Error != nil {
		return nil, fmt.Errorf("gemini: %s", parseada.Error.Message)
	}

	if len(parseada.Candidates) == 0 {
		return nil, fmt.Errorf("%w: gemini no devolvio ningun candidato", ErrFormatoRespuesta)
	}

	varBuilder := strings.Builder{}
	for _, parte := range parseada.Candidates[0].Content.Parts {
		varBuilder.WriteString(parte.Text)
	}

	contenido := varBuilder.String()
	if strings.TrimSpace(contenido) == "" {
		return nil, ErrRespuestaVacia
	}

	return []byte(contenido), nil
}

// Asercion de compilacion del puerto.
var _ domain.LLMProvider = (*Gemini)(nil)

// esquemaParaGemini convierte el JSON Schema al subconjunto que Gemini acepta.
//
// Gemini usa nombres de campo en MAYUSCULAS en su responseSchema (OPENAPI style:
// "type", "properties"), pero exige "type" en minusculas y no admite
// additionalProperties, $schema ni $id. Cualquier palabra clave no soportada se
// descarta: enviarlaprovocaria un error del servidor sin valor para el usuario.
//
// La validacion fuerte NO se pierde por esto: sigue existiendo en la capa de
// aplicacion contra el schema completo.
func esquemaParaGemini(schemaJSON string) map[string]any {
	recortado := strings.TrimSpace(schemaJSON)
	if recortado == "" {
		return nil
	}

	var crudo any
	if err := json.Unmarshal([]byte(recortado), &crudo); err != nil {
		return nil
	}

	simplificado, _ := simplificarGemini(crudo).(map[string]any)
	return simplificado
}

// simplificarGemini recorta un nodo de esquema a las palabras clave admitidas.
func simplificarGemini(nodo any) any {
	mapa, ok := nodo.(map[string]any)
	if !ok {
		return nodo
	}

	// propiedades de Gemini en responseSchema
	admitidas := []string{
		"type", "description", "enum", "items", "properties",
		"required", "nullable", "propertyOrdering",
	}

	salida := make(map[string]any, len(admitidas))

	for _, clave := range admitidas {
		valor, existe := mapa[clave]
		if !existe {
			continue
		}

		switch clave {
		case "properties":
			propiedades, ok := valor.(map[string]any)
			if !ok {
				continue
			}
			recortadas := make(map[string]any, len(propiedades))
			for nombre, definicion := range propiedades {
				recortadas[nombre] = simplificarGemini(definicion)
			}
			salida[clave] = recortadas

		case "items":
			salida[clave] = simplificarGemini(valor)

		case "required":
			// Gemini exige que las claves de required esten tambien en
			// properties; se acepta tal cual porque nuestro schema ya lo cumple.
			salida[clave] = valor

		default:
			salida[clave] = valor
		}
	}

	return salida
}
