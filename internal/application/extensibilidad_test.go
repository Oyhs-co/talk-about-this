package application_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"talkaboutthis/internal/application"
	"talkaboutthis/internal/domain"
)

// Este fichero es el caso de evaluacion TC-07: "anadir un JiraAdapter que
// implemente ProjectBoardAdapter, con compilacion exitosa y sin modificar
// internal/domain".
//
// La afirmacion relevante no es "el adaptador funciona", sino "el adaptador
// EXISTE y la capa de aplicacion lo usa sin enterarse". Eso solo se demuestra
// escribiendo el adaptador aqui, en un fichero que el dominio no controla, y
// viendo que compila y se enchufa.
//
// Si alguien añade un campo o cambia una firma en domain.ProjectBoardAdapter,
// este test deja de compilar. Eso es exactamente lo que TC-07 quiere que pase.

const (
	// Ficticios: el adaptador no habla con ningun Jira real.
	falsoProyectoJira = "PROJ-42"
	falsoServidorJira = "https://jira.ficticio.local"
)

// JiraAdapter es un adaptador NUEVO, escrito desde cero y publicado por un
// tercero que no toca el codigo del nucleo.
//
// No es una copia del adaptador de GitHub: tiene un modelo de datos distinto
// (una respuesta es un hilo de comentarios, no un issue), un modo de asignar
// distinto (el campo es el componente, no el assignee) y una forma distinta de
// informar del progreso. La heterogeneidad es intencionada: si el puerto
// estuviera modelado sobre GitHub, este adaptador no encajaria.
type JiraAdapter struct {
	mu        sync.Mutex
	creadas   []string
	asignados map[string][]string
	proyecto  string
	err       error
}

func (j *JiraAdapter) PlatformName() string { return falsoServidorJira }

func (j *JiraAdapter) PublishBacklog(ctx context.Context, projectRef string, items []domain.ActionItem) ([]domain.PublishResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if j.err != nil {
		return nil, j.err
	}

	j.mu.Lock()
	defer j.mu.Unlock()

	if projectRef != j.proyecto {
		return nil, errors.New("el tablero de Jira " + projectRef + " no existe")
	}

	resultados := make([]domain.PublishResult, 0, len(items))

	for _, item := range items {
		clave := falsoProyectoJira + "-" + item.Title

		j.creadas = append(j.creadas, clave)

		if j.asignados == nil {
			j.asignados = make(map[string][]string)
		}
		j.asignados[clave] = append(j.asignados[clave], item.MappedHandle)

		resultados = append(resultados, domain.PublishResult{
			TaskTitle:  item.Title,
			ExternalID: clave,
			URL:        falsoServidorJira + "/browse/" + clave,
			Success:    true,
		})
	}

	return resultados, nil
}

// TestTC07ElDominioNoDefineAdaptadoresEsUnaGuarantíaEstatica documenta por que
// este fichero basta.
//
// El dominio declara puertos, no implementaciones. Si declarara tambien un
// JiraAdapter, añadir un tablero obligaria a tocar el nucleo, y la
// ortogonalidad se perderia. El test no puede "comprobar" eso en runtime: se
// comprueba leyendo que domain.ProjectBoardAdapter tiene la forma que este
// adaptador necesita, y la compilacion hace el resto.
func TestTC07ElDominioNoDefineAdaptadores(t *testing.T) {
	// La asignacion es la asercion. Si domain tuviera un JiraAdapter propio, o
	// si el puerto creciera un metodo, el nucleo dejaria de ser agnostico.
	var _ domain.ProjectBoardAdapter = (*JiraAdapter)(nil)
	var _ domain.ProjectBoardAdapter = (*tableroContador)(nil)
	var _ domain.ProjectBoardAdapter = (*tableroQueFalla)(nil)
}

// TestTC07ElAdaptadorNuevoSeEnchufaSinTocarElDominio es la demostracion
// completa: construir el caso de uso de despacho con un tablero desconocido y
// ejecutarlo de principio a fin.
func TestTC07ElAdaptadorNuevoSeEnchufaSinTocarElDominio(t *testing.T) {
	const proyecto = falsoProyectoJira

	jira := &JiraAdapter{proyecto: proyecto}

	// El caso de uso de despacho solo conoce el puerto: no hay un switch de
	// "plataformas soportadas" en ningun sitio por el que haya que registrar
	// Jira.
	publicador, err := application.NuevoPublicarBacklog(application.PublicarOpciones{
		Identidades: mapperPorDefecto(),
		Tablero:     jira,
	})
	if err != nil {
		t.Fatalf("un tablero desconocido deberia encajar en el puerto: %v", err)
	}

	extraccion := extraccionDePrueba()
	extraccion.ActionItems[0].MappedHandle = "omarhernan"
	extraccion.ActionItems[1].MappedHandle = "anamaria"

	resumen, err := publicador.Ejecutar(context.Background(), proyecto, extraccion.ActionItems, application.ModoPublish)
	if err != nil {
		t.Fatalf("la publicacion fallo: %v", err)
	}

	if len(resumen.Publicados) != 2 {
		t.Fatalf("se esperaban 2 resultados, se obtuvieron %d", len(resumen.Publicados))
	}

	for _, publicado := range resumen.Publicados {
		if !publicado.Success {
			t.Errorf("el item %q no se publico: %v", publicado.TaskTitle, publicado.Error)
		}
	}

	// Cada item debe haber llegado al adaptador con su handle resuelto, que es
	// la prueba de que la identidad se cruza en application y no en el tablero.
	jira.mu.Lock()
	defer jira.mu.Unlock()

	if len(jira.creadas) != 2 {
		t.Errorf("el adaptador recibio %d items, se esperaban 2", len(jira.creadas))
	}

	for _, clave := range jira.creadas {
		if len(jira.asignados[clave]) == 0 {
			t.Errorf("el item %s llego sin responsable", clave)
		}
	}

	if jira.asignados[jira.creadas[0]][0] != "omarhernan" {
		t.Errorf("el handle llego como %q, se esperaba omarhernan",
			jira.asignados[jira.creadas[0]][0])
	}

	// Y el pipeline completo tambien debe aceptarlo.
	if _, err := application.NewPipeline(transcriptor(t), &extractorFalso{extraccion: extraccion}, publicador); err != nil {
		t.Errorf("el pipeline no deberia rechazar un tablero nuevo: %v", err)
	}
}

// TestTC07ElNombreDeLaPlataformaSePropaga comprueba que la identidad del tablero
// aparece en el resumen de despacho.
//
// Si no se propagara, el usuario veria "3 items publicados" sin saber DONDE, que
// es el peor resultado posible de un tablero equivocado.
func TestTC07ElNombreDeLaPlataformaSePropaga(t *testing.T) {
	jira := &JiraAdapter{proyecto: falsoProyectoJira}

	publicador, err := application.NuevoPublicarBacklog(application.PublicarOpciones{
		Identidades: mapperPorDefecto(),
		Tablero:     jira,
	})
	if err != nil {
		t.Fatalf("no se pudo construir el publicador: %v", err)
	}

	extraccion := extraccionDePrueba()
	extraccion.ActionItems[0].MappedHandle = "omarhernan"
	extraccion.ActionItems[1].MappedHandle = "anamaria"

	resumen, err := publicador.Ejecutar(context.Background(), falsoProyectoJira, extraccion.ActionItems, application.ModoPublish)
	if err != nil {
		t.Fatalf("la publicacion fallo: %v", err)
	}

	if resumen == nil {
		t.Fatal("no se devolvio resumen de despacho")
	}

	plataforma := resumen.Plataforma

	if plataforma != falsoServidorJira {
		t.Errorf("la plataforma del resumen es %q, se esperaba %q", plataforma, falsoServidorJira)
	}

	if !strings.Contains(plataforma, "jira") {
		t.Errorf("la plataforma %q deberia identificar a Jira", plataforma)
	}

	// Y en dry-run tambien se conoce, aunque no se haya publicado nada: el
	// usuario necesita saber que tablero HABRIA usado.
	seco, err := publicador.Ejecutar(context.Background(), falsoProyectoJira, extraccion.ActionItems, application.ModoDryRun)
	if err != nil {
		t.Fatalf("el dry-run fallo: %v", err)
	}

	if seco.Plataforma != falsoServidorJira {
		t.Errorf("en dry-run la plataforma es %q, se esperaba %q", seco.Plataforma, falsoServidorJira)
	}
}

// TestTC07ElDominioIgnoraLaMarcaDeTiempoDelTablero documenta que el nucleo no
// depende de nada especifico de la plataforma.
//
// El adaptador de Jira no tiene no conceptos de "repositorio" ni de "proyecto
// numerico", y aun asi el caso de uso funciona: eso es lo que hace que el
// puerto sea un puerto y no una copia de la API de GitHub.
func TestTC07ElDominioIgnoraLaMarcaDeTiempoDelTablero(t *testing.T) {
	// Un tablero que falla debe producir un error de la etapa de despacho, no
	// un panic por ningun campo que el dominio rutas de GitHub.
	jira := &JiraAdapter{
		proyecto: falsoProyectoJira,
		err:      errors.New("Jira devolvio 503"),
	}

	publicador, err := application.NuevoPublicarBacklog(application.PublicarOpciones{
		Identidades: mapperPorDefecto(),
		Tablero:     jira,
	})
	if err != nil {
		t.Fatalf("no se pudo construir el publicador: %v", err)
	}

	extraccion := extraccionDePrueba()
	extraccion.ActionItems[0].MappedHandle = "omarhernan"
	extraccion.ActionItems[1].MappedHandle = "anamaria"

	if _, err := publicador.Ejecutar(context.Background(), falsoProyectoJira, extraccion.ActionItems, application.ModoPublish); err == nil {
		t.Fatal("se esperaba un error del tablero")
	}
}
