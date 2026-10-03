// Paquete application contiene los casos de uso de TalkAboutThis: la
// orquestacion que coordina los puertos del dominio.
//
// La capa application conoce las interfaces de internal/domain pero NO importa
// internal/infrastructure. Por eso este caso de uso recibe un
// domain.DocumentParser inyectado desde el composition root en lugar de
// construirlo: si importara el paquete parsers, la dependencia inviertida se
// romperia.
package application

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"talkaboutthis/internal/domain"
)

// bytesCabecera es el tamaño de la muestra que se lee para detectar el formato
// por contenido. La firma "PK" de un ZIP esta en los dos primeros bytes; se
// leen 512 por margen ante variantes.
const bytesCabecera = 512

// IngestTranscriptor ingiere un documento y devuelve un Transcript homogeneo.
//
// Depende de un selector de parser inyectado, no del paquete parsers concreto.
// El selector se declara aqui como una interfaz local para no ensuciar el
// dominio con un concepto que es propio de la orquestacion.
type IngestTranscriptor struct {
	// selector resuelve el parser adecuado a partir de una extension, y sabe
	// reconocer el formato por contenido cuando la extension engaña.
	selector SelectorDeParser

	// ahora es inyectable para que los tests puedan fijar el reloj y comprobar
	// el cumplimiento del presupuesto de tiempo de RNF-03.
	ahora func() time.Time
}

// SelectorDeParser abstrae el registro de parsers.
//
// Vive en application y no en domain a proposito: el dominio define el puerto
// DocumentParser (un parser individual), pero "seleccionar entre varios" es una
// necesidad de orquestacion. Mantenerla aqui evita ampliar el nucleo con un
// concepto que el dominio no necesita.
type SelectorDeParser interface {
	// ParserParaExtension devuelve el parser que reconoce la extension.
	ParserParaExtension(extension string) (domain.DocumentParser, error)

	// DetectarPorContenido identifica el formato leyendo una cabecera.
	// Devuelve (nil, nil) cuando ningun parser reconoce el contenido.
	DetectarPorContenido(cabecera []byte) (domain.DocumentParser, error)
}

// NuevaIngestTranscriptor construye el caso de uso con el reloj del sistema.
func NuevaIngestTranscriptor(selector SelectorDeParser) *IngestTranscriptor {
	return &IngestTranscriptor{
		selector: selector,
		ahora:    time.Now,
	}
}

// NuevaIngestTranscriptorConReloj permite inyectar el reloj en los tests.
func NuevaIngestTranscriptorConReloj(selector SelectorDeParser, ahora func() time.Time) *IngestTranscriptor {
	ing := NuevaIngestTranscriptor(selector)
	if ahora != nil {
		ing.ahora = ahora
	}
	return ing
}

// DesdeRuta ingiere un archivo del disco.
//
// A diferencia de DesdeReader, este metodo es dueno del archivo y lo cierra
// siempre con defer, inmediatamente despues de abrirlo (TC-01). Ese detalle no
// es cosmetico: procesar cientos de documentos sin cerrarlos agota los
// descriptores del proceso y el fallo aparece mas tarde, en otro archivo.
func (i *IngestTranscriptor) DesdeRuta(ctx context.Context, ruta string) (*domain.Transcript, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	info, err := os.Stat(ruta)
	if err != nil {
		return nil, fmt.Errorf("no se pudo leer el archivo %q: %w", ruta, err)
	}

	if info.IsDir() {
		return nil, fmt.Errorf(
			"%w: %q es un directorio, se esperaba un archivo",
			domain.ErrFormatoNoSoportado, ruta,
		)
	}

	archivo, err := os.Open(ruta)
	if err != nil {
		return nil, fmt.Errorf("no se pudo abrir %q: %w", ruta, err)
	}

	// TC-01: el archivo se cierra al salir de la funcion, tanto en el camino de
	// exito como en cualquier error intermedio.
	defer func() {
		_ = archivo.Close()
	}()

	metadatos := domain.NewDocumentMetadata(
		filepath.Base(ruta),
		filepath.Ext(ruta),
		info.Size(),
		info.ModTime(),
	)

	// DesdeReader toma la propiedad del reader para poder leer la cabecera sin
	// consumirla: se envuelve en un bufio.Reader y se le pasa el reader completo.
	contenido, err := i.desdeReader(ctx, archivo, metadatos, ExtensionDe(ruta))
	if err != nil {
		return nil, err
	}

	return contenido, nil
}

// DesdeReader ingiere contenido ya abierto, sin asumir su procedencia.
//
// Se usa desde pipes (stdin) y desde los tests. El reader NO se cierra: es
// dueno de quien lo abrio. Este contraste con DesdeRuta es deliberado y
// mantiene clara la regla de propiedad del recurso.
func (i *IngestTranscriptor) DesdeReader(ctx context.Context, reader io.Reader, extension string, metadatos domain.DocumentMetadata) (*domain.Transcript, error) {
	return i.desdeReader(ctx, reader, metadatos, extension)
}

// desdeReader es la implementacion comun a ambos caminos de entrada.
//
// El reader se envuelve en bufio porque se lee una cabecera para detectar el
// formato y hay que devolver esos mismos bytes al parser: MultiReader los
// reinyecta despues de la muestra.
func (i *IngestTranscriptor) desdeReader(ctx context.Context, reader io.Reader, metadatos domain.DocumentMetadata, extension string) (*domain.Transcript, error) {
	buffered := newBufferedReader(reader)

	cabecera, err := buffered.Peek(bytesCabecera)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("no se pudo leer el inicio de %q: %w", metadatos.FileName, err)
	}

	parser, err := i.seleccionarParser(extension, cabecera)
	if err != nil {
		return nil, err
	}

	texto, err := parser.Parse(ctx, buffered)
	if err != nil {
		return nil, fmt.Errorf(
			"no se pudo extraer texto de %q con el parser %T: %w",
			metadatos.FileName, parser, err,
		)
	}

	transcript := domain.NewTranscript(texto, metadatos)
	transcript.IngestedAt = i.ahora()

	// Se valida aqui, antes de gastar una llamada al LLM: un archivo vacio o
	// ilegible debe fallar en la ingesta, no con un error confuso mas adelante.
	if err := transcript.Validar(); err != nil {
		return nil, err
	}

	return transcript, nil
}

// seleccionarParser resuelve el parser para el documento.
//
// El ORDEN importa y es deliberadamente el inverso al intuitivo: primero se
// consulta el contenido y despues la extension.
//
// El motivo es el caso real que motiva esta funcion. Una minuta exportada desde
// Word y guardada como "notas.txt" tiene una extension perfectamente valida, de
// modo que consultar la extension primero devolveria el parser de texto plano y
// devolveria bytes comprimidos como si fueran prosa. Detectando antes, la firma
// "PK" identifica el DOCX real.
//
// La deteccion solo responde para formatos con firma inequivoca (los que
// implementan parsers.Firmador). Markdown y texto plano son indistinguibles a
// nivel de bytes, asi que en esos casos se recurre a la extension, que es la
// unica informacion disponible.
func (i *IngestTranscriptor) seleccionarParser(extension string, cabecera []byte) (domain.DocumentParser, error) {
	porContenido, err := i.selector.DetectarPorContenido(cabecera)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", domain.ErrFormatoNoSoportado, err)
	}

	if porContenido != nil {
		return porContenido, nil
	}

	parser, errExtension := i.selector.ParserParaExtension(extension)
	if errExtension != nil {
		// Ni la extension ni el contenido apuntan a un formato conocido.
		return nil, errExtension
	}

	return parser, nil
}

// ExtensionDe normaliza la extension de una ruta.
func ExtensionDe(ruta string) string {
	ext := filepath.Ext(ruta)
	if ext == "" {
		return ""
	}
	return ext
}

// newBufferedReader envuelve el reader para poder inspeccionar su inicio sin
// consumirlo.
func newBufferedReader(r io.Reader) *bufio.Reader {
	return bufio.NewReader(r)
}
