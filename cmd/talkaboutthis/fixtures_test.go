package main

import (
	"time"

	"talkaboutthis/internal/application"
	"talkaboutthis/internal/domain"
)

// resultadoDePrueba construye un resultado de pipeline completo y realista.
//
// Se construye a mano, sin pasar por el pipeline ni por el LLM, para que los
// tests de presentacion aislen el renderizado: si fallan, el problema esta en
// la salida, no en la extraccion.
func resultadoDePrueba() *application.PipelineResultado {
	extraccion := &domain.MeetingBacklogExtraction{
		MeetingSummary: "Reunion de sincronizacion del modulo de pagos.",
		ActionItems: []domain.ActionItem{
			{
				Title:        "Migrar la sesion",
				Description:  "Sacarla del contexto global a un middleware.",
				RawAssignee:  "Omar Hernández",
				MappedHandle: "omarhernan",
				Priority:     domain.PriorityHigh,
				Labels:       []string{"backend"},
				StoryPoints:  3,
			},
			{
				Title:       "Documentar los reintentos",
				Description: "Describir la politica de reintentos del webhook.",
				RawAssignee: "Persona Sin Registrar",
				Priority:    domain.PriorityMedium,
				Labels:      nil, // se normaliza a arreglo vacio en la salida JSON
			},
		},
	}

	return &application.PipelineResultado{
		JobID:      "job-prueba",
		Transcript: transcriptDePrueba(),
		Extraccion: extraccion,
		Despacho: &application.ResumenDespacho{
			Modo:  application.ModoDryRun,
			Items: extraccion.ActionItems,
		},
		Duracion: 1500 * time.Millisecond,
	}
}

// transcriptDePrueba es un documento ingerido minimo.
func transcriptDePrueba() *domain.Transcript {
	metadatos := domain.NewDocumentMetadata("notas.md", ".md", 1024, time.Now())

	return domain.NewTranscript(
		"Reunion de sincronizacion del modulo de pagos.\n\nMigrar la sesion.",
		metadatos,
	)
}

// resultadoParcialDePrueba simula un flujo que se detuvo en la extraccion.
//
// Sirve para comprobar que el JSON sigue siendo valido cuando el despacho no llego
// a ocurrir: una salida malformada en ese caso haria que un usuario no pudiera
// ni ver lo que si se extrajo.
func resultadoParcialDePrueba() *application.PipelineResultado {
	return &application.PipelineResultado{
		JobID:      "job-parcial",
		Transcript: transcriptDePrueba(),
		Extraccion: &domain.MeetingBacklogExtraction{
			MeetingSummary: "Resumen parcial.",
			ActionItems: []domain.ActionItem{
				{Title: "Tarea", Description: "D", RawAssignee: "Ana", Priority: domain.PriorityLow},
			},
		},
		// Despacho queda nil a proposito.
		Duracion: time.Second,
	}
}

// resultadoPublicadoDePrueba construye un resultado en modo publish.
//
// Existe separado del de dry-run porque el comportamiento de la columna de
// responsable cambia segun el modo: en dry-run no se resuelven identidades y no
// hay nada que marcar; en publish, un responsable sin resolver es un problema
// real que el usuario debe ver.
func resultadoPublicadoDePrueba() *application.PipelineResultado {
	resultado := resultadoDePrueba()

	resultado.Despacho = &application.ResumenDespacho{
		Modo:  application.ModoPublish,
		Items: resultado.Extraccion.ActionItems,
		Publicados: []domain.PublishResult{
			{
				TaskTitle: "Migrar la sesion",
				URL:       "https://github.com/org/repo/issues/1",
				Success:   true,
			},
			{
				TaskTitle: "Documentar los reintentos",
				Success:   false,
				Error:     domain.ErrIdentidadNoResuelta,
			},
		},
	}

	return resultado
}
