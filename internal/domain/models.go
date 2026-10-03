package domain

import (
	"fmt"
	"strings"
	"time"
)

// MaxTituloLen es el limite de longitud del titulo de un ActionItem.
//
// El valor 100 esta fijado por el contrato, no es arbitrario: proviene de
// backlog_schema.json ("maxLength": 100) y de la etiqueta
// `validate:"required,max=100"` del struct. Si se cambia aqui, debe cambiar en
// los tres lugares a la vez. El test TestContratoTituloSincronizado lo verifica
// contra el schema real para que no se desincronice en silencio.
const MaxTituloLen = 100

// MaxEtiquetas limita el numero de labels por item. Protege al adaptador de
// publicacion de payloads descontrolados.
const MaxEtiquetas = 10

// MaxPuntosHistoria acota la estimacion. Cero significa "no estimado" y esta
// permitido; un valor negativo o excesivo es un error del LLM.
const MaxPuntosHistoria = 21

// Priority representa la urgencia de un ActionItem.
//
// Es un value object: se compara por contenido y solo admite los tres valores
// del enum. Cualquier otro valor es invalido y debe rechazarse con
// ErrPriorityInvalida en lugar de convertirse silenciosamente en un default.
type Priority string

const (
	PriorityHigh   Priority = "HIGH"
	PriorityMedium Priority = "MEDIUM"
	PriorityLow    Priority = "LOW"
)

// prioridadesValidas es el unico conjunto de valores admitidos por el enum.
// Se mantiene en un slice para poder validarlo y para que los tests puedan
// comprobar que el enum y el schema siguen sincronizados.
var prioridadesValidas = []Priority{PriorityHigh, PriorityMedium, PriorityLow}

// String implementa fmt.Stringer.
func (p Priority) String() string { return string(p) }

// EsValida informa si el valor pertenece al enum.
//
// Debe usarse en los limites del dominio: al validar la respuesta del LLM y al
// validar una configuracion leida de disco.
func (p Priority) EsValida() bool {
	for _, v := range prioridadesValidas {
		if p == v {
			return true
		}
	}
	return false
}

// ParsePriority convierte una cadena en Priority validando contra el enum.
//
// A diferencia de Priority(s), esta funcion falla de forma explicita: es el
// unico punto donde una cadena arbitraria se convierte en un value object.
func ParsePriority(s string) (Priority, error) {
	p := Priority(strings.ToUpper(strings.TrimSpace(s)))
	if !p.EsValida() {
		return "", fmt.Errorf("%w: recibido %q", ErrPriorityInvalida, s)
	}
	return p, nil
}

// ActionItem es un elemento de backlog extraido de una reunion.
//
// Es la entidad central del dominio. La separacion entre RawAssignee y
// MappedHandle refleja las etapas del flujo: el LLM llena RawAssignee con el
// nombre tal como aparece en la minuta, y el IdentityMapper llena
// MappedHandle en la etapa siguiente. Mezclarlos perderia trazabilidad.
type ActionItem struct {
	// Title es el titulo imperativo y autocontenido de la tarea.
	Title string `json:"title" validate:"required,max=100"`
	// Description aporta contexto, criterios de aceptacion y dependencias.
	Description string `json:"description" validate:"required"`
	// RawAssignee es el nombre real o mencion textual tal como aparece en la
	// minuta. Lo produce el LLM.
	RawAssignee string `json:"assignee_name" validate:"required"`
	// MappedHandle es el handle de plataforma resuelto por el IdentityMapper.
	// Lo escribe la etapa de identidad, no el LLM.
	MappedHandle string `json:"mapped_handle,omitempty"`
	// Priority es la urgencia. Solo admite HIGH, MEDIUM o LOW.
	Priority Priority `json:"priority" validate:"required,oneof=HIGH MEDIUM LOW"`
	// Labels son etiquetas de dominio. Puede estar vacio.
	Labels []string `json:"labels"`
	// StoryPoints es la estimacion de esfuerzo. Cero significa "no estimado".
	StoryPoints int `json:"story_points,omitempty"`
}

// Validar hace cumplir las invariantes del item.
//
// Devuelve un error envuelto con %w para que el llamador pueda distinguir la
// causa con errors.Is. A diferencia de los adaptadores, aqui no hay magic
// numbers: todos los limites provienen de constantes o del schema.
func (a ActionItem) Validar() error {
	titulo := strings.TrimSpace(a.Title)
	if titulo == "" {
		return fmt.Errorf("%w: el titulo esta vacio", ErrTituloInvalido)
	}
	if len([]rune(titulo)) > MaxTituloLen {
		return fmt.Errorf("%w: %d caracteres exceden el maximo de %d",
			ErrTituloInvalido, len([]rune(titulo)), MaxTituloLen)
	}

	if strings.TrimSpace(a.Description) == "" {
		return fmt.Errorf("%w: la descripcion esta vacia", ErrDescripcionInvalida)
	}

	if strings.TrimSpace(a.RawAssignee) == "" {
		return fmt.Errorf("%w: el nombre esta vacio", ErrAsignadoInvalido)
	}

	if !a.Priority.EsValida() {
		return fmt.Errorf("%w: recibido %q", ErrPriorityInvalida, a.Priority)
	}

	if len(a.Labels) > MaxEtiquetas {
		return fmt.Errorf("%w: %d etiquetas exceden el maximo de %d",
			ErrSchemaViolation, len(a.Labels), MaxEtiquetas)
	}

	if a.StoryPoints < 0 || a.StoryPoints > MaxPuntosHistoria {
		return fmt.Errorf("%w: story_points=%d fuera del rango [0, %d]",
			ErrSchemaViolation, a.StoryPoints, MaxPuntosHistoria)
	}

	return nil
}

// ResumenNormalizado devuelve titulo, prioridad y responsable en una sola linea,
// pensado para la salida tabular del modo dry-run (RF-06).
func (a ActionItem) ResumenNormalizado() string {
	responsable := a.MappedHandle
	if responsable == "" {
		responsable = a.RawAssignee + " (sin resolver)"
	}
	return fmt.Sprintf("%s | %s | %s | %d | %v",
		a.Title, a.Priority, responsable, a.StoryPoints, a.Labels)
}

// MeetingBacklogExtraction es el resultado de procesar una transcripcion.
//
// Es la estructura que el LLM debe producir y que valida contra
// docs/specifications/backlog_schema.json (RF-03). Es el unico tipo que cruza
// la frontera entre la fase de extraccion y la de despacho.
type MeetingBacklogExtraction struct {
	// MeetingSummary resume la reunion de la que provienen los items.
	MeetingSummary string `json:"meeting_summary" validate:"required"`
	// ActionItems son los elementos de backlog extraidos.
	ActionItems []ActionItem `json:"action_items" validate:"required,dive"`
}

// Validar hace cumplir las invariantes del agregado entero.
//
// Un solo item invalido invalida la extraccion completa: emitir un backlog
// parcialmente corrupto es peor que fallar y pedir al LLM que se corrija
// (RF-07), porque un item con prioridad invalida acabaria publicado como
// cualquier otra cosa en el tablero del equipo.
func (m MeetingBacklogExtraction) Validar() error {
	if strings.TrimSpace(m.MeetingSummary) == "" {
		return fmt.Errorf("%w: meeting_summary esta vacio", ErrSchemaViolation)
	}

	if len(m.ActionItems) == 0 {
		return fmt.Errorf("%w: action_items esta vacio, no hay nada que publicar",
			ErrSchemaViolation)
	}

	for i, item := range m.ActionItems {
		if err := item.Validar(); err != nil {
			return fmt.Errorf("%w: action_items[%d]: %w", ErrSchemaViolation, i, err)
		}
	}

	return nil
}

// ResumenConstruye una linea por item, para la salida en modo dry-run.
func (m MeetingBacklogExtraction) Resumen() []string {
	lineas := make([]string, 0, len(m.ActionItems))
	for _, item := range m.ActionItems {
		lineas = append(lineas, item.ResumenNormalizado())
	}
	return lineas
}

// PublishResult es el resultado de intentar publicar un item en la plataforma
// destino.
//
// Existe para que un fallo parcial sea representable: si de cinco tarjetas solo
// fracasa una, el resto ya se creo y el usuario debe saber exactamente cuales.
type PublishResult struct {
	// TaskTitle identifica el item de origen.
	TaskTitle string
	// ExternalID es el identificador que devuelve la plataforma (por ejemplo el
	// node_id de GraphQL).
	ExternalID string
	// URL es el enlace permanente a la tarjeta creada.
	URL string
	// Success indica si la tarjeta quedo creada.
	Success bool
	// Error describe el fallo cuando Success es false.
	Error error
}

// Err devuelve el error de la publicacion o nil si la operacion fue exitosa.
//
// Permite que los llamantes traten el resultado con un unico idiom a.
func (p PublishResult) Err() error { return p.Error }

// ResumenConstruye una linea descriptiva del resultado para el log final.
func (p PublishResult) Resumen() string {
	if p.Success {
		return fmt.Sprintf("OK    %s -> %s", p.TaskTitle, p.URL)
	}
	return fmt.Sprintf("FALLO %s -> %v", p.TaskTitle, p.Error)
}

// Estados posibles de un ExtractionJob.
const (
	JobPendiente  = "PENDING"
	JobEnCurso    = "RUNNING"
	JobCompletado = "COMPLETED"
	JobFallido    = "FAILED"
)

// ExtractionJob es el agregado raiz que orquesta el procesamiento de una
// transcripcion.
//
// El ID es el identificador de correlacion que aparece en cada linea de log
// (RNF-06). RawText puede contener datos sensibles de la reunion, por lo que
// quien lo registre en logs debe hacerlo con cuidado.
type ExtractionJob struct {
	ID         string
	CreatedAt  time.Time
	Status     string
	RawText    string
	Result     *MeetingBacklogExtraction
	Transcript *Transcript
}

// NuevoExtractionJob crea un job en estado pendiente con la marca de tiempo
// actual. Concentra aqui la inicializacion para que ningun llamador olvide
// CreatedAt y termine con un zero time en los logs.
func NuevoExtractionJob(id, rawText string) *ExtractionJob {
	return &ExtractionJob{
		ID:        id,
		CreatedAt: time.Now(),
		Status:    JobPendiente,
		RawText:   rawText,
	}
}

// MarcarEnCurso transiciona el job a RUNNING.
func (j *ExtractionJob) MarcarEnCurso() { j.Status = JobEnCurso }

// MarcarCompletado transiciona el job a COMPLETED y adjunta el resultado.
func (j *ExtractionJob) MarcarCompletado(result *MeetingBacklogExtraction) {
	j.Status = JobCompletado
	j.Result = result
}

// MarcarFallido transiciona el job a FAILED. Se mantiene el resultado parcial
// si lo hubiera, para poder diagnosticar que se extrajo antes del fallo.
func (j *ExtractionJob) MarcarFallido() { j.Status = JobFallido }

// Duracion devuelve el tiempo transcurrido desde la creacion del job. Es la
// metrica que permite comprobar RNF-03.
func (j *ExtractionJob) Duracion() time.Duration {
	return time.Since(j.CreatedAt)
}
