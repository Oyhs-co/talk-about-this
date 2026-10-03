package parsers

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"

	"talkaboutthis/internal/domain"
)

// DocxParser extrae texto plano de archivos .docx (RF-01, TC-01).
//
// Un .docx es un contenedor ZIP (Office Open XML) que guarda el contenido en
// word/document.xml como XML. Este parser lo descomprime y extrae el texto de
// los nodos <w:t>, uniendo los parrafos con saltos de linea.
//
// Se usa SOLO la biblioteca estandar (archive/zip + encoding/xml) en lugar de
// una dependencia de terceros. Es coherente con el objetivo de INIT.md seccion 1
// de un binario estatico y ligero, y para este subconjunto de OOXML la
// complejidad adicional de una libreria externa no se justifica.
//
// Limitacion conocida y deliberada: se ignoran imagenes, graficos y el formato
// tipografico (negrita, color). Para el proposito del proyecto eso es correcto,
// porque el objetivo es extraer tareas, no reproducir el documento.
type DocxParser struct{}

// NewDocxParser construye el parser de DOCX.
func NewDocxParser() *DocxParser { return &DocxParser{} }

// CanParse implementa domain.DocumentParser.
func (p *DocxParser) CanParse(extension string) bool {
	return normalizarExtension(extension) == domain.ExtensionDocx
}

// Parse implementa domain.DocumentParser.
//
// A diferencia de los otros parsers, aqui no puede usarse leerTexto: el
// contenido esta comprimido dentro del ZIP, asi que se descomprime primero y se
// aplica la normalizacion al texto ya extraido.
func (p *DocxParser) Parse(ctx context.Context, reader io.Reader) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	if reader == nil {
		return "", errors.New("se intento leer un documento nil")
	}

	// El archivo comprimido se limita tambien: un .docx es mayor que su texto,
	// pero no deberia pasar de decenas de megabytes.
	// zip.NewReader exige un io.ReaderAt con su tamaño, y io.LimitReader solo
	// devuelve io.Reader. Por eso el archivo comprimido se lee completo en
	// memoria (con techo) y se le pasa un *bytes.Reader, que si es ReaderAt.
	// Para un .docx de reunion es razonable: son unos cientos de kilobytes.
	crudo, err := leerBytesLimitados(reader)
	if err != nil {
		return "", err
	}

	zipReader, err := zip.NewReader(bytes.NewReader(crudo), int64(len(crudo)))
	if err != nil {
		return "", fmt.Errorf("el archivo no es un contenedor ZIP valido: %w", err)
	}

	documento, err := abrirParte(zipReader, "word/document.xml")
	if err != nil {
		return "", err
	}

	// El descriptor real del archivo lo abre y cierra el caso de uso de
	// ingesta: este parser recibe un io.Reader ya abierto y nunca tiene uno
	// propio, porque trabaja sobre los bytes ya leidos en memoria. Por eso no
	// hay ningun os.File que cerrar aqui (TC-01).
	//
	// Lo que si debe cerrarse es el reader de la parte ZIP.
	entrada, err := documento.Open()
	if err != nil {
		return "", fmt.Errorf("no se pudo abrir %s dentro del ZIP: %w", documento.Name, err)
	}
	defer func() {
		_ = entrada.Close()
	}()

	// La entrada descomprimida tambien se limita: dentro de un ZIP valido puede
	// declararse un tamaño enorme (la "bomba ZIP").
	limite := int64(maxBytesDocumento)
	if documento.UncompressedSize64 < uint64(limite) {
		limite = int64(documento.UncompressedSize64)
	}

	contenido, err := io.ReadAll(io.LimitReader(entrada, limite))
	if err != nil {
		return "", fmt.Errorf("no se pudo descomprimir %s: %w", documento.Name, err)
	}

	texto, err := extraerTextoWordprocessingML(contenido)
	if err != nil {
		return "", err
	}

	return normalizarTexto([]byte(texto)), nil
}

// Asercion de compilacion.
var _ domain.DocumentParser = (*DocxParser)(nil)

// ErrNoEsDocx indica que el ZIP no contiene la parte principal de Word.
var ErrNoEsDocx = errors.New("el archivo no parece un documento .docx: falta word/document.xml")

// leerBytesLimitados lee un reader completo con un techo de tamano.
//
// Es la contrapartida en bytes de leerTexto para los formatos comprimidos:
// zip.NewReader necesita io.ReaderAt, que io.LimitReader no puede dar.
func leerBytesLimitados(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, errors.New("se intento leer un documento nil")
	}

	// El +1 permite distinguir "justo en el limite" de "lo excede".
	bruto, err := io.ReadAll(io.LimitReader(r, maxBytesDocumento+1))
	if err != nil {
		return nil, err
	}

	if int64(len(bruto)) > maxBytesDocumento {
		return nil, ErrDocumentoDemasiadoGrande
	}

	return bruto, nil
}

// abrirParte localiza una entrada del ZIP por nombre.
func abrirParte(leer *zip.Reader, nombre string) (*zip.File, error) {
	for _, archivo := range leer.File {
		if archivo.Name == nombre {
			return archivo, nil
		}
	}

	return nil, fmt.Errorf("%w (se encontraron %d entradas)", ErrNoEsDocx, len(leer.File))
}

// espacioDeNombresWord es el namespace de WordprocessingML.
//
// Los prefijos de namespace pueden cambiar entre documentos (w:, w14:, etc.), de
// modo que la comparacion se hace por el namespace local del elemento, no por
// la cadena "w:t" del XML crudo.
const espacioDeNombresWord = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"

// extraerTextoWordprocessingML recorre word/document.xml y concatena el texto
// de los parrafos.
//
// Se usa un Decoder XML en modo token en lugar de Unmarshal sobre una estructura
// completa porque el XML de Word es grande y muy variable (campos, hipervinculos,
// saltos de formato): recorrerlo por tokens permite conservar el texto sea cual
// sea la profundidad del anidamiento, incluidas las tablas.
func extraerTextoWordprocessingML(documento []byte) (string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(documento))
	decoder.Strict = true

	var (
		// esTexto marca que se esta dentro de un nodo <w:t>: el contenido de
		// otros elementos del XML no es texto del documento.
		esTexto bool

		// descartarTexto cubre <instrText> y <delText>: codigo de campo y
		// texto eliminado Revision, que nunca forman parte del contenido.
		descartarTexto bool

		// parrafoActual delimita la acumulacion: el texto solo se recoge entre
		// <w:p> y </w:p>, de modo que la maquetacion de la pagina no se cuela.
		parrafoActual bool

		// ultimoCaracter evita duplicar espacios y tabuladores cuando el XML
		// contiene marcas de formato consecutivas.
		ultimoCaracter rune

		fragmento strings.Builder
		parrafos  []string
	)

	for {
		token, err := decoder.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return "", fmt.Errorf("XML de Word malformed: %w", err)
		}

		switch t := token.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "t":
				// Solo el namespace de Word se considera. Un <t> de otro
				// namespace no es texto del documento.
				if t.Name.Space == espacioDeNombresWord {
					esTexto = true
					descartarTexto = false
				}

			case "instrText", "delText":
				// Codigo de campo y texto eliminado: nunca es contenido.
				esTexto = true
				descartarTexto = true

			case "tab":
				if parrafoActual && ultimoCaracter != '\t' {
					fragmento.WriteRune('\t')
					ultimoCaracter = '\t'
				}

			case "br", "cr":
				// Un salto explicito dentro de un parrafo se conserva como salto
				// de linea, no como espacio. En una minuta el autor lo puso para
				// separar ideas, y aplanarlo produciria frases sin puntuacion
				// ("decision tomada Se descarta...") que el LLM interpreta peor.
				if parrafoActual && ultimoCaracter != '\n' {
					fragmento.WriteRune('\n')
					ultimoCaracter = '\n'
				}

			case "p":
				parrafoActual = true
			}

		case xml.CharData:
			if esTexto && descartarTexto {
				continue
			}
			if !esTexto {
				continue
			}

			texto := string(t)
			if parrafoActual {
				fragmento.WriteString(texto)
				if runes := []rune(texto); len(runes) > 0 {
					ultimoCaracter = runes[len(runes)-1]
				}
			}

		case xml.EndElement:
			switch t.Name.Local {
			case "t", "instrText", "delText":
				esTexto = false
				descartarTexto = false

			case "p":
				parrafoActual = false

				contenido := strings.TrimSpace(fragmento.String())
				fragmento.Reset()
				ultimoCaracter = 0

				// Los parrafos vacios se descartan: Word los usa para separar
				// bloques y no aportan contenido.
				if contenido != "" {
					parrafos = append(parrafos, contenido)
				}
			}
		}
	}

	if len(parrafos) == 0 {
		return "", errors.New("el documento no contiene texto extraible")
	}

	return strings.Join(parrafos, "\n"), nil
}

// DetectaFirma implementa parsers.Firmador.
//
// La firma de un contenedor OOXML son los dos primeros bytes "PK", comunes a
// todo formato ZIP. Es una comprobacion deliberadamente laxa: distingue un
// .docx de un .txt, que es exactamente lo que hace falta para el respaldo por
// contenido, y la validacion real ocurre despues al buscar word/document.xml.
func (p *DocxParser) DetectaFirma(cabecera []byte) bool {
	if len(cabecera) < len(firmaZIP) {
		return false
	}

	return cabecera[0] == firmaZIP[0] && cabecera[1] == firmaZIP[1]
}

// Asercion de compilacion de la capacidad opcional de deteccion por firma.
var _ Firmador = (*DocxParser)(nil)
