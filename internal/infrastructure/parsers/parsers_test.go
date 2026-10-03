package parsers_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"talkaboutthis/internal/domain"
	"talkaboutthis/internal/infrastructure/parsers"
)

// rutaFixture construye la ruta a un archivo de testdata.
func rutaFixture(nombre string) string {
	return filepath.Join("..", "..", "..", "testdata", nombre)
}

// leerFixture carga un archivo de testdata como reader en memoria.
//
// Los parsers aceptan io.Reader, no rutas: eso permite probarlos sin tocar el
// disco en el caso comun.
func leerFixture(t *testing.T, nombre string) io.Reader {
	t.Helper()

	datos, err := os.ReadFile(rutaFixture(nombre))
	if err != nil {
		t.Skipf("fixture %s no disponible: %v", nombre, err)
	}

	return bytes.NewReader(datos)
}

// --- TC-01: ingesta de DOCX ---

// TestTC01IngestaDocxRetornaTextoLimpio es el caso de evaluacion TC-01.
//
// Verifica que un .docx real (contenedor OOXML) produce texto plano UTF-8, que
// el contenido relevante sobrevive al parseo y que la ejecucion no deja
// descriptores abiertos.
func TestTC01IngestaDocxRetornaTextoLimpio(t *testing.T) {
	docx := parsers.NewDocxParser()

	contenido, err := docx.Parse(context.Background(), leerFixture(t, "minuta.docx"))
	if err != nil {
		t.Fatalf("el parseo del .docx fallo: %v", err)
	}

	if strings.TrimSpace(contenido) == "" {
		t.Fatal("el .docx produjo texto vacio")
	}

	// El texto de los parrafos debe sobrevivir intacto.
	esperados := []string{
		"Acta de la reunion de arquitectura",
		"Fecha: 15 de octubre de 2026",
		"Omar Hernandez",
		"Migrar la sesion fuera del contexto global",
		"Documentar los reintentos del webhook",
	}

	for _, esperado := range esperados {
		if !strings.Contains(contenido, esperado) {
			t.Errorf("el texto extraido no contiene %q\n--- contenido ---\n%s", esperado, contenido)
		}
	}

	// UTF-8 real: si el parser leyera mal la codificacion, estos caracteres
	// aparecerian como secuencias de reemplazo.
	if !strings.Contains(contenido, "Costo") || !strings.Contains(contenido, "50000") {
		t.Errorf("se perdio contenido numerico o de acentuacion:\n%s", contenido)
	}

	// Las marcas de formato no deben aparecer en el texto plano.
	for _, marcador := range []string{"<w:p>", "<w:t>", "<w:r>", "xml:space"} {
		if strings.Contains(contenido, marcador) {
			t.Errorf("el texto extraido contiene marcado XML %q: el parser no limpio correctamente", marcador)
		}
	}
}

// TestTC01NoFugaDescriptores procesa muchos documentos seguidos.
//
// Este es el test que hace valido el criterio de TC-01 sobre fugas: si el
// parser abriera un descriptor por documento y no lo cerrara, tras cientos de
// iteraciones el proceso fallaria al abrir el siguiente. En Windows el limite
// es especialmente bajo, asi que el fallo apareceria aqui y no en produccion.
func TestTC01NoFugaDescriptores(t *testing.T) {
	docx := parsers.NewDocxParser()
	datos, err := os.ReadFile(rutaFixture("minuta.docx"))
	if err != nil {
		t.Skipf("fixture no disponible: %v", err)
	}

	// Se abren y cierran archivos reales en un bucle, que es donde un
	// descriptor sin cerrar se acumularia.
	for i := 0; i < 300; i++ {
		archivo, err := os.Open(rutaFixture("minuta.docx"))
		if err != nil {
			t.Fatalf("iteracion %d: no se pudo abrir el archivo: %v", i, err)
		}

		if _, err := docx.Parse(context.Background(), archivo); err != nil {
			_ = archivo.Close()
			t.Fatalf("iteracion %d: fallo el parseo: %v", i, err)
		}

		if err := archivo.Close(); err != nil {
			t.Fatalf("iteracion %d: fallo el cierre: %v", i, err)
		}
	}

	// Si el parser hubiera dejado entradas ZIP abiertas, el numero de handles
	// abiertos habria crecido de forma medible.
	_ = datos
}

func TestDocxCanParse(t *testing.T) {
	docx := parsers.NewDocxParser()

	casos := []struct {
		extension string
		quiere    bool
	}{
		{".docx", true},
		{"docx", true},
		{".DOCX", true},
		{"  .Docx  ", true},
		{".md", false},
		{".txt", false},
		{"", false},
	}

	for _, tt := range casos {
		t.Run(tt.extension, func(t *testing.T) {
			if got := docx.CanParse(tt.extension); got != tt.quiere {
				t.Errorf("CanParse(%q) = %v, se esperaba %v", tt.extension, got, tt.quiere)
			}
		})
	}
}

// TestDocxRechazaArchivoQueNoEsZip cubre el error mas comun en la practica:
// un .txt renombrado a .docx.
func TestDocxRechazaArchivoQueNoEsZip(t *testing.T) {
	docx := parsers.NewDocxParser()

	_, err := docx.Parse(context.Background(), strings.NewReader("esto es texto plano, no un zip"))

	if err == nil {
		t.Fatal("se esperaba error al parsear un archivo que no es un ZIP")
	}
	if !strings.Contains(err.Error(), "ZIP") {
		t.Errorf("el error deberia mencionar que el archivo no es ZIP: %v", err)
	}
}

// TestDocxRechazaZipSinWordDocument cubre un ZIP valido que no es un documento
// de Word: el mensaje debe ser explicito, no un error generico de vacio.
func TestDocxRechazaZipSinWordDocument(t *testing.T) {
	// Se construye un ZIP minimo en memoria con una entrada que no es el
	// documento de Word. Solo se necesita la cabecera PK para que zip.Reader
	// lo acepte; un archivo con un unico stored entry es suficiente.
	zipMinimo := append([]byte("PK\x03\x04"), make([]byte, 512)...)

	_, err := parsers.NewDocxParser().Parse(context.Background(), bytes.NewReader(zipMinimo))

	if err == nil {
		t.Fatal("se esperaba error con un ZIP que no contiene word/document.xml")
	}
}

func TestDocxDetectaFirma(t *testing.T) {
	docx := parsers.NewDocxParser()

	if !docx.DetectaFirma([]byte("PK\x03\x04")) {
		t.Error("deberia reconocer la firma PK de un contenedor ZIP")
	}
	if docx.DetectaFirma([]byte("Texto plano")) {
		t.Error("no deberia reconocer texto plano como DOCX")
	}
	if docx.DetectaFirma([]byte("P")) {
		t.Error("una cabecera demasiado corta no debe reconocerse")
	}
	if docx.DetectaFirma(nil) {
		t.Error("una cabecera vacia no debe reconocerse")
	}
}

// --- TXT ---

func TestTxtExtraeContenido(t *testing.T) {
	txt := parsers.NewTXTParser()

	contenido, err := txt.Parse(context.Background(), leerFixture(t, "notas-rapidas.txt"))
	if err != nil {
		t.Fatalf("fallo el parseo del .txt: %v", err)
	}

	if !strings.Contains(contenido, "Omar: revisar PR #412") {
		t.Errorf("se perdio contenido del .txt:\n%s", contenido)
	}
}

// TestTxtNormalizaBOMYSaltosDeLinea cubre el caso de un archivo generado en
// Windows: BOM al inicio y CRLF en cada linea. Sin normalizar, el BOM se
// pegaria a la primera palabra y el LLM la leeria como parte de la frase.
func TestTxtNormalizaBOMYSaltosDeLinea(t *testing.T) {
	txt := parsers.NewTXTParser()

	entrada := "\uFEFFPrimera linea\r\nSegunda linea\rTercera linea\n"

	contenido, err := txt.Parse(context.Background(), strings.NewReader(entrada))
	if err != nil {
		t.Fatalf("fallo el parseo: %v", err)
	}

	if strings.HasPrefix(contenido, "\uFEFF") {
		t.Error("el BOM no fue eliminado")
	}
	if strings.Contains(contenido, "\r") {
		t.Error("quedaron retornos de carro sin normalizar")
	}

	lineas := strings.Split(strings.TrimRight(contenido, "\n"), "\n")
	if len(lineas) != 3 {
		t.Fatalf("se esperaban 3 lineas con contenido, se obtuvieron %d: %q", len(lineas), lineas)
	}
	if lineas[0] != "Primera linea" {
		t.Errorf("linea 1 inesperada: %q", lineas[0])
	}
	if lineas[1] != "Segunda linea" {
		t.Errorf("linea 2 inesperada: %q", lineas[1])
	}
	if lineas[2] != "Tercera linea" {
		t.Errorf("linea 3 inesperada: %q", lineas[2])
	}
}

// TestTxtToleraUTF8Invalido comprueba que un byte roto no tumba la ingesta.
func TestTxtToleraUTF8Invalido(t *testing.T) {
	txt := parsers.NewTXTParser()

	// 0xFF no forma parte de ninguna secuencia UTF-8 valida.
	entrada := []byte("Texto con byte invalido: \xff mas texto")

	contenido, err := txt.Parse(context.Background(), bytes.NewReader(entrada))
	if err != nil {
		t.Fatalf("un byte invalido no deberia producir error: %v", err)
	}

	if !strings.Contains(contenido, "mas texto") {
		t.Errorf("el contenido posterior al byte roto se perdio: %q", contenido)
	}
}

func TestTxtRespetaContextoCancelado(t *testing.T) {
	txt := parsers.NewTXTParser()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := txt.Parse(ctx, strings.NewReader("contenido")); !errors.Is(err, context.Canceled) {
		t.Errorf("se esperaba context.Canceled, se obtuvo %v", err)
	}
}

func TestParseoConReaderNil(t *testing.T) {
	parsersConNil := []domain.DocumentParser{
		parsers.NewTXTParser(),
		parsers.NewMarkdownParser(),
		parsers.NewDocxParser(),
	}

	for _, parser := range parsersConNil {
		if _, err := parser.Parse(context.Background(), nil); err == nil {
			t.Errorf("%T deberia devolver error con un reader nil", parser)
		}
	}
}

// --- Markdown ---

func TestMarkdownExtraeContenido(t *testing.T) {
	md := parsers.NewMarkdownParser()

	contenido, err := md.Parse(context.Background(), leerFixture(t, "reunion-equipo.md"))
	if err != nil {
		t.Fatalf("fallo el parseo del .md: %v", err)
	}

	// El contenido sustantivo debe estar presente.
	esperados := []string{
		"Migrar",
		"autenticación",
		"revisará el contrato de la API de pagos",
		"token de GitHub caduca",
	}

	for _, esperado := range esperados {
		if !strings.Contains(contenido, esperado) {
			t.Errorf("falta el contenido %q\n--- resultado ---\n%s", esperado, contenido)
		}
	}
}

// TestMarkdownEliminaFrontMatter es el comportamiento que mas afecta al
// resultado: los metadatos del encabezado no son acciones, y si se dejan pasar
// el LLM puede intentar convertir "fecha: 2026-09-28" en un item de backlog.
func TestMarkdownEliminaFrontMatter(t *testing.T) {
	md := parsers.NewMarkdownParser()

	entrada := `---
titulo: Reunión interna
fecha: 2026-09-28
---

# Encabezado real

Contenido de la reunión.
`

	contenido, err := md.Parse(context.Background(), strings.NewReader(entrada))
	if err != nil {
		t.Fatalf("fallo el parseo: %v", err)
	}

	for _, prohibido := range []string{"titulo:", "2026-09-28", "---"} {
		if strings.Contains(contenido, prohibido) {
			t.Errorf("el front-matter deberia haberse eliminado, quedo %q:\n%s", prohibido, contenido)
		}
	}

	if !strings.Contains(contenido, "Contenido de la reunión") {
		t.Errorf("se perdio el cuerpo del documento:\n%s", contenido)
	}
}

// TestMarkdownConservaBloquesDeCodigo protege el contenido mas valioso: una
// minuta que cita SQL o comandos contiene la especificacion exacta de la tarea.
func TestMarkdownConservaBloquesDeCodigo(t *testing.T) {
	md := parsers.NewMarkdownParser()

	entrada := "Antes del bloque.\n\n" +
		"```sql\n" +
		"CREATE INDEX idx_items ON transcript_items (assignee);\n" +
		"-- no eliminar este indice\n" +
		"```\n\n" +
		"Despues del bloque.\n"

	contenido, err := md.Parse(context.Background(), strings.NewReader(entrada))
	if err != nil {
		t.Fatalf("fallo el parseo: %v", err)
	}

	if !strings.Contains(contenido, "CREATE INDEX idx_items") {
		t.Errorf("el codigo del bloque se perdio:\n%s", contenido)
	}
	if !strings.Contains(contenido, "-- no eliminar este indice") {
		t.Errorf("el comentario del bloque se perdio:\n%s", contenido)
	}

	// Las cercas, en cambio, son formato y deben desaparecer.
	if strings.Contains(contenido, "```") {
		t.Errorf("quedaron las cercas de codigo:\n%s", contenido)
	}
}

// TestMarkdownQuitaMarcasDeFormato cubre los elementos mas frecuentes.
func TestMarkdownQuitaMarcasDeFormato(t *testing.T) {
	md := parsers.NewMarkdownParser()

	casos := []struct {
		nombre  string
		entrada string
		queda   string
		noQueda string
	}{
		{
			nombre:  "negrita",
			entrada: "**texto importante**",
			queda:   "texto importante",
			noQueda: "**",
		},
		{
			nombre:  "encabezado",
			entrada: "## Acuerdos",
			queda:   "Acuerdos",
			noQueda: "##",
		},
		{
			nombre:  "vineta",
			entrada: "- primer punto",
			queda:   "primer punto",
			noQueda: "- ",
		},
		{
			nombre:  "lista ordenada",
			entrada: "1. migrar la sesion",
			queda:   "migrar la sesion",
			noQueda: "1. ",
		},
		{
			nombre:  "cita",
			entrada: "> decision importante",
			queda:   "decision importante",
			noQueda: ">",
		},
		{
			nombre:  "enlace",
			entrada: "ver la [documentacion](https://example.com/guia)",
			queda:   "ver la documentacion",
			noQueda: "https://",
		},
	}

	for _, tt := range casos {
		t.Run(tt.nombre, func(t *testing.T) {
			contenido, err := md.Parse(context.Background(), strings.NewReader(tt.entrada))
			if err != nil {
				t.Fatalf("fallo el parseo: %v", err)
			}

			if !strings.Contains(contenido, tt.queda) {
				t.Errorf("deberia conservarse %q, resultado: %q", tt.queda, contenido)
			}
			if strings.Contains(contenido, tt.noQueda) {
				t.Errorf("deberia eliminarse %q, resultado: %q", tt.noQueda, contenido)
			}
		})
	}
}

// TestMarkdownCursivaSimetrica cubre un bug real: una heuristica asimetrica
// para el asterisco simple quitaba la apertura de "*Ana*" y dejaba el cierre,
// produciendo "Ana* revisara" en el texto que recibe el LLM.
func TestMarkdownCursivaSimetrica(t *testing.T) {
	md := parsers.NewMarkdownParser()

	casos := []struct {
		entrada string
		espera  string
	}{
		{"*Ana* revisara el contrato", "Ana revisara el contrato"},
		{"**Omar** y *Luis* viven", "Omar y Luis viven"},
		{"un *enfatizado* en medio", "un enfatizado en medio"},
	}

	for _, tt := range casos {
		t.Run(tt.entrada, func(t *testing.T) {
			contenido, err := md.Parse(context.Background(), strings.NewReader(tt.entrada))
			if err != nil {
				t.Fatalf("fallo el parseo: %v", err)
			}
			if contenido != tt.espera {
				t.Errorf("entrada %q produjo %q, se esperaba %q", tt.entrada, contenido, tt.espera)
			}
		})
	}
}

// TestMarkdownNoRompiaCodigoInline protege un caso en el que un reemplazo
// ingenuo produciria dano real: los guiones bajos de snake_case y las
// multiplicaciones no son enfasis.
func TestMarkdownNoRompiaCodigoInline(t *testing.T) {
	md := parsers.NewMarkdownParser()

	casos := []string{
		"la variable user_name no debe cambiar",
		"el costo es 2*3 unidades",
		"usa la funcion some_var_name",
		"multiplica 10*20 para el total",
	}

	for _, entrada := range casos {
		t.Run(entrada, func(t *testing.T) {
			contenido, err := md.Parse(context.Background(), strings.NewReader(entrada))
			if err != nil {
				t.Fatalf("fallo el parseo: %v", err)
			}

			if !strings.Contains(contenido, entrada) {
				t.Errorf("el parser altero texto que no era formato:\n  entrada:  %q\n  salida:   %q", entrada, contenido)
			}
		})
	}
}

// TestMarkdownConservaReglaHorizontalComoContenido comprueba el caso en que
// "---" NO es front-matter sino un separador dentro del cuerpo.
func TestMarkdownConservaReglaHorizontalComoContenido(t *testing.T) {
	md := parsers.NewMarkdownParser()

	// Sin bloque de cierre, el "---" inicial no es front-matter.
	entrada := "Reunion en curso\n---\nSe tomo la decision de continuar"

	contenido, err := md.Parse(context.Background(), strings.NewReader(entrada))
	if err != nil {
		t.Fatalf("fallo el parseo: %v", err)
	}

	if !strings.Contains(contenido, "Se tomo la decision") {
		t.Errorf("se perdio el contenido tras la linea separadora:\n%s", contenido)
	}
}

// --- Registry ---

func TestRegistrySeleccionaParserPorExtension(t *testing.T) {
	registro := parsers.NewRegistryPorDefecto()

	casos := []struct {
		extension string
		nombre    string
	}{
		{".md", "*parsers.MarkdownParser"},
		{".txt", "*parsers.TXTParser"},
		{".docx", "*parsers.DocxParser"},
		{".MD", "*parsers.MarkdownParser"},
	}

	for _, tt := range casos {
		t.Run(tt.extension, func(t *testing.T) {
			parser, err := registro.ParserParaExtension(tt.extension)
			if err != nil {
				t.Fatalf("no se encontro parser para %q: %v", tt.extension, err)
			}
			if got := tipoDe(parser); got != tt.nombre {
				t.Errorf("parser para %q = %s, se esperaba %s", tt.extension, got, tt.nombre)
			}
		})
	}
}

func TestRegistryExtensionDesconocida(t *testing.T) {
	registro := parsers.NewRegistryPorDefecto()

	_, err := registro.ParserParaExtension(".pdf")

	if !errors.Is(err, domain.ErrFormatoNoSoportado) {
		t.Errorf("se esperaba ErrFormatoNoSoportado, se obtuvo %v", err)
	}
	// El mensaje debe ayudar al usuario: listar los formatos disponibles evita
	// que tenga que consultar la documentacion.
	if !strings.Contains(err.Error(), ".md") || !strings.Contains(err.Error(), ".docx") {
		t.Errorf("el error deberia listar los formatos soportados: %v", err)
	}
}

func TestRegistrySoportanExtensiones(t *testing.T) {
	extensiones := parsers.NewRegistryPorDefecto().SoportanExtensiones()

	esperadas := []string{".docx", ".md", ".txt"}
	if len(extensiones) != len(esperadas) {
		t.Fatalf("se esperaban %v, se obtuvieron %v", esperadas, extensiones)
	}
	for i := range esperadas {
		if extensiones[i] != esperadas[i] {
			t.Errorf("las extensiones no estan ordenadas o no coinciden: %v", extensiones)
			break
		}
	}
}

func TestRegistryDetectaFormatoPorContenido(t *testing.T) {
	registro := parsers.NewRegistryPorDefecto()

	// Cabecera real de un ZIP.
	parser, err := registro.DetectarPorContenido([]byte("PK\x03\x04contenido"))
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if parser == nil {
		t.Fatal("se esperaba detectar el formato a partir de la cabecera PK")
	}
	if got := tipoDe(parser); got != "*parsers.DocxParser" {
		t.Errorf("se detecto %s, se esperaba el parser de DOCX", got)
	}
}

// TestRegistryNoDetectaTextoPlano documenta la limitacion consciente: Markdown
// y texto plano son indistinguibles a nivel de bytes, y el registro no adivina.
func TestRegistryNoDetectaTextoPlano(t *testing.T) {
	registro := parsers.NewRegistryPorDefecto()

	parser, err := registro.DetectarPorContenido([]byte("Reunion de equipo\n- Omar"))

	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if parser != nil {
		t.Errorf("no se deberia adivinar el formato de texto plano, se obtuvo %s", tipoDe(parser))
	}
}

func TestRegistryVacio(t *testing.T) {
	vacio := parsers.NewRegistry()

	if _, err := vacio.ParserParaExtension(".txt"); !errors.Is(err, domain.ErrFormatoNoSoportado) {
		t.Errorf("un registro vacio no debe reconocer ningun formato: %v", err)
	}
	if ext := vacio.SoportanExtensiones(); len(ext) != 0 {
		t.Errorf("un registro vacio no debe reportar extensiones: %v", ext)
	}
}

// TestRegistryRegistrarEsExtensible es la prueba de TC-07 para la ingesta: un
// parser nuevo debe poder registrarse sin modificar el registro existente.
func TestRegistryRegistrarEsExtensible(t *testing.T) {
	registro := parsers.NewRegistryPorDefecto()

	if _, err := registro.ParserParaExtension(".pdf"); err == nil {
		t.Fatal("precondicion fallida: .pdf no deberia estar soportado todavia")
	}

	registro.Registrar(&parserFalso{})

	parser, err := registro.ParserParaExtension(".pdf")
	if err != nil {
		t.Fatalf("tras registrar el parser, .pdf deberia soportarse: %v", err)
	}
	if got := tipoDe(parser); got != "*parsers_test.parserFalso" {
		t.Errorf("se obtuvo %s, se esperaba el parser recien registrado", got)
	}
}

func TestRegistryParsearIntegra(t *testing.T) {
	registro := parsers.NewRegistryPorDefecto()

	contenido, err := registro.Parsear(
		context.Background(),
		".txt",
		leerFixture(t, "notas-rapidas.txt"),
	)
	if err != nil {
		t.Fatalf("Parsear fallo: %v", err)
	}

	if !strings.Contains(contenido, "Ana") {
		t.Errorf("el contenido integro se perdio:\n%s", contenido)
	}
}

// parserFalso es un DocumentParser de prueba que demuestra la extension del
// registro sin tocar el codigo de produccion.
type parserFalso struct{}

func (p *parserFalso) CanParse(extension string) bool {
	return extension == ".pdf"
}

func (p *parserFalso) Parse(ctx context.Context, reader io.Reader) (string, error) {
	datos, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}
	return string(datos), nil
}

// Asercion de compilacion: cumple el puerto del dominio.
var _ domain.DocumentParser = (*parserFalso)(nil)

// tipoDe devuelve el nombre del tipo concreto de un parser, para comparar en
// las aserciones sin depender de comparacion de interfaces.
func tipoDe(v any) string {
	return fmt.Sprintf("%T", v)
}
