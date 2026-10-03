package specifications_test

import (
	"encoding/json"
	"strings"
	"testing"

	"talkaboutthis/docs/specifications"
)

// Estos tests protegen el contrato de extraccion que viaja dentro del binario.
// Si el embed dejara de funcionar, la app arrancaria sin schema y cada llamada
// al LLM fallaria en tiempo de ejecucion, lejos de la causa real.

// TestValidarPasa con el schema real verifica el camino feliz y, sobre todo,
// que go:embed esta resolviendo el archivo en la ubicacion correcta.
func TestValidarPasa(t *testing.T) {
	if err := specifications.Validar(); err != nil {
		t.Fatalf("el schema embebido deberia ser valido: %v", err)
	}
}

// TestBacklogSchemaDevuelveContenidoNoVacio comprueba que el embed no quedo vacio.
func TestBacklogSchemaDevuelveContenidoNoVacio(t *testing.T) {
	contenido, err := specifications.BacklogSchema()
	if err != nil {
		t.Fatalf("BacklogSchema() devolvio error: %v", err)
	}

	if len(contenido) == 0 {
		t.Fatal("BacklogSchema() devolvio un payload vacio: el embed no esta funcionando")
	}

	var v any
	if err := json.Unmarshal(contenido, &v); err != nil {
		t.Fatalf("el contenido devuelto no es JSON valido: %v", err)
	}
}

// TestBacklogSchemaDevuelveCopiaVerifica que el llamante no puede alterar el
// contenido compartido. Sin esta copia, un consumidor que mutara el slice
// corromperia el schema para el resto del proceso.
func TestBacklogSchemaDevuelveCopia(t *testing.T) {
	original, err := specifications.BacklogSchema()
	if err != nil {
		t.Fatalf("BacklogSchema() devolvio error: %v", err)
	}

	// Se corrompe deliberadamente la copia recibida.
	for i := range original {
		original[i] = 'X'
	}

	fresco, err := specifications.BacklogSchema()
	if err != nil {
		t.Fatalf("la segunda llamada devolvio error: %v", err)
	}

	if strings.Contains(string(fresco), "XXXX") {
		t.Error("el contenido embebido fue alterado por un llamante: BacklogSchema debe devolver una copia")
	}

	if err := specifications.Validar(); err != nil {
		t.Errorf("tras alterar una copia, el schema deberia seguir siendo valido: %v", err)
	}
}

// TestSchemaDeclaraLasClavesObligatorias documenta el contrato que espera el
// dominio. Si alguien renombra una clave raiz, este test lo detecta.
func TestSchemaDeclaraLasClavesObligatorias(t *testing.T) {
	contenido, err := specifications.BacklogSchema()
	if err != nil {
		t.Fatalf("BacklogSchema() devolvio error: %v", err)
	}

	var schema struct {
		Type       string         `json:"type"`
		Properties map[string]any `json:"properties"`
		Required   []string       `json:"required"`
	}

	if err := json.Unmarshal(contenido, &schema); err != nil {
		t.Fatalf("no se pudo interpretar el schema: %v", err)
	}

	if schema.Type != "object" {
		t.Errorf("la raiz del schema deberia ser un objeto, es %q", schema.Type)
	}

	for _, clave := range specifications.ClavesRaizObligatorias {
		if _, ok := schema.Properties[clave]; !ok {
			t.Errorf("el schema no declara la propiedad %q", clave)
		}
	}

	// Las claves raiz deben ademas estar en "required": de lo contrario el LLM
	// podria omitir el resumen y devolver solo una lista de tareas.
	for _, clave := range specifications.ClavesRaizObligatorias {
		encontrada := false
		for _, r := range schema.Required {
			if r == clave {
				encontrada = true
				break
			}
		}
		if !encontrada {
			t.Errorf("la clave %q no esta declarada en required", clave)
		}
	}
}
