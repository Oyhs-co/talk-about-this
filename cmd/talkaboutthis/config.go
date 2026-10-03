package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"talkaboutthis/docs/specifications"
	"talkaboutthis/internal/application"
	"talkaboutthis/internal/domain"
	"talkaboutthis/internal/infrastructure/identity"
	"talkaboutthis/internal/infrastructure/logging"
)

// Configuracion es la configuracion completa de una ejecucion.
//
// Se construye combinando flags de linea de comandos y variables de entorno, con
// el flag ganando: lo explicito manda sobre lo heredado.
type Configuracion struct {
	// Ruta del documento a procesar.
	Ruta string
	// Proveedor de LLM: ollama, openai, anthropic o gemini.
	Proveedor string
	// Modo de salida de la etapa de despacho.
	Modo application.ModoSalida
	// Formato de la salida en stdout: json o table.
	FormatoSalida string
	// Tablero destino.
	ProjectRef  string
	Owner       string
	Repositorio string
	// Adapter de tablero: graphql o cli.
	Adaptador string
	// Ruta de la tabla de aliases.
	RutaMappings string
	// Schema de extraccion.
	Schema []byte
	// Politica ante responsables no resueltos.
	Politica string
	// Configuracion de logging.
	LogNivel   string
	LogFormato logging.Formato
	// Credenciales.
	Token           string
	OllamaBaseURL   string
	OllamaModelo    string
	OpenAIAPIKey    string
	OpenAIModelo    string
	OpenAIBaseURL   string
	AnthropicAPIKey string
	AnthropicModelo string
	GeminiAPIKey    string
	GeminiModelo    string
	// Timeout de las operaciones de red.
	Timeout time.Duration
}

// Valores por defecto.
const (
	ProveedorPorDefecto = "ollama"
	AdaptadorPorDefecto = "graphql"
	FormatoPorDefecto   = "json"
	TimeoutPorDefecto   = 60 * time.Second

	// NivelLogPorDefecto y FormatoLogPorDefecto se comparan contra el valor del
	// flag para decidir si LOG_LEVEL y LOG_FORMAT pueden entrar desde el entorno.
	NivelLogPorDefecto   = "info"
	FormatoLogPorDefecto = "text"

	// RutaMappingsPorDefecto evita depender del directorio de trabajo: el
	// usuario puede ejecutar el binario desde cualquier sitio.
	RutaMappingsPorDefecto = "configs/mappings.json"
)

// registrarFlags declara las banderas del subcomando ingest.
//
// Se separan en una funcion aparte para que main.go quede reducido a cableado y
// las banderas sean faciles de listar y probar.
func registrarFlags(fs *flag.FlagSet, cfg *Configuracion) {
	fs.StringVar(&cfg.Ruta, "file", "", "ruta de la transcripcion (.md, .txt, .docx)")
	fs.StringVar(&cfg.Proveedor, "provider", ProveedorPorDefecto, "motor de LLM: ollama, openai, anthropic, gemini")
	fs.BoolVar(&dryRunFlag, "dry-run", false, "muestra el backlog sin publicar nada (modo por defecto)")
	fs.BoolVar(&publishFlag, "publish", false, "publica el backlog en el tablero destino")
	fs.StringVar(&cfg.FormatoSalida, "output", FormatoPorDefecto, "formato de salida: json o table")
	fs.StringVar(&cfg.ProjectRef, "project", "", "id o numero del GitHub Project v2")
	fs.StringVar(&cfg.Owner, "owner", "", "organizacion o usuario dueno del repositorio")
	fs.StringVar(&cfg.Repositorio, "repo", "", "repositorio donde se crean los issues")
	fs.StringVar(&cfg.Adaptador, "adapter", AdaptadorPorDefecto, "backend de publicacion: graphql o cli")
	fs.StringVar(&cfg.RutaMappings, "mappings", "", "ruta de la tabla de aliases")
	fs.StringVar(&cfg.Politica, "assignee-policy", "", "politica ante responsable desconocido: fail, assign_unassigned, skip")
	fs.StringVar(&cfg.LogNivel, "log-level", NivelLogPorDefecto, "nivel de log: debug, info, warn, error")
	fs.StringVar(&logFormatoFlag, "log-format", FormatoLogPorDefecto, "formato de log: text o json")
	fs.DurationVar(&cfg.Timeout, "timeout", TimeoutPorDefecto, "timeout de las operaciones de red")
}

// registrarFlagsEnviadas anota que banderas se escribieron de forma explicita.
//
// Comparar el valor con el de por defecto NO sirve para saberlo: --log-format
// solo admite text y json, y text es el de por defecto, asi que el flag nunca
// podria ganar al entorno. fs.Visit recorre unicamente las banderas que el
// usuario escribio, que es exactamente la informacion que falta.
func registrarFlagsEnviadas(fs *flag.FlagSet, cfg *Configuracion) {
	fs.Visit(func(f *flag.Flag) {
		envios[f.Name] = true
	})
}

// fueEnviada indica si el usuario escribio esa bandera.
//
// Solo la consultan los campos cuyo valor por defecto choca con una variable
// de entorno equivalente. En el resto, comparar con el default basta.
func fueEnviada(nombre string) bool { return envios[nombre] }

// Variables de estado de los flags.
//
// Solo se separan para el formato de log, que debe validarse (un valor invalido
// es un error de configuracion). Los flags de modo y formato de salida SI viven
// en Configuracion, porque forman parte de la configuracion efectiva.
var logFormatoFlag string

// Banderas de modo de salida.
//
// Se guardan aparte porque son contradictorias y la combinacion debe validarse
// ANTES de decidir el modo: si vivieran en Configuracion, el valor final ya
// habria perdido la informacion de que el usuario pidio los dos.
var (
	dryRunFlag  bool
	publishFlag bool
)

// envios registra que banderas aparecieron en la linea de comandos.
//
// Vive en el paquete, y no en Configuracion, porque es informacion sobre COMO
// se invoco la CLI, no sobre que se quiere ejecutar. La funcion ejecutar la
// reinicia en cada llamada para que dos invocaciones en el mismo proceso (los
// tests) no se contaminen.
var envios = map[string]bool{}

// Errores de configuracion especificos de la CLI.
var (
	errFaltanParametros = errors.New("faltan parametros obligatorios")
	errModoAmbiguo      = errors.New("modo de salida ambiguo")
)

// completar rellena la configuracion desde el entorno y el schema embebido,
// y decide el modo de salida.
//
// SEPARAR ESTO DE registrarFlags PERMITE PROBARLO sin lanzar el binario.
func (cfg *Configuracion) completar() error {
	// --- Variables de entorno ---
	//
	// Se leen aqui, y no en la construccion de adaptadores, para que toda la
	// configuracion quede reunida en un unico sitio y sea auditable.
	aplicarEntorno(cfg)

	// El formato de log se resuelve DESPUES del entorno, no antes: si no, el
	// entorno no podria cambiarlo. Se lee en una variable local en lugar de
	// reescribir el flag global, para que aplicarEntorno siga siendo una
	// funcion sin efectos observables fuera de la configuracion.
	//
	// Solo se consulta el entorno si el usuario NO escribio --log-format, o si
	// lo escribio con el valor por defecto: en ese caso no hay eleccion que
	// respetar y el entorno es la unica fuente de intencion.
	formatoTexto := logFormatoFlag
	if !fueEnviada("log-format") || formatoTexto == "" {
		if valor := os.Getenv("LOG_FORMAT"); valor != "" {
			formatoTexto = valor
		}
	}

	// Se valida aparte del parseo porque un formato invalido es un error de
	// configuracion, no un error de linea de comandos.
	formato, err := logging.ParseFormato(formatoTexto)
	if err != nil {
		return fmt.Errorf("%w: %w", domain.ErrConfigInvalida, err)
	}
	cfg.LogFormato = formato

	// --- Modo de salida ---
	//
	// Si el usuario no indica nada, se usa dry-run. Publicar en el tablero de un
	// equipo es una accion con efecto externo: que ocurra por omision seria
	// una sorpresa inaceptable.
	switch {
	case dryRunFlag && publishFlag:
		return fmt.Errorf("%w: --dry-run y --publish son excluyentes", errModoAmbiguo)

	case publishFlag:
		cfg.Modo = application.ModoPublish

	default:
		cfg.Modo = application.ModoDryRun
	}

	// --- Rutas por defecto ---
	if cfg.RutaMappings == "" {
		cfg.RutaMappings = RutaMappingsPorDefecto
	}

	// --- Schema de extraccion ---
	//
	// Se usa el embebido salvo que se indique otro. El embebido garantiza que
	// el binario funciona sin archivos sueltos a su lado.
	if cfg.Schema == nil {
		schema, err := specifications.BacklogSchema()
		if err != nil {
			return fmt.Errorf("%w: %w", domain.ErrConfigInvalida, err)
		}
		cfg.Schema = schema
	}

	return nil
}

// aplicarEntorno rellena los campos no cubiertos por flags desde el entorno.
//
// Se llama DESPUES de registrar los flags para que un flag pueda sobrescribir
// una variable de entorno. Cuando el valor del flag sigue siendo el de por
// defecto, se deja que el entorno gane: un usuario que no paso el flag no esta
// afirmacion de nada sobre la variable.
func aplicarEntorno(cfg *Configuracion) {
	// Se aplica si el proveedor esta vacio o si conserva el valor por defecto.
	// El caso vacio importa: una Configuracion construida a mano (tests, o uso
	// futuro como libreria) no pasa por registrarFlags, y sin esta comprobacion
	// LLM_PROVIDER se ignoraria en silencio.
	if !fueEnviada("provider") || cfg.Proveedor == "" || cfg.Proveedor == ProveedorPorDefecto {
		if valor := os.Getenv("LLM_PROVIDER"); valor != "" {
			cfg.Proveedor = valor
		}
	}

	// El flag va SIEMPRE primero en estas llamadas: el valor explicito en la
	// linea de comandos gana, y la variable de entorno solo rellena lo que el
	// usuario no dijo. Al reves, un GITHUB_OWNER exportado en la sesion
	// pisaria un --owner escrito a proposito, que es justo el caso en que el
	// usuario mas necesita que su eleccion prevailzca.
	cfg.Politica = primeroNoVacio(cfg.Politica, os.Getenv("ASSIGNEE_POLICY"))

	cfg.Token = primeroNoVacio(cfg.Token, os.Getenv("GITHUB_TOKEN"))
	cfg.Owner = primeroNoVacio(cfg.Owner, os.Getenv("GITHUB_OWNER"))
	cfg.Repositorio = primeroNoVacio(cfg.Repositorio, os.Getenv("GITHUB_REPO"))
	cfg.ProjectRef = primeroNoVacio(cfg.ProjectRef, os.Getenv("GITHUB_PROJECT_ID"))

	cfg.OllamaBaseURL = primeroNoVacio(cfg.OllamaBaseURL, os.Getenv("OLLAMA_BASE_URL"))
	cfg.OllamaModelo = primeroNoVacio(cfg.OllamaModelo, os.Getenv("OLLAMA_MODEL"))

	cfg.OpenAIAPIKey = primeroNoVacio(cfg.OpenAIAPIKey, os.Getenv("OPENAI_API_KEY"))
	cfg.OpenAIModelo = primeroNoVacio(cfg.OpenAIModelo, os.Getenv("OPENAI_MODEL"))
	cfg.OpenAIBaseURL = primeroNoVacio(cfg.OpenAIBaseURL, os.Getenv("OPENAI_BASE_URL"))

	cfg.AnthropicAPIKey = primeroNoVacio(cfg.AnthropicAPIKey, os.Getenv("ANTHROPIC_API_KEY"))
	cfg.AnthropicModelo = primeroNoVacio(cfg.AnthropicModelo, os.Getenv("ANTHROPIC_MODEL"))

	cfg.GeminiAPIKey = primeroNoVacio(cfg.GeminiAPIKey, os.Getenv("GEMINI_API_KEY"))
	cfg.GeminiModelo = primeroNoVacio(cfg.GeminiModelo, os.Getenv("GEMINI_MODEL"))

	// Logging: solo entra del entorno si el usuario no dijo nada.
	if !fueEnviada("log-level") || cfg.LogNivel == "" || cfg.LogNivel == NivelLogPorDefecto {
		if valor := os.Getenv("LOG_LEVEL"); valor != "" {
			cfg.LogNivel = valor
		}
	}

	// El timeout solo viene del entorno si el flag sigue en su valor por
	// defecto. Un --timeout explicito se respeta aunque REQUEST_TIMEOUT exista.
	if !fueEnviada("timeout") || cfg.Timeout == 0 || cfg.Timeout == TimeoutPorDefecto {
		if valor := os.Getenv("REQUEST_TIMEOUT"); valor != "" {
			if d, err := time.ParseDuration(valor); err == nil && d > 0 {
				cfg.Timeout = d
			}
		}
	}
}

// validar comprueba que la configuracion tiene todo lo necesario.
//
// Los requisitos cambian segun el modo: en dry-run no hace falta ningun dato de
// GitHub, porque no se va a publicar nada. Exigirlo en dry-run seria hacer que el
// usuario configure credenciales que no va a usar.
func (cfg *Configuracion) validar() error {
	if strings.TrimSpace(cfg.Ruta) == "" {
		return fmt.Errorf("%w: --file es obligatorio (usa -h para ver las opciones)", errFaltanParametros)
	}

	switch cfg.Proveedor {
	case "ollama", "openai", "anthropic", "gemini":
	default:
		return fmt.Errorf("%w: proveedor desconocido %q (usa ollama, openai, anthropic o gemini)",
			domain.ErrConfigInvalida, cfg.Proveedor)
	}

	switch cfg.FormatoSalida {
	case "json", "table":
	default:
		return fmt.Errorf("%w: formato de salida desconocido %q (usa json o table)",
			domain.ErrConfigInvalida, cfg.FormatoSalida)
	}

	if cfg.Modo == application.ModoPublish {
		if cfg.ProjectRef == "" {
			return fmt.Errorf("%w: --publish necesita --project (o GITHUB_PROJECT_ID)", errFaltanParametros)
		}

		if cfg.Adaptador == "graphql" {
			if cfg.Token == "" {
				return fmt.Errorf("%w: --publish necesita un token (GITHUB_TOKEN con scope 'project')",
					errFaltanParametros)
			}
			if cfg.Owner == "" || cfg.Repositorio == "" {
				return fmt.Errorf("%w: --publish necesita --owner y --repo (o sus variables de entorno)",
					errFaltanParametros)
			}
		}
	}

	return nil
}

// politicaNormalizada traduce la politica de la CLI al valor de la aplicacion.
func politicaNormalizada(valor string) application.PoliticaDesconocido {
	switch strings.ToLower(strings.TrimSpace(valor)) {
	case "assign_unassigned":
		return application.AnteDesconocidoSinAsignado
	case "skip":
		return application.AnteDesconocidoOmitir
	default:
		return application.AnteDesconocidoFallar
	}
}

// cargarMappings lee la tabla de aliases y devuelve el mapper.
//
// En dry-run la tabla es OPCIONAL: las identidades no se consultan (TC-05), asi
// que exigirla obligaria al usuario a copiar un archivo que no va a usar. Si
// esta, se carga y se aprovecha para el handle por defecto; si no, se devuelve un
// mapper vacio y la ejecucion continua. Requerirla en seco convertia el modo por
// defecto en un Obstaculo, y ademas hacia que un error de archivo se reportara
// con el codigo de identidad (5) en lugar del de ingesta (3).
//
// Se separa para que el composition root no mezcle lectura de archivos con
// construcción de adaptadores.
func cargarMappings(ruta string, modo application.ModoSalida) (*identity.JSONIdentityMapper, error) {
	mapper, err := identity.NuevoJSONIdentityMapper(ruta)
	if err == nil {
		return mapper, nil
	}

	if modo == application.ModoDryRun {
		return identity.MapperVacio(), nil
	}

	return nil, fmt.Errorf(
		"%w: no se pudo cargar desde %q: %v\n"+
			"        Copia el ejemplo con: cp configs/mappings.example.json configs/mappings.json",
		domain.ErrMapeoDeIdentidadInvalido, ruta, err)
}

// primeroNoVacio devuelve el primer valor no vacio.
func primeroNoVacio(valores ...string) string {
	for _, v := range valores {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}

	return ""
}

// timeoutEfectivo devuelve el timeout configurado con un suelo razonable.
//
// Sin un minimo, un --timeout de 1ms haria que toda la extraccion fallara sin
// darle al LLM tiempo de responder, y el mensaje seria desconcertante.
func timeoutEfectivo(cfg *Configuracion) time.Duration {
	const minimo = 5 * time.Second

	if cfg.Timeout < minimo {
		return minimo
	}

	return cfg.Timeout
}
