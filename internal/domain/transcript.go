package domain

import (
	"fmt"
	"strings"
	"time"
)

// Extensiones soportadas por la ingesta. La arquitectura no las restringe: un
// DocumentParser nuevo se registra y se reconoce su extension sin tocar el
// dominio. Se listan aqui solo para los mensajes de error legibles.
const (
	ExtensionMarkdown = ".md"
	ExtensionTexto    = ".txt"
	ExtensionDocx     = ".docx"
)

// DocumentMetadata son los metadatos del archivo de entrada.
//
// Es un value object: describe el origen del contenido y no tiene identidad
// propia. Los parsers lo rellenan a medida que descubren datos durante la
// lectura.
type DocumentMetadata struct {
	// FileName es el nombre del archivo sin directorio.
	FileName string
	// Extension incluye el punto inicial, en minusculas (".docx").
	Extension string
	// SizeInBytes es el tamano del archivo original, 0 si se desconocido.
	SizeInBytes int64
	// ModifiedAt es la fecha de modificacion del archivo. Puede ser el zero
	// time si el sistema de archivos no la expone.
	ModifiedAt time.Time
	// Title es el titulo del documento cuando el formato lo provee
	// (front-matter de Markdown, propiedades de DOCX). Vacio si no existe.
	Title string
}

// NewDocumentMetadata construye los metadatos normalizando la extension a
// minusculas y Ensuring del punto inicial.
//
// Normalizar aqui evita que cada parser repita el mismo trabajo y que un
// archivo "NOTAS.MD" falle el CanParse de un parser bien escrito.
func NewDocumentMetadata(fileName, extension string, size int64, modifiedAt time.Time) DocumentMetadata {
	ext := strings.ToLower(strings.TrimSpace(extension))
	if ext != "" && !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	return DocumentMetadata{
		FileName:    fileName,
		Extension:   ext,
		SizeInBytes: size,
		ModifiedAt:  modifiedAt,
	}
}

// Transcript es la entidad que representa el contexto ya ingerido.
//
// Es la frontera entre el bounded context de Ingestion y el de Extraction: el
// parser devuelve un Transcript homogeneo, con independencia de que el origen
// fuera .md, .txt o .docx. A partir de aqui, ningun componente posterior sabe
// cual fue el formato original.
//
// Content debe ser texto plano UTF-8 limpio.
type Transcript struct {
	// Content es el texto plano extraido, sin marcas de formato.
	Content string
	// Metadata describe el archivo de origen.
	Metadata DocumentMetadata
	// IngestedAt es la marca de tiempo de la ingesta, para medir RNF-03.
	IngestedAt time.Time
}

// NewTranscript construye un Transcript con la marca de tiempo actual.
func NewTranscript(content string, metadata DocumentMetadata) *Transcript {
	return &Transcript{
		Content:    content,
		Metadata:   metadata,
		IngestedAt: time.Now(),
	}
}

// EsVacio indica si el transcript no contiene contenido util.
//
// Un archivo valido pero vacio debe fallar antes de gastar una llamada al LLM:
// la etapa de extraccion no puede producir un backlog a partir de nada, y
// reportarlo como vacio es mas honesto que inventar un resultado.
func (t *Transcript) EsVacio() bool {
	return strings.TrimSpace(t.Content) == ""
}

// Longitud devuelve el numero de runes del contenido.
//
// Se cuenta en runes, no en bytes, para que un documento en español o con
// emojis no se mida mal al decidir si supera un limite de prompt.
func (t *Transcript) Longitud() int { return len([]rune(t.Content)) }

// Validar hace cumplir las invariantes de la entidad ingestada.
func (t *Transcript) Validar() error {
	if t == nil {
		return fmt.Errorf("%w: transcript nil", ErrFormatoNoSoportado)
	}
	if t.EsVacio() {
		return fmt.Errorf("%w: %s no contiene texto tras la limpieza",
			ErrFormatoNoSoportado, t.Metadata.FileName)
	}
	return nil
}
