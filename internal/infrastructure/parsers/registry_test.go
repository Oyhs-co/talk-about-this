package parsers_test

import (
	"testing"

	"talkaboutthis/internal/infrastructure/parsers"
)

// Estos tests cubren la normalizacion de extensiones y la seleccion de parser,
// que es donde se decide que formato se cree que tiene un archivo.

// TestExtensionDeRutaNormalizaElCaso documenta que la extension se pasa a
// minusculas antes de buscar un parser.
//
// Sin esto, un ".DOCX" exportado por Word en Windows no encontraria parser y
// caeria en deteccion por contenido, que funciona pero es un camino distinto
// del previsto.
func TestExtensionDeRutaNormalizaElCaso(t *testing.T) {
	casos := []struct {
		ruta     string
		esperado string
	}{
		{"notas.md", ".md"},
		{"NOTAS.MD", ".md"},
		{"Minuta.DocX", ".docx"},
		{"/ruta/con/espacios/archivo.TXT", ".txt"},
		{"sin_extension", ""},
		{"", ""},
	}

	for _, tt := range casos {
		t.Run(tt.ruta, func(t *testing.T) {
			if obtenido := parsers.ExtensionDeRuta(tt.ruta); obtenido != tt.esperado {
				t.Errorf("ExtensionDeRuta(%q) = %q, se esperaba %q", tt.ruta, obtenido, tt.esperado)
			}
		})
	}
}

// TestRegistroResuelveCadaFormatoSoportado comprueba que las tres extensiones
// de RF-01 llegan a un parser.
func TestRegistroResuelveCadaFormatoSoportado(t *testing.T) {
	registro := parsers.NewRegistryPorDefecto()

	for _, ext := range []string{".md", ".txt", ".docx"} {
		t.Run(ext, func(t *testing.T) {
			parser, err := registro.ParserParaExtension(ext)

			if err != nil {
				t.Fatalf("no hay parser para %q: %v", ext, err)
			}

			if parser == nil {
				t.Fatalf("el parser para %q es nil", ext)
			}
		})
	}
}

// TestRegistroRechazaExtensionesDesconocidas comprueba que una extension no
// soportada falla con un mensaje que la nombra.
func TestRegistroRechazaExtensionesDesconocidas(t *testing.T) {
	registro := parsers.NewRegistryPorDefecto()

	for _, ext := range []string{".pdf", ".xlsx", ".json", ""} {
		t.Run("ext="+ext, func(t *testing.T) {
			_, err := registro.ParserParaExtension(ext)

			if err == nil {
				t.Fatalf("se esperaba un error para la extension %q", ext)
			}
		})
	}
}

// TestRegistroDeteccionPorContenidoIgnoraNombresDeCampo cubre la regresion de
// F3: el detector recorria "properties" y reportaba los nombres de campo
// ("action_items", "titulo") como reglas no soportadas.
//
// Aqui la consecuencia es que un documento confronta nombres que parecen
// magicos. Un DOCX real, cuya primera pagina suele contener una tabla de
// campos, no debe detectarse por sus propias etiquetas.
func TestRegistroDeteccionPorContenidoIgnoraNombresDeCampo(t *testing.T) {
	registro := parsers.NewRegistryPorDefecto()

	// Un DOCX minimo pero real, generado como lo haria Word: una lista de
	// campos en el cuerpo del documento.
	contenido := "Prioridad: HIGH\n" +
		"action_items\n" +
		"meeting_summary\n" +
		"properties\n" +
		"required\n"

	parser, err := registro.DetectarPorContenido([]byte(contenido))
	if err != nil {
		t.Fatalf("la deteccion por contenido fallo: %v", err)
	}

	// Ningun formato del registro es texto con esa firma, asi que no debe
	// reconocer ningun parser: (nil, nil) significa "no reconocible".
	if parser != nil {
		t.Errorf("un texto que no es ningun formato soportado no debe producir parser, se obtuvo %T", parser)
	}
}

// TestRegistroDeteccionPorContenidoDevuelveNilSinErrorParaDesconocido fija el
// contrato de la deteccion.
//
// (nil, nil) y (nil, error) significan cosas distintas: la primera es "no lo
// reconozco, prueba con la extension"; la segunda es un fallo real de lectura.
func TestRegistroDeteccionPorContenidoDevuelveNilSinErrorParaDesconocido(t *testing.T) {
	registro := parsers.NewRegistryPorDefecto()

	parser, err := registro.DetectarPorContenido([]byte("texto plano cualquiera sin ninguna firma"))

	if err != nil {
		t.Errorf("un contenido no reconocido no es un error: %v", err)
	}

	if parser != nil {
		t.Errorf("se obtuvo un parser %T para contenido no reconocido", parser)
	}
}

// TestRegistroDeteccionPorContenidoConEntradaVacia no debe inventar un formato.
func TestRegistroDeteccionPorContenidoConEntradaVacia(t *testing.T) {
	registro := parsers.NewRegistryPorDefecto()

	parser, err := registro.DetectarPorContenido(nil)
	if err != nil {
		t.Errorf("una entrada vacia no deberia ser un error: %v", err)
	}

	if parser != nil {
		t.Errorf("una entrada vacia no puede reconocer ningun formato, se obtuvo %T", parser)
	}
}

// TestRegistroDetectaPorFirmaDeArchivo comprueba que la deteccion por
// contenido funciona cuando la extension miente.
//
// Es el caso que justifica que DetectarPorContenido exista: un .txt que en
// realidad es un .docx renombrado.
func TestRegistroDetectaPorFirmaDeArchivo(t *testing.T) {
	registro := parsers.NewRegistryPorDefecto()

	// Firma ZIP: los dos primeros bytes de un .docx.
	firma := []byte{'P', 'K', 0x03, 0x04, 0x14, 0x00, 0x00, 0x00}

	parser, err := registro.DetectarPorContenido(firma)
	if err != nil {
		t.Fatalf("la deteccion fallo: %v", err)
	}

	if parser == nil {
		t.Fatal("una firma ZIP deberia reconocerse como DOCX")
	}

	// No hay Name() en el puerto: se afirma sobre el tipo concreto, que es
	// lo que el puerto no puede expresar y por lo que el registro lo necesita.
	if _, esDocx := parser.(*parsers.DocxParser); !esDocx {
		t.Errorf("la firma ZIP se resolvio a %T, se esperaba *parsers.DocxParser", parser)
	}
}
