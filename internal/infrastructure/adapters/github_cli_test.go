package adapters_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"talkaboutthis/internal/domain"
	"talkaboutthis/internal/infrastructure/adapters"
)

// Estos tests ejercitan el adaptador de la CLI de GitHub SIN que "gh" este
// instalado, usando la tecnica del proceso-ayudante: el propio binario de test
// se reejecuta a si mismo y se comporta como un "gh" falso cuando encuentra la
// variable de entorno correspondiente.
//
// La alternativa (una prueba de integracion que exija "gh" y un PAT) no es
// aceptable en una suite: fallaria en cualquier equipo que no lo tenga
// instalado, y son justamente esos fallos los que la gente empieza a ignorar.

const (
	envFakeGh      = "TALKABOUTTHIS_FAKE_GH"
	envFakeGhFalla = "TALKABOUTTHIS_FAKE_GH_FALLA"
	envFakeGhLog   = "TALKABOUTTHIS_FAKE_GH_LOG"
)

// separadorInvocaciones marca donde acaba una invocacion del falso gh y empieza
// la siguiente.
const separadorInvocaciones = "--- siguiente invocacion ---"

// TestMain convierte el binario de test en un "gh" falso cuando se le pide.
func TestMain(m *testing.M) {
	if os.Getenv(envFakeGh) == "1" {
		ejecutarComoFakeGh()
		return
	}

	os.Exit(m.Run())
}

// ejecutarComoFakeGh simula la salida de la CLI de GitHub.
func ejecutarComoFakeGh() {
	args := os.Args[1:]

	registrarInvocacion(args)

	if os.Getenv(envFakeGhFalla) == "1" {
		// gh escribe los errores por stderr, que es de donde el adaptador los
		// extrae para construir su mensaje.
		fmt.Fprintln(os.Stderr, "gh: authentication required (simulado)")
		os.Exit(1)
	}

	// "gh issue create" imprime por stdout la URL del issue creado.
	if len(args) >= 2 && args[0] == "issue" && args[1] == "create" {
		fmt.Println("https://github.com/organizacion/repo/issues/99")
	}

	os.Exit(0)
}

// registrarInvocacion deja constancia de lo recibido.
//
// Se registra el stdin ademas de los argumentos porque "gh api graphql" solo
// acepta la consulta por ahi: sin registrarlo, el test no podria afirmar nada
// sobre la mutacion que se envia al tablero.
func registrarInvocacion(args []string) {
	ruta := os.Getenv(envFakeGhLog)
	if ruta == "" {
		return
	}

	var entrada string
	if os.Stdin != nil {
		bruto, _ := io.ReadAll(os.Stdin)
		entrada = string(bruto)
	}

	var registro strings.Builder
	registro.WriteString("ARGS: " + strings.Join(args, " ") + "\n")

	if entrada != "" {
		registro.WriteString("STDIN: " + entrada + "\n")
	}

	// Se ANADE en vez de sobrescribir: publicar un item implica dos invocaciones
	// (crear el issue y asociarlo al tablero) y el test quiere ver ambas.
	previo, err := os.ReadFile(ruta)
	if err == nil && len(previo) > 0 {
		registro.WriteString(separadorInvocaciones + "\n")
		registro.Write(previo)
	}

	_ = os.WriteFile(ruta, []byte(registro.String()), 0o600)
}

// nuevoAdaptorCLI construye el adaptador usando el binario de test como "gh".
func nuevoAdaptorCLI(t *testing.T, rutaLog string) *adapters.GitHubCLI {
	t.Helper()

	t.Setenv(envFakeGh, "1")
	t.Setenv(envFakeGhLog, rutaLog)

	adaptador, err := adapters.NuevoGitHubCLI(adapters.GitHubCLIOpciones{
		// os.Args[0] es el propio binario de test, que ya sabe comportarse como
		// un "gh" falso gracias a TestMain.
		Ejecutable:  os.Args[0],
		Timeout:     10 * time.Second,
		Repositorio: "organizacion/repo",
	})
	if err != nil {
		t.Fatalf("no se pudo construir el adaptador de CLI: %v", err)
	}

	return adaptador
}

// leerRegistro devuelve todo lo registrado por el falso gh, con los saltos de
// linea normalizados para poder buscar subcadenas comodamente.
func leerRegistro(t *testing.T, ruta string) string {
	t.Helper()

	contenido, err := os.ReadFile(ruta)
	if err != nil {
		t.Fatalf("no se pudo leer el registro de invocaciones: %v", err)
	}

	return strings.ReplaceAll(string(contenido), "\n", " ")
}

// TestAdaptadorCLIPublicaConLaCli es el camino feliz del backend alternativo.
func TestAdaptadorCLIPublicaConLaCli(t *testing.T) {
	log := filepath.Join(t.TempDir(), "args.txt")
	adaptador := nuevoAdaptorCLI(t, log)

	if _, err := adaptador.PublishBacklog(context.Background(), "PVT_1",
		[]domain.ActionItem{itemDePrueba("Migrar la sesion")}); err != nil {
		t.Fatalf("la publicacion fallo: %v", err)
	}

	registro := leerRegistro(t, log)

	esperados := []string{
		"issue create",
		"--title",
		"Migrar la sesion",
		"--body",
		"--repo",
		"organizacion/repo",
		"--assignee",
		"omarhernan",
		"--label",
		"backend",
	}

	for _, esperado := range esperados {
		if !strings.Contains(registro, esperado) {
			t.Errorf("los argumentos no contienen %q:\n%s", esperado, registro)
		}
	}
}

// TestAdaptadorCLIImprimeLaURLDelIssue comprueba que se recoge la salida de
// "gh issue create" y se devuelve como URL.
func TestAdaptadorCLIImprimeLaURLDelIssue(t *testing.T) {
	log := filepath.Join(t.TempDir(), "args.txt")
	adaptador := nuevoAdaptorCLI(t, log)

	resultados, err := adaptador.PublishBacklog(context.Background(), "PVT_1",
		[]domain.ActionItem{itemDePrueba("Tarea")})
	if err != nil {
		t.Fatalf("la publicacion fallo: %v", err)
	}

	if len(resultados) != 1 {
		t.Fatalf("se esperaba 1 resultado, se obtuvieron %d", len(resultados))
	}
	if !resultados[0].Success {
		t.Fatalf("la publicacion deberia haber funcionado: %v", resultados[0].Error)
	}
	if !strings.Contains(resultados[0].URL, "/issues/99") {
		t.Errorf("se esperaba la URL emitida por gh, se obtuvo %q", resultados[0].URL)
	}
}

// TestAdaptadorCLIIntentaAgregarAlTableroViaGraphQL documenta la limitacion de
// "gh": no tiene comando para Projects v2, asi que se usa "gh api graphql".
func TestAdaptadorCLIIntentaAgregarAlTableroViaGraphQL(t *testing.T) {
	log := filepath.Join(t.TempDir(), "args.txt")
	adaptador := nuevoAdaptorCLI(t, log)

	if _, err := adaptador.PublishBacklog(context.Background(), "PVT_1",
		[]domain.ActionItem{itemDePrueba("Tarea")}); err != nil {
		t.Fatalf("la publicacion fallo: %v", err)
	}

	registro := leerRegistro(t, log)

	if !strings.Contains(registro, "api graphql") {
		t.Errorf("deberia delegar la asociacion al tablero en `gh api graphql`:\n%s", registro)
	}
	if !strings.Contains(registro, "addIssueToProject") {
		t.Errorf("deberia enviar la mutacion addIssueToProject:\n%s", registro)
	}
	if !strings.Contains(registro, "STDIN") {
		t.Errorf("la consulta GraphQL debe viajar por stdin:\n%s", registro)
	}
}

// TestAdaptadorCLIFalloPropagaStderr comprueba que el diagnostico de gh llega al
// usuario, y no un error generico.
func TestAdaptadorCLIFalloPropagaStderr(t *testing.T) {
	log := filepath.Join(t.TempDir(), "args.txt")
	adaptador := nuevoAdaptorCLI(t, log)

	t.Setenv(envFakeGhFalla, "1")

	resultados, err := adaptador.PublishBacklog(context.Background(), "PVT_1",
		[]domain.ActionItem{itemDePrueba("Tarea")})
	if err != nil {
		t.Fatalf("un fallo de gh debe quedar en el resultado, no abortar: %v", err)
	}

	if resultados[0].Success {
		t.Fatal("un proceso que termina con codigo 1 no puede considerarse exito")
	}
	if !strings.Contains(resultados[0].Error.Error(), "authentication required") {
		t.Errorf("el stderr de gh deberia conservarse: %v", resultados[0].Error)
	}
}

func TestAdaptadorCLIListaVacia(t *testing.T) {
	log := filepath.Join(t.TempDir(), "args.txt")
	adaptador := nuevoAdaptorCLI(t, log)

	resultados, err := adaptador.PublishBacklog(context.Background(), "PVT_1", nil)

	if err != nil {
		t.Fatalf("una lista vacia no es un error: %v", err)
	}
	if len(resultados) != 0 {
		t.Errorf("se esperaban 0 resultados, se obtuvieron %d", len(resultados))
	}
}

func TestAdaptadorCLIProjectRefVacio(t *testing.T) {
	log := filepath.Join(t.TempDir(), "args.txt")
	adaptador := nuevoAdaptorCLI(t, log)

	_, err := adaptador.PublishBacklog(context.Background(), "",
		[]domain.ActionItem{itemDePrueba("Tarea")})

	if !errors.Is(err, adapters.ErrConfigInvalida) {
		t.Errorf("se esperaba ErrConfigInvalida, se obtuvo %v", err)
	}
}

// TestAdaptadoresCumplenElMismoPuerto es la comprobacion de TC-07: los dos
// backends de GitHub implementan el mismo puerto, de modo que la capa de
// aplicacion no puede distinguirlos.
func TestAdaptadoresCumplenElMismoPuerto(t *testing.T) {
	var (
		_ domain.ProjectBoardAdapter = (*adapters.GitHubGraphQL)(nil)
		_ domain.ProjectBoardAdapter = (*adapters.GitHubCLI)(nil)
	)

	// Se usan como un unico valor del mismo tipo, que es como los consume el
	// caso de uso.
	var lista []domain.ProjectBoardAdapter

	log := filepath.Join(t.TempDir(), "args.txt")
	lista = append(lista, nuevoAdaptorCLI(t, log))
	lista = append(lista, adaptadorDePrueba(nuevoServidor(t)))

	if len(lista) != 2 {
		t.Fatal("se esperaban dos adaptadores")
	}

	for _, a := range lista {
		if a.PlatformName() == "" {
			t.Error("PlatformName no deberia estar vacio")
		}
	}
}
