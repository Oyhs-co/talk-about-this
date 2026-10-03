package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"talkaboutthis/internal/application"
	"talkaboutthis/internal/domain"
)

// salidaJSON de un resultado.
//
// Mapea el resultado del pipeline a una estructura estable para consumo por
// maquinas. Se define aqui, en el borde, y no en el dominio: el formato de
// salida es una decision de presentacion que no debe filtrarse hasta el nucleo.
type salidaJSON struct {
	JobID          string          `json:"job_id"`
	Archivo        string          `json:"archivo"`
	Modo           string          `json:"modo"`
	ResumenReunion string          `json:"meeting_summary"`
	Items          []salidaItem    `json:"action_items"`
	Despacho       *salidaDespacho `json:"despacho,omitempty"`
	DuracionMs     int64           `json:"duracion_ms"`
	Etapas         []string        `json:"etapas"`
}

// salidaItem es un item del backlog en formato JSON.
type salidaItem struct {
	Titulo      string   `json:"title"`
	Descripcion string   `json:"description"`
	Responsable string   `json:"assignee_name"`
	Handle      string   `json:"mapped_handle,omitempty"`
	Prioridad   string   `json:"priority"`
	Etiquetas   []string `json:"labels"`
	Estimacion  int      `json:"story_points,omitempty"`
}

// salidaDespacho resume lo ocurrido en la etapa de publicacion.
type salidaDespacho struct {
	Plataforma string          `json:"plataforma,omitempty"`
	Publicados []salidaTarjeta `json:"publicados"`
	Omitidos   []salidaOmitido `json:"omitidos,omitempty"`
	Exitos     int             `json:"exitos"`
	Fallos     int             `json:"fallos"`
}

// salidaTarjeta es el resultado de publicar un item.
type salidaTarjeta struct {
	Titulo     string `json:"title"`
	Exito      bool   `json:"success"`
	URL        string `json:"url,omitempty"`
	ExternalID string `json:"external_id,omitempty"`
	Error      string `json:"error,omitempty"`
}

// salidaOmitido es un item descartado por la politica de identidades.
type salidaOmitido struct {
	Titulo      string `json:"title"`
	Responsable string `json:"assignee_name"`
	Motivo      string `json:"motivo"`
}

// renderizar escribe el resultado en el formato pedido.
func renderizar(w io.Writer, resultado *application.PipelineResultado, formato string) error {
	switch formato {
	case "table":
		return renderizarTabla(w, resultado)
	case "json", "":
		return renderizarJSON(w, resultado)
	default:
		return fmt.Errorf("formato de salida desconocido: %q", formato)
	}
}

// renderizarJSON emite un documento JSON indentado.
func renderizarJSON(w io.Writer, resultado *application.PipelineResultado) error {
	salida := construirSalidaJSON(resultado)

	// SetEscapeHTML(false) evita que un "&" o un "<" en una descripcion de tarea
	// aparezcan como entities, que es comun en comentarios que hablan de HTML.
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)

	return encoder.Encode(salida)
}

// construirSalidaJSON proyecta el resultado del pipeline a la forma pública.
func construirSalidaJSON(resultado *application.PipelineResultado) salidaJSON {
	salida := salidaJSON{
		JobID:      resultado.JobID,
		Modo:       "",
		DuracionMs: resultado.Duracion.Milliseconds(),
	}

	for _, etapa := range resultado.EtapasCompletadas() {
		salida.Etapas = append(salida.Etapas, string(etapa))
	}

	if resultado.Transcript != nil {
		salida.Archivo = resultado.Transcript.Metadata.FileName
	}

	// El modo se lee del despacho, que puede ser nil si el flujo no llego a esa
	// etapa; se comprueba antes de desreferenciarlo.
	if resultado.Despacho != nil {
		salida.Modo = string(resultado.Despacho.Modo)
	}

	if resultado.Extraccion != nil {
		salida.ResumenReunion = resultado.Extraccion.MeetingSummary

		for _, item := range resultado.Extraccion.ActionItems {
			etiquetas := item.Labels
			if etiquetas == nil {
				// Se normaliza a arreglo vacio: en JSON, null y [] significan
				// cosas distintas y un consumidor riguroso fallaria con null.
				etiquetas = []string{}
			}

			salida.Items = append(salida.Items, salidaItem{
				Titulo:      item.Title,
				Descripcion: item.Description,
				Responsable: item.RawAssignee,
				Handle:      item.MappedHandle,
				Prioridad:   string(item.Priority),
				Etiquetas:   etiquetas,
				Estimacion:  item.StoryPoints,
			})
		}
	}

	if resultado.Despacho == nil {
		return salida
	}

	despacho := &salidaDespacho{
		Publicados: []salidaTarjeta{},
	}

	for _, publicado := range resultado.Despacho.Publicados {
		tarjeta := salidaTarjeta{
			Titulo:     publicado.TaskTitle,
			Exito:      publicado.Success,
			URL:        publicado.URL,
			ExternalID: publicado.ExternalID,
		}

		if publicado.Error != nil {
			tarjeta.Error = publicado.Error.Error()
		}

		despacho.Publicados = append(despacho.Publicados, tarjeta)

		if publicado.Success {
			despacho.Exitos++
		} else {
			despacho.Fallos++
		}
	}

	for _, omitido := range resultado.Despacho.Omitidos {
		despacho.Omitidos = append(despacho.Omitidos, salidaOmitido{
			Titulo:      omitido.Title,
			Responsable: omitido.RawAssignee,
			Motivo:      "responsable no resuelto en la tabla de aliases",
		})
	}

	salida.Despacho = despacho

	return salida
}

// renderizarTabla emite una vista legible para uso interactivo.
//
// En dry-run NO imprime la seccion de despacho: no hubo ninguno, y una tabla con
// ceros de "publicados 0" solo confundiria.
func renderizarTabla(w io.Writer, resultado *application.PipelineResultado) error {
	if resultado.Transcript == nil || resultado.Extraccion == nil {
		return nil
	}

	metadatos := resultado.Transcript.Metadata

	fmt.Fprintf(w, "Archivo:  %s\n", metadatos.FileName)
	fmt.Fprintf(w, "Modo:     %s\n", modoDe(resultado))
	fmt.Fprintf(w, "Resumen:  %s\n\n", resultado.Extraccion.MeetingSummary)

	tabla := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	fmt.Fprintln(tabla, "PRIORIDAD\tRESPONSABLE\tESTIMACION\tTITULO")
	// En dry-run las identidades no se resuelven (garantia TC-05), asi que
	// no debe marcarse el responsable como no resuelto.
	esDryRun := resultado.Despacho != nil && resultado.Despacho.Modo == application.ModoDryRun

	for _, item := range resultado.Extraccion.ActionItems {
		fmt.Fprintf(tabla, "%s\t%s\t%s\t%s\n",
			prioridadOrdenada(item.Priority),
			responsableLegible(item, esDryRun),
			estimacionLegible(item.StoryPoints),
			tituloEnLinea(item.Title),
		)
	}

	if err := tabla.Flush(); err != nil {
		return err
	}

	fmt.Fprintf(w, "\nTotal: %d item(s)\n", len(resultado.Extraccion.ActionItems))

	renderizarDespacho(w, resultado.Despacho)

	return nil
}

// renderizarDespacho imprime el resumen de publicacion, si lo hubo.
func renderizarDespacho(w io.Writer, despacho *application.ResumenDespacho) {
	if despacho == nil || len(despacho.Publicados) == 0 {
		return
	}

	exitos, fallos := despacho.ResumenDePublicacion()

	fmt.Fprintf(w, "\nPublicacion: %d correcto(s), %d con error\n", exitos, fallos)

	tabla := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tabla, "ESTADO\tTITULO\tURL")

	for _, resultado := range despacho.Publicados {
		estado := "OK"
		url := resultado.URL

		if !resultado.Success {
			estado = "ERROR"
			if resultado.Error != nil {
				url = resultado.Error.Error()
			}
		}

		fmt.Fprintf(tabla, "%s\t%s\t%s\n", estado, tituloEnLinea(resultado.TaskTitle), url)
	}

	_ = tabla.Flush()

	for _, omitido := range despacho.Omitidos {
		fmt.Fprintf(w, "OMITIDO\t%s\t(responsable no resuelto: %s)\n",
			tituloEnLinea(omitido.Title), omitido.RawAssignee)
	}
}

// prioridadOrdenada ordena la tabla por urgencia, no por orden alfabetico.
func prioridadOrdenada(p domain.Priority) string {
	switch p {
	case domain.PriorityHigh:
		return "ALTA"
	case domain.PriorityMedium:
		return "MEDIA"
	case domain.PriorityLow:
		return "BAJA"
	default:
		// Una prioridad invalida no deberia llegar aqui: el dominio la rechaza
		// antes. Se muestra tal cual para que sea visible si se colara.
		return string(p)
	}
}

// responsableLegible muestra el handle resuelto, o el nombre con una marca cuando
// no se pudo resolver.
//
// La marca importa: si el usuario ve "Omar Hernández" sin más, puede creer que
// se asigno a la persona correcta cuando en realidad el item quedo huerfano.
//
// EN DRY-RUN NO SE MARCA, y es un detalle importante. El dry-run no consulta la
// tabla de aliases (es la garantia de TC-05: cero llamadas de red), asi que el
// handle siempre esta vacio. Marcarlo como "SIN RESOLVER" ensuciaria cada fila
// con una alarma que no corresponde: en dry-run no se intento resolver nada, y el
// usuario ya sabe que no se publico. El nombre en claro es lo honesto.
func responsableLegible(item domain.ActionItem, esDryRun bool) string {
	if item.MappedHandle != "" {
		return item.MappedHandle
	}

	if esDryRun {
		return item.RawAssignee
	}

	return item.RawAssignee + " (SIN RESOLVER)"
}

// estimacionLegible muestra los puntos de historia o un guion.
func estimacionLegible(puntos int) string {
	if puntos <= 0 {
		return "-"
	}

	return fmt.Sprintf("%d", puntos)
}

// tituloEnLinea colapsa los saltos de linea de un titulo.
//
// Un titulo con saltos romperia la alineacion de la tabla y la haria ilegible.
func tituloEnLinea(titulo string) string {
	reemplazado := strings.ReplaceAll(titulo, "\n", " ")
	reemplazado = strings.ReplaceAll(reemplazado, "\r", " ")
	reemplazado = strings.ReplaceAll(reemplazado, "\t", " ")

	return strings.TrimSpace(reemplazado)
}

// modoDe devuelve el modo de salida como texto.
func modoDe(resultado *application.PipelineResultado) string {
	if resultado.Despacho == nil {
		return "desconocido"
	}

	return string(resultado.Despacho.Modo)
}
