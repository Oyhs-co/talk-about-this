package adapters

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"talkaboutthis/internal/domain"
)

// GitHubGraphQL publica el backlog en un GitHub Project (v2) via GraphQL.
//
// Implementa domain.ProjectBoardAdapter (RF-05).
//
// # SECUENCIA DE PUBLICACION
//
// Crear un issue y meterlo en un tablero son dos operaciones distintas, porque
// en Projects v2 el tablero no contiene issues: contiene ITEMS, y un item es la
// vinculacion entre un contenido (el issue) y el tablero. Por eso:
//
//  1. addIssueToProject  -> crea el issue en el repositorio.
//  2. addIssueToProject  -> asocia ese issue al tablero.
//
// Si la segunda falla, el issue existe pero no aparece en el tablero. Es un
// estado parcial real, y el adaptador lo refleja en el PublishResult en lugar
// de ocultarlo: el usuario necesita saber que hay un issue suelto.
type GitHubGraphQL struct {
	cliente     *ClienteGraphQL
	owner       string
	repositorio string

	// mu protege la cache de identificadores.
	mu sync.Mutex
	// maxConcurrency limita las publicaciones simultaneas.
	maxConc int

	// cache de IDs ya resueltos, para no repetir consultas al publicar N items.
	//
	// Publicar 20 items sin cache haria 20 busquedas del mismo node_id.
	cache *cacheIDs
}

// cacheIDs guarda identificadores ya resueltos, con un lock por clave.
//
// El lock por clave no es un detalle menor: sin el, los items que se publican en
// paralelo consultan TODOS el node_id del repositorio a la vez (cache
// stampede), que es justo lo que la cache debe evitar. Con el lock, el primero
// resuelve y los demas esperan y leen el valor ya cacheado.
//
// Se evita un mutex global porque resolver "usuario X" no debe bloquear la
// resolucion del repositorio.
type cacheIDs struct {
	mu      sync.Mutex
	valores map[string]string
	locks   map[string]*sync.Mutex
}

func nuevaCacheIDs() *cacheIDs {
	return &cacheIDs{
		valores: make(map[string]string),
		locks:   make(map[string]*sync.Mutex),
	}
}

// lockDe devuelve el mutex de una clave, creandolo si hace falta.
func (c *cacheIDs) lockDe(clave string) *sync.Mutex {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.locks[clave] == nil {
		c.locks[clave] = &sync.Mutex{}
	}

	return c.locks[clave]
}

func (c *cacheIDs) obtener(clave string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	valor, existe := c.valores[clave]
	return valor, existe && valor != ""
}

func (c *cacheIDs) guardar(clave, valor string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.valores[clave] = valor
}

// GitHubGraphQLOpciones configura el adaptador.
type GitHubGraphQLOpciones struct {
	// Token es el PAT con scope "project".
	Token string
	// Owner es la organizacion o el usuario dueno del repositorio.
	Owner string
	// Repositorio es el repositorio donde se crean los issues.
	Repositorio string
	// Endpoint permite redirigir la API (tests).
	Endpoint string
	// HTTP permite inyectar un cliente propio (tests).
	HTTP *http.Client
	// MaxConcurrencia limita los items publicados en paralelo. Si es cero se
	// usa un valor modesto.
	MaxConcurrencia int
}

// maxConcurrenciaPorDefecto limita las mutaciones simultaneas.
//
// Es un valor deliberadamente bajo: la API de GitHub limita por autenticado y
// disparar veinte mutaciones a la vez es la via rapida a un 403 por rate limit.
// Publicar veinte issues no es una operacion urgente.
const maxConcurrenciaPorDefecto = 4

// NuevoGitHubGraphQL construye el adaptador.
func NuevoGitHubGraphQL(opciones GitHubGraphQLOpciones) *GitHubGraphQL {
	concurrencia := opciones.MaxConcurrencia
	if concurrencia <= 0 {
		concurrencia = maxConcurrenciaPorDefecto
	}

	return &GitHubGraphQL{
		cliente: NuevoClienteGraphQL(ClienteGraphQLOpciones{
			Endpoint: opciones.Endpoint,
			Token:    opciones.Token,
			HTTP:     opciones.HTTP,
		}),
		owner:       opciones.Owner,
		repositorio: opciones.Repositorio,
		cache:       nuevaCacheIDs(),
		maxConc:     concurrencia,
	}
}

// PlatformName implementa domain.ProjectBoardAdapter.
func (g *GitHubGraphQL) PlatformName() string { return "github-projects-v2" }

// PublishBacklog implementa domain.ProjectBoardAdapter.
//
// # TOLERANCIA A FALLOS PARCIALES
//
// Devuelve un PublishResult por item, con exito o fallo en cada uno. Si cinco de
// seis tarjetas se crearon y la sexta fallo, abortar en el primer error dejaria
// al usuario sin saber que las otras cinco ya existen en su tablero.
//
// Los items se publican en paralelo con un limite de concurrencia. La API de
// GitHub limita por autenticado, y disparar veinte mutaciones simultaneas es la
// via rapida a un 403 por rate limit.
func (g *GitHubGraphQL) PublishBacklog(ctx context.Context, projectRef string, items []domain.ActionItem) ([]domain.PublishResult, error) {
	if len(items) == 0 {
		return []domain.PublishResult{}, nil
	}

	if strings.TrimSpace(g.owner) == "" || strings.TrimSpace(g.repositorio) == "" {
		return nil, fmt.Errorf("%w: owner y repositorio son obligatorios", ErrConfigInvalida)
	}

	if strings.TrimSpace(projectRef) == "" {
		return nil, fmt.Errorf("%w: falta el identificador del proyecto", ErrConfigInvalida)
	}

	// El token se comprueba aqui, y no por item. Una credencial ausente es un
	// error de configuracion: devolverlo como el fallo del primer item dejaria
	// los restantes resultados como "sin publicar" cuando en realidad no se
	// intento nada.
	if !g.cliente.TieneToken() {
		return nil, fmt.Errorf("%w: falta el token de GitHub (GITHUB_TOKEN)", ErrConfigInvalida)
	}

	resultados := make([]domain.PublishResult, len(items))
	sem := make(chan struct{}, g.maxConc)
	var wg sync.WaitGroup

	for i, item := range items {
		wg.Add(1)

		go func(indice int, item domain.ActionItem) {
			defer wg.Done()

			// Se toma un hueco del semaforo. Si el contexto se cancela mientras
			// se espera, el item no se publica: es preferible omitirlo a
			// seguir golpeando una API que el usuario ya cancelo.
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				resultados[indice] = domain.PublishResult{
					TaskTitle: item.Title,
					Success:   false,
					Error:     ctx.Err(),
				}
				return
			}

			resultados[indice] = g.publicarItem(ctx, projectRef, item)
		}(i, item)
	}

	wg.Wait()

	return resultados, nil
}

// publicarItem crea el issue y lo asocia al tablero.
func (g *GitHubGraphQL) publicarItem(ctx context.Context, projectRef string, item domain.ActionItem) domain.PublishResult {
	resultado := domain.PublishResult{TaskTitle: item.Title}

	// 1. Identificadores del repositorio y del tablero.
	repoID, err := g.resolverNodeID(ctx, "repo:"+g.owner+"/"+g.repositorio, func(ctx context.Context) (string, error) {
		return g.consultarRepositoryID(ctx)
	})
	if err != nil {
		resultado.Error = fmt.Errorf("no se pudo resolver el repositorio: %w", err)
		return resultado
	}

	// 2. Crear el issue.
	issueID, issueURL, err := g.crearIssue(ctx, repoID, item)
	if err != nil {
		resultado.Error = fmt.Errorf("no se pudo crear el issue: %w", err)
		return resultado
	}

	resultado.ExternalID = issueID
	resultado.URL = issueURL

	// 3. Asociar el issue al tablero.
	if err := g.agregarAlProyecto(ctx, projectRef, issueID); err != nil {
		// El issue YA existe aunque la asociacion falle. El error lo dice
		// explicitamente para que el usuario no busque una tarjeta que si esta
		// en el repositorio pero no en el tablero.
		resultado.Error = fmt.Errorf(
			"el issue %s se creo pero NO se pudo agregar al tablero: %w", issueURL, err)
		return resultado
	}

	resultado.Success = true

	slog.Info("item publicado",
		slog.String("titulo", item.Title),
		slog.String("issue_url", issueURL),
	)

	return resultado
}

// Consultas y mutaciones GraphQL.
const (
	queryRepositoryID = `
query RepositoryID($owner: String!, $name: String!) {
  repository(owner: $owner, name: $name) { id }
}`

	queryProjectID = `
query ProjectID($owner: String!, $number: Int!) {
  organization(login: $owner) { projectV2(number: $number) { id } }
}`

	mutationCreateIssue = `
mutation CreateIssue($repositoryId: ID!, $title: String!, $body: String) {
  createIssue(input: {repositoryId: $repositoryId, title: $title, body: $body}) {
    issue { id url }
  }
}`

	mutationAddIssueToProject = `
mutation AddIssueToProject($projectId: ID!, $contentId: ID!) {
  addIssueToProject(input: {projectId: $projectId, contentId: $contentId}) {
    item { id }
  }
}`

	mutationAddAssignees = `
mutation AddAssignees($issueId: ID!, $assigneeIds: [ID!]!) {
  addAssigneesToAssignable(input: {assignableId: $issueId, assigneeIds: $assigneeIds}) {
    issue { id }
  }
}`
)

// consultarRepositoryID obtiene el node_id del repositorio.
func (g *GitHubGraphQL) consultarRepositoryID(ctx context.Context) (string, error) {
	var datos struct {
		Repository struct {
			ID string `json:"id"`
		} `json:"repository"`
	}

	variables := map[string]any{
		"owner": g.owner,
		"name":  g.repositorio,
	}

	if err := g.cliente.Ejecutar(ctx, queryRepositoryID, variables, &datos); err != nil {
		return "", err
	}

	if datos.Repository.ID == "" {
		return "", fmt.Errorf("%w: el repositorio %s/%s no existe o no es accesible",
			ErrConfigInvalida, g.owner, g.repositorio)
	}

	return datos.Repository.ID, nil
}

// consultarProjectID obtiene el node_id del tablero a partir de su numero.
//
// El projectRef puede ser ya un node_id (empieza por "PVT_") o un numero de
// tablero. Aceptar ambos evita que el usuario tenga que descobrir el id opaco
// a mano.
func (g *GitHubGraphQL) consultarProjectID(ctx context.Context, projectRef string) (string, error) {
	if strings.HasPrefix(projectRef, "PVT_") {
		return projectRef, nil
	}

	numero, err := strconv.Atoi(strings.TrimSpace(projectRef))
	if err != nil {
		return "", fmt.Errorf("%w: el proyecto %q no es un node_id ni un numero valido",
			ErrConfigInvalida, projectRef)
	}

	var datos struct {
		Organization struct {
			ProjectV2 struct {
				ID string `json:"id"`
			} `json:"projectV2"`
		} `json:"organization"`
	}

	variables := map[string]any{
		"owner":  g.owner,
		"number": numero,
	}

	if err := g.cliente.Ejecutar(ctx, queryProjectID, variables, &datos); err != nil {
		return "", err
	}

	if datos.Organization.ProjectV2.ID == "" {
		return "", fmt.Errorf("%w: no se encontro el proyecto numero %d en %s",
			ErrConfigInvalida, numero, g.owner)
	}

	return datos.Organization.ProjectV2.ID, nil
}

// crearIssue crea el issue en el repositorio y devuelve su node_id y URL.
func (g *GitHubGraphQL) crearIssue(ctx context.Context, repoID string, item domain.ActionItem) (string, string, error) {
	// Se reintenta la operacion si el fallo es transitorio. Crear un issue es
	// idempotente en la practica salvo que la primera llamada hubiera creadasido
	// y la respuesta se perdiera, caso en el que GitHub deduplica por titulo
	// solo si el usuario lo pide explicitamente.
	//
	// El riesgo real de reintentar es duplicar issues, asi que solo se reintenta
	// ante rate limit, donde la mutacion no llego a ejecutarse.
	var id, url string

	err := conReintentos(ctx, func() error {
		var datos struct {
			CreateIssue struct {
				Issue struct {
					ID  string `json:"id"`
					URL string `json:"url"`
				} `json:"issue"`
			} `json:"createIssue"`
		}

		variables := map[string]any{
			"repositoryId": repoID,
			"title":        item.Title,
			"body":         construirCuerpo(item),
		}

		if err := g.cliente.Ejecutar(ctx, mutationCreateIssue, variables, &datos); err != nil {
			return err
		}

		if datos.CreateIssue.Issue.ID == "" {
			return fmt.Errorf("%w: GitHub no devolvio el id del issue creado", ErrGraphQL)
		}

		id = datos.CreateIssue.Issue.ID
		url = datos.CreateIssue.Issue.URL

		return nil
	})

	if err != nil {
		return "", "", err
	}

	// El responsable se asigna despues de crear el issue, porque addAssignees
	// necesita el node_id del issue. Un fallo aqui NO invalida la creacion: el
	// issue existe y el usuario puede asignarlo a mano.
	if item.MappedHandle != "" {
		if err := g.asignarResponsable(ctx, id, item.MappedHandle); err != nil {
			slog.Warn("el issue se creo pero no se pudo asignar el responsable",
				slog.String("issue_url", url),
				slog.String("handle", item.MappedHandle),
				slog.String("error", err.Error()),
			)
		}
	}

	return id, url, nil
}

// agregarAlProyecto asocia el issue al tablero.
func (g *GitHubGraphQL) agregarAlProyecto(ctx context.Context, projectRef, issueID string) error {
	projectID, err := g.resolverNodeID(ctx, "project:"+projectRef, func(ctx context.Context) (string, error) {
		return g.consultarProjectID(ctx, projectRef)
	})
	if err != nil {
		return err
	}

	return conReintentos(ctx, func() error {
		var datos struct {
			AddIssueToProject struct {
				Item struct {
					ID string `json:"id"`
				} `json:"item"`
			} `json:"addIssueToProject"`
		}

		variables := map[string]any{
			"projectId": projectID,
			"contentId": issueID,
		}

		if err := g.cliente.Ejecutar(ctx, mutationAddIssueToProject, variables, &datos); err != nil {
			return err
		}

		if datos.AddIssueToProject.Item.ID == "" {
			return fmt.Errorf("%w: GitHub no confirmo la asociacion al tablero", ErrGraphQL)
		}

		return nil
	})
}

// asignarResponsable intenta asignar el issue a un usuario.
func (g *GitHubGraphQL) asignarResponsable(ctx context.Context, issueID, handle string) error {
	// Resolver el node_id del usuario requiere una consulta adicional y puede
	// fallar si el usuario no existe en la organizacion. Es una mejora
	// progresiva: si falla, el issue se publica igual sin asignar y el log lo
	// explica.
	usuarioID, err := g.resolverNodeID(ctx, "user:"+handle, func(ctx context.Context) (string, error) {
		return g.consultarUserID(ctx, handle)
	})
	if err != nil {
		return err
	}

	var datos struct {
		AddAssigneesToAssignable struct {
			Issue struct {
				ID string `json:"id"`
			} `json:"issue"`
		} `json:"addAssigneesToAssignable"`
	}

	variables := map[string]any{
		"issueId":     issueID,
		"assigneeIds": []string{usuarioID},
	}

	return g.cliente.Ejecutar(ctx, mutationAddAssignees, variables, &datos)
}

// consultarUserID obtiene el node_id de un usuario por su login.
func (g *GitHubGraphQL) consultarUserID(ctx context.Context, handle string) (string, error) {
	query := `
query UserID($login: String!) {
  user(login: $login) { id }
}`

	var datos struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}

	if err := g.cliente.Ejecutar(ctx, query, map[string]any{"login": handle}, &datos); err != nil {
		return "", err
	}

	if datos.User.ID == "" {
		return "", fmt.Errorf("%w: el usuario %q no existe en GitHub", ErrConfigInvalida, handle)
	}

	return datos.User.ID, nil
}

// resolverNodeID devuelve un identificador cacheado, resolviendolo si hace falta.
//
// Los ids de repositorio, tablero y usuario no cambian durante una ejecucion, y
// sin cache cada item repetiria las mismas consultas.
func (g *GitHubGraphQL) resolverNodeID(ctx context.Context, clave string, resolver func(context.Context) (string, error)) (string, error) {
	if valor, existe := g.cache.obtener(clave); existe {
		return valor, nil
	}

	// Se serializa la resolucion de UNA clave. La consulta ocurre FUERA del lock
	// global de la cache: solo los items que necesitan el mismo id esperan, y
	// las consultas de otros ids siguen en paralelo.
	lock := g.cache.lockDe(clave)
	lock.Lock()
	defer lock.Unlock()

	// Segunda comprobacion: otro item pudo resolverlo mientras este esperaba.
	if valor, existe := g.cache.obtener(clave); existe {
		return valor, nil
	}

	valor, err := resolver(ctx)
	if err != nil {
		return "", err
	}

	g.cache.guardar(clave, valor)

	return valor, nil
}

// construirCuerpo compone el cuerpo del issue a partir del item.
//
// Incluye la prioridad, las etiquetas y la estimacion como texto visible. GitHub
// Projects puede Thereafter mapear labels a columnas, y esa informacion tiene
// que estar en el issue para que exista.
func construirCuerpo(item domain.ActionItem) string {
	var b strings.Builder

	b.WriteString(item.Description)

	b.WriteString("\n\n---\n\n")
	b.WriteString("**Prioridad:** ")
	b.WriteString(string(item.Priority))
	b.WriteString("\n")

	if len(item.Labels) > 0 {
		b.WriteString("**Etiquetas:** ")
		b.WriteString(strings.Join(item.Labels, ", "))
		b.WriteString("\n")
	}

	if item.StoryPoints > 0 {
		fmt.Fprintf(&b, "**Estimacion:** %d puntos\n", item.StoryPoints)
	}

	// Se conserva el nombre original de la minuta: permite a quien lea el issue
	// en el tablero saber de donde salio la tarea, y ayuda a depurar el mapeo.
	if item.RawAssignee != "" {
		b.WriteString("\n<!-- origen: ")
		b.WriteString(item.RawAssignee)
		b.WriteString(" -->")
	}

	return b.String()
}

// conReintentos ejecuta una operacion reintentando solo ante fallos transitorios.
//
// El intervalo crece exponencialmente y respeta la cancelacion del contexto.
func conReintentos(ctx context.Context, operacion func() error) error {
	var ultimoError error

	for intento := 0; intento < MaxIntentosGraphQL; intento++ {
		if intento > 0 {
			espera := time.Duration(1<<uint(intento-1)) * time.Second

			temporizador := time.NewTimer(espera)
			select {
			case <-ctx.Done():
				temporizador.Stop()
				return ctx.Err()
			case <-temporizador.C:
			}
		}

		ultimoError = operacion()
		if ultimoError == nil {
			return nil
		}

		if !EsReintentable(ultimoError) {
			return ultimoError
		}
	}

	return fmt.Errorf("%w tras %d intentos: %w", ErrGraphQL, MaxIntentosGraphQL, ultimoError)
}

// asercion de compilacion del puerto.
var _ domain.ProjectBoardAdapter = (*GitHubGraphQL)(nil)
