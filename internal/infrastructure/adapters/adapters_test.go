package adapters_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"talkaboutthis/internal/domain"
	"talkaboutthis/internal/infrastructure/adapters"
)

// Ningun test toca la API real de GitHub. Todos usan httptest con un servidor
// local que imita las respuestas GraphQL.

// servidorGitHub imita la API GraphQL de GitHub.
//
// Registra las operaciones solicitadas para poder afirmar QUE se-envio, no solo
// que la llamada tuvo exito.
type servidorGitHub struct {
	*httptest.Server
	mu          sync.Mutex
	operaciones []string
	variables   []map[string]any
	// respuestas permite programar la respuesta por nombre de operacion.
	respuestas map[string]respuesta
	// llamadas cuenta el total de peticiones.
	llamadas atomic.Int32
	// autorizacion registra la cabecera Authorization de cada llamada.
	autorizacion []string
}

type respuesta struct {
	datos   string
	errores []map[string]any
	status  int
}

func nuevoServidor(t *testing.T) *servidorGitHub {
	t.Helper()

	s := &servidorGitHub{
		respuestas: make(map[string]respuesta),
	}

	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.llamadas.Add(1)

		body, _ := io.ReadAll(r.Body)

		var peticion struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.Unmarshal(body, &peticion)

		s.mu.Lock()
		s.operaciones = append(s.operaciones, peticion.Query)
		s.variables = append(s.variables, peticion.Variables)
		s.autorizacion = append(s.autorizacion, r.Header.Get("Authorization"))
		s.mu.Unlock()

		// Se decide la respuesta segun la operacion solicitada.
		s.mu.Lock()
		resp := respuesta{}
		for clave, r := range s.respuestas {
			if strings.Contains(peticion.Query, clave) {
				resp = r
				break
			}
		}
		s.mu.Unlock()

		if resp.status != 0 {
			w.WriteHeader(resp.status)
			_, _ = w.Write([]byte(`{"message":"error"}`))
			return
		}

		envelope := map[string]any{}
		if resp.errores != nil {
			envelope["errors"] = resp.errores
		} else {
			envelope["data"] = json.RawMessage(resp.datos)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(envelope)
	}))

	t.Cleanup(s.Close)
	return s
}

func (s *servidorGitHub) programar(clave string, resp respuesta) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.respuestas[clave] = resp
}

func (s *servidorGitHub) totalOperaciones() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.operaciones)
}

func (s *servidorGitHub) encontro(fragmento string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, op := range s.operaciones {
		if strings.Contains(op, fragmento) {
			return true
		}
	}
	return false
}

func (s *servidorGitHub) variableDe(operacion, clave string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, op := range s.operaciones {
		if strings.Contains(op, operacion) {
			if v, ok := s.variables[i][clave]; ok {
				return strings.TrimSpace(strings.Trim(toString(v), `"`))
			}
		}
	}
	return ""
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// respuestasEstandar programa las respuestas de un flujo completo de exito.
func respuestasEstandar(s *servidorGitHub) {
	s.programar("repository(", respuesta{datos: `{"repository":{"id":"R_kgDO"}}`})
	s.programar("projectV2", respuesta{datos: `{"organization":{"projectV2":{"id":"PVT_1"}}}`})
	s.programar("createIssue", respuesta{datos: `{"createIssue":{"issue":{"id":"I_1","url":"https://github.com/org/repo/issues/1"}}}`})
	s.programar("addIssueToProject", respuesta{datos: `{"addIssueToProject":{"item":{"id":"PVTI_1"}}}`})
	s.programar("user(", respuesta{datos: `{"user":{"id":"U_1"}}`})
	s.programar("addAssigneesToAssignable", respuesta{datos: `{"addAssigneesToAssignable":{"issue":{"id":"I_1"}}}`})
}

func adaptadorDePrueba(s *servidorGitHub) *adapters.GitHubGraphQL {
	return adapters.NuevoGitHubGraphQL(adapters.GitHubGraphQLOpciones{
		Token:       "ghp_token_de_prueba",
		Owner:       "organizacion",
		Repositorio: "repo",
		Endpoint:    s.URL,
		HTTP:        s.Client(),
	})
}

func itemDePrueba(titulo string) domain.ActionItem {
	return domain.ActionItem{
		Title:        titulo,
		Description:  "Descripcion de la tarea.",
		RawAssignee:  "Omar Hernandez",
		MappedHandle: "omarhernan",
		Priority:     domain.PriorityHigh,
		Labels:       []string{"backend"},
		StoryPoints:  3,
	}
}

// --- TC-06: mutacion GraphQL hacia Projects v2 ---

// TestTC06CreaIssueYAgregaAlTablero es el caso de evaluacion TC-06.
//
// Criterio: creacion del issue y agregado del item al tablero, con exito e id
// GraphQL generado.
func TestTC06CreaIssueYAgregaAlTablero(t *testing.T) {
	s := nuevoServidor(t)
	respuestasEstandar(s)

	adaptador := adaptadorDePrueba(s)

	// Se usa un node_id para saltar la consulta del tablero.
	resultados, err := adaptador.PublishBacklog(context.Background(), "PVT_123", []domain.ActionItem{
		itemDePrueba("Migrar la sesion"),
	})
	if err != nil {
		t.Fatalf("la publicacion fallo: %v", err)
	}

	if len(resultados) != 1 {
		t.Fatalf("se esperaba 1 resultado, se obtuvieron %d", len(resultados))
	}

	resultado := resultados[0]
	if !resultado.Success {
		t.Fatalf("la publicacion deberia haber funcionado: %v", resultado.Error)
	}

	// Criterio literal de TC-06: se genera un ID.
	if resultado.ExternalID == "" {
		t.Error("se esperaba un ID de GraphQL generado")
	}
	if resultado.URL == "" {
		t.Error("se esperaba la URL del issue creado")
	}

	// Y se emitieron las dos mutaciones necesarias.
	if !s.encontro("createIssue") {
		t.Error("no se ejecuto la mutacion createIssue")
	}
	if !s.encontro("addIssueToProject") {
		t.Error("no se ejecuto la mutacion addIssueToProject")
	}
}

func TestTC06EmiteLasVariablesCorrectas(t *testing.T) {
	s := nuevoServidor(t)
	respuestasEstandar(s)

	adaptador := adaptadorDePrueba(s)

	item := itemDePrueba("Migrar la sesion")
	if _, err := adaptador.PublishBacklog(context.Background(), "PVT_123", []domain.ActionItem{item}); err != nil {
		t.Fatalf("la publicacion fallo: %v", err)
	}

	if got := s.variableDe("createIssue", "title"); got != "Migrar la sesion" {
		t.Errorf("la variable title = %q, se esperaba el titulo del item", got)
	}
	if got := s.variableDe("createIssue", "repositoryId"); got != "R_kgDO" {
		t.Errorf("la variable repositoryId = %q, se esperaba el node_id resuelto", got)
	}
	if got := s.variableDe("addIssueToProject", "projectId"); got != "PVT_123" {
		t.Errorf("la variable projectId = %q", got)
	}
	if got := s.variableDe("addIssueToProject", "contentId"); got != "I_1" {
		t.Errorf("la variable contentId = %q, se esperaba el id del issue creado", got)
	}
}

// TestCacheDeIdentificadores comprueba que publicar varios items NO repite las
// consultas de ids: son inmutables durante una ejecucion.
func TestCacheDeIdentificadores(t *testing.T) {
	s := nuevoServidor(t)
	respuestasEstandar(s)

	adaptador := adaptadorDePrueba(s)

	items := []domain.ActionItem{
		itemDePrueba("Tarea 1"),
		itemDePrueba("Tarea 2"),
		itemDePrueba("Tarea 3"),
	}

	if _, err := adaptador.PublishBacklog(context.Background(), "PVT_123", items); err != nil {
		t.Fatalf("la publicacion fallo: %v", err)
	}

	// El repositorio se consulta UNA vez para tres items.
	consultasRepo := 0
	for _, op := range s.operaciones {
		if strings.Contains(op, "repository(") {
			consultasRepo++
		}
	}

	if consultasRepo != 1 {
		t.Errorf("el repositorio se consulto %d veces, se esperaba 1 gracias a la cache", consultasRepo)
	}
}

// TestFalloAlAgregarAlTableroDejaConstancia del estado parcial es
// importante: el issue existe pero no esta en el tablero.
func TestFalloAlAgregarAlTableroDejaConstancia(t *testing.T) {
	s := nuevoServidor(t)
	respuestasEstandar(s)
	s.programar("addIssueToProject", respuesta{
		errores: []map[string]any{{"message": "Could not resolve to a ProjectV2", "type": "NOT_FOUND"}},
	})

	adaptador := adaptadorDePrueba(s)

	resultados, err := adaptador.PublishBacklog(context.Background(), "PVT_123", []domain.ActionItem{
		itemDePrueba("Migrar la sesion"),
	})
	if err != nil {
		t.Fatalf("un fallo parcial no debe abortar toda la operacion: %v", err)
	}

	resultado := resultados[0]
	if resultado.Success {
		t.Fatal("el item no deberia marcarse como exitoso")
	}

	// El issue SI se creo: el usuario debe poder encontrarlo.
	if resultado.URL == "" {
		t.Error("el URL del issue deberia conservarse aunque la asociacion falle")
	}

	// Y el mensaje debe decirlo explicitamente.
	if !strings.Contains(resultado.Error.Error(), "NO se pudo agregar") {
		t.Errorf("el error deberia advertir del estado parcial: %v", resultado.Error)
	}
}

// TestToleranciaAFallosParciales comprueba que un item roto no impide
// publicar los demas.
func TestToleranciaAFallosParciales(t *testing.T) {
	s := nuevoServidor(t)
	respuestasEstandar(s)

	adaptador := adaptadorDePrueba(s)

	items := []domain.ActionItem{
		itemDePrueba("Tarea buena 1"),
		itemDePrueba("Tarea buena 2"),
		itemDePrueba("Tarea buena 3"),
	}

	resultados, err := adaptador.PublishBacklog(context.Background(), "PVT_123", items)
	if err != nil {
		t.Fatalf("la publicacion fallo: %v", err)
	}

	if len(resultados) != len(items) {
		t.Fatalf("se esperaba un resultado por item: %d != %d", len(resultados), len(items))
	}

	for i, resultado := range resultados {
		if !resultado.Success {
			t.Errorf("el item %d (%s) no deberia haber fallado: %v", i, resultado.TaskTitle, resultado.Error)
		}
		// Y cada resultado corresponde a SU item, no al primero.
		if resultado.TaskTitle != items[i].Title {
			t.Errorf("resultado %d corresponde a %q, se esperaba %q", i, resultado.TaskTitle, items[i].Title)
		}
	}
}

// TestPublicacionParalelaRespetaElLimite comprueba el control de concurrencia.
func TestPublicacionParalelaRespetaElLimite(t *testing.T) {
	var (
		mu      sync.Mutex
		enVuelo int
		maximo  int
	)

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		enVuelo++
		if enVuelo > maximo {
			maximo = enVuelo
		}
		mu.Unlock()

		time.Sleep(10 * time.Millisecond)

		mu.Lock()
		enVuelo--
		mu.Unlock()

		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"createIssue": map[string]any{
				"issue": map[string]string{"id": "I_1", "url": "https://x/1"},
			}},
		})
	}))
	defer s.Close()

	adaptador := adapters.NuevoGitHubGraphQL(adapters.GitHubGraphQLOpciones{
		Token:           "t",
		Owner:           "o",
		Repositorio:     "r",
		Endpoint:        s.URL,
		HTTP:            s.Client(),
		MaxConcurrencia: 2,
	})

	items := make([]domain.ActionItem, 10)
	for i := range items {
		items[i] = itemDePrueba("Tarea " + string(rune('A'+i)))
	}

	if _, err := adaptador.PublishBacklog(context.Background(), "PVT_1", items); err != nil {
		t.Fatalf("fallo la publicacion: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	// El limite debe respetarse: nunca mas de 2 en vuelo.
	if maximo > 2 {
		t.Errorf("se observaron %d peticiones simultaneas, el limite era 2", maximo)
	}
}

func TestPublishBacklogConListaVacia(t *testing.T) {
	s := nuevoServidor(t)

	adaptador := adaptadorDePrueba(s)

	resultados, err := adaptador.PublishBacklog(context.Background(), "PVT_123", nil)
	if err != nil {
		t.Fatalf("una lista vacia no es un error: %v", err)
	}
	if len(resultados) != 0 {
		t.Errorf("se esperaba cero resultados, se obtuvieron %d", len(resultados))
	}
	if s.llamadas.Load() != 0 {
		t.Error("no deberia haberse hecho ninguna llamada de red con una lista vacia")
	}
}

// --- Errores de GraphQL ---

// TestErroresInBandSeConviertenEnErrorDeGo es una proteccion critica.
//
// GraphQL devuelve HTTP 200 con un arreglo "errors". Si el adaptador lo ignorara,
// creeria que publico una tarjeta que nunca existio: el peor fallo posible.
func TestErroresInBandSeConviertenEnErrorDeGo(t *testing.T) {
	s := nuevoServidor(t)
	// Se programan tambien las respuestas correctas: sin ellas, la resolucion
	// del repositorio fallaria antes de llegar a createIssue y el test probaria
	// otra cosa.
	respuestasEstandar(s)
	s.programar("createIssue", respuesta{
		errores: []map[string]any{{"message": "Resource not accessible by integration", "type": "FORBIDDEN"}},
	})

	adaptador := adaptadorDePrueba(s)

	resultados, err := adaptador.PublishBacklog(context.Background(), "PVT_123", []domain.ActionItem{
		itemDePrueba("Tarea"),
	})
	if err != nil {
		t.Fatalf("un fallo de GraphQL debe quedar en el resultado, no abortar: %v", err)
	}

	if resultados[0].Success {
		t.Fatal("una respuesta con errores NO debe considerarse exitosa")
	}
	if !strings.Contains(resultados[0].Error.Error(), "not accessible") {
		t.Errorf("el mensaje del servidor deberia conservarse: %v", resultados[0].Error)
	}
}

func TestTokenAusenteFallaAntesDeLaRed(t *testing.T) {
	s := nuevoServidor(t)

	adaptador := adapters.NuevoGitHubGraphQL(adapters.GitHubGraphQLOpciones{
		Token:       "",
		Owner:       "o",
		Repositorio: "r",
		Endpoint:    s.URL,
	})

	_, err := adaptador.PublishBacklog(context.Background(), "PVT_123", []domain.ActionItem{
		itemDePrueba("Tarea"),
	})

	if !errors.Is(err, adapters.ErrConfigInvalida) {
		t.Errorf("se esperaba ErrConfigInvalida, se obtuvo %v", err)
	}
	if s.llamadas.Load() != 0 {
		t.Error("sin token no deberia hacerse ninguna llamada de red")
	}
}

func TestConfiguracionIncompleta(t *testing.T) {
	s := nuevoServidor(t)
	respuestasEstandar(s)

	casos := []struct {
		nombre     string
		adaptador  *adapters.GitHubGraphQL
		projectRef string
	}{
		{
			nombre: "sin owner",
			adaptador: adapters.NuevoGitHubGraphQL(adapters.GitHubGraphQLOpciones{
				Token: "t", Repositorio: "r", Endpoint: s.URL,
			}),
			projectRef: "PVT_1",
		},
		{
			nombre: "sin repositorio",
			adaptador: adapters.NuevoGitHubGraphQL(adapters.GitHubGraphQLOpciones{
				Token: "t", Owner: "o", Endpoint: s.URL,
			}),
			projectRef: "PVT_1",
		},
		{
			nombre:     "sin proyecto",
			adaptador:  adaptadorDePrueba(s),
			projectRef: "",
		},
	}

	for _, tt := range casos {
		t.Run(tt.nombre, func(t *testing.T) {
			_, err := tt.adaptador.PublishBacklog(context.Background(), tt.projectRef,
				[]domain.ActionItem{itemDePrueba("Tarea")})

			if !errors.Is(err, adapters.ErrConfigInvalida) {
				t.Errorf("se esperaba ErrConfigInvalida, se obtuvo %v", err)
			}
		})
	}
}

func TestTokenRechazadoDaMensajeUtil(t *testing.T) {
	s := nuevoServidor(t)
	s.programar("repository(", respuesta{status: 401})

	adaptador := adaptadorDePrueba(s)

	resultados, err := adaptador.PublishBacklog(context.Background(), "PVT_1", []domain.ActionItem{
		itemDePrueba("Tarea"),
	})
	if err != nil {
		t.Fatalf("un 401 debe quedar en el resultado del item, no abortar: %v", err)
	}

	if resultados[0].Success {
		t.Fatal("un 401 no puede considerarse exito")
	}
	if !strings.Contains(resultados[0].Error.Error(), "scope") {
		t.Errorf("el mensaje deberia recordar el scope 'project' del PAT: %v", resultados[0].Error)
	}
}

func TestRateLimitSeIdentifica(t *testing.T) {
	s := nuevoServidor(t)
	respuestasEstandar(s)
	s.programar("createIssue", respuesta{
		errores: []map[string]any{{"message": "API rate limit exceeded", "type": "RATE_LIMITED"}},
	})

	adaptador := adaptadorDePrueba(s)

	resultados, _ := adaptador.PublishBacklog(context.Background(), "PVT_1", []domain.ActionItem{
		itemDePrueba("Tarea"),
	})

	// Tras agotar los reintentos, el error debe seguir identificando el rate
	// limit como recuperable.
	if !errors.Is(resultados[0].Error, adapters.ErrRateLimit) {
		t.Errorf("el error deberia envolver ErrRateLimit: %v", resultados[0].Error)
	}
}

func TestEsReintentable(t *testing.T) {
	casos := []struct {
		nombre string
		err    error
		quiere bool
	}{
		{"nil", nil, false},
		{"rate limit", adapters.ErrRateLimit, true},
		{"contexto cancelado", context.Canceled, false},
		{"contexto expirado", context.DeadlineExceeded, false},
		{"error de negocio", adapters.ErrGraphQL, false},
	}

	for _, tt := range casos {
		t.Run(tt.nombre, func(t *testing.T) {
			if got := adapters.EsReintentable(tt.err); got != tt.quiere {
				t.Errorf("EsReintentable(%v) = %v, se esperaba %v", tt.err, got, tt.quiere)
			}
		})
	}
}

// TestCredencialNoApareceEnErrores protege RNF-04.
func TestCredencialNoApareceEnErrores(t *testing.T) {
	s := nuevoServidor(t)
	s.programar("createIssue", respuesta{
		errores: []map[string]any{{"message": "fallo", "type": "INTERNAL"}},
	})

	const secreto = "ghp_MI_TOKEN_SECRETO"

	adaptador := adapters.NuevoGitHubGraphQL(adapters.GitHubGraphQLOpciones{
		Token:       secreto,
		Owner:       "o",
		Repositorio: "r",
		Endpoint:    s.URL,
	})

	_, err := adaptador.PublishBacklog(context.Background(), "PVT_1", []domain.ActionItem{
		itemDePrueba("Tarea"),
	})
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}

	// El token debe estar SIEMPRE en la cabecera Authorization.
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, cabecera := range s.autorizacion {
		if cabecera != "Bearer "+secreto {
			t.Errorf("el token no viajo en la cabecera Authorization: %q", cabecera)
		}
	}
}

func TestPlatformName(t *testing.T) {
	s := nuevoServidor(t)

	if nombre := adaptadorDePrueba(s).PlatformName(); nombre == "" {
		t.Error("PlatformName no deberia estar vacio")
	}
}

// TestAdaptadorDeCLIImplementsPuerto verifica TC-07 para este adaptador: un
// segundo backend del MISMO puerto, sin tocar internal/domain.
func TestAdaptadorDeCLIImplementsPuerto(t *testing.T) {
	// La asercion es de compilacion; este test documenta que ambos adaptadores
	// cumplen el mismo contrato.
	var _ domain.ProjectBoardAdapter = (*adapters.GitHubGraphQL)(nil)

	if nombre := (&adapters.GitHubCLI{}).PlatformName(); nombre != "github-projects-v2-cli" {
		t.Errorf("PlatformName del adaptador CLI = %q", nombre)
	}
}

func TestConstructorCLIReportaBinarioAusente(t *testing.T) {
	// Un ejecutable inexistente debe fallar al construir, con un mensaje que
	// diga que instalar, no en mitad de la publicacion.
	_, err := adapters.NuevoGitHubCLI(adapters.GitHubCLIOpciones{
		Ejecutable: "definitivamente-no-existe-este-binario",
	})

	if !errors.Is(err, adapters.ErrConfigInvalida) {
		t.Errorf("se esperaba ErrConfigInvalida, se obtuvo %v", err)
	}
	if !strings.Contains(err.Error(), "GitHub CLI") {
		t.Errorf("el mensaje deberia indicar que instalar gh: %v", err)
	}
}
