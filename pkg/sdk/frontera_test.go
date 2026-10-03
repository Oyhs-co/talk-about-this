package sdk_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Estas guardas vigilan la RESERVA de pkg/sdk.
//
// pkg/sdk esta vacio a proposito: es el sitio donde vivira la API publica del
// proyecto, y esa API todavia no existe. Un directorio reservado sin reglas es
// una invitacion a que alguien lo rellene de cualquier manera; estas dos reglas
// son las que no se pueden relajar cuando lo haga.
//
// El analisis usa go/parser y no go/build por el mismo motivo que
// internal/domain/architecture_test.go: no depende de como el sistema normaliza
// rutas, que en Windows rompe las rutas POSIX de go/build.

// modulosInternos es todo lo que un paquete bajo pkg/ no puede importar.
//
// La razon es que pkg/ es la SUPERFICIE PUBLICA: es lo unico que un tercero
// puede importar desde fuera del modulo, porque internal/ es invisible para el
// resto del mundo por diseno del lenguaje. Si un paquete de pkg/ importara
// internal/, ese paquete no seria utilizable fuera del repositorio, y el
// directorio existiria sin cumplir su proposito.
var modulosInternos = []string{
	"talkaboutthis/internal",
	"talkaboutthis/cmd",
}

// TestPkgSdkNoDependeDeInternal es la regla principal.
//
// Mientras el SDK este vacio, el test se salta: no hay codigo que vigilar. En
// cuanto se escriba el primer .go aqui, empezara a comprobarlo de verdad.
func TestPkgSdkNoDependeDeInternal(t *testing.T) {
	ficheros := ficherosGo(t, ".")

	if len(ficheros) == 0 {
		t.Skip("pkg/sdk sigue siendo una reserva vacia: no hay todavia codigo que vigilar")
	}

	for _, ruta := range ficheros {
		t.Run(nombreDe(ruta), func(t *testing.T) {
			for _, importado := range importsDe(t, ruta) {
				for _, prohibido := range modulosInternos {
					if importado == prohibido || strings.HasPrefix(importado, prohibido+"/") {
						t.Errorf(
							"VIOLACION DE LA FRONTERA: %s importa %q.\n"+
								"           Un paquete bajo pkg/ es la API PUBLICA del proyecto.\n"+
								"           Si depende de internal/, nadie fuera de este repositorio\n"+
								"           podra importarlo, y el directorio no cumple su proposito.\n"+
								"           Mueve el contrato necesario a pkg/ o reexportalo desde ahi.",
							nombreDe(ruta), importado,
						)
					}
				}
			}
		})
	}
}

// TestPkgSdkNoEsImportadoPorInternal es la regla reciproca.
//
// El nucleo NO puede depender de su propia API publica. Si lo hiciera, la
// superficie que se ofrece a terceros empezaria a dictar el diseno interno, y
// cualquier cambio pensado para un consumidor externo romperia el nucleo.
//
// Es la contraparte de la guarda que ya vive en
// internal/domain/architecture_test.go, extendida a TODA la capa internal.
func TestPkgSdkNoEsImportadoPorInternal(t *testing.T) {
	ficheros := ficherosGo(t, filepath.Join("..", "..", "internal"))

	if len(ficheros) == 0 {
		t.Fatal("no se encontro ningun .go en internal/: el test no esta leyendo los archivos correctos")
	}

	for _, ruta := range ficheros {
		t.Run(nombreDe(ruta), func(t *testing.T) {
			for _, importado := range importsDe(t, ruta) {
				if importado == "talkaboutthis/pkg/sdk" || strings.HasPrefix(importado, "talkaboutthis/pkg/") {
					t.Errorf(
						"VIOLACION DE LA FRONTERA: %s (capa internal) importa %q.\n"+
							"           El nucleo no puede depender de su API publica.\n"+
							"           Si lo hiciera, cambiar lo que se ofrece a terceros\n"+
							"           llegaria a romper el codigo interno.",
						nombreDe(ruta), importado,
					)
				}
			}
		})
	}
}

// TestLaReservaExiste documenta que el directorio sigue en su sitio.
//
// Si alguien lo borrara sin tomar la decision de llenarlo, la guarda anterior
// pasaria sin comprobar nada: no habria nada que violar y nada que proteger.
func TestLaReservaExiste(t *testing.T) {
	info, err := os.Stat(".")
	if err != nil {
		t.Fatalf("pkg/sdk deberia existir: %v", err)
	}

	if !info.IsDir() {
		t.Fatal("pkg/sdk deberia ser un directorio")
	}
}

// --- utilidades ---

// ficherosGo devuelve los .go de un arbol, en orden estable.
func ficherosGo(t *testing.T, raiz string) []string {
	t.Helper()

	var encontrados []string

	err := filepath.WalkDir(raiz, func(ruta string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".go") {
			encontrados = append(encontrados, ruta)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("no se pudo recorrer %s: %v", raiz, err)
	}

	return encontrados
}

// importsDe devuelve las rutas declaradas en los imports de un fichero.
func importsDe(t *testing.T, ruta string) []string {
	t.Helper()

	fset := token.NewFileSet()

	fichero, err := parser.ParseFile(fset, ruta, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("no se pudo analizar %s: %v", ruta, err)
	}

	var rutas []string

	for _, spec := range fichero.Imports {
		rutas = append(rutas, strings.Trim(spec.Path.Value, `"`))
	}

	return rutas
}

func nombreDe(ruta string) string {
	return filepath.ToSlash(ruta)
}
