package application

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"talkaboutthis/internal/domain"
	"talkaboutthis/internal/infrastructure/jsonschema"
)

// MaxIntentosExtraccion es el numero MAXIMO de intentos, incluido el primero
// (INIT.md seccion 4.2).
//
// Tres significa: uno original y dos correcciones. No es un numero arbitrario:
// es el punto donde seguir reintentando cuesta mas que devolver un error al
// usuario y dejar que decida.
const MaxIntentosExtraccion = 3

// EsperaEntreIntentos es la pausa antes de cada reintento.
//
// No es inmediata porque la mayoria de los fallos de formato son transitorios en
// la practica: el mismo prompt con el error adjunto produce una respuesta
// distinta en el siguiente muestreo. Reiniciar el sampling es parte del
// mecanismo de correccion.
const EsperaEntreIntentos = 400 * time.Millisecond

// ConstructorDePrompt construye los prompts de extraccion y de correccion.
//
// La interfaz vive en application, no en el dominio, por el mismo motivo que
// SelectorDeParser: el texto literal que se envia a un modelo es una decision de
// infraestructura. Aqui solo se necesita el contrato.
type ConstructorDePrompt interface {
	// Construir arma el prompt inicial.
	Construir(transcript, schemaJSON string) string
	// ConstruirCorreccion arma el prompt de un reintento, adjuntando los
	// errores detectados en los intentos anteriores.
	ConstruirCorreccion(transcript, schemaJSON string, errores []string) string
}

// ExtractBacklog extrae el backlog de una transcripcion (RF-03, RF-07).
//
// Es el corazon del Retry Loop con autocorreccion:
//
//  1. Se pide al proveedor una salida estructurada con el schema.
//  2. Se valida la respuesta contra el JSON Schema.
//  3. Si falla, se reintenta hasta MaxIntentosExtraccion veces, ENVIANDO los
//     errores concretos al prompt para que el modelo se corrija.
//
// La validacion vive aqui y no en el adaptador a proposito: el adaptador solo
// devuelve bytes (lo dice el contrato de domain.LLMProvider), y hace falta el
// detalle de los fallos para construir el prompt de correccion.
type ExtractBacklog struct {
	proveedor domain.LLMProvider
	prompt    ConstructorDePrompt
	schema    *jsonschema.Schema
	// schemaJSON es el texto original, que se envia al modelo. Se conserva
	// ademas de la version parseada porque el prompt necesita el texto literal.
	schemaJSON string
	// dormir es inyectable para que los tests no paguen esperas reales.
	dormir func(context.Context, time.Duration) error
}

// ExtractOpciones configura el caso de uso.
type ExtractOpciones struct {
	// Proveedor es el motor de LLM (obligatorio).
	Proveedor domain.LLMProvider
	// Prompt construye los prompts. Si es nil se usa uno por defecto.
	Prompt ConstructorDePrompt
	// Schema es el JSON Schema en bytes (obligatorio).
	Schema []byte
}

// NuevoExtractBacklog construye el caso de uso.
func NuevoExtractBacklog(opciones ExtractOpciones) (*ExtractBacklog, error) {
	if opciones.Proveedor == nil {
		return nil, fmt.Errorf("%w: falta el proveedor de LLM", domain.ErrConfigInvalida)
	}

	if len(opciones.Schema) == 0 {
		return nil, fmt.Errorf("%w: falta el JSON Schema de extraccion", domain.ErrConfigInvalida)
	}

	schema, desconocidas, err := jsonschema.ParseSchema(opciones.Schema)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", domain.ErrConfigInvalida, err)
	}

	// Si el schema usa palabras clave que el validador no aplica, se avisa por
	// log en lugar de callar: una regla que no se comprueba es peor que no
	// tenerla. No es un error porque el schema puede seguir siendo valido.
	for _, palabra := range desconocidas {
		// En la construccion no hay contexto: se usa el logger global, que es
		// lo unico disponible antes de que exista una ejecucion.
		slog.Warn("el JSON Schema usa una palabra clave que el validador no aplica",
			slog.String("palabra_clave", palabra),
			slog.String("titulo", schema.Title),
		)
	}

	// Sin prompt inyectado se usa uno minimo en vez de fallar: la extraccion es
	// funcional sin instruccionesadicionales, aunque su calidad sera peor. El
	// composition root es quien debe inyectar el prompt real.
	prompt := opciones.Prompt
	if prompt == nil {
		prompt = promptPorDefecto{}
	}

	return &ExtractBacklog{
		proveedor:  opciones.Proveedor,
		prompt:     prompt,
		schema:     schema,
		schemaJSON: string(opciones.Schema),
		dormir:     dormirRespetandoContexto,
	}, nil
}

// promptPorDefecto es un ConstructorDePrompt minimo.
//
// Existe para que el caso de uso tenga un comportamiento razonable si se omite
// la inyeccion, en lugar de fallar con un error de configuracion.
type promptPorDefecto struct{}

func (promptPorDefecto) Construir(transcript, schemaJSON string) string {
	return transcript + "\n\nResponde solo con JSON que cumpla este esquema:\n" + schemaJSON
}

func (promptPorDefecto) ConstruirCorreccion(transcript, schemaJSON string, errores []string) string {
	return transcript
}

// Ejecutar extrae el backlog, reintentando con autocorreccion.
//
// Devuelve ErrReintentosAgotados cuando los intentos se acaban, encadenando el
// ultimo error concreto para que el usuario sepa QUE le fallo al modelo.
func (e *ExtractBacklog) Ejecutar(ctx context.Context, transcript string) (*domain.MeetingBacklogExtraction, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var erroresAcumulados []string

	log := loggerDe(ctx)

	for intento := 1; intento <= MaxIntentosExtraccion; intento++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		// El primer intento usa el prompt limpio; los siguientes adjuntan los
		// errores acumulados. Acumular (y no solo el ultimo) permite que un
		// reintento corrija de golpe varios problemas.
		var prompt string
		if intento == 1 {
			prompt = e.prompt.Construir(transcript, e.schemaJSON)
		} else {
			prompt = e.prompt.ConstruirCorreccion(transcript, e.schemaJSON, erroresAcumulados)
		}

		log.Info("intentando extraer el backlog",
			slog.Int("intento", intento),
			slog.Int("max_intentos", MaxIntentosExtraccion),
			slog.String("proveedor", e.proveedor.Name()),
			slog.Int("errores_previos", len(erroresAcumulados)),
		)

		crudo, err := e.proveedor.GenerateStructuredOutput(ctx, prompt, e.schemaJSON)
		if err != nil {
			// Un fallo del PROVEEDOR (red caida, credencial) no se corrige
			// cambiando el prompt: repetir la misma llamada dara el mismo error.
			// Se propaga de inmediato y lo reintenta la politica de red del
			// propio cliente HTTP.
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}

			return nil, fmt.Errorf("el proveedor %s fallo: %w", e.proveedor.Name(), err)
		}

		extraccion, errores := e.validarRespuesta(crudo)
		if len(errores) == 0 {
			log := loggerDe(ctx)
			log.Info("extraccion valida",
				slog.Int("intento", intento),
				slog.Int("items", len(extraccion.ActionItems)),
			)
			return extraccion, nil
		}

		// La respuesta llego pero no cumple el contrato: esto SI se puede
		// corregir, asi que se acumula el error y se reintenta.
		erroresAcumulados = append(erroresAcumulados, errores...)
		log.Warn("la respuesta no cumple el esquema, se reintentara con correccion",
			slog.Int("intento", intento),
			slog.Int("errores", len(errores)),
			slog.String("detalle", errores[0]),
		)

		if intento < MaxIntentosExtraccion {
			if err := e.dormir(ctx, EsperaEntreIntentos); err != nil {
				return nil, err
			}
		}
	}

	return nil, fmt.Errorf("%w: despues de %d intentos. Ultimos errores: %s",
		domain.ErrReintentosAgotados,
		MaxIntentosExtraccion,
		resumir(erroresAcumulados),
	)
}

// validarRespuesta comprueba la respuesta del LLM en tres capas.
//
// Las tres son necesarias porque cada una atrapa un fallo distinto:
//
//   - Capa 1: sintaxis JSON. Un JSON truncado o con comas colgantes.
//   - Capa 2: forma segun el JSON Schema. Tipos, campos obligatorios, enums.
//   - Capa 3: invariantes del dominio. Lo que el schema no puede expresar
//     (por ejemplo, que un titulo en blanco no es una tarea).
//
// Devuelve la extraccion (puede ser nil si fallo la capa 1) y la lista de
// errores, TODOS, para que un reintento los corrija de una sola vez.
func (e *ExtractBacklog) validarRespuesta(crudo []byte) (*domain.MeetingBacklogExtraction, []string) {
	var errores []string

	// Capa 1: sintaxis.
	var documento any
	if err := json.Unmarshal(crudo, &documento); err != nil {
		return nil, []string{fmt.Sprintf("la respuesta no es JSON valido: %v", err)}
	}

	// Antes de validar se normalizan los enums. Un LLM escribe "high" con una
	// frecuencia altisima aunque el schema diga "HIGH": el prompt travels entero
	// hasta el modelo, y alli las mayusculas no sobreviven. Fallar por eso
	// consumiria los tres reintentos del Retry Loop y devolveria un error para
	// un dato que era correcto en sustancia. Normalizar aqui, en la frontera con
	// el modelo, es el punto donde el dato deja de ser entrada externa.
	normalizarEnums(documento)

	// Capa 2: forma segun el schema.
	resultado := jsonschema.Validar(documento, e.schema)
	if !resultado.Valido() {
		for _, err := range resultado.Errores {
			errores = append(errores, err.Error())
		}
		// Sin una forma correcta no tiene sentido deserializar.
		return nil, errores
	}

	// Capa 3: deserializacion a structs del dominio.
	//
	// Se deserializa del documento NORMALIZADO, no de los bytes originales. Es
	// la unica forma de que las dos capas及以上 coincidan: si se leyera `crudo`,
	// el validador habria visto "HIGH" (ya normalizado) mientras el modelo del
	// dominio leeria "high" del JSON original y lo rechazaria. Validar y
	// deserializar cosas distintas es como un error pasa el filtro y luego estalla.
	normalizado, err := json.Marshal(documento)
	if err != nil {
		return nil, []string{fmt.Sprintf(
			"la respuesta normalizada no se pudo serializar: %v", err)}
	}

	var extraccion domain.MeetingBacklogExtraction
	if err := json.Unmarshal(normalizado, &extraccion); err != nil {
		return nil, []string{fmt.Sprintf(
			"el JSON cumple el esquema pero no se pudo convertir al modelo interno: %v", err)}
	}

	// Capa 3 (continuacion): invariantes del dominio.
	if err := extraccion.Validar(); err != nil {
		errores = append(errores, err.Error())
		return nil, errores
	}

	return &extraccion, nil
}

// normalizarEnums pasa a mayusculas los valores de los campos enumerados.
//
// Se trabaja sobre el documento ya deserializado a `any` y se modifica en el
// sitio: el mismo valor es el que despues se valida y se convierte al modelo
// del dominio, de modo que no puede haber una discrepancia entre lo validado y
// lo guardado.
//
// Un valor que ya es valido se deja intacto. Uno que no corresponde a ningun
// miembro del enum NO se inventa: se deja como estaba para que el validador lo
// reporte, porque un "urgente" silenciosamente convertido en "MEDIUM" seria
// publicar una tarea con una prioridad que nadie eligio.
func normalizarEnums(documento any) {
	raiz, ok := documento.(map[string]any)
	if !ok {
		return
	}

	items, ok := raiz["action_items"].([]any)
	if !ok {
		return
	}

	for _, bruto := range items {
		item, ok := bruto.(map[string]any)
		if !ok {
			continue
		}

		valor, ok := item["priority"].(string)
		if !ok {
			continue
		}

		for _, candidato := range []domain.Priority{domain.PriorityHigh, domain.PriorityMedium, domain.PriorityLow} {
			if strings.EqualFold(valor, string(candidato)) {
				item["priority"] = string(candidato)
				break
			}
		}
	}
}

// dormirRespetandoContexto espera sin ignorar la cancelacion.
func dormirRespetandoContexto(ctx context.Context, d time.Duration) error {
	temporizador := time.NewTimer(d)
	defer temporizador.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-temporizador.C:
		return nil
	}
}

// resumir compacta los errores acumulados para el mensaje final.
func resumir(errores []string) string {
	if len(errores) == 0 {
		return "(sin detalles)"
	}

	const maxErrores = 5

	salida := ""
	for i, err := range errores {
		if i >= maxErrores {
			salida += fmt.Sprintf(" (y %d mas)", len(errores)-maxErrores)
			break
		}
		if i > 0 {
			salida += "; "
		}
		salida += err
	}

	return salida
}
