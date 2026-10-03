package adapters

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"talkaboutthis/internal/domain"
)

// GitHubCLI publica el backlog usando el binario `gh` en vez de la API.
//
// Implementa domain.ProjectBoardAdapter (RF-05) y ofrece exactamente el mismo
// comportamiento observable que GitHubGraphQL.
//
// CUANDO USAR ESTE ADAPTADOR
//
//   - Cuando el token ya esta configurado en `gh auth login` y se prefiere no
//     duplicar credenciales.
//   - En entornos donde la API no es accesible pero si el binario (políticas de
//     red corporativas, proxies).
//   - Como respaldo si la API GraphQL rechaza el token.
//
// # QUE NO HACE
//
// `gh` no tiene comando para agregar un issue a un Project v2. Por eso este
// adaptador delega en `gh api graphql` para esa mutacion, y usa los comandos
// propios de `gh` solo para lo que si cubre de forma estable (crear el issue).
type GitHubCLI struct {
	// ejecutable permite sustituir el binario en los tests.
	ejecutable string
	// timeout acota cada invocacion.
	timeout time.Duration
	// repositorio es el destino con formato "owner/repo".
	repositorio string
}

// GitHubCLIOpciones configura el adaptador.
type GitHubCLIOpciones struct {
	// Ejecutable. Por defecto "gh".
	Ejecutable string
	// Timeout por invocacion. Por defecto 60s, mas holgado que el de la API
	// porque arrancar un proceso es mas caro que una peticion HTTP.
	Timeout time.Duration
	// Repositorio en formato "owner/repo". Si es vacio se deduce de --repo.
	Repositorio string
}

// Valores por defecto.
const (
	EjecutableGhPorDefecto = "gh"
	TimeoutGhPorDefecto    = 60 * time.Second
	EndpointGraphQLParaCli = "https://api.github.com/graphql"
)

// NuevoGitHubCLI construye el adaptador.
//
// Devuelve error si el binario `gh` no esta disponible: es mejor fallar al
// arrancar con un mensaje claro ("instala gh") que en mitad de la publicacion
// con veinte items a medias.
func NuevoGitHubCLI(opciones GitHubCLIOpciones) (*GitHubCLI, error) {
	ejecutable := opciones.Ejecutable
	if ejecutable == "" {
		ejecutable = EjecutableGhPorDefecto
	}

	timeout := opciones.Timeout
	if timeout == 0 {
		timeout = TimeoutGhPorDefecto
	}

	resuelto, err := exec.LookPath(ejecutable)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: no se encontro el ejecutable %q en el PATH (instala GitHub CLI para usar este adaptador)",
			ErrConfigInvalida, ejecutable)
	}

	return &GitHubCLI{
		ejecutable:  resuelto,
		timeout:     timeout,
		repositorio: opciones.Repositorio,
	}, nil
}

// PlatformName implementa domain.ProjectBoardAdapter.
func (g *GitHubCLI) PlatformName() string { return "github-projects-v2-cli" }

// PublishBacklog implementa domain.ProjectBoardAdapter.
//
// Publica los items en serie, no en paralelo. A diferencia de la API, cada
// invocacion de `gh` arranca un proceso nuevo y resolución de autenticacion;
// lanzar quince procesos a la vez compite por el bloqueo del archivo de
// configuracion de gh y produce fallos interdependientes. En serie es mas lento
// pero predecible.
func (g *GitHubCLI) PublishBacklog(ctx context.Context, projectRef string, items []domain.ActionItem) ([]domain.PublishResult, error) {
	if len(items) == 0 {
		return []domain.PublishResult{}, nil
	}

	if strings.TrimSpace(projectRef) == "" {
		return nil, fmt.Errorf("%w: falta el identificador del proyecto", ErrConfigInvalida)
	}

	resultados := make([]domain.PublishResult, len(items))

	for i, item := range items {
		if err := ctx.Err(); err != nil {
			// Se rellena lo pendiente como cancelado para que el resumen
			// refleje que no todo se intento.
			for j := i; j < len(items); j++ {
				resultados[j] = domain.PublishResult{
					TaskTitle: items[j].Title,
					Success:   false,
					Error:     err,
				}
			}
			return resultados, nil
		}

		resultados[i] = g.publicarItem(ctx, projectRef, item)
	}

	return resultados, nil
}

// publicarItem crea el issue con `gh issue create` y lo agrega al tablero.
func (g *GitHubCLI) publicarItem(ctx context.Context, projectRef string, item domain.ActionItem) domain.PublishResult {
	resultado := domain.PublishResult{TaskTitle: item.Title}

	// 1. Crear el issue.
	args := []string{
		"issue", "create",
		"--title", item.Title,
		"--body", construirCuerpo(item),
	}

	if g.repositorio != "" {
		args = append(args, "--repo", g.repositorio)
	}

	if item.MappedHandle != "" {
		args = append(args, "--assignee", item.MappedHandle)
	}

	for _, etiqueta := range item.Labels {
		args = append(args, "--label", etiqueta)
	}

	salida, err := g.ejecutar(ctx, args...)
	if err != nil {
		resultado.Error = fmt.Errorf("gh issue create fallo: %w", err)
		return resultado
	}

	// `gh issue create` imprime la URL del issue creado.
	url := strings.TrimSpace(salida)
	resultado.URL = url
	resultado.ExternalID = url

	// 2. Agregarlo al tablero. Requiere GraphQL, porque `gh` no tiene comando
	// para Projects v2.
	if err := g.agregarAlProyecto(ctx, projectRef, url); err != nil {
		resultado.Error = fmt.Errorf(
			"el issue %s se creo pero NO se pudo agregar al tablero: %w", url, err)
		return resultado
	}

	resultado.Success = true
	return resultado
}

// agregarAlProyecto asocia el issue al tablero via `gh api graphql`.
func (g *GitHubCLI) agregarAlProyecto(ctx context.Context, projectRef, issueURL string) error {
	// `gh api graphql` acepta unicamente el cuerpo por stdin, por lo que la
	// consulta y las variables viajan por ahi y no en la linea de comandos: asi
	// no quedan en la lista de procesos del sistema, visibles para otros
	// usuarios de la maquina.
	payload := fmt.Sprintf(
		`{"query":%q,"variables":{"projectId":%q,"contentId":%q}}`,
		mutationAddIssueToProject,
		projectRef,
		issueURL,
	)

	args := []string{"api", "graphql", "--input", "-"}
	if g.repositorio != "" {
		args = append(args, "--repo", g.repositorio)
	}

	_, err := g.ejecutarConEntrada(ctx, payload, args...)
	return err
}

// ejecutar invoca el binario y devuelve su salida estandar.
func (g *GitHubCLI) ejecutar(ctx context.Context, args ...string) (string, error) {
	return g.ejecutarConEntrada(ctx, "", args...)
}

// ejecutarConEntrada invoca el binario, escribiendo stdin si se indica.
//
// El contexto se propaga al proceso, de modo que cancelar la operacion mata el
// proceso hijo en vez de dejarlo huérfano consumiendo CPU.
func (g *GitHubCLI) ejecutarConEntrada(ctx context.Context, entrada string, args ...string) (string, error) {
	contextoTimeout, cancelar := context.WithTimeout(ctx, g.timeout)
	defer cancelar()

	cmd := exec.CommandContext(contextoTimeout, g.ejecutable, args...)

	if entrada != "" {
		cmd.Stdin = strings.NewReader(entrada)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// El stderr de `gh` es la parte util del error: dice si el fallo fue de
		// autenticacion, de permisos o de la peticion.
		mensaje := strings.TrimSpace(stderr.String())
		if mensaje == "" {
			mensaje = err.Error()
		}

		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", fmt.Errorf("%w: %s", ErrGraphQL, recortar([]byte(mensaje)))
		}

		return "", fmt.Errorf("%w: %s", ErrGraphQL, recortar([]byte(mensaje)))
	}

	return stdout.String(), nil
}

// asercion de compilacion del puerto.
var _ domain.ProjectBoardAdapter = (*GitHubCLI)(nil)
