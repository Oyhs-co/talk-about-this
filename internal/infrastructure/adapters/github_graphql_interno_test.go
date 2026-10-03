package adapters

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Estos tests viven dentro del paquete (y no en adapters_test) porque prueban
// funciones NO exportadas que son la parte delicada del adaptador:
//
//   - consultarProjectID: la resolucion del node_id del tablero.
//   - resolverNodeID y cacheIDs: el lock por clave que evita el cache stampede.
//
// Probar solo lo exportado no los alcanzaria: publicar con un ProjectRef ya
// resuelto nunca entra en la rama que consulta el tablero.

type peticionDeTest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

// servidorDeProjects imita la consulta de projectV2 y cuenta las peticiones por
// operacion.
func servidorDeProjects(t *testing.T, projectID string) (*httptest.Server, func(string) int) {
	t.Helper()

	var mu sync.Mutex
	conteo := map[string]int{}

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		var p peticionDeTest
		if err := json.Unmarshal(body, &p); err != nil {
			http.Error(w, "peticion invalida", http.StatusBadRequest)
			return
		}

		operacion := "otra"
		switch {
		case strings.Contains(p.Query, "projectV2"):
			operacion = "projectV2"
		case strings.Contains(p.Query, "repository("):
			operacion = "repository"
		case strings.Contains(p.Query, "user("):
			operacion = "user"
		}

		mu.Lock()
		conteo[operacion]++
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")

		var datos string
		switch operacion {
		case "projectV2":
			datos = `{"data":{"organization":{"projectV2":{"id":"` + projectID + `"}}}}`
		default:
			datos = `{"data":{}}`
		}

		_, _ = io.WriteString(w, datos)
	}))

	t.Cleanup(s.Close)

	return s, func(operacion string) int {
		mu.Lock()
		defer mu.Unlock()
		return conteo[operacion]
	}
}

// adaptadorContra construye un adaptador apuntando al servidor dado.
//
// Un servidor nil es legitimo para los tests de cache: resolverNodeID recibe el
// resolutor por parametro, asi que no hace falta ninguna red para ejercitarlo.
func adaptadorContra(s *httptest.Server) *GitHubGraphQL {
	opciones := GitHubGraphQLOpciones{
		Token:       "ghp_token_de_prueba",
		Owner:       "organizacion",
		Repositorio: "repo",
	}

	if s != nil {
		opciones.Endpoint = s.URL
		opciones.HTTP = s.Client()
	}

	return NuevoGitHubGraphQL(opciones)
}

// TestConsultarProjectIDAceptaNodeIDSinIrALaRed documenta la optimizacion.
//
// Un node_id no se puede "resolver": ya ES el identificador. Mandarlo a GitHub
// seria una consulta inutil que ademas consume cuota de la API.
func TestConsultarProjectIDAceptaNodeIDSinIrALaRed(t *testing.T) {
	s, conteo := servidorDeProjects(t, "PVT_1")

	g := adaptadorContra(s)

	obtenido, err := g.consultarProjectID(context.Background(), "PVT_123456")
	if err != nil {
		t.Fatalf("un node_id deberia aceptarse sin error: %v", err)
	}

	if obtenido != "PVT_123456" {
		t.Errorf("se obtuvo %q, se esperaba el node_id sin modificar", obtenido)
	}

	if peticiones := conteo("projectV2"); peticiones != 0 {
		t.Errorf("se hicieron %d consultas al tablero; un node_id no debe generar trafico", peticiones)
	}
}

// TestConsultarProjectIDResuelveNumeroDeTablero cubre la rama util: el usuario
// pasa "7" y el adaptador obtiene el node_id opaco.
func TestConsultarProjectIDResuelveNumeroDeTablero(t *testing.T) {
	s, conteo := servidorDeProjects(t, "PVT_kwDOABCDEF")

	g := adaptadorContra(s)

	obtenido, err := g.consultarProjectID(context.Background(), "7")
	if err != nil {
		t.Fatalf("no se pudo resolver el tablero: %v", err)
	}

	if obtenido != "PVT_kwDOABCDEF" {
		t.Errorf("se obtuvo %q, se esperaba PVT_kwDOABCDEF", obtenido)
	}

	if peticiones := conteo("projectV2"); peticiones != 1 {
		t.Errorf("se hicieron %d consultas, se esperaba 1", peticiones)
	}
}

// TestConsultarProjectIDToleraEspaciosAlrededor documenta que " 7 " funciona.
//
// Viene de una variable de entorno o de un fichero de configuracion, donde los
// espacios sobrantes son un clasico.
func TestConsultarProjectIDToleraEspaciosAlrededor(t *testing.T) {
	s, _ := servidorDeProjects(t, "PVT_1")

	g := adaptadorContra(s)

	if _, err := g.consultarProjectID(context.Background(), "  7  "); err != nil {
		t.Errorf("los espacios alrededor del numero no deberian ser un error: %v", err)
	}
}

// TestConsultarProjectIDRechazaReferenciasInvalidas comprueba que una entrada
// mal formada falla con un mensaje que dice QUE se esperaba.
func TestConsultarProjectIDRechazaReferenciasInvalidas(t *testing.T) {
	s, conteo := servidorDeProjects(t, "PVT_1")

	g := adaptadorContra(s)

	casos := []string{"", "   ", "abc", "PVT", "12abc"}

	for _, ref := range casos {
		t.Run("ref="+ref, func(t *testing.T) {
			_, err := g.consultarProjectID(context.Background(), ref)

			if err == nil {
				t.Fatalf("se esperaba un error para la referencia %q", ref)
			}

			if !strings.Contains(err.Error(), "node_id") {
				t.Errorf("el mensaje %q deberia explicar que se espera un node_id o un numero", err)
			}
		})
	}

	if peticiones := conteo("projectV2"); peticiones != 0 {
		t.Errorf("se hicieron %d consultas para referencias invalidas; deberian fallar antes de la red",
			peticiones)
	}
}

// TestConsultarProjectIDFallaSiElTableroNoExiste cubre el 404 silencioso de la
// API: GitHub responde con data vacia y status 200 cuando el tablero no existe o
// no es visible para el token.
//
// Sin esta comprobacion, el adaptador devolveria un node_id vacio y el fallo
// apareceria mas tarde, como un error de mutacion desconectado de su causa.
func TestConsultarProjectIDFallaSiElTableroNoExiste(t *testing.T) {
	s, _ := servidorDeProjects(t, "")

	g := adaptadorContra(s)

	_, err := g.consultarProjectID(context.Background(), "99")

	if err == nil {
		t.Fatal("se esperaba un error cuando el tablero no existe")
	}

	if !strings.Contains(err.Error(), "99") {
		t.Errorf("el mensaje %q deberia indicar que tablero se busco", err)
	}
}

// TestConsultarProjectIDRespetaContextoCancelado verifica que un contexto ya
// cancelado no genera trafico.
func TestConsultarProjectIDRespetaContextoCancelado(t *testing.T) {
	s, conteo := servidorDeProjects(t, "PVT_1")

	g := adaptadorContra(s)

	ctx, cancelar := context.WithCancel(context.Background())
	cancelar()

	if _, err := g.consultarProjectID(ctx, "7"); err == nil {
		t.Error("se esperaba un error de contexto cancelado")
	}

	if peticiones := conteo("projectV2"); peticiones != 0 {
		t.Errorf("se hicieron %d peticiones con el contexto ya cancelado", peticiones)
	}
}

// TestResolverNodeIDEvitaElStampede comprueba que N resolvers simultaneos de la
// misma clave provocan UNA sola consulta.
//
// Este es el bug que la combinacion de lock por clave y doble comprobacion
// evita. Con un mutex global, o con una simple comprobacion sin lock, las
// goroutines pasan todas la comprobacion antes de que ninguna escriba y se
// lanzan N consultas identicas a la API.
func TestResolverNodeIDEvitaElStampede(t *testing.T) {
	const goroutines = 32

	g := adaptadorContra(nil)

	var (
		mu        sync.Mutex
		resueltas int
	)

	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			valor, err := g.resolverNodeID(context.Background(), "repositorio:org/repo",
				func(context.Context) (string, error) {
					mu.Lock()
					resueltas++
					mu.Unlock()

					return "R_kgDO", nil
				})
			if err != nil {
				t.Errorf("resolverNodeID fallo: %v", err)
				return
			}

			if valor != "R_kgDO" {
				t.Errorf("valor = %q, se esperaba R_kgDO", valor)
			}
		}()
	}

	wg.Wait()

	mu.Lock()
	total := resueltas
	mu.Unlock()

	if total != 1 {
		t.Errorf("se ejecuto el resolutor %d veces, se esperaba 1 (cache stampede)", total)
	}
}

// TestResolverNodeIDSirveDesdeCacheSinVolverALaRed comprueba que la segunda
// llamada no invoca al resolutor.
func TestResolverNodeIDSirveDesdeCacheSinVolverALaRed(t *testing.T) {
	g := adaptadorContra(nil)

	llamadas := 0

	resolver := func(context.Context) (string, error) {
		llamadas++
		return "PVT_1", nil
	}

	for i := 0; i < 5; i++ {
		valor, err := g.resolverNodeID(context.Background(), "tablero", resolver)
		if err != nil {
			t.Fatalf("llamada %d: %v", i, err)
		}
		if valor != "PVT_1" {
			t.Errorf("llamada %d: valor = %q", i, valor)
		}
	}

	if llamadas != 1 {
		t.Errorf("el resolutor se ejecuto %d veces, se esperaba 1", llamadas)
	}
}

// TestResolverNodeIDPropagaElErrorYNoLoCachea comprueba que un fallo no se
// memoriza.
//
// Cachear un error convertiria un fallo transitorio de red en un fallo
// permanente durante toda la vida del proceso: el usuario tendria que relanzar
// la herramienta para volver a intentarlo.
func TestResolverNodeIDPropagaElErrorYNoLoCachea(t *testing.T) {
	g := adaptadorContra(nil)

	intentos := 0

	fallo := func(context.Context) (string, error) {
		intentos++
		return "", context.DeadlineExceeded
	}

	for i := 0; i < 3; i++ {
		if _, err := g.resolverNodeID(context.Background(), "clave", fallo); err == nil {
			t.Fatalf("intento %d: se esperaba error", i)
		}
	}

	if intentos != 3 {
		t.Errorf("el resolutor se ejecuto %d veces, se esperaban 3: el error no debe cachearse", intentos)
	}

	// Tras el fallo, un intento correcto debe funcionar y cachearse.
	ok := 0

	exito := func(context.Context) (string, error) {
		ok++
		return "valor", nil
	}

	for i := 0; i < 3; i++ {
		valor, err := g.resolverNodeID(context.Background(), "clave", exito)
		if err != nil {
			t.Fatalf("intento %d tras el fallo: %v", i, err)
		}
		if valor != "valor" {
			t.Errorf("valor = %q, se esperaba valor", valor)
		}
	}

	if ok != 1 {
		t.Errorf("el resolutor correcto se ejecuto %d veces, se esperaba 1", ok)
	}
}

// TestCacheIDsSeparaLasClaves comprueba que el lock por clave no serializa todo.
//
// Resolver "usuario X" no debe bloquear la resolucion del repositorio: un
// mutex global seria correcto, pero ralentizaria cada item.
func TestCacheIDsSeparaLasClaves(t *testing.T) {
	c := nuevaCacheIDs()

	if v, ok := c.obtener("a"); ok || v != "" {
		t.Errorf("una clave ausente devolvio %q, %v", v, ok)
	}

	c.guardar("a", "A")
	c.guardar("b", "B")

	if v, ok := c.obtener("a"); !ok || v != "A" {
		t.Errorf("clave a = %q, %v", v, ok)
	}

	if v, ok := c.obtener("b"); !ok || v != "B" {
		t.Errorf("clave b = %q, %v", v, ok)
	}

	if len(c.valores) != 2 {
		t.Errorf("la cache guarda %d entradas, se esperaban 2", len(c.valores))
	}
}

// TestCacheIDsIgnoraValoresVacios documenta que un id vacio no se considera
// cacheado.
//
// Cachear "" haria que todos los items siguientes creyeran tener el id y no
// consultaran nunca, reproduciendo el fallo que la cache pretendia evitar.
func TestCacheIDsIgnoraValoresVacios(t *testing.T) {
	c := nuevaCacheIDs()

	c.guardar("vacia", "")

	if v, ok := c.obtener("vacia"); ok {
		t.Errorf("un valor vacio no debe considerarse cacheado, devolvio %q", v)
	}
}
