// Package specifications contiene los contratos de intercambio de datos
// (SDD) de TalkAboutThis y los expone al binario.
//
// Los archivos de este directorio son la FUENTE DE VERDAD del contrato con el
// modelo de lenguaje. El paquete existe para incrustarlos en el binario: la
// directiva go:embed solo puede alcanzar archivos dentro del propio directorio
// del paquete o sus subdirectorios, de modo que incrustar desde cmd/ un archivo
// situado en docs/ es imposible. Anclar el embed aqui evita mantener una copia
// del schema que podria divergir sin avisar.
//
// Uso tipico desde el composition root:
//
//	const (
//	    schema, err = specifications.BacklogSchema()
//	)
package specifications

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

// backlogSchemaEmbebido es el JSON Schema que gobierna la salida estructurada
// del LLM (RF-03). go:embed falla en TIEMPO DE COMPILACION si el archivo no
// existe, de modo que es imposible construir un binario sin contrato.
//
//go:embed backlog_schema.json
var backlogSchemaEmbebido []byte

// BacklogSchema devuelve el JSON Schema de extraccion como bytes.
//
// Se devuelve una copia para que ningun llamante pueda alterar el contenido
// embebido, que es compartido por toda la aplicacion.
func BacklogSchema() ([]byte, error) {
	if len(backlogSchemaEmbebido) == 0 {
		return nil, fmt.Errorf("el JSON Schema de extraccion esta vacio")
	}

	// Se valida aqui, y no en cada peticion al LLM: si el contrato esta
	// corrupto el fallo debe aparecer al arrancar, no a mitad de una
	// ejecucion con credenciales ya cargadas.
	var v any
	if err := json.Unmarshal(backlogSchemaEmbebido, &v); err != nil {
		return nil, fmt.Errorf("el JSON Schema de extraccion no es JSON valido: %w", err)
	}

	schema := make([]byte, len(backlogSchemaEmbebido))
	copy(schema, backlogSchemaEmbebido)
	return schema, nil
}

// ClavesRaizObligatorias son las claves que el dominio espera encontrar en el
// schema. Mantenerlas aqui permite que el arranque falle con un mensaje claro
// si alguien edita el contrato de forma incompatible.
var ClavesRaizObligatorias = []string{"meeting_summary", "action_items"}

// Validar comprueba que el schema embebido es JSON valido y que declara las
// claves raiz obligatorias.
func Validar() error {
	contenido, err := BacklogSchema()
	if err != nil {
		return err
	}

	var schema struct {
		Type       string         `json:"type"`
		Properties map[string]any `json:"properties"`
	}

	if err := json.Unmarshal(contenido, &schema); err != nil {
		return fmt.Errorf("el JSON Schema de extraccion no es JSON valido: %w", err)
	}

	for _, clave := range ClavesRaizObligatorias {
		if _, ok := schema.Properties[clave]; !ok {
			return fmt.Errorf("el schema de extraccion no declara la clave raiz obligatoria %q", clave)
		}
	}

	return nil
}
