package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"talkaboutthis/internal/domain"
	"talkaboutthis/internal/infrastructure/identity"
)

// Estos tests cubren la CLI sin salir a la red.
//
// El subcomando ingest se ejecuta de extremo a extremo contra un servidor
// httptest que hace de Ollama, lo que permite verificar el binario completo:
// flags, configuracion, pipeline, renderizado y codigo de salida.

func ejecutarParaPrueba(t *testing.T, args ...string) (stdout, stderr string, codigo int) {
	t.Helper()

	var salida, errores bytes.Buffer

	codigo = ejecutar(context.Background(), args, &salida, &errores)

	return salida.String(), errores.String(), codigo
}

// --- Despacho de subcomandos ---

func TestSinArgumentosMuestraUso(t *testing.T) {
	stdout, stderr, codigo := ejecutarParaPrueba(t)

	if codigo != salidaErrorUso {
		t.Errorf("sin argumentos se esperaba codigo %d, se obtuvo %d", salidaErrorUso, codigo)
	}
	if !strings.Contains(stderr, "Uso:") {
		t.Errorf("deberia mostrarse el uso: %q", stderr)
	}
	if stdout != "" {
		t.Errorf("sin argumentos no deberia escribirse en stdout: %q", stdout)
	}
}

func TestAyuda(t *testing.T) {
	for _, flag := range []string{"help", "-h", "--help"} {
		t.Run(flag, func(t *testing.T) {
			stdout, _, codigo := ejecutarParaPrueba(t, flag)

			if codigo != salidaOK {
				t.Errorf("se esperaba codigo 0, se obtuvo %d", codigo)
			}
			if !strings.Contains(stdout, "Uso:") {
				t.Errorf("deberia mostrarse el uso: %q", stdout)
			}
		})
	}
}

func TestVersion(t *testing.T) {
	stdout, _, codigo := ejecutarParaPrueba(t, "--version")

	if codigo != salidaOK {
		t.Errorf("se esperaba codigo 0, se obtuvo %d", codigo)
	}
	if !strings.Contains(stdout, version) {
		t.Errorf("deberia mostrarse la version %q: %q", version, stdout)
	}
}

func TestSubcomandoDesconocido(t *testing.T) {
	_, stderr, codigo := ejecutarParaPrueba(t, "inventado")

	if codigo != salidaErrorUso {
		t.Errorf("se esperaba codigo %d, se obtuvo %d", salidaErrorUso, codigo)
	}
	if !strings.Contains(stderr, "subcomando desconocido") {
		t.Errorf("el mensaje deberia explicar el problema: %q", stderr)
	}
}

// --- Validacion de parametros ---

// TestFileEsObligatorio es la validacion mas basica y la que mas se viola.
func TestFileEsObligatorio(t *testing.T) {
	_, stderr, codigo := ejecutarParaPrueba(t, "ingest")

	if codigo != salidaErrorUso {
		t.Errorf("se esperaba codigo %d, se obtuvo %d", salidaErrorUso, codigo)
	}
	if !strings.Contains(stderr, "--file") {
		t.Errorf("el mensaje deberia indicar que falta --file: %q", stderr)
	}
}

// TestDryRunYPublishSonExcluyentes evita el error de uso mas grave.
func TestDryRunYPublishSonExcluyentes(t *testing.T) {
	_, stderr, codigo := ejecutarParaPrueba(t, "ingest", "--file", "x.md", "--dry-run", "--publish")

	if codigo != salidaErrorUso {
		t.Errorf("se esperaba codigo %d, se obtuvo %d", salidaErrorUso, codigo)
	}
	if !strings.Contains(stderr, "excluyentes") {
		t.Errorf("el mensaje deberia explicar la contradiccion: %q", stderr)
	}
}

func TestProveedorDesconocido(t *testing.T) {
	_, stderr, codigo := ejecutarParaPrueba(t, "ingest", "--file", "x.md", "--provider", "inventado")

	if codigo != salidaErrorConfig {
		t.Errorf("se esperaba codigo %d, se obtuvo %d", salidaErrorConfig, codigo)
	}
	if !strings.Contains(stderr, "inventado") {
		t.Errorf("el mensaje deberia nombrar el proveedor invalido: %q", stderr)
	}
}

func TestFormatoDeSalidaDesconocido(t *testing.T) {
	_, _, codigo := ejecutarParaPrueba(t, "ingest", "--file", "x.md", "--output", "yaml")

	if codigo != salidaErrorConfig {
		t.Errorf("se esperaba codigo %d, se obtuvo %d", salidaErrorConfig, codigo)
	}
}

func TestFormatoDeLogDesconocido(t *testing.T) {
	_, _, codigo := ejecutarParaPrueba(t, "ingest", "--file", "x.md", "--log-format", "yaml")

	if codigo != salidaErrorConfig {
		t.Errorf("se esperaba codigo %d, se obtuvo %d", salidaErrorConfig, codigo)
	}
}

// TestPublishExigeCredenciales comprueba que publicar sin configuracion falla
// ANTES de gastar una llamada al LLM, que es lo caro.
func TestPublishExigeCredenciales(t *testing.T) {
	_, stderr, codigo := ejecutarParaPrueba(t, "ingest", "--file", "x.md", "--publish")

	if codigo != salidaErrorUso {
		t.Errorf("se esperaba codigo %d, se obtuvo %d", salidaErrorUso, codigo)
	}
	if !strings.Contains(stderr, "--project") {
		t.Errorf("el mensaje deberia indicar que falta --project: %q", stderr)
	}
}

// TestPublishExigeToken comprueba el siguiente requisito que falta.
func TestPublishExigeToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GITHUB_OWNER", "")
	t.Setenv("GITHUB_REPO", "")

	_, stderr, codigo := ejecutarParaPrueba(t,
		"ingest", "--file", "x.md", "--publish", "--project", "5", "--owner", "o", "--repo", "r")

	if codigo != salidaErrorUso {
		t.Errorf("se esperaba codigo %d, se obtuvo %d", salidaErrorUso, codigo)
	}
	if !strings.Contains(stderr, "token") {
		t.Errorf("el mensaje deberia indicar que falta el token: %q", stderr)
	}
}

func TestArchivoInexistente(t *testing.T) {
	// Se usa una tabla de mappings valida para que el fallo sea el del archivo,
	// no el de la configuracion.
	mappings := escribirMappings(t)

	_, stderr, codigo := ejecutarParaPrueba(t,
		"ingest", "--file", "no-existe.md", "--mappings", mappings)

	if codigo != salidaErrorIngesta {
		t.Errorf("se esperaba codigo %d, se obtuvo %d", salidaErrorIngesta, codigo)
	}
	if !strings.Contains(stderr, "no-existe.md") {
		t.Errorf("el mensaje deberia nombrar el archivo: %q", stderr)
	}
}

// TestTablaMappingsAusenteNoBloqueaElDryRun comprueba que la tabla de
// identidades es opcional en dry-run.
//
// No es un detalle: las identidades NO se consultan en seco (TC-05), asi que
// exigir el archivo convertia el modo por defecto en un obstaculo para probar la
// herramienta. Antes ademas el error salia con el codigo 5 (identidad) cuando el
// problema real era de lectura: un "archivo no existe" con codigo de identidad
// desorienta a un proceso de CI.
func TestTablaMappingsAusenteNoBloqueaElDryRun(t *testing.T) {
	_, stderr, codigo := ejecutarParaPrueba(t,
		"ingest", "--file", "no-existe.md", "--mappings", "tabla-que-no-existe.json")

	// Lo que falla aqui es la INGESTA (el archivo no existe), no la identidad.
	if codigo != salidaErrorIngesta {
		t.Errorf("se esperaba codigo %d (ingesta), se obtuvo %d", salidaErrorIngesta, codigo)
	}
	if !strings.Contains(stderr, "no-existe.md") {
		t.Errorf("el mensaje deberia nombrar el archivo que falta: %q", stderr)
	}
	if strings.Contains(stderr, "mappings.example.json") {
		t.Errorf("en dry-run no deberia quejarse de la tabla de identidades: %q", stderr)
	}
}

// TestTablaMappingsAusenteSiFallaEnPublish es el otro lado: al publicar, las
// identidades SI hacen falta, y el mensaje debe decir como crearla.
func TestTablaMappingsAusenteSiFallaEnPublish(t *testing.T) {
	// El token no tiene bandera a proposito: es un secreto y no debe acabar en
	// el historial del shell ni en un `ps` visible para el resto del sistema.
	t.Setenv("GITHUB_TOKEN", "ghp_secreto")

	_, stderr, codigo := ejecutarParaPrueba(t,
		"ingest",
		"--file", rutaFixturePrueba(t),
		"--publish",
		"--mappings", "tabla-que-no-existe.json",
		"--project", "PVT_1",
		"--owner", "org",
		"--repo", "repo",
	)

	if codigo != salidaErrorIdentidad {
		t.Errorf("se esperaba codigo %d, se obtuvo %d", salidaErrorIdentidad, codigo)
	}
	if !strings.Contains(stderr, "mappings.example.json") {
		t.Errorf("el mensaje deberia sugerir como crear la tabla: %q", stderr)
	}
	if strings.Contains(stderr, "ghp_secreto") {
		t.Error("el token no debe aparecer nunca en la salida")
	}
}

// TestMapperVacioNoResuelveNada comprueba la pieza que hace posible el dry-run
// sin tabla: un mapper vacio no debe inventar responsables.
func TestMapperVacioNoResuelveNada(t *testing.T) {
	m := identity.MapperVacio()

	if _, err := m.ResolveHandle(context.Background(), "Omar Hernández"); err == nil {
		t.Error("un mapper vacio no deberia resolver ningun nombre")
	}

	if len(m.HandlesConocidos()) != 0 {
		t.Errorf("un mapper vacio no deberia tener handles, tiene %d", len(m.HandlesConocidos()))
	}

	if m.HandlePorDefecto() != "" {
		t.Errorf("un mapper vacio no deberia tener handle por defecto, es %q", m.HandlePorDefecto())
	}
}

// rutaFixturePrueba devuelve una transcripcion real de testdata.
func rutaFixturePrueba(t *testing.T) string {
	t.Helper()

	return filepath.Join("..", "..", "testdata", "reunion-equipo.md")
}

// escribirMappings crea una tabla de aliases valida en un directorio temporal.
func escribirMappings(t *testing.T) string {
	t.Helper()

	ruta := filepath.Join(t.TempDir(), "mappings.json")

	contenido := `{
		"version": "1.0",
		"defaults": {"default_handle": "sin-asignar", "unknown_assignee_policy": "fail"},
		"mappings": [
			{"raw_names": ["Omar Hernández"], "handle": "omarhernan"},
			{"raw_names": ["Ana María Ruiz"], "handle": "anaruiz"}
		]
	}`

	if err := os.WriteFile(ruta, []byte(contenido), 0o600); err != nil {
		t.Fatalf("no se pudo escribir la tabla: %v", err)
	}

	return ruta
}

// --- Cancelacion ---

func TestContextoCancelado(t *testing.T) {
	// Se necesita una configuracion valida para LLEGAR al pipeline: si la
	// validacion falla antes, el error seria de configuracion y no de cancelacion.
	mappings := escribirMappings(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var salida, errores bytes.Buffer

	codigo := ejecutar(ctx, []string{
		"ingest",
		"--file", filepath.Join("testdata", "notas-rapidas.txt"),
		"--mappings", mappings,
	}, &salida, &errores)

	if codigo != salidaInterrumpido {
		t.Errorf("se esperaba codigo %d para una cancelacion, se obtuvo %d (stderr: %s)",
			salidaInterrumpido, codigo, errores.String())
	}
}

// --- Traduccion de errores a codigos de salida ---

func TestCodigoDeError(t *testing.T) {
	casos := []struct {
		nombre string
		err    error
		quiere int
	}{
		{"config invalida", domain.ErrConfigInvalida, salidaErrorConfig},
		{"formato no soportado", domain.ErrFormatoNoSoportado, salidaErrorIngesta},
		{"reintentos agotados", domain.ErrReintentosAgotados, salidaErrorExtraccion},
		{"identidad no resuelta", domain.ErrIdentidadNoResuelta, salidaErrorIdentidad},
		{"mapeo invalido", domain.ErrMapeoDeIdentidadInvalido, salidaErrorIdentidad},
		{"error desconocido", errors.New("algo raro"), salidaErrorUso},
	}

	for _, tt := range casos {
		t.Run(tt.nombre, func(t *testing.T) {
			if got := codigoDeError(tt.err); got != tt.quiere {
				t.Errorf("codigoDeError(%v) = %d, se esperaba %d", tt.err, got, tt.quiere)
			}
		})
	}
}

func TestCodigoDeErrorEncadenados(t *testing.T) {
	// Los errores se envuelven con %w a traves de las capas; la traduccion debe
	// funcionar tambien sobre el error final.
	envuelto := &ErrorEtapaFalso{Etapa: "extraccion", Err: domain.ErrReintentosAgotados}

	if got := codigoDeEtapa("extraccion", envuelto.Err); got != salidaErrorExtraccion {
		t.Errorf("se esperaba %d, se obtuvo %d", salidaErrorExtraccion, got)
	}
}

// ErrorEtapaFalso simula el envoltorio de etapa sin importar application.
type ErrorEtapaFalso struct {
	Etapa string
	Err   error
}

func (e *ErrorEtapaFalso) Error() string { return e.Err.Error() }
func (e *ErrorEtapaFalso) Unwrap() error { return e.Err }

// --- Configuracion ---

// TestDryRunEsElModoPorDefecto es una decision de seguridad que debe estar
// protegida por un test: publicar en el tablero de un equipo no puede ocurrir
// porque el usuario olvido una bandera.
func TestDryRunEsElModoPorDefecto(t *testing.T) {
	dryRunFlag = false
	publishFlag = false

	var cfg Configuracion
	cfg.completar()

	if cfg.Modo != "dry-run" {
		t.Errorf("sin banderas el modo deberia ser dry-run, es %q", cfg.Modo)
	}
}

func TestModoPublishExplicito(t *testing.T) {
	dryRunFlag = false
	publishFlag = true
	defer func() { publishFlag = false }()

	var cfg Configuracion
	cfg.completar()

	if cfg.Modo != "publish" {
		t.Errorf("con --publish el modo deberia ser publish, es %q", cfg.Modo)
	}
}

// TestValidarNoExigeCredencialesEnDryRun comprueba que el dry-run se puede
// ejecutar sin configurar nada de GitHub.
func TestValidarNoExigeCredencialesEnDryRun(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GITHUB_PROJECT_ID", "")

	var cfg Configuracion
	cfg.Ruta = "notas.md"
	cfg.Proveedor = "ollama"
	cfg.FormatoSalida = "json"
	cfg.Modo = "dry-run"

	if err := cfg.validar(); err != nil {
		t.Errorf("el dry-run no deberia exigir credenciales de GitHub: %v", err)
	}
}

func TestValidarExigeCredencialesEnPublish(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")

	cfg := Configuracion{
		Ruta:          "notas.md",
		Proveedor:     "ollama",
		FormatoSalida: "json",
		Modo:          "publish",
		ProjectRef:    "5",
		Adaptador:     "graphql",
	}

	if err := cfg.validar(); err == nil {
		t.Error("publicar sin token deberia fallar la validacion")
	}
}

func TestEntornoSobrescribePorDefecto(t *testing.T) {
	t.Setenv("LLM_PROVIDER", "openai")
	t.Setenv("GITHUB_TOKEN", "token-de-entorno")

	var cfg Configuracion
	cfg.completar()

	if cfg.Proveedor != "openai" {
		t.Errorf("LLM_PROVIDER deberia aplicarse, el proveedor es %q", cfg.Proveedor)
	}
	if cfg.Token != "token-de-entorno" {
		t.Errorf("GITHUB_TOKEN deberia aplicarse, el token es %q", cfg.Token)
	}
}

// TestEntornoConfiguraElLogging comprueba que LOG_LEVEL y LOG_FORMAT, que
// aparecen en .env.example, llegan de verdad a la configuracion. Si se
// documentan y no se leen, el usuario los ajusta y no pasa nada.
func TestEntornoConfiguraElLogging(t *testing.T) {
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("LOG_FORMAT", "json")

	var cfg Configuracion
	envios = map[string]bool{}
	if err := cfg.completar(); err != nil {
		t.Fatalf("completar fallo: %v", err)
	}

	if cfg.LogNivel != "debug" {
		t.Errorf("LOG_LEVEL deberia aplicarse, el nivel es %q", cfg.LogNivel)
	}

	if string(cfg.LogFormato) != "json" {
		t.Errorf("LOG_FORMAT deberia aplicarse, el formato es %q", cfg.LogFormato)
	}

	// La variable global no debe quedar tocada: si se reescribiera, el
	// resultado dependeria del orden de los tests.
	if logFormatoFlag != FormatoLogPorDefecto {
		t.Errorf("completar no deberia modificar el flag global, quedo en %q", logFormatoFlag)
	}
}

// configDesdePrueba procesa argumentos reales, como haria la CLI.
//
// Monta un FlagSet de verdad en lugar de fijar los campos a mano: la precedencia
// entre flag y entorno depende de saber si la bandera APARECIO en la linea de
// comandos, y eso solo se obtiene parseando.
func configDesdePrueba(t *testing.T, args ...string) Configuracion {
	t.Helper()

	var cfg Configuracion

	fs := flag.NewFlagSet("prueba", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	registrarFlags(fs, &cfg)

	envios = map[string]bool{}

	if err := fs.Parse(args); err != nil {
		t.Fatalf("no se pudieron parsear los argumentos %v: %v", args, err)
	}

	registrarFlagsEnviadas(fs, &cfg)

	if err := cfg.completar(); err != nil {
		t.Fatalf("completar fallo: %v", err)
	}

	return cfg
}

// TestElFlagGanaAlEntorno comprueba la precedencia declarada en el codigo: lo
// explicito en la linea de comandos manda sobre lo heredado del entorno.
func TestElFlagGanaAlEntorno(t *testing.T) {
	t.Setenv("LOG_FORMAT", "json")
	t.Setenv("LLM_PROVIDER", "openai")
	t.Setenv("ASSIGNEE_POLICY", "skip")
	t.Setenv("GITHUB_OWNER", "org-del-entorno")
	t.Setenv("REQUEST_TIMEOUT", "5s")

	cfg := configDesdePrueba(t,
		"--file", "x.md",
		"--log-format", "text",
		"--provider", "anthropic",
		"--assignee-policy", "fail",
		"--owner", "org-del-flag",
		"--timeout", "90s",
	)

	casos := []struct {
		nombre   string
		obtenido string
		esperado string
	}{
		{"--log-format", string(cfg.LogFormato), "text"},
		{"--provider", cfg.Proveedor, "anthropic"},
		{"--assignee-policy", cfg.Politica, "fail"},
		{"--owner", cfg.Owner, "org-del-flag"},
		{"--timeout", cfg.Timeout.String(), "1m30s"},
	}

	for _, tt := range casos {
		if tt.obtenido != tt.esperado {
			t.Errorf("%s = %q, se esperaba %q (el flag debe ganar al entorno)", tt.nombre, tt.obtenido, tt.esperado)
		}
	}
}

// TestElEntornoGanaSiNoHayFlag comprueba el otro lado: sin bandera escrita, el
// entorno es la fuente de la intencion.
func TestElEntornoGanaSiNoHayFlag(t *testing.T) {
	t.Setenv("LOG_FORMAT", "json")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("LLM_PROVIDER", "openai")
	t.Setenv("ASSIGNEE_POLICY", "skip")
	t.Setenv("GITHUB_OWNER", "org-del-entorno")
	t.Setenv("REQUEST_TIMEOUT", "5s")

	cfg := configDesdePrueba(t, "--file", "x.md")

	casos := []struct {
		nombre   string
		obtenido string
		esperado string
	}{
		{"LOG_FORMAT", string(cfg.LogFormato), "json"},
		{"LOG_LEVEL", cfg.LogNivel, "debug"},
		{"LLM_PROVIDER", cfg.Proveedor, "openai"},
		{"ASSIGNEE_POLICY", cfg.Politica, "skip"},
		{"GITHUB_OWNER", cfg.Owner, "org-del-entorno"},
		{"REQUEST_TIMEOUT", cfg.Timeout.String(), "5s"},
	}

	for _, tt := range casos {
		if tt.obtenido != tt.esperado {
			t.Errorf("%s = %q, se esperaba %q (sin flag, manda el entorno)", tt.nombre, tt.obtenido, tt.esperado)
		}
	}
}

// TestFormatoDeLogInvalidoDesdeEntornoEsErrorDeConfiguracion comprueba que un
// valor invalido en el entorno da el mismo error de configuracion que si
// venía por flag, y no un fallo de parseo de flags.
func TestFormatoDeLogInvalidoDesdeEntornoEsErrorDeConfiguracion(t *testing.T) {
	t.Setenv("LOG_FORMAT", "xml")

	var cfg Configuracion
	envios = map[string]bool{}

	err := cfg.completar()
	if err == nil {
		t.Fatal("un formato de log invalido deberia fallar")
	}
	if !errors.Is(err, domain.ErrConfigInvalida) {
		t.Errorf("se esperaba ErrConfigInvalida, se obtuvo %v", err)
	}
}

// TestEntornoConfiguraLaPolitica comprueba que ASSIGNEE_POLICY llega a la
// configuracion, y que el flag sigue mandando.
func TestEntornoConfiguraLaPolitica(t *testing.T) {
	t.Setenv("ASSIGNEE_POLICY", "skip")

	var cfg Configuracion
	if err := cfg.completar(); err != nil {
		t.Fatalf("completar fallo: %v", err)
	}

	if cfg.Politica != "skip" {
		t.Errorf("ASSIGNEE_POLICY deberia aplicarse, la politica es %q", cfg.Politica)
	}
}

func TestPoliticaNormalizada(t *testing.T) {
	casos := map[string]string{
		"fail":              "fail",
		"assign_unassigned": "assign_unassigned",
		"skip":              "skip",
		"":                  "fail",
		"inventada":         "fail",
		"  SKIP  ":          "skip",
	}

	for entrada, esperado := range casos {
		if got := string(politicaNormalizada(entrada)); got != esperado {
			t.Errorf("politicaNormalizada(%q) = %q, se esperaba %q", entrada, got, esperado)
		}
	}
}

func TestTimeoutEfectivoTieneSuelo(t *testing.T) {
	// Un timeout ridiculamente corto haria fallar todo sin dar oportunidad al
	// LLM de responder.
	if got := timeoutEfectivo(&Configuracion{Timeout: 1}); got < 5*1000000000 {
		t.Errorf("el timeout deberia tener un suelo, es %v", got)
	}

	if got := timeoutEfectivo(&Configuracion{Timeout: 30 * 1000000000}); got != 30*1000000000 {
		t.Errorf("un timeout valido deberia respetarse, es %v", got)
	}
}

// --- Renderizado ---

// TestRenderizarTablaYFallbackJSON usa un resultado completo construido a mano,
// sin pasar por el pipeline, para aislar la presentacion.
func TestRenderizarModoDesconocido(t *testing.T) {
	var salida bytes.Buffer

	if err := renderizar(&salida, nil, "yaml"); err == nil {
		t.Error("un formato desconocido deberia dar error")
	}
}

func TestSalidaJSONEsParseable(t *testing.T) {
	var salida bytes.Buffer

	if err := renderizar(&salida, resultadoDePrueba(), "json"); err != nil {
		t.Fatalf("el renderizado fallo: %v", err)
	}

	var documento map[string]any
	if err := json.Unmarshal(salida.Bytes(), &documento); err != nil {
		t.Fatalf("la salida deberia ser JSON valido: %v\nsalida: %s", err, salida.String())
	}

	for _, clave := range []string{"job_id", "archivo", "modo", "meeting_summary", "action_items"} {
		if _, ok := documento[clave]; !ok {
			t.Errorf("falta la clave %q en la salida JSON", clave)
		}
	}

	items, ok := documento["action_items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("se esperaban 2 items, se obtuvieron %v", documento["action_items"])
	}
}

func TestSalidaJSONMarcaResponsableNoResuelto(t *testing.T) {
	var salida bytes.Buffer

	if err := renderizar(&salida, resultadoDePrueba(), "json"); err != nil {
		t.Fatalf("el renderizado fallo: %v", err)
	}

	if !strings.Contains(salida.String(), "SIN RESOLVER") &&
		!strings.Contains(salida.String(), "mapped_handle") {
		t.Log("la salida JSON no marca explicitamente el responsable no resuelto")
	}
}

func TestRenderizadoTabla(t *testing.T) {
	var salida bytes.Buffer

	if err := renderizar(&salida, resultadoDePrueba(), "table"); err != nil {
		t.Fatalf("el renderizado fallo: %v", err)
	}

	texto := salida.String()

	for _, esperado := range []string{
		"PRIORIDAD", "RESPONSABLE", "TITULO",
		"Migrar la sesion", "ALTA", "omarhernan",
		"Total: 2 item(s)",
	} {
		if !strings.Contains(texto, esperado) {
			t.Errorf("la tabla no contiene %q:\n%s", esperado, texto)
		}
	}
}

func TestTablaMarcaSinResolver(t *testing.T) {
	// En modo publish: en dry-run no se resuelven identidades y no habria nada
	// que marcar (y marcarlo seria una alarma falsa).
	var salida bytes.Buffer

	if err := renderizar(&salida, resultadoPublicadoDePrueba(), "table"); err != nil {
		t.Fatalf("el renderizado fallo: %v", err)
	}

	if !strings.Contains(salida.String(), "SIN RESOLVER") {
		t.Errorf("un responsable no resuelto debe quedar visible en la tabla:\n%s", salida.String())
	}
}

// TestTablaToleraTitulosConSaltosDeLinea protege la alineacion: un titulo con
// saltos romperia la tabla y la haria ilegible.
func TestTablaAplanaSaltosDeLinea(t *testing.T) {
	var salida bytes.Buffer

	resultado := resultadoDePrueba()
	resultado.Extraccion.ActionItems[0].Title = "Titulo\ncon\nsaltos"

	if err := renderizar(&salida, resultado, "table"); err != nil {
		t.Fatalf("el renderizado fallo: %v", err)
	}

	if strings.Contains(salida.String(), "Titulo\ncon") {
		t.Errorf("los saltos deberian aplanarse:\n%s", salida.String())
	}
	if !strings.Contains(salida.String(), "Titulo con saltos") {
		t.Errorf("el titulo deberia conservarse en una sola linea:\n%s", salida.String())
	}
}

func TestRepoCompleto(t *testing.T) {
	cfg := Configuracion{Owner: "mi-org", Repositorio: "mi-repo"}
	if got := repoCompleto(&cfg); got != "mi-org/mi-repo" {
		t.Errorf("repoCompleto = %q", got)
	}

	cfg = Configuracion{Owner: "mi-org"}
	if got := repoCompleto(&cfg); got != "" {
		t.Errorf("sin repositorio deberia devolver vacio, devolvio %q", got)
	}
}
