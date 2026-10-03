package domain_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"talkaboutthis/internal/domain"
)

// rutaSchema apunta al contrato de intercambio con el LLM (RNF-02).
const rutaSchema = "../../docs/specifications/backlog_schema.json"

// TestEsquemaEsJSONValido comprueba que el schema se puede cargar. Si este test
// falla por sintaxis, la app no podria validar ninguna respuesta del LLM.
func TestEsquemaEsJSONValido(t *testing.T) {
	contenido := leerSchema(t)

	var v any
	if err := json.Unmarshal(contenido, &v); err != nil {
		t.Fatalf("backlog_schema.json no es JSON valido: %v", err)
	}
}

// TestEsquemaCoincideConStructTags es el test que hace cumplir RNF-02.
//
// El schema JSON y los struct tags de Go son el mismo contrato expresado en dos
// formatos. Este test los compara campo a campo para que no puedan divergir en
// silencio: cambiar un struct sin tocar el schema (o al reves) rompe la suite,
// en lugar de producir un backlog silenciosamente corrupto.
func TestEsquemaCoincideConStructTags(t *testing.T) {
	contenido := leerSchema(t)

	var schema struct {
		Properties struct {
			MeetingSummary struct {
				Type string `json:"type"`
			} `json:"meeting_summary"`
			ActionItems struct {
				Items struct {
					Properties map[string]struct {
						Type string `json:"type"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"action_items"`
		} `json:"properties"`
	}

	if err := json.Unmarshal(contenido, &schema); err != nil {
		t.Fatalf("no se pudo interpretar backlog_schema.json: %v", err)
	}

	t.Run("ActionItem", func(t *testing.T) {
		compararCampos(t, schema.Properties.ActionItems.Items.Properties, reflect.TypeOf(domain.ActionItem{}))
	})

	t.Run("MeetingBacklogExtraction", func(t *testing.T) {
		// El schema solo declara las dos claves raiz. Se comprueba que ambas
		// existen en el struct, sin exigir el viceversa (el struct puede tener
		// mas campos internos, como mapped_handle, que no forman parte del
		// contrato con el LLM).
		tipo := reflect.TypeOf(domain.MeetingBacklogExtraction{})
		for _, clave := range []string{"meeting_summary", "action_items"} {
			if _, ok := campoPorTag(tipo, clave); !ok {
				t.Errorf("el schema declara %q pero MeetingBacklogExtraction no tiene ese campo json", clave)
			}
		}
	})
}

// TestEnumPrioridadSincronizado verifica que los valores de Priority y el enum
// del schema son el mismo conjunto.
//
// Si el schema admitiera "CRITICAL" y el enum no, el LLM podria devolver una
// prioridad que el dominio rechazaria, convirtiendo cada item en un fallo de
// validacion.
func TestEnumPrioridadSincronizado(t *testing.T) {
	contenido := leerSchema(t)

	var schema struct {
		Properties struct {
			ActionItems struct {
				Items struct {
					Properties struct {
						Priority struct {
							Enum []string `json:"enum"`
						} `json:"priority"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"action_items"`
		} `json:"properties"`
	}

	if err := json.Unmarshal(contenido, &schema); err != nil {
		t.Fatalf("no se pudo interpretar backlog_schema.json: %v", err)
	}

	declarados := schema.Properties.ActionItems.Items.Properties.Priority.Enum
	if len(declarados) == 0 {
		t.Fatal("el schema no declara el enum de priority")
	}

	esperados := []string{
		string(domain.PriorityHigh),
		string(domain.PriorityMedium),
		string(domain.PriorityLow),
	}

	sort.Strings(declarados)
	sort.Strings(esperados)

	if !slicesIguales(declarados, esperados) {
		t.Errorf(
			"el enum de priority del schema no coincide con las constantes del dominio.\n"+
				"  schema: %v\n"+
				"  dominio: %v",
			declarados, esperados,
		)
	}
}

// TestTituloCoincideConSchemaMaxLength mantiene alineados los tres lugares donde
// vive el limite de 100 caracteres: la constante del dominio, la etiqueta
// `validate:"...,max=100"` y el `"maxLength": 100` del schema.
func TestTituloCoincideConSchemaMaxLength(t *testing.T) {
	tipo := reflect.TypeOf(domain.ActionItem{})
	campo, ok := campoPorTag(tipo, "title")
	if !ok {
		t.Fatal("ActionItem no tiene un campo json \"title\"")
	}

	// La etiqueta validate debe declarar el mismo maximo que la constante.
	tagValidate := campo.Tag.Get("validate")
	if !strings.Contains(tagValidate, "max=") {
		t.Fatalf("el campo title no declara max en su etiqueta validate: %q", tagValidate)
	}

	partes := strings.Split(tagValidate, ",")
	for _, parte := range partes {
		if !strings.HasPrefix(parte, "max=") {
			continue
		}
		valor, err := strconv.Atoi(strings.TrimPrefix(parte, "max="))
		if err != nil {
			t.Fatalf("no se pudo leer el valor max= de la etiqueta validate %q: %v", tagValidate, err)
		}
		if valor != domain.MaxTituloLen {
			t.Errorf(
				"el max de la etiqueta validate (%d) no coincide con domain.MaxTituloLen (%d)",
				valor, domain.MaxTituloLen,
			)
		}
	}

	// Y el schema debe declarar el mismo maxLength.
	contenido := leerSchema(t)

	var schema struct {
		Properties struct {
			ActionItems struct {
				Items struct {
					Properties struct {
						Title struct {
							MaxLength int `json:"maxLength"`
						} `json:"title"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"action_items"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(contenido, &schema); err != nil {
		t.Fatalf("no se pudo interpretar backlog_schema.json: %v", err)
	}

	if schema.Properties.ActionItems.Items.Properties.Title.MaxLength != domain.MaxTituloLen {
		t.Errorf(
			"el maxLength del schema (%d) no coincide con domain.MaxTituloLen (%d)",
			schema.Properties.ActionItems.Items.Properties.Title.MaxLength,
			domain.MaxTituloLen,
		)
	}
}

// TestEsquemaMarcaRequiredCoincideConTagsOmitempty comprueba que los campos que
// el schema exige coinciden con los que el struct no marca como omitempty.
//
// Un campo requerido en el schema pero omitempty en el struct significa que Go
// lo omitiria al serializar, produciendo un JSON que el propio contrato prohibe.
func TestEsquemaMarcaRequiredCoincideConTagsOmitempty(t *testing.T) {
	contenido := leerSchema(t)

	var schema struct {
		Properties struct {
			ActionItems struct {
				Items struct {
					Required   []string `json:"required"`
					Properties map[string]struct {
						Type string `json:"type"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"action_items"`
		} `json:"properties"`
	}

	if err := json.Unmarshal(contenido, &schema); err != nil {
		t.Fatalf("no se pudo interpretar backlog_schema.json: %v", err)
	}

	items := schema.Properties.ActionItems.Items
	tipo := reflect.TypeOf(domain.ActionItem{})

	for _, requerida := range items.Required {
		campo, ok := campoPorTag(tipo, requerida)
		if !ok {
			t.Errorf("el schema requiere %q pero ActionItem no tiene ese campo", requerida)
			continue
		}
		if _, esOmitempty := campo.Tag.Lookup("json"); esOmitempty {
			// Lookup devuelve el valor ("title,omitempty"), hay que buscar el sufijo.
			tagJSON := campo.Tag.Get("json")
			if strings.Contains(tagJSON, "omitempty") {
				t.Errorf(
					"el schema exige el campo %q pero el struct lo serializa como %q: "+
						"se omitiria del JSON y el contrato lo declara obligatorio",
					requerida, tagJSON,
				)
			}
		}
	}
}

// --- helpers ---

// leerSchema lee el JSON Schema y falla el test si no esta disponible, con un
// mensaje que explica que es un fallo de la repo y no del codigo bajo prueba.
func leerSchema(t *testing.T) []byte {
	t.Helper()

	contenido, err := os.ReadFile(filepath.Clean(rutaSchema))
	if err != nil {
		t.Fatalf("no se pudo leer %s: %v", rutaSchema, err)
	}
	return contenido
}

// compararCampos verifica que cada propiedad del schema tiene su campo
// correspondiente en el struct Go, y que el tipo declarado en el schema es
// compatible con el del campo.
func compararCampos(t *testing.T, propiedades map[string]struct {
	Type string `json:"type"`
}, tipo reflect.Type) {
	t.Helper()

	for nombre, def := range propiedades {
		campo, ok := campoPorTag(tipo, nombre)
		if !ok {
			t.Errorf("el schema declara %q pero %s no tiene ese campo json", nombre, tipo.Name())
			continue
		}

		tipoJSON := def.Type

		// Un slice de string se declara como "array" en el schema.
		if campo.Type.Kind() == reflect.Slice {
			if tipoJSON != "array" {
				t.Errorf("campo %q: es un slice en Go (%s) pero el schema declara %q",
					nombre, campo.Type, tipoJSON)
			}
			continue
		}

		if tipoJSON == "string" && campo.Type.Kind() != reflect.String {
			// domain.Priority tiene underlying type string, que es un caso valido.
			if campo.Type.Kind() == reflect.String {
				continue
			}
			t.Errorf("campo %q: el schema declara string pero Go tiene %s", nombre, campo.Type)
		}
	}
}

// campoPorTag localiza un campo por su nombre en la etiqueta json.
func campoPorTag(tipo reflect.Type, nombre string) (reflect.StructField, bool) {
	for i := 0; i < tipo.NumField(); i++ {
		campo := tipo.Field(i)
		tag := campo.Tag.Get("json")
		if tag == "" {
			continue
		}
		if strings.Split(tag, ",")[0] == nombre {
			return campo, true
		}
	}
	return reflect.StructField{}, false
}

func slicesIguales(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
