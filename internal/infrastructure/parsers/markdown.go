package parsers

import (
	"context"
	"io"
	"strings"

	"talkaboutthis/internal/domain"
)

// MarkdownParser extrae texto plano de archivos .md (RF-01).
//
// Convierte Markdown en el texto que leeria una persona. El objetivo no es
// faithfully conservar la sintaxis: es quitar el ruido de formato para que el
// LLM vea los hechos, no los asteriscos.
//
// Criterio de diseno importante: los bloques de codigo se PRESERVAN tal cual.
// Una minuta puede citar una consulta SQL o un comando, y ese contenido es
// precisamente la especificacion de la tarea que hay que extraer. Aplanar el
// codigo produciria un item inutil.
type MarkdownParser struct{}

// NewMarkdownParser construye el parser de Markdown.
func NewMarkdownParser() *MarkdownParser { return &MarkdownParser{} }

// CanParse implementa domain.DocumentParser.
func (p *MarkdownParser) CanParse(extension string) bool {
	return normalizarExtension(extension) == domain.ExtensionMarkdown
}

// Parse implementa domain.DocumentParser.
func (p *MarkdownParser) Parse(ctx context.Context, reader io.Reader) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	texto, err := leerTexto(reader)
	if err != nil {
		return "", err
	}

	texto = eliminarFrontMatter(texto)
	texto = limpiarMarkdown(texto)

	return texto, nil
}

// Asercion de compilacion.
var _ domain.DocumentParser = (*MarkdownParser)(nil)

// eliminarFrontMatter retira el bloque YAML inicial (delimitado por ---).
//
// El front-matter suele contener metadatos (fecha, asistentes) que no son
// acciones. Eliminarlo completo evita que esas lineas se interpreten como
// tareas: sin este paso, "fecha: 2026-09-28" seria un candidato a item de
// backlog.
func eliminarFrontMatter(texto string) string {
	// El front-matter solo cuenta si el documento empieza con "---".
	if !strings.HasPrefix(texto, "---") {
		return texto
	}

	lineas := strings.Split(texto, "\n")

	// Se busca el cierre "---". Si no existe, no es front-matter: podría ser un
	// separador horizontal legitimo y destructuirlo seria un error de datos.
	for i := 1; i < len(lineas); i++ {
		if strings.TrimSpace(lineas[i]) == "---" {
			return strings.Join(lineas[i+1:], "\n")
		}
	}

	return texto
}

// limpiarMarkdown aplica las transformaciones de formato linea a linea.
//
// Se procesa linea a linea porque Markdown depende de la estructura de linea:
// un asterisco abre un enfasis y lo cierra en otra linea. Un reemplazo global
// sobre el texto completo no podria distinguir esos casos.
func limpiarMarkdown(texto string) string {
	lineas := strings.Split(texto, "\n")
	salida := make([]string, 0, len(lineas))

	dentroDeCodigo := false

	for _, linea := range lineas {
		// Las cercas ``` abren y cierran bloques de codigo, cuyo contenido se
		// conserva intacto.
		if strings.HasPrefix(strings.TrimSpace(linea), "```") {
			dentroDeCodigo = !dentroDeCodigo
			continue // la cerca tampoco aporta informacion
		}

		if dentroDeCodigo {
			salida = append(salida, linea)
			continue
		}

		salida = append(salida, limpiarLineaMarkdown(linea))
	}

	return strings.Join(salida, "\n")
}

// limpiarLineaMarkdown elimina las marcas de formato de una linea de prosa.
func limpiarLineaMarkdown(linea string) string {
	// Citas: ">" al inicio.
	linea = strings.TrimSpace(strings.TrimLeft(linea, " \t"))
	for strings.HasPrefix(linea, ">") {
		linea = strings.TrimSpace(strings.TrimPrefix(linea, ">"))
	}

	// Encabezados: "## " al inicio. El texto se conserva: "Acuerdos" es
	// informacion valuable sobre lo que sigue.
	for esEncabezado(linea) {
		linea = strings.TrimSpace(strings.TrimLeft(linea, "#"))
	}

	// Listas ordenadas: "1. ", "1) ".
	linea = quitarPrefijoListaOrdenada(linea)

	// Viñetas: "- ", "* ", "+ ".
	for {
		recortada := strings.TrimSpace(strings.TrimLeft(linea, " \t"))
		if len(recortada) >= 2 &&
			(recortada[0] == '-' || recortada[0] == '*' || recortada[0] == '+') &&
			(recortada[1] == ' ' || recortada[1] == '\t') {
			linea = recortada[2:]
			continue
		}
		break
	}

	// Reglas horizontales: entirely composed of dashes, asterisks or underscores.
	if esReglaHorizontal(linea) {
		return ""
	}

	// Enfasis inline: **negrita**, *cursiva*, __negrita__, _cursiva_.
	linea = quitarEnfasis(linea)

	// Enlaces: [texto](url) se queda con el texto; la URL es ruido para el LLM.
	linea = quitarEnlaces(linea)

	return strings.TrimRight(linea, " \t")
}

// esEncabezado indica si la linea empieza por uno o mas almohadillas seguidas
// de espacio. Exigir el espacio evita que "#1" o "#hashtag" se traten como
// encabezado.
func esEncabezado(linea string) bool {
	pos := 0
	for pos < len(linea) && linea[pos] == '#' {
		pos++
	}
	if pos == 0 || pos > 6 {
		return false
	}
	return pos < len(linea) && (linea[pos] == ' ' || linea[pos] == '\t')
}

// quitarPrefijoListaOrdenada elimina "1. " o "1) " del inicio de la linea.
func quitarPrefijoListaOrdenada(linea string) string {
	i := 0
	for i < len(linea) && linea[i] >= '0' && linea[i] <= '9' {
		i++
	}

	if i == 0 || i >= len(linea) {
		return linea
	}

	// Se limita a tres digitos: "2026. Something" no es una lista.
	if i > 3 {
		return linea
	}

	if linea[i] == '.' || linea[i] == ')' {
		if i+1 < len(linea) && (linea[i+1] == ' ' || linea[i+1] == '\t') {
			return strings.TrimLeft(linea[i+2:], " \t")
		}
	}

	return linea
}

// esReglaHorizontal detecta "---", "***" o "___" (y variantes mas largas).
func esReglaHorizontal(linea string) bool {
	if len(linea) < 3 {
		return false
	}

	caracter := linea[0]
	if caracter != '-' && caracter != '*' && caracter != '_' {
		return false
	}

	for i := 0; i < len(linea); i++ {
		if linea[i] != caracter && linea[i] != ' ' {
			return false
		}
	}

	return true
}

// quitarEnfasis elimina los marcadores de enfasis conservando el texto.
//
// Los pares (asterisco doble, guion bajo doble) se quitan de una vez: **esto**
// es negrita y __esto__ tambien, y en ambos casos el texto es lo que importa.
//
// El asterisco SIMPLE exige mas cuidado, porque aparece tambien fuera de
// Markdown: en "2*3" (multiplicacion) y en identificadores. Un reemplazo ingenuo
// partiria las expresiones matematicas y dejaria basura frente al LLM.
//
// En lugar de eso se decide asterisco por asterisco, aplicando la misma regla a
// la apertura y al cierre: un asterisco es delimitador si actua sobre un limite
// de palabra. Asi "*Ana*" pierde ambos, y "2*3" no pierde ninguno.
//
// El guion bajo simple se conserva a proposito: aparece en snake_case, que es
// codigo real y no enfasis, y en una transcripcion tecnica es mas frecuente que
// el cursivo. Es una decision consciente, no un descuido.
func quitarEnfasis(linea string) string {
	linea = strings.ReplaceAll(linea, "**", "")
	linea = strings.ReplaceAll(linea, "__", "")

	var b strings.Builder
	b.Grow(len(linea))

	for i := 0; i < len(linea); i++ {
		if linea[i] != '*' {
			b.WriteByte(linea[i])
			continue
		}

		if esDelimitadorDeEnfasis(linea, i) {
			continue // se descarta
		}

		b.WriteByte(linea[i])
	}

	return b.String()
}

// esDelimitadorDeEnEmphasis indica si el asterisco de la posicion pos abre o
// cierra un enfasis, es decir, si actua sobre un limite de palabra.
func esDelimitadorDeEnfasis(linea string, pos int) bool {
	precede := pos > 0 && !esEspacio(linea[pos-1])
	sigue := pos+1 < len(linea) && !esEspacio(linea[pos+1])

	// Apertura: al principio de la linea o tras un espacio, seguido de contenido.
	abre := !precede && sigue

	// Cierre: precedido de contenido y seguido del fin de linea o de un espacio.
	cierra := precede && !sigue

	return abre || cierra
}

// esEspacio indica si el byte es un espacio o un tabulador.
func esEspacio(b byte) bool {
	return b == ' ' || b == '	'
}

// quitarEnlaces convierte [texto](url) en solo el texto.
//
// La URL se descarta porque para extraer una tarea importa que hay que hacer,
// no donde esta documentado. Ademas, los enlaces souvent apuntan a issues que
// el LLM interpretaria como trabajo pendiente ya hecho.
func quitarEnlaces(linea string) string {
	var b strings.Builder
	b.Grow(len(linea))

	for i := 0; i < len(linea); {
		// [texto](url)
		if linea[i] == '[' {
			cierraTexto := strings.IndexByte(linea[i:], ']')
			if cierraTexto > 0 {
				posCierre := i + cierraTexto
				if posCierre+1 < len(linea) && linea[posCierre+1] == '(' {
					cierraParen := strings.IndexByte(linea[posCierre:], ')')
					if cierraParen > 0 {
						b.WriteString(linea[i+1 : posCierre])
						i = posCierre + cierraParen + 1
						continue
					}
				}
			}
		}

		b.WriteByte(linea[i])
		i++
	}

	return b.String()
}
