package identity_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"talkaboutthis/internal/domain"
	"talkaboutthis/internal/infrastructure/identity"
)

// tablaPrueba es una tabla de aliases con casos que aparecen en la vida real:
// acentos ausentes, abreviaturas y dos personas con el mismo nombre de pila.
const tablaPrueba = `{
  "version": "1.0",
  "defaults": {
    "default_handle": "sin-asignar",
    "unknown_assignee_policy": "fail"
  },
  "mappings": [
    {
      "raw_names": ["Omar Hernández", "Omar Hernan", "Omar Yesid Hernández Sotelo"],
      "handle": "omarhernan",
      "platforms": ["github"]
    },
    {
      "raw_names": ["Ana María Ruiz"],
      "handle": "anaruiz",
      "platforms": ["github"]
    },
    {
      "raw_names": ["Luis Cabrera"],
      "handle": "luiscabrera",
      "platforms": ["github"]
    },
    {
      "raw_names": ["Omar Second"],
      "handle": "omarsecond",
      "platforms": ["github"]
    }
  ]
}`

func mapperDePrueba(t *testing.T, tabla string) *identity.JSONIdentityMapper {
	t.Helper()

	m, err := identity.NuevoJSONIdentityMapperDesdeBytes([]byte(tabla))
	if err != nil {
		t.Fatalf("no se pudo construir el mapper: %v", err)
	}

	return m
}

// --- TC-04: mapeo de identidades ---

// TestTC04MapeaNombreRealAHandle es el caso de evaluacion TC-04.
func TestTC04MapeaNombreRealAHandle(t *testing.T) {
	m := mapperDePrueba(t, tablaPrueba)

	handle, err := m.ResolveHandle(context.Background(), "Omar Hernández")
	if err != nil {
		t.Fatalf("no se pudo resolver el nombre: %v", err)
	}

	if handle != "omarhernan" {
		t.Errorf("handle = %q, se esperaba omarhernan", handle)
	}
}

// TestResolveHandleEsCaseInsensitive comprueba el criterio explicito de TC-04.
func TestResolveHandleCaseInsensitive(t *testing.T) {
	m := mapperDePrueba(t, tablaPrueba)

	casos := []string{
		"Omar Hernández",
		"omar hernández",
		"OMAR HERNANDEZ",
		"OmAr HeRnÁnDeZ",
		"  Omar Hernández  ",
		"Omar   Hernández", // espacios multiples
	}

	for _, nombre := range casos {
		t.Run(nombre, func(t *testing.T) {
			handle, err := m.ResolveHandle(context.Background(), nombre)
			if err != nil {
				t.Fatalf("no se pudo resolver %q: %v", nombre, err)
			}
			if handle != "omarhernan" {
				t.Errorf("%q produjo %q, se esperaba omarhernan", nombre, handle)
			}
		})
	}
}

// TestResolveHandleToleraAcentosAusentes es un caso real: la gente escribe
// "Hernandez" sin tilde al dictar o cuando el teclado no la tiene.
func TestResolveHandleToleraAcentosAusentes(t *testing.T) {
	m := mapperDePrueba(t, tablaPrueba)

	casos := []string{
		"Omar Hernandez",
		"Omar Hernàndez",
		"Omar Hërnández",
	}

	for _, nombre := range casos {
		t.Run(nombre, func(t *testing.T) {
			handle, err := m.ResolveHandle(context.Background(), nombre)
			if err != nil {
				t.Fatalf("no se pudo resolver %q: %v", nombre, err)
			}
			if handle != "omarhernan" {
				t.Errorf("%q produjo %q, se esperaba omarhernan", nombre, handle)
			}
		})
	}
}

// TestResolveHandleAceptaAbreviaturas cubre "Omar H." y "Ana M.".
func TestResolveHandleAceptaAbreviaturas(t *testing.T) {
	m := mapperDePrueba(t, tablaPrueba)

	casos := map[string]string{
		"Omar H.": "omarhernan",
		"Omar H":  "omarhernan",
		"omar h.": "omarhernan",
		"Ana M.":  "anaruiz",
		"luis c.": "luiscabrera",
	}

	for nombre, esperado := range casos {
		t.Run(nombre, func(t *testing.T) {
			handle, err := m.ResolveHandle(context.Background(), nombre)
			if err != nil {
				t.Fatalf("no se pudo resolver %q: %v", nombre, err)
			}
			if handle != esperado {
				t.Errorf("%q produjo %q, se esperaba %q", nombre, handle, esperado)
			}
		})
	}
}

// TestNombreDePilaAmbiguoNoSeResuelve es una decision de seguridad.
//
// Hay dos personas llamadas "Omar" en la tabla. Resolver "Omar" a cualquiera de
// las dos publicaria una tarea bajo la persona equivocada, que es un fallo
// silencioso muy caro. El mapper debe negarse.
func TestNombreDePilaAmbiguoNoSeResuelve(t *testing.T) {
	m := mapperDePrueba(t, tablaPrueba)

	_, err := m.ResolveHandle(context.Background(), "Omar")

	if !errors.Is(err, domain.ErrIdentidadNoResuelta) {
		t.Fatalf("un nombre ambiguo no debe resolverse, se obtuvo %v", err)
	}
	if !strings.Contains(err.Error(), "Omar") {
		t.Errorf("el error deberia nombrar la persona no resuelta: %v", err)
	}
}

// TestNombreDePilaUnicoSiSeResuelve comprueba el caso contrario: "Luis" y "Ana"
// no colisionan y si deben resolverse.
func TestNombreDePilaUnicoSiSeResuelve(t *testing.T) {
	m := mapperDePrueba(t, tablaPrueba)

	casos := map[string]string{
		"Luis": "luiscabrera",
		"Ana":  "anaruiz",
		"luis": "luiscabrera",
	}

	for nombre, esperado := range casos {
		t.Run(nombre, func(t *testing.T) {
			handle, err := m.ResolveHandle(context.Background(), nombre)
			if err != nil {
				t.Fatalf("no se pudo resolver %q: %v", nombre, err)
			}
			if handle != esperado {
				t.Errorf("%q produjo %q, se esperaba %q", nombre, handle, esperado)
			}
		})
	}
}

func TestResolveHandleNombreDesconocido(t *testing.T) {
	m := mapperDePrueba(t, tablaPrueba)

	_, err := m.ResolveHandle(context.Background(), "Persona Que No Existe")

	if !errors.Is(err, domain.ErrIdentidadNoResuelta) {
		t.Errorf("se esperaba ErrIdentidadNoResuelta, se obtuvo %v", err)
	}
}

func TestResolveHandleRespetaContexto(t *testing.T) {
	m := mapperDePrueba(t, tablaPrueba)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := m.ResolveHandle(ctx, "Omar Hernández"); !errors.Is(err, context.Canceled) {
		t.Errorf("se esperaba context.Canceled, se obtuvo %v", err)
	}
}

// --- Politicas ---

func TestPoliticaPorDefecto(t *testing.T) {
	// Sin defaults en el archivo, debe caer en "fail".
	m := mapperDePrueba(t, `{
		"mappings": [{"raw_names": ["Ana"], "handle": "ana"}]
	}`)

	if m.Politica() != identity.PoliticaFallar {
		t.Errorf("la politica por defecto deberia ser fail, es %q", m.Politica())
	}
}

func TestPoliticaConfigurada(t *testing.T) {
	casos := []struct {
		politica string
		esperada identity.PoliticaDesconocido
	}{
		{"fail", identity.PoliticaFallar},
		{"assign_unassigned", identity.PoliticaAsignarSinAsignado},
		{"skip", identity.PoliticaOmitir},
		{"FAIL", identity.PoliticaFallar},
		{"  skip  ", identity.PoliticaOmitir},
		// Politica inventada: debe caer en la opcion segura.
		{"inventada", identity.PoliticaFallar},
	}

	for _, tt := range casos {
		t.Run(tt.politica, func(t *testing.T) {
			m := mapperDePrueba(t, `{
				"defaults": {"unknown_assignee_policy": "`+tt.politica+`"},
				"mappings": [{"raw_names": ["Ana"], "handle": "ana"}]
			}`)

			if m.Politica() != tt.esperada {
				t.Errorf("politica = %q, se esperaba %q", m.Politica(), tt.esperada)
			}
		})
	}
}

func TestHandlePorDefecto(t *testing.T) {
	m := mapperDePrueba(t, tablaPrueba)

	if m.HandlePorDefecto() != "sin-asignar" {
		t.Errorf("handle por defecto = %q", m.HandlePorDefecto())
	}
}

// --- Carga de tabla ---

func TestTablaVaciaSeRechaza(t *testing.T) {
	_, err := identity.NuevoJSONIdentityMapperDesdeBytes([]byte(`{"mappings": []}`))

	if !errors.Is(err, identity.ErrTablaMapeoInvalida) {
		t.Errorf("una tabla sin mapeos debe rechazarse: %v", err)
	}
}

func TestTablaJSONInvalido(t *testing.T) {
	_, err := identity.NuevoJSONIdentityMapperDesdeBytes([]byte(`{roto`))

	if !errors.Is(err, identity.ErrTablaMapeoInvalida) {
		t.Errorf("un JSON invalido debe rechazarse: %v", err)
	}
}

func TestEntradaSinHandleSeIgnora(t *testing.T) {
	// Una entrada sin handle no debe romper la carga ni contaminar el indice.
	m := mapperDePrueba(t, `{
		"mappings": [
			{"raw_names": ["Ana"], "handle": "ana"},
			{"raw_names": ["Sin Handle"], "handle": ""}
		]
	}`)

	if _, err := m.ResolveHandle(context.Background(), "Ana"); err != nil {
		t.Errorf("la entrada valida debe seguir funcionando: %v", err)
	}

	if m.ConoceInforma("Sin Handle") {
		t.Error("una entrada sin handle no debe resolverse a nada")
	}
}

func TestCargarDesdeRuta(t *testing.T) {
	dir := t.TempDir()
	ruta := filepath.Join(dir, "mappings.json")

	if err := os.WriteFile(ruta, []byte(tablaPrueba), 0o600); err != nil {
		t.Fatalf("no se pudo escribir la tabla: %v", err)
	}

	m, err := identity.NuevoJSONIdentityMapper(ruta)
	if err != nil {
		t.Fatalf("no se pudo cargar la tabla: %v", err)
	}

	handle, err := m.ResolveHandle(context.Background(), "Ana María Ruiz")
	if err != nil {
		t.Fatalf("no se pudo resolver: %v", err)
	}
	if handle != "anaruiz" {
		t.Errorf("handle = %q", handle)
	}
}

func TestCargarRutaInexistente(t *testing.T) {
	_, err := identity.NuevoJSONIdentityMapper(filepath.Join(t.TempDir(), "no-existe.json"))

	if !errors.Is(err, identity.ErrTablaMapeoInvalida) {
		t.Errorf("se esperaba ErrTablaMapeoInvalida, se obtuvo %v", err)
	}
}

// TestTablaRealDelProyectoEsValida protege el archivo versionado.
func TestTablaRealDelProyectoEsValida(t *testing.T) {
	ruta := filepath.Join("..", "..", "..", "configs", "mappings.example.json")

	m, err := identity.NuevoJSONIdentityMapper(ruta)
	if err != nil {
		t.Fatalf("la tabla de ejemplo del proyecto debe ser valida: %v", err)
	}

	// Debe contener al menos el caso de TC-04.
	if _, err := m.ResolveHandle(context.Background(), "Omar Hernández"); err != nil {
		t.Errorf("la tabla de ejemplo deberia resolver el caso de TC-04: %v", err)
	}
}

// --- Utilidades y concurrencia ---

func TestNormalizarNombre(t *testing.T) {
	casos := []struct {
		entrada  string
		esperado string
	}{
		{"Omar Hernández", "omar hernandez"},
		{"  Omar   Hernández  ", "omar hernandez"},
		{"Omar H.", "omar h"},
		{"OMAR", "omar"},
		{"", ""},
		{"...", ""},
	}

	for _, tt := range casos {
		t.Run(tt.entrada, func(t *testing.T) {
			if got := identity.NormalizarNombre(tt.entrada); got != tt.esperado {
				t.Errorf("NormalizarNombre(%q) = %q, se esperaba %q", tt.entrada, got, tt.esperado)
			}
		})
	}
}

func TestConoceInforma(t *testing.T) {
	m := mapperDePrueba(t, tablaPrueba)

	if !m.ConoceInforma("Omar Hernández") {
		t.Error("deberia conocer a Omar")
	}
	if m.ConoceInforma("Desconocido") {
		t.Error("no deberia conocer a un desconocido")
	}
}

func TestHandlesConocidos(t *testing.T) {
	manejadores := mapperDePrueba(t, tablaPrueba).HandlesConocidos()

	if len(manejadores) != 4 {
		t.Errorf("se esperaban 4 handles, se obtuvieron %d: %v", len(manejadores), manejadores)
	}
	// Ordenados, para que el diagnostico sea estable.
	for i := 1; i < len(manejadores); i++ {
		if manejadores[i-1] > manejadores[i] {
			t.Errorf("los handles no estan ordenados: %v", manejadores)
			break
		}
	}
}

// TestMapperEsSeguroEnConcurrencia: la etapa de despacho resuelve identidades
// desde varias goroutines, asi que el indice se consulta en paralelo.
func TestMapperEsSeguroEnConcurrencia(t *testing.T) {
	m := mapperDePrueba(t, tablaPrueba)

	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			if _, err := m.ResolveHandle(context.Background(), "Omar Hernández"); err != nil {
				t.Errorf("fallo la resolucion concurrente: %v", err)
			}

			m.ConoceInforma("Ana María Ruiz")
			m.HandlesConocidos()
		}()
	}

	wg.Wait()
}
