package jsonschema_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"talkaboutthis/internal/infrastructure/jsonschema"
)

// schemaPrueba es un schema pequeno que ejercita todo el subconjunto soportado.
const schemaPrueba = `{
  "title": "Tarea",
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "titulo":   { "type": "string", "minLength": 1, "maxLength": 10 },
    "prioridad": { "type": "string", "enum": ["HIGH", "MEDIUM", "LOW"] },
    "puntos":   { "type": "integer", "minimum": 0, "maximum": 13 },
    "etiquetas": { "type": "array", "minItems": 1, "items": { "type": "string" } },
    "activo":   { "type": "boolean" }
  },
  "required": ["titulo", "prioridad"]
}`

func cargar(t *testing.T, texto string) *jsonschema.Schema {
	t.Helper()

	schema, _, err := jsonschema.ParseSchema([]byte(texto))
	if err != nil {
		t.Fatalf("no se pudo cargar el schema: %v", err)
	}

	return schema
}

func validarJSON(t *testing.T, schema *jsonschema.Schema, documento string) jsonschema.Resultado {
	t.Helper()

	var v any
	if err := deserializar(documento, &v); err != nil {
		t.Fatalf("el documento de prueba no es JSON valido: %v", err)
	}

	return jsonschema.Validar(v, schema)
}

func TestValidarDocumentoCompleto(t *testing.T) {
	schema := cargar(t, schemaPrueba)

	documento := `{
		"titulo": "Migrar",
		"prioridad": "HIGH",
		"puntos": 3,
		"etiquetas": ["backend"],
		"activo": true
	}`

	resultado := validarJSON(t, schema, documento)

	if !resultado.Valido() {
		t.Errorf("un documento valido fue rechazado: %v", resultado.Errores)
	}
	if err := resultado.Err(); err != nil {
		t.Errorf("Err() deberia devolver nil cuando la validacion es correcta: %v", err)
	}
}

func TestValidarDetectaErrores(t *testing.T) {
	schema := cargar(t, schemaPrueba)

	casos := []struct {
		nombre    string
		documento string
		contiene  string
	}{
		{
			nombre:    "falta una clave obligatoria",
			documento: `{"titulo": "Migrar"}`,
			contiene:  "prioridad",
		},
		{
			nombre:    "enum invalido",
			documento: `{"titulo": "Migrar", "prioridad": "URGENTE"}`,
			contiene:  "URGENTE",
		},
		{
			nombre:    "tipo incorrecto",
			documento: `{"titulo": 42, "prioridad": "HIGH"}`,
			contiene:  "titulo",
		},
		{
			nombre:    "excede maxLength",
			documento: `{"titulo": "esta es una tarea muy larga", "prioridad": "HIGH"}`,
			contiene:  "maximo",
		},
		{
			nombre:    "clave no declarada con additionalProperties false",
			documento: `{"titulo": "Migrar", "prioridad": "HIGH", "extra": "x"}`,
			contiene:  "extra",
		},
		{
			nombre:    "excede maximum",
			documento: `{"titulo": "Migrar", "prioridad": "HIGH", "puntos": 21}`,
			contiene:  "maximo",
		},
		{
			nombre:    "arreglo con minItems",
			documento: `{"titulo": "Migrar", "prioridad": "HIGH", "etiquetas": []}`,
			contiene:  "minimo",
		},
	}

	for _, tt := range casos {
		t.Run(tt.nombre, func(t *testing.T) {
			resultado := validarJSON(t, schema, tt.documento)

			if resultado.Valido() {
				t.Fatal("se esperaba al menos un error")
			}

			// Todos los errores deben quedar en un unico mensaje: el Retry Loop
			// los adjunta juntos al prompt de correccion.
			err := resultado.Err()
			if err == nil {
				t.Fatal("Err() devolvio nil con errores presentes")
			}
			if !strings.Contains(err.Error(), tt.contiene) {
				t.Errorf("el error deberia mencionar %q, se obtuvo: %v", tt.contiene, err)
			}
		})
	}
}

// TestErroresDeArrayIncluyenElIndice importa que el Retry Loop diga QUE item
// fallo, no solo que "un item fallo".
func TestErroresDeArrayIncluyenElIndice(t *testing.T) {
	schema := cargar(t, schemaPrueba)

	documento := `{
		"titulo": "Migrar",
		"prioridad": "HIGH",
		"etiquetas": ["backend", 42, "otro"]
	}`

	resultado := validarJSON(t, schema, documento)

	if resultado.Valido() {
		t.Fatal("se esperaba error por el elemento no textual")
	}

	mensaje := resultado.Err().Error()
	if !strings.Contains(mensaje, "etiquetas[1]") {
		t.Errorf("el error deberia señalar el indice del elemento: %v", mensaje)
	}
}

// TestMaxLengthSeCuentaEnRunes protege el caso de los acentos: contar en bytes
// rechazaria textos validos en espanol.
func TestMaxLengthSeCuentaEnRunes(t *testing.T) {
	schema := cargar(t, schemaPrueba)

	// "migración" son 9 runes pero 10 bytes. El limite es 10, asi que debe pasar.
	resultado := validarJSON(t, schema, `{"titulo": "migración", "prioridad": "HIGH"}`)
	if !resultado.Valido() {
		t.Errorf("un titulo de 9 caracteres con acento debe aceptarse: %v", resultado.Errores)
	}
}

// TestEnterosAceptanFloatsEspecíficos documenta que encoding/json decodifica
// todos los numeros como float64, y que un entero escrito como 3.0 es valido.
func TestEnterosAceptanFloats(t *testing.T) {
	schema := cargar(t, schemaPrueba)

	valido := validarJSON(t, schema, `{"titulo": "x", "prioridad": "HIGH", "puntos": 3}`)
	if !valido.Valido() {
		t.Errorf("un entero debe aceptarse: %v", valido.Errores)
	}

	// Un float con parte decimal no es un entero valido para el schema.
	invalido := validarJSON(t, schema, `{"titulo": "x", "prioridad": "HIGH", "puntos": 3.5}`)
	if invalido.Valido() {
		t.Error("3.5 no debería aceptarse como integer")
	}
}

// TestAcumulaTodosLosErrores verifica que no se corta en el primero: corregirlos
// de uno en uno agota los tres intentos disponibles.
func TestAcumulaTodosLosErrores(t *testing.T) {
	schema := cargar(t, schemaPrueba)

	documento := `{"titulo": "", "prioridad": "URGENTE", "puntos": 99}`

	resultado := validarJSON(t, schema, documento)

	if len(resultado.Errores) < 3 {
		t.Errorf("se esperaban al menos 3 errores acumulados, se obtuvieron %d: %v",
			len(resultado.Errores), resultado.Errores)
	}
}

// TestAdditionalPropertiesPermitidoNoRechaza comprueba que el comportamiento
// depende de la palabra clave.
func TestAdditionalPropertiesPermitido(t *testing.T) {
	schema := cargar(t, `{
		"type": "object",
		"properties": {"titulo": {"type": "string"}}
	}`)

	resultado := validarJSON(t, schema, `{"titulo": "x", "extra": 1}`)

	if !resultado.Valido() {
		t.Errorf("sin additionalProperties:false no se deben rechazar claves extra: %v", resultado.Errores)
	}
}

// TestSchemaRealDelProyectoValidaEs el test de integracion mas importante: el
// schema real de docs/specifications debe funcionar con este validador.
func TestSchemaRealDelProyectoValidaEs(t *testing.T) {
	ruta := filepath.Join("..", "..", "..", "docs", "specifications", "backlog_schema.json")

	contenido, err := os.ReadFile(ruta)
	if err != nil {
		t.Skipf("schema no disponible: %v", err)
	}

	schema, desconocidas, err := jsonschema.ParseSchema(contenido)
	if err != nil {
		t.Fatalf("el schema real no se pudo cargar: %v", err)
	}

	// Ninguna palabra clave del schema real debe quedar sin aplicar: si el
	// schema usa algo que el validador ignora, la validacion es parcial sin que
	// nadie lo note.
	if len(desconocidas) > 0 {
		t.Errorf("el schema real usa palabras clave no soportadas por el validador: %v", desconocidas)
	}

	valido := `{
		"meeting_summary": "Reunion de arquitectura.",
		"action_items": [{
			"title": "Migrar la sesion",
			"description": "Extraerla del contexto global.",
			"assignee_name": "Omar Hernandez",
			"priority": "HIGH",
			"labels": ["backend"],
			"story_points": 3
		}]
	}`

	resultado := validarJSON(t, schema, valido)
	if !resultado.Valido() {
		t.Errorf("una extraccion valida fue rechazada por el schema real: %v", resultado.Errores)
	}
}

// TestParseSchemaDetectaPalabrasClaveDesconocidas protege la transparencia del
// validador.
func TestParseSchemaDetectaPalabrasClaveDesconocidas(t *testing.T) {
	_, desconocidas, err := jsonschema.ParseSchema([]byte(`{
		"type": "object",
		"if": { "type": "string" },
		"patternProperties": { "^x": { "type": "string" } },
		"properties": { "a": { "type": "string", "pattern": "abc" } }
	}`))
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}

	for _, esperada := range []string{"if", "patternProperties", "pattern"} {
		encontrada := false
		for _, palabra := range desconocidas {
			if palabra == esperada {
				encontrada = true
				break
			}
		}
		if !encontrada {
			t.Errorf("deberia reportarse la palabra clave %q, se obtuvo: %v", esperada, desconocidas)
		}
	}
}

// TestParseSchemaRechazaJSONInvalido cubre el error de entrada.
func TestParseSchemaRechazaJSONInvalido(t *testing.T) {
	if _, _, err := jsonschema.ParseSchema([]byte(`{ "roto": `)); err == nil {
		t.Fatal("se esperaba error con JSON invalido")
	}
}

// deserializar es un alias de json.Unmarshal, usado por el helper validarJSON.
func deserializar(texto string, destino any) error {
	return json.Unmarshal([]byte(texto), destino)
}
