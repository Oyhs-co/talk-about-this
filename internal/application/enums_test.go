package application_test

import (
	"context"
	"testing"

	"talkaboutthis/internal/domain"
	"talkaboutthis/internal/infrastructure/llm"
)

// Estos tests cubren la normalizacion de enums en la frontera con el LLM.
//
// Van por la API publica del caso de uso a proposito: lo que importa no es que
// exista una funcion que normalice, sino que una respuesta realista de un modelo
// real produzca un backlog valido.

// backlogConPrioridad arma una respuesta del LLM con la prioridad indicada.
func backlogConPrioridad(prioridad string) string {
	return `{"meeting_summary":"Reunion","action_items":[{
		"title":"Migrar la sesion",
		"description":"Pasar la sesion a cookie segura",
		"assignee_name":"Omar Hernandez",
		"priority":"` + prioridad + `",
		"labels":["Backend","post-venta"]
	}]}`
}

// extraerRespuesta ejecuta una extraccion con una unica respuesta y devuelve el
// backlog, o el error que produjo.
func extraerRespuesta(t *testing.T, respuesta string) (*domain.MeetingBacklogExtraction, error) {
	t.Helper()

	caso := extraer(t, &proveedorFalso{respuestas: []string{respuesta}}, llm.NuevoPromptBuilder())

	return caso.Ejecutar(context.Background(), "transcripcion de prueba")
}

// TestEnumDePrioridadEnMinusculasSeAcepta es un caso real, no teorico.
//
// Los modelos escriben "high" aunque el schema pida "HIGH": el prompt travels
// entero hasta el modelo y alli las mayusculas no sobreviven. Sin normalizar,
// cada extraccion gastaba los TRES reintentos del Retry Loop para acabar
// fallando por un detalle de caja que nunca cambia el significado del dato.
func TestEnumDePrioridadEnMinusculasSeAcepta(t *testing.T) {
	casos := []struct {
		entrada string
		salida  domain.Priority
	}{
		{"high", domain.PriorityHigh},
		{"HIGH", domain.PriorityHigh},
		{"High", domain.PriorityHigh},
		{"hIgH", domain.PriorityHigh},
		{"medium", domain.PriorityMedium},
		{"low", domain.PriorityLow},
	}

	for _, tt := range casos {
		t.Run(tt.entrada, func(t *testing.T) {
			extraccion, err := extraerRespuesta(t, backlogConPrioridad(tt.entrada))
			if err != nil {
				t.Fatalf("la respuesta con priority=%q fue rechazada: %v", tt.entrada, err)
			}

			if extraccion.ActionItems[0].Priority != tt.salida {
				t.Errorf("priority = %q, se esperaba %q",
					extraccion.ActionItems[0].Priority, tt.salida)
			}
		})
	}
}

// TestEnumDePrioridadInvalidoNoSeInventa comprueba que normalizar NO adivina.
//
// Un "urgente" o un "critica" no se convierten a MEDIUM en silencio: la respuesta
// se rechaza. Convertir una prioridad que nadie eligio es peor que fallar, porque
// la tarea se publicaria con una urgencia inventada.
func TestEnumDePrioridadInvalidoNoSeInventa(t *testing.T) {
	for _, valor := range []string{"urgente", "critica", "", "blocker"} {
		extraccion, err := extraerRespuesta(t, backlogConPrioridad(valor))

		if err == nil {
			t.Errorf("priority=%q deberia rechazarse, pero se acepto como %q",
				valor, extraccion.ActionItems[0].Priority)
		}
	}
}

// TestNormalizarEnumsNoTocaLasEtiquetas comprueba el limite de la normalizacion.
//
// Las etiquetas son texto libre: "Backend" y "post-venta" son validas y deben
// salir intactas. Un recorrido generico que metiera todo en mayusculas romperia
// los datos de quien los escribio.
func TestNormalizarEnumsNoTocaLasEtiquetas(t *testing.T) {
	extraccion, err := extraerRespuesta(t, backlogConPrioridad("low"))
	if err != nil {
		t.Fatalf("la respuesta fue rechazada: %v", err)
	}

	etiquetas := extraccion.ActionItems[0].Labels
	esperadas := []string{"Backend", "post-venta"}

	if len(etiquetas) != len(esperadas) {
		t.Fatalf("etiquetas = %v, se esperaba %v", etiquetas, esperadas)
	}

	for i, esperada := range esperadas {
		if etiquetas[i] != esperada {
			t.Errorf("etiqueta %d = %q, se esperaba %q", i, etiquetas[i], esperada)
		}
	}
}

// TestNormalizarEnumsToleraRespuestasMalformadas comprueba que la normalizacion
// no entra en panic con formas que puede encontrar. La respuesta de un LLM no
// esta garantizada, y un panic en esta capa tumbaria el proceso entero.
func TestNormalizarEnumsToleraRespuestasMalformadas(t *testing.T) {
	malformadas := []string{
		`{}`,
		`{"action_items": null}`,
		`{"action_items": "no es un array"}`,
		`{"action_items": [null]}`,
		`{"action_items": ["no es un objeto"]}`,
		`{"action_items": [{"priority": 5}]}`,
		`{"action_items": [{"priority": null}]}`,
		`[]`,
		`"una cadena suelta"`,
	}

	for _, respuesta := range malformadas {
		// El resultado da igual: lo que se comprueba es que no haya panic y que
		// lo que salga sea un error reportado, no un crash.
		if _, err := extraerRespuesta(t, respuesta); err == nil {
			t.Errorf("la respuesta malformada %s deberia reportarse como error", respuesta)
		}
	}
}
