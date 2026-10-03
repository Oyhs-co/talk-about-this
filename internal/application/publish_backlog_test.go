package application_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"talkaboutthis/internal/application"
	"talkaboutthis/internal/domain"
	"talkaboutthis/internal/infrastructure/identity"
)

// Estos tests cubren la etapa de despacho, con la garantia central de TC-05: en
// modo dry-run no se hace NINGUNA llamada de red hacia la plataforma destino.

// tableroQueFalla entra en panic si se invoca.
//
// Es la forma mas fuerte de comprobar la garantia de TC-05: si el caso de uso
// tocara el adaptador en dry-run, el test reventaria aqui en lugar de tener que
// inspeccionar un contador de llamadas.
type tableroQueFalla struct {
	t *testing.T
}

func (t tableroQueFalla) PlatformName() string { return "tablero-que-falla" }

func (t tableroQueFalla) PublishBacklog(ctx context.Context, projectRef string, items []domain.ActionItem) ([]domain.PublishResult, error) {
	t.t.Error("SE INVOCO EL ADAPTADOR DE TABLERO EN MODO DRY-RUN: incumple TC-05")
	panic("llamada de red en dry-run")
}

// tableroContador registra las publicaciones sin hacer red.
type tableroContador struct {
	mu       sync.Mutex
	llamadas int
	items    []domain.ActionItem
	fallarEn map[string]bool
}

func (t *tableroContador) PlatformName() string { return "tablero-contador" }

func (t *tableroContador) PublishBacklog(ctx context.Context, projectRef string, items []domain.ActionItem) ([]domain.PublishResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.llamadas++
	t.items = items

	resultados := make([]domain.PublishResult, 0, len(items))
	for _, item := range items {
		falla := t.fallarEn[item.Title]
		resultados = append(resultados, domain.PublishResult{
			TaskTitle: item.Title,
			URL:       "https://tablero/" + item.Title,
			Success:   !falla,
		})
	}

	return resultados, nil
}

func (t *tableroContador) numLlamadas() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.llamadas
}

// mapperFalso resuelve nombres conocidos y rechaza el resto.
type mapperFalso struct {
	conocidos map[string]string
}

func (m *mapperFalso) ResolveHandle(ctx context.Context, rawName string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	if handle, ok := m.conocidos[rawName]; ok {
		return handle, nil
	}

	return "", domain.ErrIdentidadNoResuelta
}

func mapperPorDefecto() *mapperFalso {
	return &mapperFalso{conocidos: map[string]string{
		"Omar Hernández": "omarhernan",
		"Ana María Ruiz": "anaruiz",
		"Luis Cabrera":   "luiscabrera",
	}}
}

func item(nombre, titulo string) domain.ActionItem {
	return domain.ActionItem{
		Title:       titulo,
		Description: "Descripcion de " + titulo,
		RawAssignee: nombre,
		Priority:    domain.PriorityHigh,
		Labels:      []string{"backend"},
	}
}

// --- TC-05: dry-run sin red ---

// TestTC05DryRunNoLlamaAlTablero es el caso de evaluacion TC-05.
func TestTC05DryRunNoLlamaAlTablero(t *testing.T) {
	caso, err := application.NuevoPublicarBacklog(application.PublicarOpciones{
		Identidades: mapperPorDefecto(),
		// Este tablero falla el test si se invoca.
		Tablero: tableroQueFalla{t: t},
	})
	if err != nil {
		t.Fatalf("no se pudo construir el caso de uso: %v", err)
	}

	items := []domain.ActionItem{
		item("Omar Hernández", "Migrar la sesion"),
		item("Ana María Ruiz", "Documentar los reintentos"),
	}

	resumen, err := caso.Ejecutar(context.Background(), "PVT_123", items, application.ModoDryRun)
	if err != nil {
		t.Fatalf("el dry-run fallo: %v", err)
	}

	if resumen.Modo != application.ModoDryRun {
		t.Errorf("el modo deberia seguir siendo dry-run, es %q", resumen.Modo)
	}

	// Los items se devuelven para poder revisarlos.
	if len(resumen.Items) != len(items) {
		t.Errorf("el dry-run deberia devolver los items, devolvio %d", len(resumen.Items))
	}

	// Y no hay resultados de publicacion: nada se publico.
	if len(resumen.Publicados) != 0 {
		t.Errorf("en dry-run no debe haber resultados de publicacion: %v", resumen.Publicados)
	}
}

// TestTC05DryRunTampocoResuelveIdentidades documenta una decision: el dry-run
// muestra lo que el LLM extrajo, sin tocar la tabla de aliases.
//
// Consultar el mapper podria ser inocuo hoy (es local), pero es una llamada
// capaz de salir a la red en cuanto el mapper crezca hacia un directorio de la
// empresa. Mantener el dry-run libre de I/O evita tener que auditarlo mas adelante.
func TestTC05DryRunTampocoResuelveIdentidades(t *testing.T) {
	mapper := &mapperQueFalla{t: t}

	caso, err := application.NuevoPublicarBacklog(application.PublicarOpciones{
		Identidades: mapper,
		Tablero:     &tableroContador{},
	})
	if err != nil {
		t.Fatalf("no se pudo construir el caso de uso: %v", err)
	}

	if _, err := caso.Ejecutar(context.Background(), "PVT_123",
		[]domain.ActionItem{item("Omar Hernández", "Tarea")},
		application.ModoDryRun); err != nil {
		t.Fatalf("el dry-run fallo: %v", err)
	}
}

// mapperQueFalla entra en panic si se invoca.
type mapperQueFalla struct{ t *testing.T }

func (m *mapperQueFalla) ResolveHandle(ctx context.Context, rawName string) (string, error) {
	m.t.Error("SE RESOLVIERON IDENTIDADES EN MODO DRY-RUN")
	panic("consulta de identidades en dry-run")
}

// --- Publicacion real ---

func TestPublishResuelveIdentidades(t *testing.T) {
	tablero := &tableroContador{}

	caso, err := application.NuevoPublicarBacklog(application.PublicarOpciones{
		Identidades: mapperPorDefecto(),
		Tablero:     tablero,
	})
	if err != nil {
		t.Fatalf("no se pudo construir el caso de uso: %v", err)
	}

	items := []domain.ActionItem{
		item("Omar Hernández", "Migrar la sesion"),
		item("Ana María Ruiz", "Documentar los reintentos"),
	}

	resumen, err := caso.Ejecutar(context.Background(), "PVT_123", items, application.ModoPublish)
	if err != nil {
		t.Fatalf("la publicacion fallo: %v", err)
	}

	if tablero.numLlamadas() != 1 {
		t.Errorf("el tablero deberia haberse invocado una vez, se invoco %d veces", tablero.numLlamadas())
	}

	// Todos los items deben llevar handle resuelto.
	for _, publicado := range tablero.items {
		if publicado.MappedHandle == "" {
			t.Errorf("el item %q se publico sin handle resuelto", publicado.Title)
		}
	}

	if len(resumen.Publicados) != 2 {
		t.Errorf("se esperaban 2 resultados, se obtuvieron %d", len(resumen.Publicados))
	}
}

// TestPublishConIdentidadRealIntegra el mapper JSON de infraestructura con el
// caso de uso, comprobando la normalizacion de extremo a extremo.
func TestPublishConIdentidadRealIntegra(t *testing.T) {
	// La minuta escribe "Omar Hernandez" sin tilde; la tabla lo tiene con tilde.
	m, err := identity.NuevoJSONIdentityMapperDesdeBytes([]byte(`{
		"mappings": [
			{"raw_names": ["Omar Hernández"], "handle": "omarhernan"}
		]
	}`))
	if err != nil {
		t.Fatalf("no se pudo construir el mapper: %v", err)
	}

	tablero := &tableroContador{}

	caso, _ := application.NuevoPublicarBacklog(application.PublicarOpciones{
		Identidades: m,
		Tablero:     tablero,
	})

	items := []domain.ActionItem{item("Omar Hernandez", "Migrar la sesion")}

	if _, err := caso.Ejecutar(context.Background(), "PVT_1", items, application.ModoPublish); err != nil {
		t.Fatalf("la publicacion fallo: %v", err)
	}

	if len(tablero.items) != 1 || tablero.items[0].MappedHandle != "omarhernan" {
		t.Errorf("el nombre sin tilde no se resolvio a omarhernan: %+v", tablero.items)
	}
}

// --- Politicas ante identidades desconocidas ---

func TestPoliticaFallar(t *testing.T) {
	caso, _ := application.NuevoPublicarBacklog(application.PublicarOpciones{
		Identidades: mapperPorDefecto(),
		Tablero:     &tableroContador{},
		Politica:    application.AnteDesconocidoFallar,
	})

	items := []domain.ActionItem{
		item("Omar Hernández", "Tarea buena"),
		item("Desconocido Total", "Tarea sin responsable"),
	}

	_, err := caso.Ejecutar(context.Background(), "PVT_1", items, application.ModoPublish)

	if !errors.Is(err, domain.ErrIdentidadNoResuelta) {
		t.Fatalf("se esperaba ErrIdentidadNoResuelta, se obtuvo %v", err)
	}

	// El error debe nombrar a la persona, para que el usuario sepa que añadir.
	if !strings.Contains(err.Error(), "Desconocido Total") {
		t.Errorf("el error deberia nombrar al responsable desconocido: %v", err)
	}
}

func TestPoliticaSinAsignado(t *testing.T) {
	tablero := &tableroContador{}

	caso, _ := application.NuevoPublicarBacklog(application.PublicarOpciones{
		Identidades:      mapperPorDefecto(),
		Tablero:          tablero,
		Politica:         application.AnteDesconocidoSinAsignado,
		HandlePorDefecto: "sin-asignar",
	})

	items := []domain.ActionItem{
		item("Omar Hernández", "Tarea buena"),
		item("Desconocido Total", "Tarea sin responsable"),
	}

	resumen, err := caso.Ejecutar(context.Background(), "PVT_1", items, application.ModoPublish)
	if err != nil {
		t.Fatalf("la politica assign_unassigned no deberia fallar: %v", err)
	}

	// Ambos items se publican; el segundo con el handle por defecto.
	if len(tablero.items) != 2 {
		t.Fatalf("deben publicarse ambos items, se publicaron %d", len(tablero.items))
	}

	var sinAsignado bool
	for _, publicado := range tablero.items {
		if publicado.Title == "Tarea sin responsable" {
			sinAsignado = true
			if publicado.MappedHandle != "sin-asignar" {
				t.Errorf("el item sin responsable deberia llevar %q, lleva %q",
					"sin-asignar", publicado.MappedHandle)
			}
		}
	}

	if !sinAsignado {
		t.Error("el item sin responsable no llego al tablero")
	}
	if len(resumen.Omitidos) != 0 {
		t.Errorf("assign_unassigned no omite items: %v", resumen.Omitidos)
	}
}

func TestPoliticaOmitir(t *testing.T) {
	tablero := &tableroContador{}

	caso, _ := application.NuevoPublicarBacklog(application.PublicarOpciones{
		Identidades: mapperPorDefecto(),
		Tablero:     tablero,
		Politica:    application.AnteDesconocidoOmitir,
	})

	items := []domain.ActionItem{
		item("Omar Hernández", "Tarea buena"),
		item("Desconocido Total", "Tarea descartada"),
	}

	resumen, err := caso.Ejecutar(context.Background(), "PVT_1", items, application.ModoPublish)
	if err != nil {
		t.Fatalf("la politica skip no deberia fallar: %v", err)
	}

	if len(tablero.items) != 1 {
		t.Errorf("solo deberia publicarse la tarea con responsable conocido, se publicaron %d", len(tablero.items))
	}

	if len(resumen.Omitidos) != 1 {
		t.Errorf("se esperaba 1 item omitido, se omitieron %d", len(resumen.Omitidos))
	}
	if resumen.Omitidos[0].Title != "Tarea descartada" {
		t.Errorf("el item omitido no es el esperado: %q", resumen.Omitidos[0].Title)
	}
}

// TestAcumulaNombresDesconocidos comprueba que el error los lista todos, para
// que el usuario corrija la tabla de una vez.
func TestAcumulaNombresDesconocidos(t *testing.T) {
	caso, _ := application.NuevoPublicarBacklog(application.PublicarOpciones{
		Identidades: mapperPorDefecto(),
		Tablero:     &tableroContador{},
		Politica:    application.AnteDesconocidoFallar,
	})

	items := []domain.ActionItem{
		item("Fulano Uno", "Tarea 1"),
		item("Mengano Dos", "Tarea 2"),
		item("Omar Hernández", "Tarea 3"),
	}

	_, err := caso.Ejecutar(context.Background(), "PVT_1", items, application.ModoPublish)

	if err == nil {
		t.Fatal("se esperaba error")
	}
	for _, nombre := range []string{"Fulano Uno", "Mengano Dos"} {
		if !strings.Contains(err.Error(), nombre) {
			t.Errorf("el error deberia listar %q: %v", nombre, err)
		}
	}
}

// TestTodosDesconocidosConOmitirNoFallaConTableroVacio comprueba el borde: si
// todos los items se omiten, no se invoca al tablero.
func TestTodosDesconocidosConOmitir(t *testing.T) {
	tablero := &tableroContador{}

	caso, _ := application.NuevoPublicarBacklog(application.PublicarOpciones{
		Identidades: mapperPorDefecto(),
		Tablero:     tablero,
		Politica:    application.AnteDesconocidoOmitir,
	})

	items := []domain.ActionItem{item("Desconocido A", "Tarea 1")}

	resumen, err := caso.Ejecutar(context.Background(), "PVT_1", items, application.ModoPublish)
	if err != nil {
		t.Fatalf("fallo inesperado: %v", err)
	}

	if tablero.numLlamadas() != 0 {
		t.Error("sin items publicables no deberia invocarse al tablero")
	}
	if len(resumen.Omitidos) != 1 {
		t.Errorf("se esperaba 1 omitido, se obtuvieron %d", len(resumen.Omitidos))
	}
}

// --- Fallos parciales ---

// TestFalloParcialNoEsErrorGlobal: cinco de seis tarjetas creadas, el resumen lo
// refleja sin reportar un fallo total.
func TestFalloParcialNoEsErrorGlobal(t *testing.T) {
	tablero := &tableroContador{
		fallarEn: map[string]bool{"Tarea mala": true},
	}

	caso, _ := application.NuevoPublicarBacklog(application.PublicarOpciones{
		Identidades: mapperPorDefecto(),
		Tablero:     tablero,
	})

	items := []domain.ActionItem{
		item("Omar Hernández", "Tarea buena 1"),
		item("Ana María Ruiz", "Tarea mala"),
		item("Luis Cabrera", "Tarea buena 2"),
	}

	resumen, err := caso.Ejecutar(context.Background(), "PVT_1", items, application.ModoPublish)
	if err != nil {
		t.Fatalf("un fallo parcial no debe abortar la operacion: %v", err)
	}

	exitos, fallos := resumen.ResumenDePublicacion()
	if exitos != 2 || fallos != 1 {
		t.Errorf("se esperaban 2 exitos y 1 fallo, se obtuvieron %d y %d", exitos, fallos)
	}
}

// --- Configuracion y modos ---

func TestModoDesconocido(t *testing.T) {
	caso, _ := application.NuevoPublicarBacklog(application.PublicarOpciones{
		Identidades: mapperPorDefecto(),
		Tablero:     &tableroContador{},
	})

	_, err := caso.Ejecutar(context.Background(), "PVT_1",
		[]domain.ActionItem{item("Omar Hernández", "Tarea")}, "modo-inventado")

	if !errors.Is(err, domain.ErrConfigInvalida) {
		t.Errorf("se esperaba ErrConfigInvalida, se obtuvo %v", err)
	}
}

func TestConfiguracionIncompleta(t *testing.T) {
	casos := []struct {
		nombre   string
		opciones application.PublicarOpciones
	}{
		{
			nombre:   "sin identidades",
			opciones: application.PublicarOpciones{Tablero: &tableroContador{}},
		},
		{
			nombre:   "sin tablero",
			opciones: application.PublicarOpciones{Identidades: mapperPorDefecto()},
		},
		{
			nombre: "politica invalida",
			opciones: application.PublicarOpciones{
				Identidades: mapperPorDefecto(),
				Tablero:     &tableroContador{},
				Politica:    "inventada",
			},
		},
	}

	for _, tt := range casos {
		t.Run(tt.nombre, func(t *testing.T) {
			_, err := application.NuevoPublicarBacklog(tt.opciones)

			if !errors.Is(err, domain.ErrConfigInvalida) {
				t.Errorf("se esperaba ErrConfigInvalida, se obtuvo %v", err)
			}
		})
	}
}

func TestPublishRespetaContextoCancelado(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	caso, _ := application.NuevoPublicarBacklog(application.PublicarOpciones{
		Identidades: mapperPorDefecto(),
		Tablero:     &tableroContador{},
	})

	_, err := caso.Ejecutar(ctx, "PVT_1",
		[]domain.ActionItem{item("Omar Hernández", "Tarea")}, application.ModoPublish)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("se esperaba context.Canceled, se obtuvo %v", err)
	}
}

// TestMapeoDeErroresIdentidadNoMascaraFalloOperativo: un fallo que no es
// "nombre desconocido" (contexto cancelado, error de red) no debe tratarse como
// si fuera un nombre no mapeado.
func TestFalloOperativoDelMapperSePropaga(t *testing.T) {
	caso, _ := application.NuevoPublicarBacklog(application.PublicarOpciones{
		Identidades: &mapperConFallo{},
		Tablero:     &tableroContador{},
	})

	_, err := caso.Ejecutar(context.Background(), "PVT_1",
		[]domain.ActionItem{item("Omar Hernández", "Tarea")}, application.ModoPublish)

	if err == nil {
		t.Fatal("se esperaba error")
	}
	if errors.Is(err, domain.ErrIdentidadNoResuelta) {
		t.Errorf("un fallo operativo no debe disfrazarse de nombre desconocido: %v", err)
	}
}

type mapperConFallo struct{}

func (m *mapperConFallo) ResolveHandle(ctx context.Context, rawName string) (string, error) {
	return "", errors.New("el directorio de la empresa no responde")
}
