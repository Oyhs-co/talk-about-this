// Command talkaboutthis es el punto de entrada de TalkAboutThis.
//
// ESTE ARCHIVO ES UN COMPOSITION ROOT Y NADA MAS.
//
// Su unica responsabilidad es construir las implementaciones concretas,
// inyectarlas en los casos de uso y traducir el resultado a un codigo de salida.
// No contiene reglas de negocio ni orquestacion: la secuencia ingesta ->
// extraccion -> despacho vive en application.Pipeline, precisamente para que
// este archivo pueda permanecer sin ninguna logica que probar.
//
// La unica decision que se toma aqui es el modo por defecto (dry-run), y esta
// justificada conComentarios donde ocurre.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"talkaboutthis/internal/application"
	"talkaboutthis/internal/domain"
	"talkaboutthis/internal/infrastructure/adapters"
	"talkaboutthis/internal/infrastructure/llm"
	"talkaboutthis/internal/infrastructure/logging"
	"talkaboutthis/internal/infrastructure/parsers"
)

const (
	programa = "talkaboutthis"
	version  = "1.0.0"
)

// Codigos de salida.
//
// Cada clase de fallo tiene su propio codigo para que un proceso de CI pueda
// reaccionar de forma distinta: reintentar por un problema de red tiene sentido,
// reintentar porque el archivo no existe no.
//
// Son los valores habituales de sysexits.h para que resulten legibles para
// quien no lea la documentacion.
const (
	salidaOK              = 0   // todo correcto
	salidaErrorUso        = 1   // parametros incorrectos o flag contradictorio
	salidaErrorConfig     = 2   // credenciales o configuracion incompleta
	salidaErrorIngesta    = 3   // archivo ilegible o formato no soportado
	salidaErrorExtraccion = 4   // el LLM no produjo un backlog valido
	salidaErrorIdentidad  = 5   // responsable sin resolver en modo estricto
	salidaErrorPublica    = 6   // fallo al publicar en la plataforma destino
	salidaInterrumpido    = 130 // cancelado por el usuario (128 + SIGINT)
)

func main() {
	ctx, detener := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer detener()

	codigo := ejecutar(ctx, os.Args[1:], os.Stdout, os.Stderr)
	os.Exit(codigo)
}

// ejecutar contiene la logica del comando y devuelve el codigo de salida.
//
// Existe separada de main para que los tests puedan invocar la CLI completa
// capturando stdout y stderr, sin manipulate os.Exit ni las senales del proceso.
func ejecutar(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		imprimirUso(stderr)
		return salidaErrorUso
	}

	switch args[0] {
	case "ingest":
		return ejecutarIngest(ctx, args[1:], stdout, stderr)
	case "-h", "--help", "help":
		imprimirUso(stdout)
		return salidaOK
	case "-version", "--version":
		fmt.Fprintf(stdout, "%s %s\n", programa, version)
		return salidaOK
	default:
		fmt.Fprintf(stderr, "%s: subcomando desconocido %q\n\n", programa, args[0])
		imprimirUso(stderr)
		return salidaErrorUso
	}
}

// ejecutarIngest es el subcomando principal.
func ejecutarIngest(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	var cfg Configuracion

	fs := flag.NewFlagSet(programa+" ingest", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { imprimirUsoIngest(stderr) }

	registrarFlags(fs, &cfg)

	// Cada invocacion parte de cero: un proceso real solo corre una vez, pero
	// los tests ejecutan la CLI muchas veces en el mismo proceso.
	envios = map[string]bool{}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return salidaOK
		}
		return salidaErrorUso
	}

	// Se registra que banderas se escribieron ANTES de completar la
	// configuracion, porque es completar quien decide si el entorno puede
	// tomar el relevo.
	registrarFlagsEnviadas(fs, &cfg)

	if err := cfg.completar(); err != nil {
		return reportar(stderr, err)
	}

	if err := cfg.validar(); err != nil {
		return reportar(stderr, err)
	}

	// El logger se crea aqui porque es lo primero que debe existir: a partir de
	// este punto, cualquier fallo queda registrado con su Job ID.
	logger, err := logging.Nuevo(logging.Configuracion{
		Nivel:   cfg.LogNivel,
		Formato: cfg.LogFormato,
	}, stderr)
	if err != nil {
		return reportar(stderr, err)
	}

	jobID := logging.NuevoJobID()
	logger = logging.ConJobID(logger, jobID)
	slog.SetDefault(logger)

	// El logger entra tambien en el contexto. El pipeline lo respeta tal cual,
	// de modo que el job_id aparece UNA vez por linea en lugar de duplicarse
	// porque cada capa anade sus propios atributos.
	ctx = application.ConLoggerEnContexto(ctx, logger)

	// Las dependencias se construyen aqui y se inyectan en los casos de uso.
	// Ningun constructor de infraestructura se invoca desde application.
	pipeline, err := construirPipeline(&cfg)
	if err != nil {
		return reportar(stderr, err)
	}

	resultado, err := pipeline.Ejecutar(ctx, application.PipelineConfiguracion{
		Ruta:       cfg.Ruta,
		ProjectRef: cfg.ProjectRef,
		Modo:       cfg.Modo,
		JobID:      jobID,
	})

	if err != nil {
		// El resultado puede venir parcialmente poblado: se renderiza igualmente
		// para que el usuario vea hasta donde llego el proceso.
		if resultado != nil && resultado.Transcript != nil {
			_ = renderizar(stdout, resultado, cfg.FormatoSalida)
			fmt.Fprintln(stdout)
		}
		return reportar(stderr, err)
	}

	if err := renderizar(stdout, resultado, cfg.FormatoSalida); err != nil {
		return reportar(stderr, err)
	}

	// Un fallo parcial de publicacion NO es un error del proceso: las tarjetas
	// correctas ya existen. Se informa por el resumen.
	if resumen := resultado.Despacho; resumen != nil {
		if _, fallos := resumen.ResumenDePublicacion(); fallos > 0 {
			fmt.Fprintf(stderr, "\n%d item(s) no se publicaron (ver el detalle arriba)\n", fallos)
			return salidaErrorPublica
		}
	}

	return salidaOK
}

// construirPipeline arma el grafo de dependencias completo.
//
// Es la unica funcion del programa que conoce los adaptadores concretos, y es
// deliberadamente lineal y explicita: leerla de arriba abajo debe bastar para
// entender de donde sale cada pieza.
func construirPipeline(cfg *Configuracion) (*application.Pipeline, error) {
	// 1. Ingesta: el registro de parsers.
	registro := parsers.NewRegistryPorDefecto()

	ingestor := application.NuevaIngestTranscriptor(registro)

	// 2. Extraccion: el proveedor de LLM y el constructor de prompts.
	timeout := timeoutEfectivo(cfg)

	cliente := llm.NuevoCliente(llm.ClienteOpciones{Timeout: timeout})

	proveedor, err := construirProveedor(cfg, cliente)
	if err != nil {
		return nil, err
	}

	extractor, err := application.NuevoExtractBacklog(application.ExtractOpciones{
		Proveedor: proveedor,
		Prompt:    llm.NuevoPromptBuilder(),
		Schema:    cfg.Schema,
	})
	if err != nil {
		return nil, err
	}

	// 3. Despacho: la tabla de identidades y el adaptador de tablero.
	mapper, err := cargarMappings(cfg.RutaMappings, cfg.Modo)
	if err != nil {
		return nil, err
	}

	tablero, err := construirTablero(cfg)
	if err != nil {
		return nil, err
	}

	// El handle por defecto lo toma el mapper: el archivo de configuracion es la
	// fuente de verdad, no una constante del codigo.
	publicador, err := application.NuevoPublicarBacklog(application.PublicarOpciones{
		Identidades:      mapper,
		Tablero:          tablero,
		Politica:         politicaNormalizada(cfg.Politica),
		HandlePorDefecto: mapper.HandlePorDefecto(),
	})
	if err != nil {
		return nil, err
	}

	return application.NewPipeline(ingestor, extractor, publicador)
}

// construirProveedor selecciona el motor de LLM segun la configuracion.
//
// La politica por defecto (ollama) evita enviar la transcripcion de una reunion
// a un servicio externo sin que el usuario lo haya pedido de forma explicita.
func construirProveedor(cfg *Configuracion, cliente *llm.Cliente) (domain.LLMProvider, error) {
	switch cfg.Proveedor {
	case "ollama":
		return llm.NuevaOllama(llm.OllamaOpciones{
			BaseURL: cfg.OllamaBaseURL,
			Modelo:  cfg.OllamaModelo,
			Cliente: cliente,
		}), nil

	case "openai":
		return llm.NuevaOpenAI(llm.OpenAIOpciones{
			BaseURL: cfg.OpenAIBaseURL,
			Modelo:  cfg.OpenAIModelo,
			APIKey:  cfg.OpenAIAPIKey,
			Cliente: cliente,
		}), nil

	case "anthropic":
		return llm.NuevaAnthropic(llm.AnthropicOpciones{
			Modelo:  cfg.AnthropicModelo,
			APIKey:  cfg.AnthropicAPIKey,
			Cliente: cliente,
		}), nil

	case "gemini":
		return llm.NuevaGemini(llm.GeminiOpciones{
			Modelo:  cfg.GeminiModelo,
			APIKey:  cfg.GeminiAPIKey,
			Cliente: cliente,
		}), nil

	default:
		return nil, fmt.Errorf("%w: proveedor desconocido %q", domain.ErrConfigInvalida, cfg.Proveedor)
	}
}

// construirTablero selecciona el backend de publicacion.
func construirTablero(cfg *Configuracion) (domain.ProjectBoardAdapter, error) {
	switch cfg.Adaptador {
	case "graphql":
		return adapters.NuevoGitHubGraphQL(adapters.GitHubGraphQLOpciones{
			Token:       cfg.Token,
			Owner:       cfg.Owner,
			Repositorio: cfg.Repositorio,
		}), nil

	case "cli":
		// El adaptador de CLI se construye con el repositorio que se pasara por
		// flag en cada invocacion.
		return adapters.NuevoGitHubCLI(adapters.GitHubCLIOpciones{
			Repositorio: repoCompleto(cfg),
		})

	default:
		return nil, fmt.Errorf("%w: adaptador de tablero desconocido %q (usa graphql o cli)",
			domain.ErrConfigInvalida, cfg.Adaptador)
	}
}

// repoCompleto compone "owner/repo", que es el formato que espera la CLI de GitHub.
func repoCompleto(cfg *Configuracion) string {
	if cfg.Owner == "" || cfg.Repositorio == "" {
		return ""
	}

	return cfg.Owner + "/" + cfg.Repositorio
}

// reportar imprime el error y traduce la clase de fallo a un codigo de salida.
//
// La traduccion por clases es lo que hace util la CLI en un proceso automatico:
// el llamador puede reintentar por red pero no por un archivo mal escrito.
func reportar(stderr io.Writer, err error) int {
	if err == nil {
		return salidaOK
	}

	// La cancelacion tiene su propio codigo: 130 es la convencion de shell para
	// un proceso interrumpido, y un 1 haria pensar que fallo por su cuenta.
	if errors.Is(err, context.Canceled) {
		fmt.Fprintf(stderr, "%s: cancelado por el usuario\n", programa)
		return salidaInterrumpido
	}

	// El error de etapa manda sobre el error interno: un fallo de publicacion
	// debe salir como publicacion, aunque por dentro envuelva un error de
	// credenciales.
	if etapa := application.EtapaDelError(err); etapa != "" {
		fmt.Fprintf(stderr, "%s: %v\n", programa, err)

		return codigoDeEtapa(etapa, err)
	}

	fmt.Fprintf(stderr, "%s: %v\n", programa, err)

	return codigoDeError(err)
}

// codigoDeEtapa traduce la etapa del pipeline a un codigo de salida.
func codigoDeEtapa(etapa application.PipelineEtapa, err error) int {
	switch etapa {
	case application.EtapaIngesta:
		return salidaErrorIngesta
	case application.EtapaExtraccion:
		return salidaErrorExtraccion
	case application.EtapaDespacho:
		return salidaErrorPublica
	default:
		return codigoDeError(err)
	}
}

// codigoDeError traduce el error a un codigo de salida.
func codigoDeError(err error) int {
	switch {
	case errors.Is(err, domain.ErrConfigInvalida),
		errors.Is(err, adapters.ErrConfigInvalida):
		return salidaErrorConfig

	case errors.Is(err, domain.ErrMapeoDeIdentidadInvalido):
		return salidaErrorIdentidad

	case errors.Is(err, domain.ErrIdentidadNoResuelta):
		return salidaErrorIdentidad

	case errors.Is(err, domain.ErrFormatoNoSoportado):
		return salidaErrorIngesta

	case errors.Is(err, domain.ErrReintentosAgotados):
		return salidaErrorExtraccion

	case errors.Is(err, errFaltanParametros),
		errors.Is(err, errModoAmbiguo):
		return salidaErrorUso

	case errors.Is(err, flag.ErrHelp):
		return salidaOK

	default:
		return salidaErrorUso
	}
}

// imprimirUso escribe el ayuda general.
func imprimirUso(w io.Writer) {
	fmt.Fprintf(w, `%s %s - convierte minutas de reunion en items de backlog

Uso:
  %s <subcomando> [opciones]

Subcomandos:
  ingest      procesa una transcripcion y extrae (o publica) el backlog

Opciones globales:
  -h, --help      muestra esta ayuda
  --version       muestra la version

Ejemplo:
  %s ingest --file notas.md --dry-run --provider ollama
`, programa, version, programa, programa)
}

// imprimirUsoIngest escribe el ayuda del subcomando ingest.
func imprimirUsoIngest(w io.Writer) {
	fmt.Fprintf(w, `%s ingest - procesa una transcripcion de reunion

Uso:
  %s ingest --file <ruta> [opciones]

Opciones de entrada:
  --file <ruta>        transcripcion .md, .txt o .docx (obligatorio)
  --provider <id>      ollama (por defecto), openai, anthropic, gemini
  --timeout <dur>      timeout de red (por defecto %v)

Opciones de salida:
  --dry-run            muestra el backlog sin publicar nada (POR DEFECTO)
  --publish            publica el backlog en el tablero destino
  --output <formato>   json (por defecto) o table

Opciones de publicacion (solo con --publish):
  --project <id|num>   GitHub Project v2 destino
  --owner <org|user>   dueno del repositorio
  --repo <nombre>      repositorio donde se crean los issues
  --adapter <id>       graphql (por defecto) o cli

Opciones de identidades:
  --mappings <ruta>    tabla de aliases
  --assignee-policy    fail (por defecto), assign_unassigned, skip

Opciones de diagnostico:
  --log-level <nivel>  debug, info, warn, error (por defecto info)
  --log-format <fmt>   text (por defecto) o json

Variables de entorno equivalentes:
  LLM_PROVIDER, GITHUB_TOKEN, GITHUB_OWNER, GITHUB_REPO, GITHUB_PROJECT_ID,
  OLLAMA_BASE_URL, OLLAMA_MODEL, OPENAI_API_KEY, ANTHROPIC_API_KEY, GEMINI_API_KEY

Sin --dry-run ni --publish se asume --dry-run: publicar en el tablero de un
equipo es una accion con efecto externo y no debe ocurrir por omision.

Ejemplos:
  %s ingest --file notas.md --dry-run
  %s ingest --file notas.md --dry-run --output table
  %s ingest --file notas.md --publish --project 5 --owner mi-org --repo mi-repo
`, programa, programa, TimeoutPorDefecto, programa, programa, programa)
}
