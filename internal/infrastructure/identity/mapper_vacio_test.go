package identity_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"talkaboutthis/internal/infrastructure/identity"
)

// Estos tests cubren el mapper de dry-run y la normalizacion de nombres, dos
// rutas que la suite no cubria pese a estar en el camino critico de la
// publicacion.

// --- MapperVacio ---

// TestMapperVacioNoResuelveNada documenta el contrato del mapper de dry-run.
//
// Existe para que el modo seco pueda inyectar una identidad sin exigir el
// fichero de mapeo. Si resolvera algo por su cuenta, publicaria silenciosamente
// con un responsable equivocado, que es justo el fallo que TC-04 evita.
func TestMapperVacioNoResuelveNada(t *testing.T) {
	m := identity.MapperVacio()

	casos := []string{
		"Omar Hernández",
		"omar",
		"ana maria",
		"cualquier nombre",
		"",
	}

	for _, nombre := range casos {
		t.Run(nombre, func(t *testing.T) {
			manejador, err := m.ResolveHandle(context.Background(), nombre)

			if err == nil {
				t.Fatalf("un mapper vacio no debe resolver %q, devolvio %q", nombre, manejador)
			}

			if manejador != "" {
				t.Errorf("se devolvio el manejador %q para un nombre no conocido", manejador)
			}
		})
	}
}

// TestMapperVacioUsaLaPoliticaDeNoAsignar comprueba que ante un nombre
// desconocido se deja el item sin responsable en vez de inventar uno.
func TestMapperVacioUsaLaPoliticaDeNoAsignar(t *testing.T) {
	m := identity.MapperVacio()

	if politica := m.Politica(); politica != identity.PoliticaAsignarSinAsignado {
		t.Errorf("Politica = %q, se esperaba %q", politica, identity.PoliticaAsignarSinAsignado)
	}

	if defecto := m.HandlePorDefecto(); defecto != "" {
		t.Errorf("HandlePorDefecto = %q, se esperaba cadena vacia", defecto)
	}
}

// TestMapperVacioNoInventaAliases comprueba que el mapper construido sin tabla
// no tiene ningun alias registrado.
func TestMapperVacioNoInventaAliases(t *testing.T) {
	m := identity.MapperVacio()

	if handles := m.HandlesConocidos(); len(handles) != 0 {
		t.Errorf("HandlesConocidos = %v, se esperaba vacio", handles)
	}

	if m.ConoceInforma("Omar Hernández") {
		t.Error("ConoceInforma deberia ser falso: no hay nada que conocer")
	}
}

// TestMapperVacioEsSeguroEnConcurrencia documenta que se puede compartir entre
// goroutines sin proteccion adicional.
//
// La etapa de despacho consulta identidades desde varios items en paralelo.
func TestMapperVacioEsSeguroEnConcurrencia(t *testing.T) {
	m := identity.MapperVacio()

	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)

		go func(n int) {
			defer wg.Done()

			nombre := "Persona Numero " + string(rune('A'+n%26))

			if _, err := m.ResolveHandle(context.Background(), nombre); err == nil {
				t.Errorf("se esperaba error para %q", nombre)
			}

			m.ConoceInforma(nombre)
			m.HandlesConocidos()
		}(i)
	}

	wg.Wait()
}

// TestMapperVacioPropagaElContexto verifies que un contexto cancelado no se
// ignora.
func TestMapperVacioPropagaElContexto(t *testing.T) {
	m := identity.MapperVacio()

	ctx, cancelar := context.WithCancel(context.Background())
	cancelar()

	if _, err := m.ResolveHandle(ctx, "Omar Hernández"); !errors.Is(err, context.Canceled) {
		t.Errorf("se esperaba context.Canceled, se obtuvo %v", err)
	}
}

// --- Normalizacion de acentos ---

// TestNormalizarNombreCubreTodasLasVocalesAcentuadas comprueba la tabla de
// sinAcento completa.
//
// La tabla es explicita porque la biblioteca estandar de Go no normaliza
// Unicode. Una vocal que se colara sin cubrir haria que "José" y "Jose" fueran
// dos personas distintas y el item se publicaria sin responsable.
func TestNormalizarNombreCobreTodasLasVocalesAcentuadas(t *testing.T) {
	casos := []struct {
		entrada  string
		esperado string
	}{
		{"áàäâ", "aaaa"},
		{"éèëê", "eeee"},
		{"íìïî", "iiii"},
		{"óòöô", "oooo"},
		{"úùüû", "uuuu"},
		{"ñ", "n"},
		{"ç", "c"},
		{"Ñ", "n"},
		{"Ç", "c"},
		// La enye con tilde es la mas importante: "Hernández" y
		// "Hernandez" deben converger a la misma persona.
		{"Hernández", "hernandez"},
		{"Muñoz", "munoz"},
		{"García", "garcia"},
		{"José Ángel", "jose angel"},
		{"Añez", "anez"},
	}

	for _, tt := range casos {
		t.Run(tt.entrada, func(t *testing.T) {
			if got := identity.NormalizarNombre(tt.entrada); got != tt.esperado {
				t.Errorf("NormalizarNombre(%q) = %q, se esperaba %q", tt.entrada, got, tt.esperado)
			}
		})
	}
}

// TestNormalizarNombreConvierteSeparadoresEnEspacioUnico documenta la
// normalizacion de separadores.
func TestNormalizarNombreConvierteSeparadoresEnEspacioUnico(t *testing.T) {
	casos := []struct {
		entrada  string
		esperado string
	}{
		{"Omar Hernández", "omar hernandez"},
		{"Omar, Hernández", "omar hernandez"},
		{"Omar . Hernández", "omar hernandez"},
		{"Omar\t\nHernández", "omar hernandez"},
		{"  Omar Hernández  ", "omar hernandez"},
		// Un separador al principio o al final no debe producir un espacio
		// inicial ni final: son los casos que rompen la comparacion con el
		// alias del indice.
		{".Omar.", "omar"},
		{"   ", ""},
		{"...", ""},
		{"", ""},
	}

	for _, tt := range casos {
		t.Run(tt.entrada, func(t *testing.T) {
			if got := identity.NormalizarNombre(tt.entrada); got != tt.esperado {
				t.Errorf("NormalizarNombre(%q) = %q, se esperaba %q", tt.entrada, got, tt.esperado)
			}

			if got := identity.NormalizarNombre(tt.entrada); strings.TrimSpace(got) != got {
				t.Errorf("el resultado %q tiene espacios en los extremos", got)
			}
		})
	}
}
