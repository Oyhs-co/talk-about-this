// Paquete adapters contiene las implementaciones concretas de
// domain.ProjectBoardAdapter.
//
// Un adaptador por plataforma, todos hablando el mismo puerto. Anadir Jira o
// Linear consiste en escribir un archivo mas aqui, sin tocar internal/domain
// (TC-07).
package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// Configuracion comun a los adaptadores que hablan con una API HTTP.
const (
	// TimeoutGraphQL acota cada mutacion. Publicar en GitHub implica crear un
	// issue y despues asociarlo al tablero: son dos viajes de red por item.
	TimeoutGraphQL = 30 * time.Second

	// MaxBytesRespuestaGraphQL limita el cuerpo de la respuesta. Un item con
	// many labels podria generar una respuesta grande, pero ni de lejos tanto.
	MaxBytesRespuestaGraphQL = 8 << 20

	// MaxIntentosGraphQL son los reintentos ante fallos transitorios.
	MaxIntentosGraphQL = 3
)

// Errores de la capa de adaptadores.
var (
	// ErrGraphQL indica un fallo al ejecutar una consulta o mutacion.
	ErrGraphQL = errors.New("fallo la peticion a la API GraphQL de GitHub")

	// ErrConfigInvalida indica que faltan datos de configuracion del adaptador.
	ErrConfigInvalida = errors.New("la configuracion del adaptador es invalida o esta incompleta")

	// ErrRateLimit indica que se alcanzo el limite de peticiones de la API.
	ErrRateLimit = errors.New("se alcanzo el limite de peticiones de la API de GitHub")
)

// ClienteGraphQL ejecuta consultas y mutaciones contra la API GraphQL de GitHub.
//
// # POR QUE NO SE REUTILIZA EL CLIENTE DE LOS PROVEEDORES DE LLM
//
// La diferencia no es cosmetica: GraphQL devuelve los errores DENTRO del cuerpo
// con HTTP 200, en un campo "errors" que es un arreglo, y las mutaciones se
// reintentan segun el campo "type" (RATE_LIMITED, INTERNAL). Un cliente que
// solo mira el codigo de estado daria por buena una respuesta que en realidad
// es un fallo, que es el peor fallo posible: el adaptador creeria que publico
// una tarjeta que nunca existio.
type ClienteGraphQL struct {
	http     *http.Client
	endpoint string
	token    string
}

// ClienteGraphQLOpciones configura el cliente.
type ClienteGraphQLOpciones struct {
	// Endpoint es la URL de la API. Por defecto https://api.github.com/graphql.
	Endpoint string
	// Token es el PAT. Viaja solo en la cabecera Authorization.
	Token string
	// HTTP permite inyectar un cliente propio (tests).
	HTTP *http.Client
}

// EndpointGraphQLPorDefecto es la URL de la API GraphQL de GitHub.
const EndpointGraphQLPorDefecto = "https://api.github.com/graphql"

// NuevoClienteGraphQL construye el cliente.
func NuevoClienteGraphQL(opciones ClienteGraphQLOpciones) *ClienteGraphQL {
	endpoint := opciones.Endpoint
	if endpoint == "" {
		endpoint = EndpointGraphQLPorDefecto
	}

	httpClient := opciones.HTTP
	if httpClient == nil {
		httpClient = &http.Client{}
	}

	return &ClienteGraphQL{
		http:     httpClient,
		endpoint: endpoint,
		token:    opciones.Token,
	}
}

// peticionGraphQL es el sobre de una operacion.
type peticionGraphQL struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

// errorGraphQL es un error devuelto dentro del cuerpo de la respuesta.
type errorGraphQL struct {
	Message    string         `json:"message"`
	Type       string         `json:"type"`
	Path       []any          `json:"path"`
	Extensions map[string]any `json:"extensions"`
}

// envelopeGraphQL es la forma de toda respuesta de la API.
type envelopeGraphQL struct {
	Data   json.RawMessage `json:"data"`
	Errors []errorGraphQL  `json:"errors"`
}

// TieneToken indica si hay credencial configurada.
//
// Permite al adaptador validar la configuracion ANTES de empezar a publicar, en
// lugar de descubrirla en el primer item.
func (c *ClienteGraphQL) TieneToken() bool {
	return strings.TrimSpace(c.token) != ""
}

// Ejecutar ejecuta una consulta o mutacion y devuelve el campo "data".
//
// Los errores in-band se convierten en error de Go. Si la respuesta trae datos
// Y errores, se consideran fallo: en GraphQL una mutacion parcialmente fallida
// no es un exito parcial que se pueda ignorar sin revisar que campos quedaron
// nulos.
func (c *ClienteGraphQL) Ejecutar(ctx context.Context, query string, variables map[string]any, destino any) error {
	if strings.TrimSpace(c.token) == "" {
		return fmt.Errorf("%w: falta el token de GitHub (GITHUB_TOKEN)", ErrConfigInvalida)
	}

	payload, err := json.Marshal(peticionGraphQL{Query: query, Variables: variables})
	if err != nil {
		return fmt.Errorf("no se pudo serializar la peticion: %w", err)
	}

	// El timeout se aplica por operacion: publicar N items son N operaciones
	// distintas, cada una con su propio plazo.
	contextoOperacion, cancelar := context.WithTimeout(ctx, TimeoutGraphQL)
	defer cancelar()

	req, err := http.NewRequestWithContext(contextoOperacion, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("%w: %w", ErrGraphQL, err)
	}

	req.Header.Set("Content-Type", "application/json")
	// La credencial viaja unicamente en la cabecera. Nunca en el cuerpo ni en la
	// URL, y el endpoint nunca se registra en logs (RNF-04).
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", "talk-about-this")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrGraphQL, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBytesRespuestaGraphQL))
	if err != nil {
		return fmt.Errorf("%w: no se pudo leer la respuesta: %w", ErrGraphQL, err)
	}

	// Un 401 o 403 aqui significa credencial o permiso insuficiente. GitHub
	// responde 200 con un error in-band en algunos casos, pero un 401 claro es
	// mucho mas util que un error generico de GraphQL.
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("%w: token rechazado (revisa el scope 'project' del PAT)", ErrGraphQL)
	case http.StatusForbidden:
		return fmt.Errorf("%w: acceso denegado o token sin permisos suficientes", ErrGraphQL)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%w: HTTP %d: %s", ErrGraphQL, resp.StatusCode, recortar(body))
	}

	var envelope envelopeGraphQL
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("%w: la respuesta no es JSON valido: %w", ErrGraphQL, err)
	}

	if len(envelope.Errors) > 0 {
		return errorDeGraphQL(envelope.Errors)
	}

	if destino == nil {
		return nil
	}

	if len(envelope.Data) == 0 {
		return fmt.Errorf("%w: la respuesta no contiene campo data", ErrGraphQL)
	}

	if err := json.Unmarshal(envelope.Data, destino); err != nil {
		return fmt.Errorf("%w: no se pudo interpretar el campo data: %w", ErrGraphQL, err)
	}

	return nil
}

// errorDeGraphQL traduce el arreglo de errores in-band a un error de Go.
//
// El tipo del error determina si conviene reintentar: RATE_LIMITED es
// recuperable con espera, pero NOT_FOUND no lo es.
func errorDeGraphQL(errores []errorGraphQL) error {
	mensajes := make([]string, 0, len(errores))
	reintentable := false

	for _, e := range errores {
		mensajes = append(mensajes, e.Message)

		switch strings.ToUpper(e.Type) {
		case "RATE_LIMITED":
			reintentable = true
		}
	}

	texto := strings.Join(mensajes, "; ")

	if reintentable {
		return fmt.Errorf("%w: %s", ErrRateLimit, texto)
	}

	return fmt.Errorf("%w: %s", ErrGraphQL, texto)
}

// EsReintentable indica si un fallo merece otro intento.
//
// Se limita a lo transitorio. Reintentar un "Not Found" tres veces solo
// retrasa el error que el usuario va a ver igual.
func EsReintentable(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	if errors.Is(err, ErrRateLimit) {
		return true
	}

	// Un error de transporte suele ser transitorio (DNS, conexion reiniciada) y
	// se reconoce por implementar net.Error. Un error de GraphQL de negocio ya
	// viene envuelto en ErrGraphQL y no llega aqui.
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}

	return false
}

// recortar limita el cuerpo de una respuesta de error antes de incluirlo en el
// mensaje, para no saturar logs ni el prompt de correccion.
func recortar(cuerpo []byte) string {
	const max = 300

	texto := string(cuerpo)
	if len(texto) <= max {
		return texto
	}

	return texto[:max] + "..."
}
