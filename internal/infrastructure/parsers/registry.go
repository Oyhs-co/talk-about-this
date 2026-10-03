package parsers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"talkaboutthis/internal/domain"
)

// Registry selecciona el parser adecuado para cada documento.
//
// Es el punto unico donde vive el conocimiento de "que formatos soportamos".
// Anadir soporte para .pdf o .json consiste en registrar un DocumentParser mas,
// sin tocar ningun otro paquete: es la extension natural del Principio
// Open-Closed que AGENTS.md exige.
//
// La seleccion es por extension, con un respaldo que inspecciona el contenido.
// Ese respaldo existe porque en la practica los usuarios guardan una minuta
// exportada como "notas.txt" cuando en realidad es un .docx, y fallar con un
// error de formato seria menos util que intentarlo.
type Registry struct {
	// parsers se conserva como slice, no como mapa, para que ante varias
	// coincidencias sea determinista: gana el que se registro primero.
	parsers []domain.DocumentParser
}

// NewRegistry construye un registro vacio.
func NewRegistry() *Registry {
	return &Registry{}
}

// NewRegistryPorDefecto devuelve un registro con los parsers de RF-01 (.md,
// .txt y .docx) ya registrados.
//
// El orden importa para el respaldo por contenido: se registra DOCX primero
// porque su firma (PK) es inequivoca, mientras que Markdown y texto plano son
// indistinguibles a nivel de bytes.
func NewRegistryPorDefecto() *Registry {
	return NewRegistry().
		Registrar(NewDocxParser()).
		Registrar(NewMarkdownParser()).
		Registrar(NewTXTParser())
}

// Registrar anade un parser al registro.
//
// Se devuelven los metodos encadenables para que el registro se construya de
// forma declarativa en el composition root.
func (r *Registry) Registrar(parser domain.DocumentParser) *Registry {
	if parser != nil {
		r.parsers = append(r.parsers, parser)
	}
	return r
}

// SoportanExtensiones devuelve la lista de extensiones registradas, ordenada.
//
// Existe para que el mensaje de error que ve el usuario sea util: en lugar de
// "formato no soportado", puede saber que convertir el archivo o usar otra
// extension.
func (r *Registry) SoportanExtensiones() []string {
	vistas := make(map[string]bool)

	for _, parser := range r.parsers {
		// Se recorren las extensiones conocidas del dominio. Mantener la lista
		// aqui evita que cada parser exponga su propia extension.
		for _, ext := range []string{
			domain.ExtensionMarkdown,
			domain.ExtensionTexto,
			domain.ExtensionDocx,
		} {
			if parser.CanParse(ext) {
				vistas[ext] = true
			}
		}
	}

	salida := make([]string, 0, len(vistas))
	for ext := range vistas {
		salida = append(salida, ext)
	}

	sort.Strings(salida)
	return salida
}

// ParserParaExtension devuelve el parser registrado que reconoce la extension.
func (r *Registry) ParserParaExtension(extension string) (domain.DocumentParser, error) {
	ext := normalizarExtension(extension)

	for _, parser := range r.parsers {
		if parser.CanParse(ext) {
			return parser, nil
		}
	}

	return nil, fmt.Errorf(
		"%w: la extension %q no corresponde a ningun formato soportado (soportados: %s)",
		domain.ErrFormatoNoSoportado,
		ext,
		strings.Join(r.SoportanExtensiones(), ", "),
	)
}

// Parsear selecciona el parser y extrae el texto en un solo paso.
//
// Es la forma que usa el caso de uso de ingesta. Para reservar el reader (por
// ejemplo, para detectar el formato por contenido antes de parsear) existe
// ParserParaExtension.
func (r *Registry) Parsear(ctx context.Context, extension string, reader io.Reader) (string, error) {
	parser, err := r.ParserParaExtension(extension)
	if err != nil {
		return "", err
	}

	texto, err := parser.Parse(ctx, reader)
	if err != nil {
		return "", fmt.Errorf("el parser de %q no pudo extraer el texto: %w", extension, err)
	}

	return texto, nil
}

// DetectarPorContenido intenta identificar el formato leyendo los primeros bytes.
//
// Cubre el caso real en que la extension no corresponde al contenido: una
// minuta exportada desde Word guardada como .txt, o un .docx renombrado.
//
// Devuelve (nil, nil) si ningun parser reconoce el contenido. No es un error:
// quien llama decide si el respaldo es obligatorio (ingesta desde disco) o
// accesorio (llamada directa).
func (r *Registry) DetectarPorContenido(cabecera []byte) (domain.DocumentParser, error) {
	for _, parser := range r.parsers {
		firmador, ok := parser.(Firmador)
		if !ok {
			// Los parsers que no saben detectar su formato (Markdown, texto
			// plano) no participan en el respaldo: distinguirlos por bytes es
			// imposible, y adivinar seria peor que no intentarlo.
			continue
		}

		if firmador.DetectaFirma(cabecera) {
			return parser, nil
		}
	}

	return nil, nil
}

// Firmador es una interfaz opcional que implementan los parsers capaces de
// reconocer su formato por contenido.
//
// No forma parte de domain a proposito: es una capacidad concreta de la
// infraestructura. Anadirla al nucleo solo para eso ensuciaria el contrato
// principal, y el puerto DocumentParser debe seguir siendo minimo.
type Firmador interface {
	// DetectaFirma informa si la cabecera corresponde a este formato.
	DetectaFirma(cabecera []byte) bool
}

// firmaZIP son los dos primeros bytes de todo contenedor ZIP/OOXML: "PK".
var firmaZIP = []byte{'P', 'K'}

// ErrDocumentoVacio indica que el archivo no tiene contenido.
var ErrDocumentoVacio = errors.New("el documento esta vacio")

// normalizarExtension deja la extension en un formato comparable: minusculas,
// con punto inicial y sin espacios.
func normalizarExtension(extension string) string {
	ext := strings.ToLower(strings.TrimSpace(extension))

	if ext == "" {
		return ""
	}

	// Se acepta tanto ".md" como "md": los flags de la CLI y los valores de
	// filepath.Ext no coinciden siempre.
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}

	return ext
}

// ExtensionDeRuta extrae y normaliza la extension de una ruta de archivo.
func ExtensionDeRuta(ruta string) string {
	return normalizarExtension(filepath.Ext(ruta))
}
