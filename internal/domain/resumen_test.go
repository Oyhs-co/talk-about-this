package domain_test

import (
	"strings"
	"testing"

	"talkaboutthis/internal/domain"
)

// Estos tests cubren los metodos de presentacion que usa el modo dry-run (RF-06).
// No son cosmeticos: si el resumen no distingue un responsable resuelto de uno
// sin resolver, el usuario no puede detectar que el IdentityMapper fallo.

// TestResumenNormalizadoMuestraHandleResuelto comprueba que, una vez resuelta la
// identidad, se muestra el handle y no el nombre en texto libre.
func TestResumenNormalizadoMuestraHandleResuelto(t *testing.T) {
	item := itemValido()
	item.MappedHandle = "omarhernan"

	resumen := item.ResumenNormalizado()

	if !strings.Contains(resumen, "omarhernan") {
		t.Errorf("el resumen debe mostrar el handle resuelto: %s", resumen)
	}
	if strings.Contains(resumen, "sin resolver") {
		t.Errorf("un item con handle no debe marcarse como sin resolver: %s", resumen)
	}
	if !strings.Contains(resumen, string(domain.PriorityHigh)) {
		t.Errorf("el resumen debe incluir la prioridad: %s", resumen)
	}
	if !strings.Contains(resumen, "backend") {
		t.Errorf("el resumen debe incluir las etiquetas: %s", resumen)
	}
}

// TestResumenNormalizadoMarcaSinResolver es el caso que mas importa en la
// operacion: si la identidad no se pudo resolver, hay que decirlo.
func TestResumenNormalizadoMarcaSinResolver(t *testing.T) {
	item := itemValido()
	item.MappedHandle = ""

	resumen := item.ResumenNormalizado()

	if !strings.Contains(resumen, "sin resolver") {
		t.Errorf("un item sin handle debe marcarse explicitamente: %s", resumen)
	}
	// El nombre original debe seguir visible para que el usuario sepa a quien
	// pertenece la tarea.
	if !strings.Contains(resumen, "Omar Hernández") {
		t.Errorf("el resumen debe conservar el nombre original: %s", resumen)
	}
}

// TestResumenDevuelveUnaLineaPorItem comprueba la forma tabular de la salida.
func TestResumenDevuelveUnaLineaPorItem(t *testing.T) {
	extraccion := domain.MeetingBacklogExtraction{
		MeetingSummary: "Reunion de seguimiento.",
		ActionItems: []domain.ActionItem{
			func() domain.ActionItem {
				a := itemValido()
				a.Title = "Primera tarea"
				return a
			}(),
			func() domain.ActionItem {
				a := itemValido()
				a.Title = "Segunda tarea"
				return a
			}(),
		},
	}

	lineas := extraccion.Resumen()
	if len(lineas) != 2 {
		t.Fatalf("se esperaban 2 lineas, se obtuvieron %d: %v", len(lineas), lineas)
	}
	if !strings.Contains(lineas[0], "Primera tarea") {
		t.Errorf("la primera linea no corresponde: %s", lineas[0])
	}
	if !strings.Contains(lineas[1], "Segunda tarea") {
		t.Errorf("la segunda linea no corresponde: %s", lineas[1])
	}
}

// TestResumenDeExtraccionVaciaNoFalla cubre el borde de una extraccion sin
// items: el dry-run debe imprimir un encabezado, no un panic.
func TestResumenDeExtraccionVaciaNoFalla(t *testing.T) {
	extraccion := domain.MeetingBacklogExtraction{
		MeetingSummary: "Reunion sin decisiones.",
		ActionItems:    nil,
	}

	lineas := extraccion.Resumen()
	if len(lineas) != 0 {
		t.Errorf("una extraccion sin items debe producir cero lineas, no %d", len(lineas))
	}
}

// TestPublishResultResumenIndicaDestino comprueba que el resumen de una
// publicacion exitosa incluye la URL, que es lo que el usuario necesita para
// navegar a la tarjeta creada.
func TestPublishResultResumenIndicaDestino(t *testing.T) {
	ok := domain.PublishResult{
		TaskTitle:  "Migrar autenticacion",
		URL:        "https://github.com/org/repo/issues/42",
		ExternalID: "PVT_123",
		Success:    true,
	}

	resumen := ok.Resumen()
	if !strings.Contains(resumen, "https://github.com/org/repo/issues/42") {
		t.Errorf("el resumen debe incluir la URL de la tarjeta: %s", resumen)
	}

	fallo := domain.PublishResult{
		TaskTitle: "Migrar autenticacion",
		Success:   false,
		Error:     domain.ErrPublicacionFallida,
	}

	if !strings.Contains(fallo.Resumen(), "Migrar autenticacion") {
		t.Errorf("el resumen de un fallo debe identificar la tarea: %s", fallo.Resumen())
	}
}
