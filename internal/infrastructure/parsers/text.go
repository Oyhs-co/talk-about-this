// Paquete parsers contiene las implementaciones concretas de
// domain.DocumentParser (RF-01).
//
// Cada parser traduce un formato de archivo a texto plano UTF-8 y nada mas.
// Ninguno debe interpretar el contenido ni tomar decisiones de negocio: eso
// es trabajo de la etapa de extraccion. Un parser que "entiende" una minuta
// acoplaria el formato a reglas que cambian con cada reunion.
package parsers

import (
	"bufio"
	"errors"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxBytesDocumento pone un techo defensivo a la lectura.
//
// Sin este limite, un archivo accidentalmente enorme (o un .docx con la
// "bomba ZIP") agotaria la memoria del proceso. 32 MiB es muy holgado para una
// transcripcion de reunion y mantiene el techo lejos del uso legitimo.
const maxBytesDocumento = 32 << 20

// ErrDocumentoDemasiadoGrande indica que el archivo supera el techo de lectura.
// Se envuelve con %w para que el llamador pueda compararlo con errors.Is.
var ErrDocumentoDemasiadoGrande = errors.New("el documento supera el tamano maximo permitido")

// leerTexto lee un io.Reader completo como texto UTF-8.
//
// Es el helper comun a todos los parsers y concentra las garantias que el
// dominio necesita recibir:
//
//   - No lee mas de maxBytesDocumento.
//   - Elimina el BOM UTF-8, presente a menudo en .txt generados en Windows.
//   - Sustituye secuencias UTF-8 invalidas en vez de fallar: una minuta con un
//     byte roto debe producir texto utilizable, no un error.
//
// No cierra el reader: es responsabilidad de quien lo abrio (el caso de uso de
// ingesta), lo que permite envolver el reader con buferes intermedios.
func leerTexto(r io.Reader) (string, error) {
	if r == nil {
		return "", errors.New("se intento leer un documento nil")
	}

	// bufio evita una llamada al sistema por cada Read, lo que importa en
	// archivos de varios megabytes. El LimitReader lleva +1 para poder
	// distinguir "justo en el limite" de "lo excede".
	limitado := io.LimitReader(r, maxBytesDocumento+1)

	bruto, err := io.ReadAll(bufio.NewReader(limitado))
	if err != nil {
		return "", err
	}

	if int64(len(bruto)) > maxBytesDocumento {
		return "", ErrDocumentoDemasiadoGrande
	}

	return normalizarTexto(bruto), nil
}

// normalizarTexto limpia los bytes leidos para producir texto plano estable.
//
// Las reglas son deliberadamente conservadoras: se elimina el ruido que
// confunde al LLM (BOM, caracteres de control, espacios redundantes) pero NO se
// toca el contenido de las palabras. Quitar comillas o asteriscos de una
// transcripcion es responsabilidad de los parsers especificos; aqui solo se
// normaliza lo que es inequivocamente formato.
func normalizarTexto(bruto []byte) string {
	texto := string(bruto)

	// BOM UTF-8: Word y algunos editores de Windows lo anteponen al archivo.
	texto = strings.TrimPrefix(texto, "\uFEFF")

	// Secuencias UTF-8 invalidas: se sustituyen por U+FFFD en lugar de fallar,
	// porque un byte roto no invalida el resto de la transcripcion.
	if !utf8.ValidString(texto) {
		texto = strings.ToValidUTF8(texto, string(utf8.RuneError))
	}

	// Normalizacion de saltos de linea: CRLF y CR sueltos pasan a LF.
	texto = strings.ReplaceAll(texto, "\r\n", "\n")
	texto = strings.ReplaceAll(texto, "\r", "\n")

	// Caracteres de control, conservando tabulador y salto de linea. Word
	// inserta vertical tab y form feed que, sin limpiar, llegan al LLM como ruido.
	texto = eliminarControles(texto)

	lineas := strings.Split(texto, "\n")
	for i, linea := range lineas {
		lineas[i] = limpiarEspaciosLinea(linea)
	}

	return strings.Join(lineas, "\n")
}

// eliminarControles descarta los caracteres de control salvo tabulador y salto
// de linea. El byte nulo tampoco se conserva: nunca significa nada en una
// transcripcion.
func eliminarControles(texto string) string {
	var b strings.Builder
	b.Grow(len(texto))

	for _, r := range texto {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r == '\x00' || unicode.IsControl(r):
			// Se descarta.
		default:
			b.WriteRune(r)
		}
	}

	return b.String()
}

// limpiarEspaciosLinea normaliza los espacios de una linea.
//
// Los finales se eliminan porque no aportan informacion. Los iniciales se
// colapsan a un unico tabulador cuando hay sangria profunda, porque una
// subtarea anidada dentro de una lista es informacion estructural que el LLM
// puede aprovechar para inferir dependencias.
func limpiarEspaciosLinea(linea string) string {
	linea = strings.TrimRight(linea, " \t")

	i := 0
	for i < len(linea) && (linea[i] == ' ' || linea[i] == '\t') {
		i++
	}

	if i == 0 {
		return linea
	}

	if i >= 2 {
		return "\t" + linea[i:]
	}

	return linea[i:]
}
