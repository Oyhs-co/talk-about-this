package application_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"talkaboutthis/internal/application"
	"talkaboutthis/internal/domain"
)

// Estos tests cubren el Pipeline, que es la orquestacion que antes vivia en
// cmd/talkaboutthis/main.go. Al estar en la capa de aplicacion, aqui se puede
// comprobar el ORDEN de las etapas y el error de cada una sin lanzar un proceso.

// extractorFalso devuelve un backlog fijo o un error programado.
type extractorFalso struct {
	extraccion *domain.MeetingBacklogExtraction
	err        error
	llamadas   int

	// retraso hace que el extractor tarde una cantidad conocida de tiempo.
	//
	// No es cosmetico: sin el, el pipeline completo termina en menos de un
	// milisegundo y la resolucion del reloj del sistema devuelve 0 para
	// time.Since, con lo que cualquier asercion sobre Duracion depende de la
	// granularidad del reloj y no del comportamiento del pipeline. Fijar el
	// retraso convierte la asercion en una prueba determinista.
	retraso time.Duration
}

func (e *extractorFalso) Ejecutar(ctx context.Context, transcript string) (*domain.MeetingBacklogExtraction, error) {
	e.llamadas++

	if e.retraso > 0 {
		select {
		case <-time.After(e.retraso):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	if e.err != nil {
		return nil, e.err
	}

	return e.extraccion, nil
}

// tableroConError devuelve un fallo en vez de publicar.
//
// Permite provocar un error de la etapa de despacho sin depender de la red.
type tableroConError struct{ err error }

func (t *tableroConError) PlatformName() string { return "tablero-con-error" }

func (t *tableroConError) PublishBacklog(ctx context.Context, projectRef string, items []domain.ActionItem) ([]domain.PublishResult, error) {
	return nil, t.err
}

// extraccionDePrueba es un backlog minimo pero valido.
func extraccionDePrueba() *domain.MeetingBacklogExtraction {
	return &domain.MeetingBacklogExtraction{
		MeetingSummary: "Reunion de seguimiento del sprint",
		ActionItems: []domain.ActionItem{
			item("Omar Hernández", "Migrar la sesion"),
			item("Ana María Ruiz", "Documentar los reintentos"),
		},
	}
}

// pipelineDePrueba monta un pipeline con el tablero que se le pase.
func pipelineDePrueba(t *testing.T, ext application.Extractor, tablero domain.ProjectBoardAdapter) *application.Pipeline {
	t.Helper()

	publicador, err := application.NuevoPublicarBacklog(application.PublicarOpciones{
		Identidades: mapperPorDefecto(),
		Tablero:     tablero,
	})
	if err != nil {
		t.Fatalf("no se pudo construir el publicador: %v", err)
	}

	p, err := application.NewPipeline(transcriptor(t), ext, publicador)
	if err != nil {
		t.Fatalf("no se pudo construir el pipeline: %v", err)
	}

	return p
}

// --- Construccion ---

// TestNewPipelineExigeLasTresDependencias comprueba que un grafo incompleto se
// detecta al construir, no en la primera ejecucion: es preferible un fallo de
// arranque que un fallo a mitad de una reunion.
func TestNewPipelineExigeLasTresDependencias(t *testing.T) {
	publicador, err := application.NuevoPublicarBacklog(application.PublicarOpciones{
		Identidades: mapperPorDefecto(),
		Tablero:     &tableroContador{},
	})
	if err != nil {
		t.Fatalf("no se pudo construir el publicador: %v", err)
	}

	casos := []struct {
		nombre     string
		ingestor   *application.IngestTranscriptor
		extractor  application.Extractor
		publicador *application.PublicarBacklog
	}{
		{"sin ingesta", nil, &extractorFalso{extraccion: extraccionDePrueba()}, publicador},
		{"sin extractor", transcriptor(t), nil, publicador},
		{"sin despacho", transcriptor(t), &extractorFalso{extraccion: extraccionDePrueba()}, nil},
	}

	for _, tt := range casos {
		t.Run(tt.nombre, func(t *testing.T) {
			if _, err := application.NewPipeline(tt.ingestor, tt.extractor, tt.publicador); !errors.Is(err, domain.ErrConfigInvalida) {
				t.Errorf("se esperaba ErrConfigInvalida, se obtuvo %v", err)
			}
		})
	}
}

// --- Ejecucion completa ---

// TestPipelineDryRunCompleto recorre el flujo entero sin tocar la red.
func TestPipelineDryRunCompleto(t *testing.T) {
	const retraso = 25 * time.Millisecond

	ext := &extractorFalso{extraccion: extraccionDePrueba(), retraso: retraso}

	// El tablero falla el test si se invoca: es la forma mas fuerte de
	// comprobar que el pipeline respeta el modo dry-run.
	p := pipelineDePrueba(t, ext, tableroQueFalla{t: t})

	resultado, err := p.Ejecutar(context.Background(), application.PipelineConfiguracion{
		Ruta:  rutaFixture("reunion-equipo.md"),
		Modo:  application.ModoDryRun,
		JobID: "job-test-1",
	})
	if err != nil {
		t.Fatalf("el pipeline fallo en dry-run: %v", err)
	}

	if resultado.Transcript == nil || resultado.Extraccion == nil {
		t.Fatalf("resultado incompleto: %+v", resultado)
	}

	if resultado.JobID != "job-test-1" {
		t.Errorf("JobID = %q, se esperaba job-test-1", resultado.JobID)
	}

	// La duracion se compara contra el retraso conocido del extractor, no
	// contra cero. Un pipeline que no midiese devolveria 0 y este test lo
	// detectaria igual, pero sin depender de la resolucion del reloj.
	if resultado.Duracion < retraso {
		t.Errorf("Duracion = %s, se esperaba al menos %s", resultado.Duracion, retraso)
	}

	if ext.llamadas != 1 {
		t.Errorf("el extractor se llamo %d veces, se esperaba 1", ext.llamadas)
	}
}

// TestPipelinePublishSiUsaElTablero es el complemento del anterior.
func TestPipelinePublishSiUsaElTablero(t *testing.T) {
	tablero := &tableroContador{}
	p := pipelineDePrueba(t, &extractorFalso{extraccion: extraccionDePrueba()}, tablero)

	if _, err := p.Ejecutar(context.Background(), application.PipelineConfiguracion{
		Ruta:       rutaFixture("reunion-equipo.md"),
		ProjectRef: "PVT_123",
		Modo:       application.ModoPublish,
	}); err != nil {
		t.Fatalf("el pipeline fallo en publish: %v", err)
	}

	if tablero.numLlamadas() != 1 {
		t.Errorf("llamadas al tablero = %d, se esperaba 1", tablero.numLlamadas())
	}
}

// TestPipelineFalloPorEtapa comprueba que cada etapa etiqueta su error con el
// nombre correcto. Es lo que permite a la CLI elegir el codigo de salida.
func TestPipelineFalloPorEtapa(t *testing.T) {
	sentinel := errors.New("fallo programado")

	casos := []struct {
		nombre   string
		cfg      application.PipelineConfiguracion
		ext      application.Extractor
		tablero  domain.ProjectBoardAdapter
		esperada application.PipelineEtapa
	}{
		{
			nombre:   "ingesta: archivo inexistente",
			cfg:      application.PipelineConfiguracion{Ruta: rutaFixture("no-existe.md")},
			ext:      &extractorFalso{extraccion: extraccionDePrueba()},
			tablero:  &tableroContador{},
			esperada: application.EtapaIngesta,
		},
		{
			nombre:   "extraccion: el LLM no produce backlog",
			cfg:      application.PipelineConfiguracion{Ruta: rutaFixture("reunion-equipo.md")},
			ext:      &extractorFalso{err: sentinel},
			tablero:  &tableroContador{},
			esperada: application.EtapaExtraccion,
		},
		{
			nombre: "despacho: la plataforma rechaza la publicacion",
			cfg: application.PipelineConfiguracion{
				Ruta: rutaFixture("reunion-equipo.md"), ProjectRef: "PVT_1", Modo: application.ModoPublish,
			},
			ext:      &extractorFalso{extraccion: extraccionDePrueba()},
			tablero:  &tableroConError{err: sentinel},
			esperada: application.EtapaDespacho,
		},
	}

	for _, tt := range casos {
		t.Run(tt.nombre, func(t *testing.T) {
			p := pipelineDePrueba(t, tt.ext, tt.tablero)

			_, err := p.Ejecutar(context.Background(), tt.cfg)
			if err == nil {
				t.Fatal("se esperaba un error")
			}

			if etapa := application.EtapaDelError(err); etapa != tt.esperada {
				t.Errorf("etapa = %q, se esperaba %q", etapa, tt.esperada)
			}

			if !strings.Contains(err.Error(), string(tt.esperada)) {
				t.Errorf("el mensaje %q no menciona la etapa", err)
			}
		})
	}
}

// TestPipelineFalloDeDespachoSeDistingue comprueba el helper que usa la CLI para
// no tratar un fallo de red como un fallo de configuracion.
func TestPipelineFalloDeDespachoSeDistingue(t *testing.T) {
	publicador, err := application.NuevoPublicarBacklog(application.PublicarOpciones{
		Identidades: mapperPorDefecto(),
		Tablero:     &tableroContador{},
	})
	if err != nil {
		t.Fatalf("no se pudo construir el publicador: %v", err)
	}

	p, err := application.NewPipeline(
		transcriptor(t),
		// Un item con responsable desconocido hace fallar el despacho en modo
		// estricto, que es el caso mas comun en la realidad.
		&extractorFalso{extraccion: &domain.MeetingBacklogExtraction{
			ActionItems: []domain.ActionItem{item("Persona Sin Mapear", "Tarea imposible")},
		}},
		publicador,
	)
	if err != nil {
		t.Fatalf("no se pudo construir el pipeline: %v", err)
	}

	_, err = p.Ejecutar(context.Background(), application.PipelineConfiguracion{
		Ruta:       rutaFixture("reunion-equipo.md"),
		ProjectRef: "PVT_1",
		Modo:       application.ModoPublish,
	})
	if err == nil {
		t.Fatal("se esperaba un fallo de identidad")
	}

	if !application.EsFalloDePublicacion(err) {
		t.Error("EsFalloDePublicacion deberia reconocer un fallo de despacho")
	}
}

// TestEtapasCompletadasReflejaDondeSeRompio comprueba que el resultado parcial
// dice hasta donde llego el proceso, para que la CLI pueda mostrarlo.
func TestEtapasCompletadasReflejaDondeSeRompio(t *testing.T) {
	casos := []struct {
		nombre    string
		resultado application.PipelineResultado
		esperada  []application.PipelineEtapa
	}{
		{
			nombre:    "nada",
			resultado: application.PipelineResultado{},
			esperada:  []application.PipelineEtapa{},
		},
		{
			nombre:    "solo ingesta",
			resultado: application.PipelineResultado{Transcript: &domain.Transcript{}},
			esperada:  []application.PipelineEtapa{application.EtapaIngesta},
		},
		{
			nombre: "ingesta y extraccion",
			resultado: application.PipelineResultado{
				Transcript: &domain.Transcript{},
				Extraccion: &domain.MeetingBacklogExtraction{},
			},
			esperada: []application.PipelineEtapa{application.EtapaIngesta, application.EtapaExtraccion},
		},
		{
			nombre: "todas",
			resultado: application.PipelineResultado{
				Transcript: &domain.Transcript{},
				Extraccion: &domain.MeetingBacklogExtraction{},
				Despacho:   &application.ResumenDespacho{},
			},
			esperada: []application.PipelineEtapa{
				application.EtapaIngesta,
				application.EtapaExtraccion,
				application.EtapaDespacho,
			},
		},
	}

	for _, tt := range casos {
		t.Run(tt.nombre, func(t *testing.T) {
			obtenidas := tt.resultado.EtapasCompletadas()

			if len(obtenidas) != len(tt.esperada) {
				t.Fatalf("etapas = %v, se esperaba %v", obtenidas, tt.esperada)
			}

			for i := range obtenidas {
				if obtenidas[i] != tt.esperada[i] {
					t.Errorf("etapa %d = %q, se esperaba %q", i, obtenidas[i], tt.esperada[i])
				}
			}
		})
	}
}

// TestPipelineToleraContextoNulo comprueba que un contexto nil no revienta el
// proceso: loggerDe lo trata y devuelve el logger por defecto.
func TestPipelineToleraContextoNulo(t *testing.T) {
	p := pipelineDePrueba(t, &extractorFalso{extraccion: extraccionDePrueba()}, &tableroContador{})

	//nolint:staticcheck // se prueba justamente que un nil no rompe el flujo
	if _, err := p.Ejecutar(nil, application.PipelineConfiguracion{ //nolint:SA1012
		Ruta: rutaFixture("reunion-equipo.md"),
		Modo: application.ModoDryRun,
	}); err != nil {
		t.Errorf("un contexto nil no deberia fallar, se obtuvo: %v", err)
	}
}

// TestPipelineJobIDPorDefecto comprueba que sin JobID se usa un marcador
// legible en lugar de una cadena vacia en los logs.
func TestPipelineJobIDPorDefecto(t *testing.T) {
	p := pipelineDePrueba(t, &extractorFalso{extraccion: extraccionDePrueba()}, &tableroContador{})

	resultado, err := p.Ejecutar(context.Background(), application.PipelineConfiguracion{
		Ruta: rutaFixture("reunion-equipo.md"),
		Modo: application.ModoDryRun,
	})
	if err != nil {
		t.Fatalf("el pipeline fallo: %v", err)
	}

	if resultado.JobID == "" {
		t.Error("deberia haber un JobID de sustitucion")
	}
}

// TestPipelineNoDuplicaElJobIDDelInyector comprueba que el pipeline NO vuelve a
// anadir el job_id cuando el contexto ya trae un logger que lo lleva.
//
// La CLI inyecta su logger (formato elegido + job_id) y pone el mismo valor en
// PipelineConfiguracion. Si el pipeline lo anadiera otra vez, cada linea de log
// saldria con "job_id=job-123 job_id=job-123": ruido que ademas hace dificil
// leer los logs a ojo o con una herramienta que indexe por campo.
func TestPipelineNoDuplicaElJobIDDelInyector(t *testing.T) {
	var registros bytes.Buffer

	anterior := slog.Default()
	defer slog.SetDefault(anterior)

	// El logger por defecto NO lleva job_id, para que solo se vea lo que anade
	// el pipeline.
	slog.SetDefault(slog.New(slog.NewTextHandler(&registros, &slog.HandlerOptions{Level: slog.LevelDebug})))

	// El contexto lleva un logger que YA tiene el job_id, como hace la CLI.
	conJobID := application.ConLoggerEnContexto(context.Background(),
		slog.New(slog.NewTextHandler(&registros, &slog.HandlerOptions{Level: slog.LevelDebug})).
			With(slog.String("job_id", "job-inyectado")))

	p := pipelineDePrueba(t, &extractorFalso{extraccion: extraccionDePrueba()}, &tableroContador{})

	if _, err := p.Ejecutar(conJobID, application.PipelineConfiguracion{
		Ruta:  rutaFixture("reunion-equipo.md"),
		Modo:  application.ModoDryRun,
		JobID: "job-inyectado",
	}); err != nil {
		t.Fatalf("el pipeline fallo: %v", err)
	}

	lineas := strings.Split(strings.TrimSpace(registros.String()), "\n")
	if len(lineas) == 0 {
		t.Fatal("no se escribieron logs")
	}

	for _, linea := range lineas {
		if n := strings.Count(linea, "job_id="); n > 1 {
			t.Errorf("el job_id aparece %d veces en una linea: %s", n, linea)
		}
	}

	if !strings.Contains(registros.String(), "job-inyectado") {
		t.Errorf("deberia aparecer el job_id del logger inyectado:\n%s", registros.String())
	}
}

// TestPipelinePropagaElJobIDEnLosLogs comprueba que el identificador de
// correlacion llega a los logs de las etapas, que es su unico proposito.
func TestPipelinePropagaElJobIDEnLosLogs(t *testing.T) {
	var registros bytes.Buffer

	anterior := slog.Default()
	defer slog.SetDefault(anterior)

	slog.SetDefault(slog.New(slog.NewTextHandler(&registros, &slog.HandlerOptions{Level: slog.LevelDebug})))

	p := pipelineDePrueba(t, &extractorFalso{extraccion: extraccionDePrueba()}, &tableroContador{})

	if _, err := p.Ejecutar(context.Background(), application.PipelineConfiguracion{
		Ruta:  rutaFixture("reunion-equipo.md"),
		Modo:  application.ModoDryRun,
		JobID: "job-correlacion-42",
	}); err != nil {
		t.Fatalf("el pipeline fallo: %v", err)
	}

	if !strings.Contains(registros.String(), "job-correlacion-42") {
		t.Errorf("el JobID no aparece en los logs:\n%s", registros.String())
	}
}
