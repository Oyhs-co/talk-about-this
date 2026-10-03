package llm

import (
	"fmt"
	"strings"
)

// Construccion del prompt.
//
// El prompt es una funcion PURA: mismo transcript, mismo prompt. Esa
// determinismo es lo que permite que un reintento sea comparable con el intento
// anterior y que solo cambie el bloque de correccion.
//
// Se mantiene en el adaptador y no en el dominio a proposito: el texto
// literal que se envia a un modelo es un detalle de como se habla con ese
// modelo, no una regla de negocio. El dominio solo sabe que necesita
// "extraer un backlog".
const (
	// VersionPrompt versiona el prompt. Cambiarlo invalida los resultados en
	// cache de los proveedores y permite saber que modelo de prompt produjo
	// cada resultado guardado.
	VersionPrompt = "v1"
)

// PromptBuilder arma los prompts de extraccion y de correccion.
//
// Es un TIPO, y no funciones sueltas, porque la capa de aplicacion (que orquesta
// el Retry Loop) no puede importar este paquete: segun la regla de dependencias
// de AGENTS.md seccion 1, application conoce las interfaces del dominio pero no
// la infraestructura. La aplicacion declara su propia interfaz ConstructorDePrompt
// y recibe esta implementacion por inyeccion, igual que hace con el selector de
// parsers.
//
// Motivar la inyeccion con una interfaz en vez de importar directamente es lo que
// mantiene la arquitectura hexagonal intacta cuando se anaden proveedores.
type PromptBuilder struct {
	// Version se imprime en el prompt. Cambiarla invalida los resultados en
	// cache de los proveedores y permite saber que plantilla produjo cada
	// resultado guardado.
	Version string
}

// NuevoPromptBuilder construye el generador de prompts con la version por defecto.
func NuevoPromptBuilder() *PromptBuilder {
	return &PromptBuilder{Version: VersionPrompt}
}

// Construir arma el prompt de extraccion.
func (p *PromptBuilder) Construir(transcript, schemaJSON string) string {
	version := p.Version
	if version == "" {
		version = VersionPrompt
	}
	return construirPrompt(transcript, schemaJSON, version)
}

// ConstruirCorreccion arma el prompt de un reintento.
func (p *PromptBuilder) ConstruirCorreccion(transcript, schemaJSON string, errores []string) string {
	return construirPromptDeCorreccion(transcript, schemaJSON, errores, p.Version)
}

// construirPrompt arma el prompt de extraccion.
//
// El prompt incluye el JSON Schema literal por tres razones:
//
//  1. Muchos proveedores (Ollama, LM Studio) lo necesitan en el prompt aunque
//     tambien lo acepten como parametro, y algunos modos de Ollama no lo usan.
//  2. Refuerza la restriccion en modelos que no soportan constrained decoding.
//  3. Permite que el modelo entienda los nombres de campo y sus meanings en
//     lugar de deducirlos.
func construirPrompt(transcript, schemaJSON string, version string) string {
	var b strings.Builder

	b.WriteString("<!-- prompt:")
	b.WriteString(version)
	b.WriteString(" -->\n\n")

	b.WriteString("Tarea: analiza la transcripcion de la reunion y extrae los ")
	b.WriteString("items de backlog accionables.\n\n")

	b.WriteString("Reglas de extraccion:\n")
	b.WriteString("1. Un item de backlog es una accion concreta que alguien debe ejecutar.\n")
	b.WriteString("2. No inventes tareas que no se mencionen. Si la reunion no decide nada,\n")
	b.WriteString("   devuelve un arreglo action_items vacio.\n")
	b.WriteString("3. En assignee_name usa el nombre tal y como aparece en la transcripcion.\n")
	b.WriteString("   NO lo conviertas a un usuario de GitHub: eso se hace despues.\n")
	b.WriteString("4. El titulo debe ser imperativo y autocontenido: que alguien que no leyo\n")
	b.WriteString("   la reunion sepa que hacer sin contexto adicional.\n")
	b.WriteString("5. En description incluye el contexto, los criterios de aceptacion y las\n")
	b.WriteString("   dependencias que se mencionen.\n")
	b.WriteString("6. La prioridad debe justificarse por el impacto acordado en la reunion,\n")
	b.WriteString("   no por tu_estimacion personal.\n\n")

	b.WriteString("El resultado debe ser EXCLUSIVAMENTE un objeto JSON valido que cumpla ")
	b.WriteString("este esquema:\n\n")
	b.WriteString("```json\n")
	b.WriteString(schemaJSON)
	b.WriteString("\n```\n\n")

	b.WriteString("No incluyas explicaciones, ni texto antes o despues del JSON, ni ")
	b.WriteString("bloques de codigo markdown alrededor de la respuesta.\n\n")

	b.WriteString("=== TRANSCRIPCION DE LA REUNION ===\n")
	b.WriteString(transcript)
	b.WriteString("\n=== FIN DE LA TRANSCRIPCION ===")

	return b.String()
}

// ConstruirPromptDeCorreccion arma el prompt de un reintento.
//
// Es el mecanismo central de RF-07: el error concreto de la respuesta anterior
// se adjunta al prompt para que el modelo lo corrija.
//
// Que se adjunte el error LITERAL y no un mensaje generico ("intenta de nuevo")
// es lo que hace que funcione. El modelo necesita saber que propiedad fallo
// ("priority: el valor CRITICAL no es valido; se esperaba HIGH, MEDIUM o LOW")
// para corregirla; con un mensaje generico volveria a fallar por el mismo motivo
// y se agotarian los tres intentos.
func construirPromptDeCorreccion(transcript, schemaJSON string, intentosPrevios []string, version string) string {
	var b strings.Builder

	b.WriteString(construirPrompt(transcript, schemaJSON, version))
	b.WriteString("\n\n=== CORRECCION REQUERIDA ===\n\n")

	b.WriteString("Tu respuesta anterior fue rechazada por no cumplir el esquema. ")
	b.WriteString("A continuacion se listan TODOS los errores detectados:\n\n")

	for i, err := range intentosPrevios {
		b.WriteString(fmt.Sprintf("%d. %s\n", i+1, err))
	}

	b.WriteString("\nCorrige esos problemas y vuelve a responder con el JSON completo.\n")
	b.WriteString("No expliques la correccion: responde solo con el JSON.")

	return b.String()
}

// ResumirErrores compacta los mensajes de error en una linea por error.
//
// Recorta porque el prompt crece con cada intento y los tokens de entrada se
// pagan en todas las llamadas. Se conserva el principio y el final del mensaje,
// que es donde esta la causa util.
func ResumirErrores(errores []string, maximoPorError int) []string {
	salida := make([]string, 0, len(errores))

	for _, err := range errores {
		texto := strings.TrimSpace(err)
		if texto == "" {
			continue
		}

		if maximoPorError > 0 && len(texto) > maximoPorError {
			texto = texto[:maximoPorError] + "..."
		}

		salida = append(salida, texto)
	}

	return salida
}
