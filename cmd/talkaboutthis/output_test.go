package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"strings"
	"testing"

	"talkaboutthis/internal/application"
	"talkaboutthis/internal/domain"
)

// Estos tests viven dentro de package main porque las funciones que cubren
// (imprimirUsoIngest, codigoDeEtapa y los formateadores de output.go) son
// privadas de la CLI: son su API, pero no forman parte de un paquete importable.
//
// Es un coste asumido: la orquestacion vive en internal/application, y lo que
// queda aqui es presentacion, que es exactamente lo que hay que probar a mano.

// --- Uso y ayuda ---

// TestImprimirUsoIngestDocumentaLasBanderas documenta que la ayuda refleja las
// banderas REALES.
//
// Es el contrato con el usuario. Una bandera que existe en el codigo pero no
// aparece en el ayuda (o al reves) hace que descubrirla sea un ejercicio de
// lectura del fuente; esta lista es la fuente de verdad de la interfaz.
func TestImprimirUsoIngestDocumentaLasBanderas(t *testing.T) {
	var buf bytes.Buffer

	imprimirUsoIngest(&buf)

	ayuda := buf.String()

	if ayuda == "" {
		t.Fatal("el ayuda de ingest esta vacio")
	}

	banderas := []string{
		"--file",
		"--provider",
		"--timeout",
		"--dry-run",
		"--publish",
		"--output",
		"--project",
		"--owner",
		"--repo",
		"--adapter",
		"--mappings",
		"--assignee-policy",
		"--log-level",
		"--log-format",
	}

	for _, bandera := range banderas {
		if !strings.Contains(ayuda, bandera) {
			t.Errorf("el ayuda de ingest no menciona la bandera %s", bandera)
		}
	}

	// El modo por defecto debe estar destacado: es la garantia de TC-05 y el
	// usuario tiene que verla sin buscar en el codigo.
	if !strings.Contains(ayuda, "POR DEFECTO") {
		t.Error("el ayuda deberia destacar cual es el modo por defecto")
	}

	// Los proveedores soportados deben ser los cuatro reales.
	for _, proveedor := range []string{"ollama", "openai", "anthropic", "gemini"} {
		if !strings.Contains(ayuda, proveedor) {
			t.Errorf("el ayuda no menciona el proveedor %s", proveedor)
		}
	}
}

// TestImprimirUsoGeneralNoRepiteElAyudaDeIngest comprueba que los dos ayudas
// estan separados.
func TestImprimirUsoGeneral(t *testing.T) {
	var buf bytes.Buffer

	imprimirUso(&buf)

	ayuda := buf.String()

	if !strings.Contains(ayuda, "ingest") {
		t.Error("el ayuda general debe listar el subcomando ingest")
	}

	if !strings.Contains(ayuda, "--version") {
		t.Error("el ayuda general debe mencionar --version")
	}

	// El detalle de las banderas de ingest vive en su propio ayuda: duplicarlo
	// obliga a mantener dos textos en sincronia.
	if strings.Contains(ayuda, "--assignee-policy") {
		t.Error("el ayuda general no debe detallar las banderas de ingest")
	}
}

// --- Codigos de salida ---

// TestCodigoDeEtapaMapeaCadaEtapaASuCodigo fija la tabla de traduccion de
// etapa a codigo.
//
// El proceso de CI decide con estos numeros: si publicar tres de cinco tarjetas
// devolviera el codigo de ingesta, un pipeline de integracion no distinguiria
// "el tablero no acepto el contenido" de "el archivo no se pudo leer".
func TestCodigoDeEtapaMapeaCadaEtapaASuCodigo(t *testing.T) {
	casos := []struct {
		etapa    application.PipelineEtapa
		err      error
		esperado int
	}{
		{application.EtapaIngesta, domain.ErrFormatoNoSoportado, salidaErrorIngesta},
		{application.EtapaExtraccion, domain.ErrReintentosAgotados, salidaErrorExtraccion},
		{application.EtapaDespacho, domain.ErrPublicacionFallida, salidaErrorPublica},
	}

	for _, tt := range casos {
		t.Run(string(tt.etapa), func(t *testing.T) {
			if obtenido := codigoDeEtapa(tt.etapa, tt.err); obtenido != tt.esperado {
				t.Errorf("codigoDeEtapa(%q) = %d, se esperaba %d", tt.etapa, obtenido, tt.esperado)
			}
		})
	}
}

// TestCodigoDeEtapaDesconocidaDelegaEnElError comprueba el fallback.
//
// Una etapa vacia significa que el error no viene del pipeline, y entonces lo
// que sabe del codigo correcto es el propio error.
func TestCodigoDeEtapaDesconocidaDelegaEnElError(t *testing.T) {
	casos := []struct {
		nombre   string
		err      error
		esperado int
	}{
		{"configuracion", domain.ErrConfigInvalida, salidaErrorConfig},
		{"identidad", domain.ErrIdentidadNoResuelta, salidaErrorIdentidad},
		{"uso", errFaltanParametros, salidaErrorUso},
	}

	for _, tt := range casos {
		t.Run(tt.nombre, func(t *testing.T) {
			// Se pasa una etapa que no es ninguna de las tres conocidas.
			if obtenido := codigoDeEtapa(application.PipelineEtapa("rara"), tt.err); obtenido != tt.esperado {
				t.Errorf("codigoDeEtapa con etapa desconocida = %d, se esperaba %d", obtenido, tt.esperado)
			}
		})
	}
}

// TestCodigoDeErrorDevuelveOKParaAyuda comprueba el caso singular de
// flag.ErrHelp.
//
// --help no es un fallo: la CLI termino con exito, solo que pidio otra cosa.
func TestCodigoDeErrorDevuelveOKParaAyuda(t *testing.T) {
	if obtenido := codigoDeError(flag.ErrHelp); obtenido != salidaOK {
		t.Errorf("codigoDeError(flag.ErrHelp) = %d, se esperaba %d", obtenido, salidaOK)
	}
}

// TestCodigoDeErrorParaErroresDesconocidos comprueba el default.
//
// Un error no reconocido se reporta como error de uso: es la eleccion
// conservadora, porque significa "algo que no sabemos que hacer" y no "algo que
// sabemos que va bien".
func TestCodigoDeErrorParaErroresDesconocidos(t *testing.T) {
	desconocidos := []error{
		errors.New("algo raro"),
		context.Canceled,
		nil,
	}

	for _, err := range desconocidos {
		if obtenido := codigoDeError(err); obtenido != salidaErrorUso {
			t.Errorf("codigoDeError(%v) = %d, se esperaba %d", err, obtenido, salidaErrorUso)
		}
	}
}

// --- Formateadores de salida ---

// TestPrioridadOrdenadaMapeaLosTresNiveles documenta la traduccion al español
// que ve el usuario en la tabla.
func TestPrioridadOrdenadaMapeaLosTresNiveles(t *testing.T) {
	casos := []struct {
		prioridad domain.Priority
		esperado  string
	}{
		{domain.PriorityHigh, "ALTA"},
		{domain.PriorityMedium, "MEDIA"},
		{domain.PriorityLow, "BAJA"},
	}

	for _, tt := range casos {
		t.Run(tt.esperado, func(t *testing.T) {
			if obtenido := prioridadOrdenada(tt.prioridad); obtenido != tt.esperado {
				t.Errorf("prioridadOrdenada(%q) = %q, se esperaba %q", tt.prioridad, obtenido, tt.esperado)
			}
		})
	}
}

// TestPrioridadOrdenadaMuestraElValorCrudoSiSeCola documenta el fallback.
//
// El dominio ya rechaza las prioridades invalidas, asi que esta rama es de
// diagnostico: si una se colara, el usuario tiene que VERLA, no ver un hueco en
// la tabla que sugiere que el item no tiene prioridad.
func TestPrioridadOrdenadaMuestraElValorCrudoSiSeCola(t *testing.T) {
	colada := domain.Priority("URGENTE")

	if obtenido := prioridadOrdenada(colada); obtenido != "URGENTE" {
		t.Errorf("prioridadOrdenada(%q) = %q, se esperaba el valor crudo", colada, obtenido)
	}

	if obtenido := prioridadOrdenada(domain.Priority("")); obtenido != "" {
		t.Errorf("prioridadOrdenada(vacia) = %q, se esperaba cadena vacia", obtenido)
	}
}

// TestEstimacionLegibleMuestraGuionSiNoHayPuntos documenta por que un 0 se
// imprime como guion y no como "0".
func TestEstimacionLegible(t *testing.T) {
	casos := []struct {
		puntos   int
		esperado string
	}{
		{3, "3"},
		{13, "13"},
		{0, "-"},
		{-1, "-"},
	}

	for _, tt := range casos {
		t.Run(tt.esperado, func(t *testing.T) {
			if obtenido := estimacionLegible(tt.puntos); obtenido != tt.esperado {
				t.Errorf("estimacionLegible(%d) = %q, se esperaba %q", tt.puntos, obtenido, tt.esperado)
			}
		})
	}
}

// TestTituloEnLineaColapsaSaltos verifica que la tabla no se rompe por un titulo
// con saltos de linea, tabuladores o espacios.
//
// El titulo viene del LLM. Si trae un "\n", cada fila ocuparia varias lineas y
// la tabla dejaria de ser una tabla.
func TestTituloEnLineaColapsaSaltos(t *testing.T) {
	casos := []struct {
		entrada  string
		esperado string
	}{
		{"Titulo normal", "Titulo normal"},
		{"Titulo\ncon salto", "Titulo con salto"},
		{"Titulo\r\ncon CRLF", "Titulo con CRLF"},
		{"Titulo\tcon tabulador", "Titulo con tabulador"},
		{"  Titulo con espacios  ", "Titulo con espacios"},
		{"\n\n", ""},
		{"", ""},
	}

	for _, tt := range casos {
		t.Run(tt.entrada, func(t *testing.T) {
			obtenido := tituloEnLinea(tt.entrada)

			if obtenido != tt.esperado {
				t.Errorf("tituloEnLinea(%q) = %q, se esperaba %q", tt.entrada, obtenido, tt.esperado)
			}

			for _, salto := range []string{"\n", "\r", "\t"} {
				if strings.Contains(obtenido, salto) {
					t.Errorf("el resultado %q todavia contiene un salto de linea", obtenido)
				}
			}
		})
	}
}

// TestModoDeCubreResultadoSinDespacho documenta el borde.
//
// Un resultado sin despacho ocurre cuando el pipeline falla antes de llegar a
// esa fase; imprimir "desconocido" es mejor que un panic al leer un nil.
func TestModoDeCubreResultadoSinDespacho(t *testing.T) {
	if obtenido := modoDe(&application.PipelineResultado{}); obtenido != "desconocido" {
		t.Errorf("modoDe sin despacho = %q, se esperaba desconocido", obtenido)
	}

	if obtenido := modoDe(nil); obtenido != "desconocido" {
		t.Errorf("modoDe(nil) = %q, se esperaba desconocido", obtenido)
	}

	conDespacho := &application.PipelineResultado{
		Despacho: &application.ResumenDespacho{Modo: application.ModoDryRun},
	}

	if obtenido := modoDe(conDespacho); obtenido != string(application.ModoDryRun) {
		t.Errorf("modoDe = %q, se esperaba %q", obtenido, application.ModoDryRun)
	}
}

// TestResponsableLegibleNoMarcaEnDryRun protege el comportamiento corregido al
// cerrar el proyecto.
//
// El dry-run no consulta la tabla de aliases (es la garantia de TC-05), asi que
// el handle siempre esta vacio. Marcarlo como "SIN RESOLVER" levantaba una
// alarma falsa en cada fila.
func TestResponsableLegibleNoMarcaEnDryRun(t *testing.T) {
	item := domain.ActionItem{
		Title:        "Migrar la sesion",
		RawAssignee:  "Omar Hernández",
		MappedHandle: "",
	}

	enSeco := responsableLegible(item, true)
	if enSeco != "Omar Hernández" {
		t.Errorf("en dry-run se obtuvo %q, se esperaba el nombre sin marca", enSeco)
	}

	if strings.Contains(enSeco, "SIN RESOLVER") {
		t.Error("el dry-run no intenta resolver identidades: marcarlo es una alarma falsa")
	}

	// En publicacion, en cambio, si debe marcarse: ahi el fallo es real.
	alPublicar := responsableLegible(item, false)
	if !strings.Contains(alPublicar, "SIN RESOLVER") {
		t.Errorf("al publicar se obtuvo %q, se esperaba la marca SIN RESOLVER", alPublicar)
	}

	// Y con handle resuelto no hay marca en ningun modo.
	resuelto := item
	resuelto.MappedHandle = "omarhernandez"

	if obtenido := responsableLegible(resuelto, false); obtenido != "omarhernandez" {
		t.Errorf("con handle resuelto se obtuvo %q, se esperaba el handle", obtenido)
	}
}
