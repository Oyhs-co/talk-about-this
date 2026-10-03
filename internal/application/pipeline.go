package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"talkaboutthis/internal/domain"
)

// Pipeline ejecuta el flujo completo: ingesta, extraccion y despacho.
//
// Existe para que cmd/talkaboutthis no orchestration nada. El composition root
// solo debe cablear dependencias y llamar a un caso de uso; si main.go
// encadenara las tres etapas, la orquestacion quedaria fuera de la capa de
// aplicacion y sin cobertura de tests.
//
// ORDEN DE LAS ETAPAS Y SUS MOTIVOS
//
//  1. Ingesta: falla pronto y barato si el archivo no existe o no se entiende.
//  2. Extraccion: es la unica etapa cara (llamada al LLM). No se intenta si la
//     ingesta ya fallo.
//  3. Despacho: en dry-run no llega a tocar la red (TC-05).
type Pipeline struct {
	ingestor   *IngestTranscriptor
	extractor  Extractor
	publicador *PublicarBacklog
}

// Extractor extrae el backlog de un transcript.
//
// Es el contrato que la capa de aplicacion necesita de la extraccion. Lo
// satisface ExtractBacklog, y tambien un doble en los tests.
type Extractor interface {
	Ejecutar(ctx context.Context, transcript string) (*domain.MeetingBacklogExtraction, error)
}

// PipelineEtapa identifica las fases del flujo, para logs y diagnostico.
type PipelineEtapa string

const (
	EtapaIngesta    PipelineEtapa = "ingesta"
	EtapaExtraccion PipelineEtapa = "extraccion"
	EtapaDespacho   PipelineEtapa = "despacho"
)

// PipelineConfiguracion son los parametros de una ejecucion.
type PipelineConfiguracion struct {
	// Ruta del documento a ingerir.
	Ruta string
	// ProjectRef es el tablero destino. Solo se usa en modo publish.
	ProjectRef string
	// Modo dry-run o publish.
	Modo ModoSalida
	// JobID es el identificador de correlacion de los logs.
	JobID string
}

// PipelineResultado es el resultado de una ejecucion completa.
type PipelineResultado struct {
	// Transcript es el documento ingerido.
	Transcript *domain.Transcript
	// Extraccion es el backlog extraido.
	Extraccion *domain.MeetingBacklogExtraction
	// Despacho es el resumen de la etapa de despacho. Nil si no llego a ella.
	Despacho *ResumenDespacho
	// JobID identifica la ejecucion.
	JobID string
	// Duracion total del flujo.
	Duracion time.Duration
}

// Etapas completadas devuelve los nombres de las etapas ejecutadas, en orden.
//
// Existe para que el error final indique DONDE se rompio el flujo, en lugar de
// devolver un error sin contexto que obliga a leer todo el log.
func (p PipelineResultado) EtapasCompletadas() []PipelineEtapa {
	etapas := make([]PipelineEtapa, 0, 3)

	if p.Transcript != nil {
		etapas = append(etapas, EtapaIngesta)
	}
	if p.Extraccion != nil {
		etapas = append(etapas, EtapaExtraccion)
	}
	if p.Despacho != nil {
		etapas = append(etapas, EtapaDespacho)
	}

	return etapas
}

// ErrorEtapa envuelve un error indicando en que fase se produjo.
type ErrorEtapa struct {
	Etapa PipelineEtapa
	Err   error
}

func (e *ErrorEtapa) Error() string {
	return fmt.Sprintf("fallo en la etapa de %s: %v", e.Etapa, e.Err)
}

func (e *ErrorEtapa) Unwrap() error { return e.Err }

// NewPipeline construye el pipeline.
//
// Las tres dependencias son obligatorias: sin ellas el flujo no tiene sentido, y
// es preferible fallar en la construccion que en la primera ejecucion.
func NewPipeline(ingestor *IngestTranscriptor, extractor Extractor, publicador *PublicarBacklog) (*Pipeline, error) {
	if ingestor == nil {
		return nil, fmt.Errorf("%w: falta el caso de uso de ingesta", domain.ErrConfigInvalida)
	}
	if extractor == nil {
		return nil, fmt.Errorf("%w: falta el extractor de backlog", domain.ErrConfigInvalida)
	}
	if publicador == nil {
		return nil, fmt.Errorf("%w: falta el caso de uso de despacho", domain.ErrConfigInvalida)
	}

	return &Pipeline{
		ingestor:   ingestor,
		extractor:  extractor,
		publicador: publicador,
	}, nil
}

// Ejecutar corre el flujo completo.
//
// Devuelve el error envuelto en ErrorEtapa para que el llamador pueda presentar
// un mensaje que diga en que fase fallo, y conservar el encadenado de errores con
// errors.Is / errors.As para el codigo de salida.
func (p *Pipeline) Ejecutar(ctx context.Context, cfg PipelineConfiguracion) (*PipelineResultado, error) {
	inicio := time.Now()

	jobID := cfg.JobID
	if jobID == "" {
		jobID = "sin-id"
	}

	// Si el llamador ya injectó un logger (la CLI lo hace, con el job_id puesto),
	// se respeta tal cual. Reanadir el job_id lo duplicaria en cada linea, y un
	// log con el mismo campo repetido es ruido que esconde el resto.
	log := loggerDe(ctx)
	if !hayLoggerEnContexto(ctx) {
		log = log.With(slog.String("job_id", jobID))
	}
	ctx = ConLoggerEnContexto(ctx, log)

	log.Info("iniciando procesamiento",
		slog.String("etapa", string(EtapaIngesta)),
		slog.String("archivo", cfg.Ruta),
		slog.String("modo", string(cfg.Modo)),
	)

	resultado := &PipelineResultado{JobID: jobID}

	// --- 1. Ingesta ---
	transcript, err := p.ingestor.DesdeRuta(ctx, cfg.Ruta)
	if err != nil {
		log.Error("fallo la ingesta",
			slog.String("etapa", string(EtapaIngesta)),
			slog.String("error", err.Error()),
		)
		resultado.Duracion = time.Since(inicio)
		return resultado, &ErrorEtapa{Etapa: EtapaIngesta, Err: err}
	}

	resultado.Transcript = transcript

	log.Info("ingesta completada",
		slog.String("etapa", string(EtapaIngesta)),
		slog.String("archivo", transcript.Metadata.FileName),
		slog.Int("caracteres", transcript.Longitud()),
	)

	// --- 2. Extraccion ---
	log.Info("iniciando extraccion",
		slog.String("etapa", string(EtapaExtraccion)),
	)

	extraccion, err := p.extractor.Ejecutar(ctx, transcript.Content)
	if err != nil {
		log.Error("fallo la extraccion",
			slog.String("etapa", string(EtapaExtraccion)),
			slog.String("error", err.Error()),
		)
		resultado.Duracion = time.Since(inicio)
		return resultado, &ErrorEtapa{Etapa: EtapaExtraccion, Err: err}
	}

	resultado.Extraccion = extraccion

	log.Info("extraccion completada",
		slog.String("etapa", string(EtapaExtraccion)),
		slog.Int("items", len(extraccion.ActionItems)),
	)

	// --- 3. Despacho ---
	log.Info("iniciando despacho",
		slog.String("etapa", string(EtapaDespacho)),
		slog.String("modo", string(cfg.Modo)),
	)

	resumenDespacho, err := p.publicador.Ejecutar(ctx, cfg.ProjectRef, extraccion.ActionItems, cfg.Modo)
	if err != nil {
		log.Error("fallo el despacho",
			slog.String("etapa", string(EtapaDespacho)),
			slog.String("error", err.Error()),
		)
		resultado.Duracion = time.Since(inicio)
		return resultado, &ErrorEtapa{Etapa: EtapaDespacho, Err: err}
	}

	resultado.Despacho = resumenDespacho
	resultado.Duracion = time.Since(inicio)

	log.Info("procesamiento completado",
		slog.Int("items", len(extraccion.ActionItems)),
		slog.String("duracion", resultado.Duracion.String()),
	)

	return resultado, nil
}

// EsFalloDePublicacion indica si un error del pipeline corresponde a la etapa
// de despacho.
//
// Lo usa la CLI para elegir el codigo de salida: publicar tres de cinco tarjetas
// y fallar dos NO es lo mismo que no haber podido leer el archivo, y un proceso
// de CI debe poder distinguirlos.
func EsFalloDePublicacion(err error) bool {
	var etapa *ErrorEtapa
	if errors.As(err, &etapa) {
		return etapa.Etapa == EtapaDespacho
	}
	return false
}

// EtapaDelError devuelve la etapa en la que fallo el flujo, o cadena vacia.
func EtapaDelError(err error) PipelineEtapa {
	var etapa *ErrorEtapa
	if errors.As(err, &etapa) {
		return etapa.Etapa
	}
	return ""
}
