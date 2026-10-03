// Paquete llm contiene las implementaciones concretas de domain.LLMProvider.
//
// Comparten un cliente HTTP comun (client.go) para que el timeout, el limite de
// tamano de respuesta y la politica de reintentos ante fallos transitorios se
// definan una unica vez (RNF-03 y RNF-05). Un proveedor con su propio cliente
// acabaria divergiendo del resto en los limites.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"
)

// Defaults de red aplicados a todos los proveedores.
const (
	// TimeoutPorDefecto acota cada peticion HTTP completa. Un LLM local puede
	// tardar bastante en el primer prompt porque esta cargando el modelo en
	// memoria, de ahi el margen generoso.
	TimeoutPorDefecto = 60 * time.Second

	// MaxBytesRespuesta impide que un proveedor desbordado (o un intermediario
	// malicioso) agote la memoria. 16 MiB es de sobra para un backlog.
	MaxBytesRespuesta = 16 << 20

	// MaxIntentosHTTP es el numero de reintentos ante fallos TRANSITORIOS
	// (red caida, 502, 503, 429).
	//
	// Es un mecanismo distinto del Retry Loop de autocorreccion: este no
	// cambia el prompt, solo espera a que un servidor caido se recupere.
	MaxIntentosHTTP = 3

	// EsperaBase es el primer intervalo del backoff exponencial.
	EsperaBase = 500 * time.Millisecond
)

// Errores especificos de la capa de proveedores.
//
// Se distinguen del dominio porque describen fallos de infraestructura externa,
// no de reglas de negocio. La capa de aplicacion los envuelve con
// domain.ErrReintentosAgotados cuando no queda margen de reintento.
var (
	// ErrRespuestaVacia indica que el proveedor devolvio una respuesta sin
	// contenido util.
	ErrRespuestaVacia = errors.New("el proveedor de LLM devolvio una respuesta vacia")

	// ErrFormatoRespuesta indica que la respuesta del proveedor no tiene la
	// forma esperada para ese proveedor (por ejemplo, sin choices en OpenAI).
	ErrFormatoRespuesta = errors.New("la respuesta del proveedor de LLM tiene un formato inesperado")

	// ErrHTTP indica un fallo de la peticion HTTP.
	ErrHTTP = errors.New("fallo la peticion HTTP al proveedor de LLM")

	// ErrExcedeIntentos indica que se agotaron los reintentos por fallo
	// transitorio.
	ErrExcedeIntentos = errors.New("se agotaron los reintentos por fallo transitorio")

	// ErrSinAPIKey indica que falta la credencial del proveedor.
	ErrSinAPIKey = errors.New("falta la credencial de API del proveedor")
)

// Cliente es el cliente HTTP compartido por todos los proveedores.
//
// Se construye una vez y se inyecta, de modo que los tests puedan sustituirlo
// por uno apuntando a un httptest.Server.
type Cliente struct {
	http        *http.Client
	httpTimeout time.Duration
	maxBytes    int64
	// esperaBase es el intervalo del primer backoff. Es configurable (y no una
	// constante rigida) por dos razones: un despliegue con servidores lentos
	// necesita esperas mas largas, y los tests necesitan poder reducirlas para
	// no pagar 1,5 s de reloj por cada caso de reintento.
	esperaBase time.Duration
}

// ClienteOpciones configura la construccion del cliente.
type ClienteOpciones struct {
	// HTTP permite inyectar un cliente propio (tests, proxies).
	HTTP *http.Client
	// Timeout acota cada peticion. Si es cero, se usa TimeoutPorDefecto.
	Timeout time.Duration
	// MaxBytes limita el tamano de la respuesta. Si es cero, MaxBytesRespuesta.
	MaxBytes int64
	// EsperaBase define el primer intervalo del backoff. Si es cero,
	// EsperaBase.
	EsperaBase time.Duration
}

// NuevoCliente construye el cliente compartido.
func NuevoCliente(opciones ClienteOpciones) *Cliente {
	timeout := opciones.Timeout
	if timeout == 0 {
		timeout = TimeoutPorDefecto
	}

	httpClient := opciones.HTTP
	if httpClient == nil {
		// No se fija Timeout en el http.Client: el timeout se aplica por
		// peticion mediante context.WithTimeout, para que el plazo cubra
		// exactamente la llamada y no dependa de si la conexion se reutiliza.
		httpClient = &http.Client{}
	}

	maxBytes := opciones.MaxBytes
	if maxBytes == 0 {
		maxBytes = MaxBytesRespuesta
	}

	esperaBase := opciones.EsperaBase
	if esperaBase == 0 {
		esperaBase = EsperaBase
	}

	return &Cliente{
		http:        httpClient,
		httpTimeout: timeout,
		maxBytes:    maxBytes,
		esperaBase:  esperaBase,
	}
}

// Do ejecuta una peticion POST con cuerpo JSON y devuelve el cuerpo de la
// respuesta.
//
// Reintenta ante fallos transitorios con backoff exponencial, respetando la
// cancelacion del contexto en cada espera (RNF-05).
func (c *Cliente) Do(ctx context.Context, url string, cuerpo any, cabeceras map[string]string) ([]byte, error) {
	payload, err := json.Marshal(cuerpo)
	if err != nil {
		return nil, fmt.Errorf("no se pudo serializar la peticion: %w", err)
	}

	var ultimoError error

	for intento := 0; intento < MaxIntentosHTTP; intento++ {
		if intento > 0 {
			// El backoff se calcula antes de esperar, y se respeta la
			// cancelacion: un segundo 500 seguido de un Ctrl+C debe terminar
			// de inmediato, no tras un minuto de esperas.
			espera := time.Duration(float64(c.esperaBase) * math.Pow(2, float64(intento-1)))

			temporizador := time.NewTimer(espera)
			select {
			case <-ctx.Done():
				temporizador.Stop()
				return nil, ctx.Err()
			case <-temporizador.C:
			}
		}

		respuesta, err := c.intentar(ctx, url, payload, cabeceras)
		if err == nil {
			return respuesta, nil
		}

		ultimoError = err

		// Un error no transitorio (credencial invalida, contexto cancelado) no
		// se reintenta: repetirlo solo agotaria tiempo y cuota.
		if !esReintentable(err) {
			return nil, err
		}
	}

	return nil, fmt.Errorf("%w tras %d intentos: %w", ErrExcedeIntentos, MaxIntentosHTTP, ultimoError)
}

// intentar ejecuta un unico intento.
func (c *Cliente) intentar(ctx context.Context, url string, payload []byte, cabeceras map[string]string) ([]byte, error) {
	// El timeout se aplica aqui, por peticion: es el unico sitio donde se
	// cumple RNF-03 de forma explicita para todos los proveedores.
	contextoPeticion, cancelar := context.WithTimeout(ctx, c.timeout())
	defer cancelar()

	req, err := http.NewRequestWithContext(contextoPeticion, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrHTTP, err)
	}

	req.Header.Set("Content-Type", "application/json")

	for clave, valor := range cabeceras {
		req.Header.Set(clave, valor)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrHTTP, err)
	}

	// El cierre del body va inmediatamente despues de la llamada, no al final
	// de la funcion: cualquier error intermedio devuelto antes llegaria a
	// filtrar la conexion si se hiciera despues.
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBytes))
	if err != nil {
		return nil, fmt.Errorf("%w: no se pudo leer la respuesta: %w", ErrHTTP, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, newErrorHTTPStatus(resp.StatusCode, body)
	}

	return body, nil
}

// timeout devuelve el timeout configurado, con el valor por defecto si no se
// fijó. c.http se usa directamente para conservar el timeout global del
// http.Client cuando se inyecta uno propio.
func (c *Cliente) timeout() time.Duration {
	if c.httpTimeout > 0 {
		return c.httpTimeout
	}
	return TimeoutPorDefecto
}

// esReintentable decide si un error merece otro intento.
//
// Solo los fallos transitorios: caida de red, 429 y 5xx. Un 400 o 401 es una
// peticion incorrecta o una credencial invalida, y repetirla dara exactamente
// el mismo resultado.
func esReintentable(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	var httpErr *errorHTTPStatus
	if errors.As(err, &httpErr) {
		return httpErr.reintentable
	}

	// Un error de transporte (DNS, conexion rechazada) es transitorio en la
	//mayoria de los casos: el servidor puede estar reiniciando.
	return errors.Is(err, ErrHTTP)
}

// errorHTTPStatus representa una respuesta con codigo de estado no exitoso.
type errorHTTPStatus struct {
	codigo       int
	cuerpo       string
	reintentable bool
}

func newErrorHTTPStatus(codigo int, cuerpo []byte) *errorHTTPStatus {
	return &errorHTTPStatus{
		codigo: codigo,
		cuerpo: recortarParaMensaje(cuerpo),
		reintentable: codigo == http.StatusTooManyRequests ||
			codigo == http.StatusBadGateway ||
			codigo == http.StatusServiceUnavailable ||
			codigo == http.StatusGatewayTimeout ||
			codigo >= 500,
	}
}

func (e *errorHTTPStatus) Error() string {
	if e.cuerpo == "" {
		return fmt.Sprintf("el proveedor de LLM respondio con estado HTTP %d", e.codigo)
	}
	return fmt.Sprintf("el proveedor de LLM respondio con estado HTTP %d: %s", e.codigo, e.cuerpo)
}

// recortarParaMensaje limita el cuerpo de un error.
//
// Se trunca porque el cuerpo va al log y al prompt de autocorreccion: una
// pagina de HTML de error de un proxy no ayuda a nadie y satura el contexto.
func recortarParaMensaje(cuerpo []byte) string {
	const max = 512

	texto := string(cuerpo)
	if len(texto) <= max {
		return texto
	}

	return texto[:max] + "..."
}
