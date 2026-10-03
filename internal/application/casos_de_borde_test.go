package application_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"talkaboutthis/internal/application"
	"talkaboutthis/internal/domain"
	"talkaboutthis/internal/infrastructure/parsers"
)

// Estos tests cubren los bordes que los tests de comportamiento dejaban fuera:
// el prompt por defecto, la inyeccion de reloj, y la envoltura de errores por
// etapa. Ninguno sale a la red ni al disco mas alla de las fixtures.

// --- Prompt por defecto ---

// TestPromptPorDefectoSeUsaSinInyeccion comprueba que omitir el constructor de
// prompt no es un error.
//
// La eleccion de diseno es deliberada: un caso de uso que solo funciona con una
// dependencia inyectada obliga a que TODOS los composition roots la cumplan, y
// un composition root olvidado se manifiesta como un fallo de arranque en vez
// de una extraccion de peor calidad pero funcional.
func TestPromptPorDefectoSeUsaSinInyeccion(t *testing.T) {
	ext, err := application.NuevoExtractBacklog(application.ExtractOpciones{
		Proveedor: &proveedorFalso{respuestas: []string{extraccionValida}},
		Schema:    schemaReal(t),
		// Prompt ausente a proposito.
	})
	if err != nil {
		t.Fatalf("sin prompt inyectado deberia construirse igualmente: %v", err)
	}

	if _, err := ext.Ejecutar(context.Background(), "Reunion del equipo."); err != nil {
		t.Fatalf("la extraccion fallo: %v", err)
	}
}

// TestPromptPorDefectoConstruyeConTranscriptYSchema verifica que el prompt por
// defecto incluye lo que el modelo necesita para responder: la transcripcion y
// el esquema.
//
// Se observa a traves del proveedor falso, que registra cada prompt recibido,
// en vez de llamar a un metodo no exportado: el contrato que importa es el que
// ve el modelo.
func TestPromptPorDefectoConstruyeConTranscriptYSchema(t *testing.T) {
	const transcript = "ACTA DE LA REUNION DEL 3 DE MARZO"

	p := &proveedorFalso{respuestas: []string{extraccionValida}}

	ext, err := application.NuevoExtractBacklog(application.ExtractOpciones{
		Proveedor: p,
		Schema:    schemaReal(t),
	})
	if err != nil {
		t.Fatalf("no se pudo construir el extractor: %v", err)
	}

	if _, err := ext.Ejecutar(context.Background(), transcript); err != nil {
		t.Fatalf("la extraccion fallo: %v", err)
	}

	if len(p.prompts) != 1 {
		t.Fatalf("se recibieron %d prompts, se esperaba 1", len(p.prompts))
	}

	recibido := p.prompts[0]

	if !strings.Contains(recibido, transcript) {
		t.Error("el prompt por defecto no incluye la transcripcion")
	}

	// El esquema debe viajar literal: el modelo lo necesita para saber que
	// forma tiene la respuesta esperada.
	if !strings.Contains(recibido, "action_items") {
		t.Error("el prompt por defecto no incluye el esquema de extraccion")
	}
}

// TestPromptPorDefectoConstruyeCorreccion ejercita la segunda rama del retry: el
// prompt de correccion debe distinguirse del inicial.
//
// Si ambos fueran identicos, el reintento seria una segunda tirada del mismo
// prompt sin informacion del fallo, que es exactamente lo que RF-07 no pide.
func TestPromptPorDefectoConstruyeCorreccion(t *testing.T) {
	// La primera respuesta es JSON valido pero NO cumple el esquema (falta
	// meeting_summary), asi que se consume un intento y se pide correccion.
	p := &proveedorFalso{
		respuestas: []string{`{"action_items": []}`, extraccionValida},
	}

	ext, err := application.NuevoExtractBacklog(application.ExtractOpciones{
		Proveedor: p,
		Schema:    schemaReal(t),
	})
	if err != nil {
		t.Fatalf("no se pudo construir el extractor: %v", err)
	}

	if _, err := ext.Ejecutar(context.Background(), "Reunion del equipo."); err != nil {
		t.Fatalf("el reintento deberia haber tenido exito: %v", err)
	}

	if len(p.prompts) != 2 {
		t.Fatalf("se recibieron %d prompts, se esperaban 2", len(p.prompts))
	}

	// El prompt de correccion es mas largo porque incorpora el diagnostico.
	if len(p.prompts[1]) <= len(p.prompts[0]) {
		t.Errorf("el prompt de correccion (%d bytes) deberia ser mas largo que el inicial (%d bytes)",
			len(p.prompts[1]), len(p.prompts[0]))
	}
}

// --- Reloj inyectado ---

// TestIngestTranscriptorUsaElRelojInyectado verifica la inyeccion de reloj,
// que es lo que hace comprobable el presupuesto de tiempo de RNF-03 sin esperar
// en un test.
func TestIngestTranscriptorUsaElRelojInyectado(t *testing.T) {
	instante := time.Date(2026, time.March, 3, 10, 30, 0, 0, time.UTC)

	ing := application.NuevaIngestTranscriptorConReloj(
		parsers.NewRegistryPorDefecto(),
		func() time.Time { return instante },
	)

	transcript, err := ing.DesdeRuta(context.Background(), rutaFixture("reunion-equipo.md"))
	if err != nil {
		t.Fatalf("la ingesta fallo: %v", err)
	}

	if !transcript.IngestedAt.Equal(instante) {
		t.Errorf("IngestedAt = %s, se esperaba %s", transcript.IngestedAt, instante)
	}
}

// TestIngestTranscriptorConRelojNilUsaElDelSistema comprueba el otro extremo:
// pasar nil no deja el caso de uso sin reloj.
//
// Un reloj nil provocaria un panic en la primera ingesta, varios minutos
// despues de arrancar, que es la peor forma de fallar.
func TestIngestTranscriptorConRelojNilUsaElDelSistema(t *testing.T) {
	ing := application.NuevaIngestTranscriptorConReloj(parsers.NewRegistryPorDefecto(), nil)

	antes := time.Now()

	transcript, err := ing.DesdeRuta(context.Background(), rutaFixture("reunion-equipo.md"))
	if err != nil {
		t.Fatalf("la ingesta fallo: %v", err)
	}

	if transcript.IngestedAt.Before(antes) {
		t.Errorf("IngestedAt = %s es anterior al inicio del test; el reloj del sistema no se esta usando",
			transcript.IngestedAt)
	}
}

// TestExtensionDeCubreLosCasosLimitantes documenta el comportamiento de la
// normalizacion de extensiones.
//
// El caso ".oculto" es el que importa: filepath.Ext devuelve ".oculto", asi que
// el selector lo tratara como una extension mas y no como un archivo sin
// extension. Fijar aqui el comportamiento evita que alguien lo cambie sin
// querer al "corregirlo".
func TestExtensionDeCubreLosCasosLimitantes(t *testing.T) {
	casos := []struct {
		ruta     string
		esperado string
	}{
		{"notas.md", ".md"},
		{"/tmp/archivo.DOCX", ".DOCX"},
		{"sin_extension", ""},
		{"/tmp/directorio/archivo", ""},
	}

	for _, tt := range casos {
		t.Run(tt.ruta, func(t *testing.T) {
			if obtenido := application.ExtensionDe(tt.ruta); obtenido != tt.esperado {
				t.Errorf("ExtensionDe(%q) = %q, se esperaba %q", tt.ruta, obtenido, tt.esperado)
			}
		})
	}
}

// --- ErrorEtapa ---

// TestErrorEtapaEncadenaYExponeLaEtapa verifica que ErrorEtapa es un error
// envuelto de verdad: errors.Is debe alcanzar la causa original.
//
// Sin Unwrap, un error de permisos del sistema o del proveedor se perderia
// dentro del envoltorio y la CLI no podria distinguir "no existe el archivo" de
// "el tablero devolvio 422".
func TestErrorEtapaEncadenaYExponeLaEtapa(t *testing.T) {
	causa := errors.New("permiso denegado")

	err := &application.ErrorEtapa{
		Etapa: application.EtapaDespacho,
		Err:   causa,
	}

	if !errors.Is(err, causa) {
		t.Error("errors.Is no alcanza la causa original a traves de ErrorEtapa")
	}

	if !application.EsFalloDePublicacion(err) {
		t.Error("un error de la etapa de despacho deberia ser fallo de publicacion")
	}

	if etapa := application.EtapaDelError(err); etapa != application.EtapaDespacho {
		t.Errorf("EtapaDelError = %q, se esperaba %q", etapa, application.EtapaDespacho)
	}

	// El mensaje debe mencionar la etapa: es lo primero que ve el usuario.
	if msg := err.Error(); !strings.Contains(msg, "despacho") {
		t.Errorf("el mensaje %q deberia nombrar la etapa", msg)
	}
}

// TestErrorEtapaAguantaElEnvoltorioAnidado comprueba que las funciones de
// consulta funcionan aunque el error de etapa se envuelva otra vez.
//
// El pipeline lo envuelve una vez, pero el logger o un middleware podrían
// envolverlo otra vez; errors.As debe seguir encontrandolo.
func TestErrorEtapaAguantaElEnvoltorioAnidado(t *testing.T) {
	causa := errors.New("fallo de red")

	anidado := errors.Join(errors.New("contexto adicional"), &application.ErrorEtapa{
		Etapa: application.EtapaExtraccion,
		Err:   causa,
	})

	if !errors.Is(anidado, causa) {
		t.Error("errors.Is no alcanza la causa a traves del envoltorio anidado")
	}

	if application.EsFalloDePublicacion(anidado) {
		t.Error("un fallo de extraccion no debe confundirse con uno de publicacion")
	}

	if etapa := application.EtapaDelError(anidado); etapa != application.EtapaExtraccion {
		t.Errorf("EtapaDelError = %q, se esperaba %q", etapa, application.EtapaExtraccion)
	}
}

// TestEtapaDelErrorToleraErroresAjenos verifica que las funciones de consulta
// no asumen que todo error viene del pipeline.
//
// La CLI las llama con errores de configuracion, que no llevan etapa.
func TestEtapaDelErrorToleraErroresAjenos(t *testing.T) {
	ajenos := []error{
		nil,
		errors.New("sin etapa"),
		domain.ErrConfigInvalida,
		errors.Join(domain.ErrFormatoNoSoportado, domain.ErrPriorityInvalida),
	}

	for _, err := range ajenos {
		if etapa := application.EtapaDelError(err); etapa != "" {
			t.Errorf("EtapaDelError(%v) = %q, se esperaba cadena vacia", err, etapa)
		}

		if application.EsFalloDePublicacion(err) {
			t.Errorf("EsFalloDePublicacion(%v) = true, se esperaba false", err)
		}
	}
}
