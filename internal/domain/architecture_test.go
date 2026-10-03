package domain_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Este archivo contiene las guardas arquitectónicas de RNF-01 y RNF-02.
//
// Los tests de AGENTS.md sección 1 son la forma más barata de detectar que el
// núcleo ha dejado de ser agnóstico a la infraestructura. Sin ellos, esa regla
// depende de que un revisor lo note, y la presión del calendario la erosiona.
//
// El análisis se hace con go/parser sobre los archivos del paquete en lugar de
// con go/build: no requiere resolver paquetes, no depende de como el sistema
// normaliza las rutas (go/build falla con rutas POSIX en Windows) y no depende
// de red. Los imports sin resolver no interesan aquí: para detectar si el
// dominio depende de infrastructure basta con leer las rutas declaradas.

// modulosProhibidos son los prefijos que el dominio no puede importar.
var modulosProhibidos = []string{
	"talkaboutthis/internal/infrastructure",
	"talkaboutthis/internal/application",
	"talkaboutthis/pkg/sdk",
	"talkaboutthis/cmd",
}

// paquetesDeRedProhibidos son rutas que implicarian I/O de red o ejecucion de
// procesos dentro del núcleo. El dominio debe declarar un puerto y delegar.
var paquetesDeRedProhibidos = []string{
	"net/http",
	"net/url",
	"os/exec",
}

// TestDominioNoImportaInfrastructure es la guarda principal de RNF-01.
func TestDominioNoImportaInfrastructure(t *testing.T) {
	rutas := rutasImportadas(t)

	if len(rutas) == 0 {
		t.Fatal("no se encontro ningun import en internal/domain; el test no esta leyendo los archivos correctos")
	}

	for _, ruta := range rutas {
		for _, prohibido := range modulosProhibidos {
			if ruta == prohibido || strings.HasPrefix(ruta, prohibido+"/") {
				t.Errorf(
					"VIOLACION DE ARQUITECTURA (RNF-01): el nucleo importa %q.\n"+
						"           El dominio solo depende de interfaces.\n"+
						"           Declara el contrato en domain/ports.go e implementalo en infrastructure.",
					ruta,
				)
			}
		}
	}
}

// TestDominioNoHaceLlamadasDeRed documenta que el nucleo no habla con la red.
func TestDominioNoHaceLlamadasDeRed(t *testing.T) {
	rutas := rutasImportadas(t)

	for _, ruta := range rutas {
		for _, prohibido := range paquetesDeRedProhibidos {
			if ruta == prohibido {
				t.Errorf(
					"el nucleo importa %q.\n"+
						"           El dominio no puede hacer I/O de red: declara un puerto y "+
						"delega en un adaptador de infrastructure.",
					ruta,
				)
			}
		}
	}
}

// TestDominioSoloUsaStdlib verifica que el nucleo no arrastra dependencias de
// terceros. Refuerza el objetivo de INIT.md sección 1: binario estático y ligero.
//
// Se admiten los paquetes propios del módulo porque no son dependencias externas.
//
// La distinción entre biblioteca estándar y paquete de terceros se hace con el
// criterio convencional: un paquete externo siempre tiene un dominio con punto
// en su primer segmento de ruta ("github.com/...", "golang.org/x/sync"), mientras
// que los de la biblioteca estándar nunca lo tienen ("encoding/json", "time").
// Es heurístico pero es el que se usa en la practica y no requiere ejecutar
// procesos ni resolver modulos, que es lo que hacia go/build inservible aqui.
func TestDominioSoloUsaStdlib(t *testing.T) {
	const modulo = "talkaboutthis"

	externos := make([]string, 0)
	for _, ruta := range rutasImportadas(t) {
		if ruta == modulo || strings.HasPrefix(ruta, modulo+"/") {
			continue
		}

		primerSegmento := ruta
		if i := strings.Index(ruta, "/"); i >= 0 {
			primerSegmento = ruta[:i]
		}

		if strings.Contains(primerSegmento, ".") {
			externos = append(externos, ruta)
		}
	}

	if len(externos) > 0 {
		t.Errorf(
			"el paquete domain debe usar solo la biblioteca estandar, pero importa dependencias externas:\n  %s\n"+
				"           Si necesitas una capacidad que no ofrece el stdlib, declara un puerto en domain/ports.go.",
			strings.Join(externos, "\n  "),
		)
	}
}

// TestDominioDeclaraLosPuertos comprueba que los contratos definidos en
// INIT.md sección 5.1 existen y siguen teniendo la firma esperada.
//
// Si alguien cambia un puerto, este test obliga a actualizar la especificación
// en lugar de dejar que la deriva pase inadvertida entre el documento y el codigo.
func TestDominioDeclaraLosPuertos(t *testing.T) {
	contenido, err := os.ReadFile("ports.go")
	if err != nil {
		t.Fatalf("no se pudo leer ports.go: %v", err)
	}

	texto := string(contenido)

	puertos := []string{
		"DocumentParser",
		"LLMProvider",
		"IdentityMapper",
		"ProjectBoardAdapter",
	}

	for _, puerto := range puertos {
		if !strings.Contains(texto, "type "+puerto+" interface") {
			t.Errorf(
				"falta la declaracion del puerto %s en ports.go.\n"+
					"           Los puertos son el contrato de INIT.md seccion 5.1.",
				puerto,
			)
		}
	}
}

// rutasImportadas devuelve las rutas de todos los imports declarados en los
// archivos .go no-test del paquete domain.
//
// Se omiten los archivos _test.go a proposito: el paquete domain_test puede
// importar encoding/json y otros paquetes de soporte para construir las
// aserciones de compilacion de los puertos, y eso no viola la arquitectura,
// que se refiere al código de producción.
func rutasImportadas(t *testing.T) []string {
	t.Helper()

	entradas, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("no se pudieron listar los archivos de domain: %v", err)
	}

	if len(entradas) == 0 {
		t.Fatal("no se encontraron archivos .go en internal/domain")
	}

	fset := token.NewFileSet()
	var rutas []string

	for _, archivo := range entradas {
		if strings.HasSuffix(archivo, "_test.go") {
			continue
		}

		// parser.ImportsOnly es suficiente y mas barato que un parseo completo:
		// aqui solo interesan los imports, no los cuerpos de las funciones.
		file, err := parser.ParseFile(fset, archivo, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("no se pudo analizar %s: %v", archivo, err)
		}

		for _, spec := range file.Imports {
			ruta := strings.Trim(spec.Path.Value, `"`)
			rutas = append(rutas, ruta)
		}
	}

	return rutas
}
