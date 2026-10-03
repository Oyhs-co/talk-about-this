package domain_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"talkaboutthis/internal/domain"
)

// itemValido es una base valida que cada test modifica en un solo aspecto.
// Evita duplicar los cuatro campos obligatorios en cada caso.
func itemValido() domain.ActionItem {
	return domain.ActionItem{
		Title:       "Migrar el modulo de autenticacion",
		Description: "Extraer la sesion del contexto global y pasarla a un middleware. Criterio: los tests siguen en verde.",
		RawAssignee: "Omar Hernández",
		Priority:    domain.PriorityHigh,
		Labels:      []string{"backend"},
		StoryPoints: 3,
	}
}

// TestActionItemValidoAceptaItemSano es el caso base: si esto falla, el resto de
// los tests de error darian un falso positivo.
func TestActionItemValidoAceptaItemSano(t *testing.T) {
	if err := itemValido().Validar(); err != nil {
		t.Fatalf("un item bien formado fue rechazado: %v", err)
	}
}

func TestActionItemValidar(t *testing.T) {
	tests := []struct {
		nombre      string
		mutar       func(*domain.ActionItem)
		seEsperaErr error
	}{
		{
			nombre:      "titulo vacio",
			mutar:       func(a *domain.ActionItem) { a.Title = "" },
			seEsperaErr: domain.ErrTituloInvalido,
		},
		{
			nombre:      "titulo solo con espacios",
			mutar:       func(a *domain.ActionItem) { a.Title = "   \t\n " },
			seEsperaErr: domain.ErrTituloInvalido,
		},
		{
			nombre:      "titulo demasiado largo",
			mutar:       func(a *domain.ActionItem) { a.Title = strings.Repeat("a", domain.MaxTituloLen+1) },
			seEsperaErr: domain.ErrTituloInvalido,
		},
		{
			nombre:      "descripcion vacia",
			mutar:       func(a *domain.ActionItem) { a.Description = "" },
			seEsperaErr: domain.ErrDescripcionInvalida,
		},
		{
			nombre:      "asignado vacio",
			mutar:       func(a *domain.ActionItem) { a.RawAssignee = "  " },
			seEsperaErr: domain.ErrAsignadoInvalido,
		},
		{
			nombre:      "prioridad fuera del enum",
			mutar:       func(a *domain.ActionItem) { a.Priority = domain.Priority("URGENT") },
			seEsperaErr: domain.ErrPriorityInvalida,
		},
		{
			nombre:      "prioridad vacia",
			mutar:       func(a *domain.ActionItem) { a.Priority = domain.Priority("") },
			seEsperaErr: domain.ErrPriorityInvalida,
		},
		{
			nombre:      "demasiadas etiquetas",
			mutar:       func(a *domain.ActionItem) { a.Labels = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k"} },
			seEsperaErr: domain.ErrSchemaViolation,
		},
		{
			nombre:      "story points negativo",
			mutar:       func(a *domain.ActionItem) { a.StoryPoints = -1 },
			seEsperaErr: domain.ErrSchemaViolation,
		},
		{
			nombre:      "story points excesivo",
			mutar:       func(a *domain.ActionItem) { a.StoryPoints = 99 },
			seEsperaErr: domain.ErrSchemaViolation,
		},
	}

	for _, tt := range tests {
		t.Run(tt.nombre, func(t *testing.T) {
			item := itemValido()
			tt.mutar(&item)

			err := item.Validar()
			if err == nil {
				t.Fatalf("se esperaba error, pero el item fue aceptado: %+v", item)
			}
			if !errors.Is(err, tt.seEsperaErr) {
				t.Errorf("error equivocado: se esperaba %v, se obtuvo %v", tt.seEsperaErr, err)
			}
		})
	}
}

// TestActionItemAceptaLimitesExactos comprueba los casos frontera en los que el
// valor debe aceptarse. Un test que solo cubre el error puede pasar con una
// comparacion estricta donde deberia ser inclusiva.
func TestActionItemAceptaLimitesExactos(t *testing.T) {
	item := itemValido()
	item.Title = strings.Repeat("a", domain.MaxTituloLen)

	if err := item.Validar(); err != nil {
		t.Errorf("un titulo de exactamente %d caracteres debe aceptarse: %v", domain.MaxTituloLen, err)
	}

	item = itemValido()
	item.Labels = make([]string, domain.MaxEtiquetas)

	if err := item.Validar(); err != nil {
		t.Errorf("exactamente %d etiquetas deben aceptarse: %v", domain.MaxEtiquetas, err)
	}

	// StoryPoints cero significa "no estimado" y es valido.
	item = itemValido()
	item.StoryPoints = 0

	if err := item.Validar(); err != nil {
		t.Errorf("story_points=0 debe aceptarse como 'no estimado': %v", err)
	}

	// nil en Labels significa "sin etiquetas" y es valido.
	item = itemValido()
	item.Labels = nil

	if err := item.Validar(); err != nil {
		t.Errorf("labels nil debe aceptarse: %v", err)
	}
}

// TestTituloSeCuentaEnRunesNoEnBytes cubre un caso real: los acentos y las
// eñes ocupan varios bytes en UTF-8. Contar bytes haria que un titulo
// valido en espanol fuera rechazado.
func TestTituloSeCuentaEnRunesNoEnBytes(t *testing.T) {
	item := itemValido()
	// "Migración" con tilde: 9 runes pero 10 bytes en UTF-8.
	item.Title = strings.Repeat("ó", domain.MaxTituloLen)

	if len(item.Title) <= domain.MaxTituloLen {
		t.Skip("la cadena de prueba no supera el limite en bytes; nada que comprobar")
	}

	if err := item.Validar(); err != nil {
		t.Errorf(
			"un titulo de %d caracteres con acentos debe aceptarse (ocupa %d bytes): %v",
			domain.MaxTituloLen, len(item.Title), err,
		)
	}
}

// TestPriorityEsValida documenta el enum aceptado sin ambiguedad.
func TestPriorityEsValida(t *testing.T) {
	validos := []domain.Priority{
		domain.PriorityHigh,
		domain.PriorityMedium,
		domain.PriorityLow,
	}
	invalidos := []domain.Priority{
		"",
		"URGENT",
		"high",   // minusculas no son validas: el enum es en mayusculas
		"Medium", // mixed case tampoco
		"CRITICAL",
		"HIGH ",
	}

	for _, p := range validos {
		if !p.EsValida() {
			t.Errorf("Priority(%q) deberia ser valida", p)
		}
	}

	for _, p := range invalidos {
		if p.EsValida() {
			t.Errorf("Priority(%q) no deberia ser valida", p)
		}
	}
}

// TestParsePriorityNormalizaYValida comprueba que la normalizacion ocurre antes
// de validar, que es lo que permite tolerar la salida del LLM con espacios o
// minusculas.
func TestParsePriorityNormalizaYValida(t *testing.T) {
	tests := []struct {
		entrada string
		quiere  domain.Priority
		falla   bool
	}{
		{entrada: "HIGH", quiere: domain.PriorityHigh},
		{entrada: "high", quiere: domain.PriorityHigh},
		{entrada: "  Medium  ", quiere: domain.PriorityMedium},
		{entrada: "LOW", quiere: domain.PriorityLow},
		{entrada: "URGENT", falla: true},
		{entrada: "", falla: true},
	}

	for _, tt := range tests {
		t.Run(tt.entrada, func(t *testing.T) {
			got, err := domain.ParsePriority(tt.entrada)

			if tt.falla {
				if err == nil {
					t.Fatalf("se esperaba error para %q, se obtuvo %q", tt.entrada, got)
				}
				if !errors.Is(err, domain.ErrPriorityInvalida) {
					t.Errorf("error equivocado: %v", err)
				}
				return
			}

			if err != nil {
				t.Fatalf("no se esperaba error para %q: %v", tt.entrada, err)
			}
			if got != tt.quiere {
				t.Errorf("ParsePriority(%q) = %q, se esperaba %q", tt.entrada, got, tt.quiere)
			}
		})
	}
}

// TestPrioridadInvalidaNoSeSustituyeEnSilencio documenta una decision de
// diseno deliberada del dominio: una prioridad desconocida produce error, nunca
// MEDIUM por defecto. Un default silencioso publicaria trabajo critico con
// prioridad media sin avisar a nadie.
func TestPrioridadInvalidaNoSeSustituyeEnSilencio(t *testing.T) {
	item := itemValido()
	item.Priority = domain.Priority("P0")

	err := item.Validar()
	if err == nil {
		t.Fatal("una prioridad desconocida debe producir error, no un valor por defecto")
	}

	// El item original no debe haber sido mutado por la validacion.
	if item.Priority != domain.Priority("P0") {
		t.Errorf("Validar() no debe mutar el item: la prioridad cambio a %q", item.Priority)
	}
}

// TestMeetingBacklogExtractionValidar cubre el agregado completo.
func TestMeetingBacklogExtractionValidar(t *testing.T) {
	t.Run("acepta una extraccion valida", func(t *testing.T) {
		extraccion := domain.MeetingBacklogExtraction{
			MeetingSummary: "Reunion de arquitectura del modulo de pagos.",
			ActionItems:    []domain.ActionItem{itemValido()},
		}

		if err := extraccion.Validar(); err != nil {
			t.Fatalf("una extraccion valida fue rechazada: %v", err)
		}
	})

	t.Run("rechaza resumen vacio", func(t *testing.T) {
		extraccion := domain.MeetingBacklogExtraction{
			MeetingSummary: "   ",
			ActionItems:    []domain.ActionItem{itemValido()},
		}

		if err := extraccion.Validar(); !errors.Is(err, domain.ErrSchemaViolation) {
			t.Errorf("se esperaba ErrSchemaViolation, se obtuvo %v", err)
		}
	})

	t.Run("rechaza lista de items vacia", func(t *testing.T) {
		// Sin items no hay nada que publicar: fallar aqui evita gastar una
		// llamada de publicacion en un tablero por vacio.
		extraccion := domain.MeetingBacklogExtraction{
			MeetingSummary: "Reunion sin decisiones accionables.",
			ActionItems:    []domain.ActionItem{},
		}

		if err := extraccion.Validar(); !errors.Is(err, domain.ErrSchemaViolation) {
			t.Errorf("se esperaba ErrSchemaViolation, se obtuvo %v", err)
		}
	})

	t.Run("rechaza si un solo item es invalido", func(t *testing.T) {
		malo := itemValido()
		malo.Priority = domain.Priority("TRIVIAL")

		extraccion := domain.MeetingBacklogExtraction{
			MeetingSummary: "Reunion con un item corrupto.",
			ActionItems:    []domain.ActionItem{itemValido(), malo},
		}

		err := extraccion.Validar()
		if err == nil {
			t.Fatal("una extraccion con un item invalido debe rechazarse completa")
		}
		if !errors.Is(err, domain.ErrSchemaViolation) {
			t.Errorf("se esperaba ErrSchemaViolation, se obtuvo %v", err)
		}
	})

	t.Run("el error identifica el indice del item problematico", func(t *testing.T) {
		malo := itemValido()
		malo.Description = ""

		extraccion := domain.MeetingBacklogExtraction{
			MeetingSummary: "Reunion.",
			ActionItems:    []domain.ActionItem{itemValido(), itemValido(), malo},
		}

		err := extraccion.Validar()
		if err == nil {
			t.Fatal("se esperaba error")
		}
		if !strings.Contains(err.Error(), "action_items[2]") {
			t.Errorf("el error deberia señalar el indice del item, se obtuvo: %v", err)
		}
	})
}

// TestErroresSeEnvolvedanConContexto comprueba que los errores pueden encadenarse
// sin perder el sentinel, que es lo que permite errors.Is aguas arriba.
func TestErroresSeEnvolvedanConContexto(t *testing.T) {
	item := itemValido()
	item.Priority = domain.Priority("NOPE")

	err := item.Validar()
	if err == nil {
		t.Fatal("se esperaba error")
	}
	if !errors.Is(err, domain.ErrPriorityInvalida) {
		t.Errorf("errors.Is debe encontrar el sentinel a traves del envoltorio: %v", err)
	}
	if !strings.Contains(err.Error(), "NOPE") {
		t.Errorf("el mensaje deberia incluir el valor recibido: %v", err)
	}
}

// TestPublicacionResultExitosaYFallida comprueba el modelo de fallo parcial.
func TestPublicacionResult(t *testing.T) {
	ok := domain.PublishResult{
		TaskTitle:  "Migrar autenticacion",
		ExternalID: "PVT_kwDOABC",
		URL:        "https://github.com/org/repo/issues/42",
		Success:    true,
	}

	if err := ok.Err(); err != nil {
		t.Errorf("una publicacion exitosa no debe tener error: %v", err)
	}
	if !strings.Contains(ok.Resumen(), "OK") {
		t.Errorf("el resumen debe indicar exito: %s", ok.Resumen())
	}

	fallo := domain.PublishResult{
		TaskTitle: "Migrar autenticacion",
		Success:   false,
		Error:     domain.ErrPublicacionFallida,
	}

	if err := fallo.Err(); !errors.Is(err, domain.ErrPublicacionFallida) {
		t.Errorf("una publicacion fallida debe exponer su causa: %v", err)
	}
	if !strings.Contains(fallo.Resumen(), "FALLO") {
		t.Errorf("el resumen debe indicar fallo: %s", fallo.Resumen())
	}
}

// TestExtractionJobTransiciones comprueba el ciclo de vida del job y que su ID
// sirve como identificador de correlacion (RNF-06).
func TestExtractionJobTransiciones(t *testing.T) {
	job := domain.NuevoExtractionJob("job-001", "contenido de la reunion")

	if job.ID != "job-001" {
		t.Errorf("el ID se perdio: %q", job.ID)
	}
	if job.CreatedAt.IsZero() {
		t.Error("CreatedAt no fue inicializada")
	}
	if job.Status != domain.JobPendiente {
		t.Errorf("un job nuevo debe estar PENDING, esta en %q", job.Status)
	}
	if job.Result != nil {
		t.Error("un job nuevo no debe tener resultado")
	}

	job.MarcarEnCurso()
	if job.Status != domain.JobEnCurso {
		t.Errorf("se esperaba RUNNING, se obtuvo %q", job.Status)
	}

	resultado := &domain.MeetingBacklogExtraction{
		MeetingSummary: "Resumen",
		ActionItems:    []domain.ActionItem{itemValido()},
	}
	job.MarcarCompletado(resultado)

	if job.Status != domain.JobCompletado {
		t.Errorf("se esperaba COMPLETED, se obtuvo %q", job.Status)
	}
	if job.Result != resultado {
		t.Error("el resultado no fue adjuntado al job")
	}
	if job.Duracion() < 0 {
		t.Error("la duracion no puede ser negativa")
	}

	job.MarcarFallido()
	if job.Status != domain.JobFallido {
		t.Errorf("se esperaba FAILED, se obtuvo %q", job.Status)
	}
	// El resultado se conserva para diagnosticar que se extrajo antes del fallo.
	if job.Result != resultado {
		t.Error("MarcarFallido no debe descartar el resultado parcial")
	}
}

// TestTranscriptInvariantes cubre la entidad de la etapa de ingesta.
func TestTranscriptInvariantes(t *testing.T) {
	meta := domain.NewDocumentMetadata("NOTAS.MD", "MD", 1024, time.Now())
	if meta.Extension != ".md" {
		t.Errorf("la extension debe normalizarse a minusculas y con punto: %q", meta.Extension)
	}

	t.Run("transcript con contenido", func(t *testing.T) {
		tr := domain.NewTranscript("Reunion sobre el modulo de pagos.", meta)

		if tr.EsVacio() {
			t.Error("un transcript con texto no debe estar vacio")
		}
		if err := tr.Validar(); err != nil {
			t.Errorf("un transcript valido fue rechazado: %v", err)
		}
		if tr.Longitud() == 0 {
			t.Error("Longitud debe reportar el numero de caracteres")
		}
	})

	t.Run("transcript solo con espacios", func(t *testing.T) {
		// Un archivo que tras la limpieza no tiene texto debe fallar aqui y no
		// gastar una llamada al LLM.
		tr := domain.NewTranscript("  \n\t ", meta)

		if !tr.EsVacio() {
			t.Error("un transcript de espacios debe considerarse vacio")
		}
		if err := tr.Validar(); !errors.Is(err, domain.ErrFormatoNoSoportado) {
			t.Errorf("se esperaba ErrFormatoNoSoportado, se obtuvo %v", err)
		}
	})

	t.Run("transcript nil", func(t *testing.T) {
		var tr *domain.Transcript
		if err := tr.Validar(); !errors.Is(err, domain.ErrFormatoNoSoportado) {
			t.Errorf("un transcript nil debe producir error controlado, no panic: %v", err)
		}
	})
}

// TestActionItemSerializaConTagsDelSchema comprueba el round-trip de JSON con las
// claves exactas que el LLM debe producir y el dominio debe consumir.
func TestActionItemSerializaConTagsDelSchema(t *testing.T) {
	original := itemValido()
	original.MappedHandle = "omarhernan"

	datos, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("no se pudo serializar: %v", err)
	}

	var mapa map[string]any
	if err := json.Unmarshal(datos, &mapa); err != nil {
		t.Fatalf("no se pudo deserializar: %v", err)
	}

	claves := []string{"title", "description", "assignee_name", "priority", "labels", "story_points", "mapped_handle"}
	for _, clave := range claves {
		if _, ok := mapa[clave]; !ok {
			t.Errorf("falta la clave %q en el JSON serializado: %s", clave, datos)
		}
	}

	// mapped_handle lleva omitempty: sin handle resuelto no debe aparecer.
	item := itemValido()
	item.MappedHandle = ""

	datos, err = json.Marshal(item)
	if err != nil {
		t.Fatalf("no se pudo serializar: %v", err)
	}
	if strings.Contains(string(datos), "mapped_handle") {
		t.Errorf("mapped_handle vacio no debe serializarse por el tag omitempty: %s", datos)
	}
}

// TestPortsSonImplementables documenta la forma de las implementaciones
// sin escribir todavia los adaptadores (Fases 2 a 4).
//
// Un doble de prueba implementa los puertos del dominio. Si alguien cambia una
// firma en ports.go, este archivo deja de compilar y el cambio queda visible de
// inmediato en los puntos de uso.
type (
	parserFalso    struct{}
	providerFalso  struct{}
	mapperFalso    struct{}
	adaptadorFalso struct{}
)

func (parserFalso) CanParse(ext string) bool { return ext == ".md" }

func (parserFalso) Parse(ctx context.Context, r io.Reader) (string, error) { return "", nil }

func (providerFalso) Name() string { return "falso" }

func (providerFalso) GenerateStructuredOutput(ctx context.Context, prompt, schemaJSON string) ([]byte, error) {
	return nil, nil
}

func (mapperFalso) ResolveHandle(ctx context.Context, rawName string) (string, error) { return "", nil }

func (adaptadorFalso) PlatformName() string { return "falsa" }

func (adaptadorFalso) PublishBacklog(ctx context.Context, projectRef string, items []domain.ActionItem) ([]domain.PublishResult, error) {
	return nil, nil
}

// Aserciones de compilacion: si las interfaces cambian, el paquete de test deja
// de compilar y el fallo es explicito.
var (
	_ domain.DocumentParser      = parserFalso{}
	_ domain.LLMProvider         = providerFalso{}
	_ domain.IdentityMapper      = mapperFalso{}
	_ domain.ProjectBoardAdapter = adaptadorFalso{}
)

func TestPortsImplementables(t *testing.T) {
	// La prueba real es de compilacion. Este test existe para que el archivo no
	// se marque como sin tests y para documentar la intencion.
	t.Log("los cuatro puertos son implementables por tipos externos al paquete domain")
}
