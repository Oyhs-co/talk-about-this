package application_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"talkaboutthis/internal/application"
	"talkaboutthis/internal/domain"
	"talkaboutthis/internal/infrastructure/parsers"
)

// Estos tests cubren el caso de uso de ingesta, que es el punto donde se abren
// y cierran los archivos. Los errores en esa gestion no se ven en un unico
// documento: aparecen al cuarto de hora, cuando el proceso ya no puede abrir
// mas descriptores.

// rutaFixture construye la ruta a un archivo de testdata.
func rutaFixture(nombre string) string {
	return filepath.Join("..", "..", "testdata", nombre)
}

func transcriptor(t *testing.T) *application.IngestTranscriptor {
	t.Helper()
	return application.NuevaIngestTranscriptor(parsers.NewRegistryPorDefecto())
}

// TestIngestaCadaFormatoSoportado recorre el happy path de RF-01 para los tres
// formatos de la especificacion.
func TestIngestaCadaFormatoSoportado(t *testing.T) {
	casos := []struct {
		archivo   string
		contiene  string
		extension string
	}{
		{"reunion-equipo.md", "autenticación", ".md"},
		{"notas-rapidas.txt", "revisar PR #412", ".txt"},
		{"minuta.docx", "Acta de la reunion", ".docx"},
	}

	for _, tt := range casos {
		t.Run(tt.archivo, func(t *testing.T) {
			transcript, err := transcriptor(t).DesdeRuta(context.Background(), rutaFixture(tt.archivo))
			if err != nil {
				t.Fatalf("la ingesta de %s fallo: %v", tt.archivo, err)
			}

			if !strings.Contains(transcript.Content, tt.contiene) {
				t.Errorf("el transcript no contiene %q:\n%s", tt.contiene, transcript.Content)
			}

			if transcript.Metadata.Extension != tt.extension {
				t.Errorf("extension = %q, se esperaba %q", transcript.Metadata.Extension, tt.extension)
			}

			if transcript.EsVacio() {
				t.Error("el transcript no deberia estar vacio")
			}
		})
	}
}

// TestIngestaDesdeRutaNoFugaDescriptores es la parte de TC-01 que corresponde al
// caso de uso: DesdeRuta abre el archivo y debe cerrarlo siempre.
//
// Se procesan muchos archivos seguidos porque una fuga no se manifiesta en una
// sola iteracion, sino al acumular descriptores.
func TestIngestaDesdeRutaNoFugaDescriptores(t *testing.T) {
	ing := transcriptor(t)
	ctx := context.Background()

	for i := 0; i < 200; i++ {
		if _, err := ing.DesdeRuta(ctx, rutaFixture("minuta.docx")); err != nil {
			t.Fatalf("iteracion %d fallo: %v", i, err)
		}
	}
}

// TestIngestaRespetaContextoCancelado comprueba que un contexto ya cancelado
// detiene el trabajo antes de tocar el disco.
func TestIngestaRespetaContextoCancelado(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := transcriptor(t).DesdeRuta(ctx, rutaFixture("notas-rapidas.txt"))

	if !errors.Is(err, context.Canceled) {
		t.Errorf("se esperaba context.Canceled, se obtuvo %v", err)
	}
}

// TestIngestaArchivoInexistente da un error accionable.
func TestIngestaArchivoInexistente(t *testing.T) {
	_, err := transcriptor(t).DesdeRuta(context.Background(), "no/existe/magia.txt")

	if err == nil {
		t.Fatal("se esperaba error con una ruta inexistente")
	}
	if !strings.Contains(err.Error(), "no/existe/magia.txt") {
		t.Errorf("el error deberia incluir la ruta para que el usuario la verifique: %v", err)
	}
}

// TestIngestaDirectorioRespetaQueEsUnArchivo evita un error confuso: tratar un
// directorio como documento daria un error de formato en lugar de explicar que
// la ruta es incorrecta.
func TestIngestaDirectorio(t *testing.T) {
	// Se usa un directorio temporal en lugar de testdata: la prueba necesita
	// una ruta que exista y sea un directorio, y no debe depender de la
	// disposicion relativa del paquete de test.
	_, err := transcriptor(t).DesdeRuta(context.Background(), t.TempDir())

	if !errors.Is(err, domain.ErrFormatoNoSoportado) {
		t.Errorf("se esperaba ErrFormatoNoSoportado, se obtuvo %v", err)
	}
	if !strings.Contains(err.Error(), "directorio") {
		t.Errorf("el error deberia mencionar que es un directorio: %v", err)
	}
}

// TestIngestaFormatoNoSoportadoSinAdivinar comprueba que, cuando no hay parser
// posible, el caso de uso falla en vez de producir un transcript vacio que
// llegaria al LLM.
func TestIngestaFormatoNoSoportado(t *testing.T) {
	dir := t.TempDir()
	ruta := filepath.Join(dir, "informe.pdf")

	if err := os.WriteFile(ruta, []byte("%PDF-1.7\ncontenido"), 0o600); err != nil {
		t.Fatalf("no se pudo crear el fixture: %v", err)
	}

	_, err := transcriptor(t).DesdeRuta(context.Background(), ruta)

	if !errors.Is(err, domain.ErrFormatoNoSoportado) {
		t.Errorf("se esperaba ErrFormatoNoSoportado, se obtuvo %v", err)
	}
}

// TestIngestaDocxConExtensionIncorrecta cubre el caso real que motiva el
// respaldo por contenido: una minuta exportada desde Word guardada como .txt.
func TestIngestaDocxConExtensionIncorrecta(t *testing.T) {
	contenidoDocx, err := os.ReadFile(rutaFixture("minuta.docx"))
	if err != nil {
		t.Skipf("fixture no disponible: %v", err)
	}

	dir := t.TempDir()
	ruta := filepath.Join(dir, "minuta-exportada.txt")

	if err := os.WriteFile(ruta, contenidoDocx, 0o600); err != nil {
		t.Fatalf("no se pudo crear el fixture: %v", err)
	}

	transcript, err := transcriptor(t).DesdeRuta(context.Background(), ruta)
	if err != nil {
		t.Fatalf("un .docx guardado como .txt deberia detectarse por contenido: %v", err)
	}

	if !strings.Contains(transcript.Content, "Acta de la reunion") {
		t.Errorf("no se extrajo el texto real del documento:\n%s", transcript.Content)
	}
}

// TestIngestaArchivoVacioFallaAntesDelLLM es una economia importante: un archivo
// vacio debe fallar en la ingesta, no gastar una llamada al modelo para
// descubrir que no hay nada que extraer.
func TestIngestaArchivoVacio(t *testing.T) {
	dir := t.TempDir()
	ruta := filepath.Join(dir, "vacio.txt")

	if err := os.WriteFile(ruta, []byte("   \n\t\n  "), 0o600); err != nil {
		t.Fatalf("no se pudo crear el fixture: %v", err)
	}

	_, err := transcriptor(t).DesdeRuta(context.Background(), ruta)

	if !errors.Is(err, domain.ErrFormatoNoSoportado) {
		t.Errorf("se esperaba ErrFormatoNoSoportado para un archivo vacio, se obtuvo %v", err)
	}
}

// TestDesdeReaderNoCierraElReaderDeQuienLoPasa verifica la regla de propiedad
// del recurso: DesdeReader no es dueno del reader y no debe cerrarlo.
func TestDesdeReaderNoCierraElReaderDeQuienLoPasa(t *testing.T) {
	ing := transcriptor(t)

	reader := &spyReader{contenido: "Contenido de la reunion del equipo."}

	_, err := ing.DesdeReader(
		context.Background(),
		reader,
		".txt",
		domain.NewDocumentMetadata("notas.txt", ".txt", 35, time.Now()),
	)
	if err != nil {
		t.Fatalf("la ingesta fallo: %v", err)
	}

	if reader.cerrado {
		t.Error("DesdeReader no debe cerrar el reader: no es dueno del recurso")
	}
}

// spyReader registra si alguien lo cerro.
type spyReader struct {
	contenido string
	cerrado   bool
}

func (r *spyReader) Read(p []byte) (int, error) {
	if r.cerrado {
		return 0, errors.New("reader cerrado")
	}
	n := copy(p, r.contenido)
	r.contenido = r.contenido[n:]
	if r.contenido == "" {
		return n, io.EOF
	}
	return n, nil
}

func (r *spyReader) Close() error {
	r.cerrado = true
	return nil
}

// TestDesdeRutaSiCierraElArchivoEsOpcional: el parser no es dueno del archivo,
// pero el caso de uso si, y debe cerrarlo aunque el reader no lo exponga.
func TestMetadatosDelArchivo(t *testing.T) {
	transcript, err := transcriptor(t).DesdeRuta(context.Background(), rutaFixture("reunion-equipo.md"))
	if err != nil {
		t.Fatalf("la ingesta fallo: %v", err)
	}

	meta := transcript.Metadata

	if meta.FileName != "reunion-equipo.md" {
		t.Errorf("FileName = %q", meta.FileName)
	}
	if meta.Extension != ".md" {
		t.Errorf("Extension = %q", meta.Extension)
	}
	if meta.SizeInBytes <= 0 {
		t.Errorf("SizeInBytes deberia ser mayor que cero, es %d", meta.SizeInBytes)
	}
	if meta.ModifiedAt.IsZero() {
		t.Error("ModifiedAt no fue poblada")
	}
	if transcript.IngestedAt.IsZero() {
		t.Error("IngestedAt no fue poblada")
	}
}

// TestIngestaEsRapida comprueba el presupuesto de RNF-03 para la etapa de I/O y
// parseo local, que debe ser holgadamente inferior a 50 ms por documento.
//
// La asercion es deliberadamente laxa (200 ms) porque medir en un entorno de
// CI compartido es ruidoso. El objetivo es detectar una regresion grosera, como
// cargar el archivo completo en memoria sin limite, no fijar un benchmark.
func TestIngestaEsRapida(t *testing.T) {
	ing := transcriptor(t)
	ctx := context.Background()

	const iteraciones = 5

	inicio := time.Now()
	for i := 0; i < iteraciones; i++ {
		if _, err := ing.DesdeRuta(ctx, rutaFixture("minuta.docx")); err != nil {
			t.Fatalf("la ingesta fallo: %v", err)
		}
	}
	porDocumento := time.Since(inicio) / iteraciones

	if porDocumento > 200*time.Millisecond {
		t.Errorf("la ingesta tardo %v por documento, el presupuesto de RNF-03 es 50ms", porDocumento)
	}

	t.Logf("ingesta: %v por documento (%d documentos)", porDocumento, iteraciones)
}

// TestIngestaConSelectorFalso comprueba que el caso de uso depende de la
// interfaz y no del paquete concreto: sustituir el registro por un doble debe
// funcionar sin cambiar una linea de produccion.
func TestIngestaConSelectorFalso(t *testing.T) {
	selector := &selectorFalso{}

	ing := application.NuevaIngestTranscriptor(selector)

	transcript, err := ing.DesdeReader(
		context.Background(),
		strings.NewReader("contenido de la reunion"),
		".txt",
		domain.NewDocumentMetadata("notas.txt", ".txt", 23, time.Now()),
	)
	if err != nil {
		t.Fatalf("la ingesta con el doble fallo: %v", err)
	}

	if !strings.Contains(transcript.Content, "reunion") {
		t.Errorf("el contenido no llego al transcript: %q", transcript.Content)
	}

	if !selector.usado {
		t.Error("el selector inyectado no fue consultado")
	}
}

// selectorFalso implementa application.SelectorDeParser.
type selectorFalso struct {
	usado bool
}

func (s *selectorFalso) ParserParaExtension(extension string) (domain.DocumentParser, error) {
	s.usado = true
	return &parserInmediato{}, nil
}

func (s *selectorFalso) DetectarPorContenido(cabecera []byte) (domain.DocumentParser, error) {
	return nil, nil
}

type parserInmediato struct{}

func (p *parserInmediato) CanParse(extension string) bool { return true }

func (p *parserInmediato) Parse(ctx context.Context, reader io.Reader) (string, error) {
	datos, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(datos)), nil
}
