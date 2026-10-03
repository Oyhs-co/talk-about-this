package jsonschema_test

import (
	"encoding/json"
	"strings"
	"testing"

	"talkaboutthis/internal/infrastructure/jsonschema"
)

// Estos tests cubren las rutas que el barrido de cobertura marco como frias:
// el orden determinista de los mensajes, el aviso de palabras clave no
// soportadas, y el nombre de los tipos en los errores.

// TestClavesOrdenadasEsDeterminista verifica que el orden de las propiedades no
// depende del iterador de mapas de Go.
//
// No es un detalle estetico. El prompt de correccion que se envia al LLM lleva
// la lista de errores: si Go devolviera las claves en orden aleatorio en cada
// intento, el modelo veria un conjunto de instrucciones distinto cada vez, y la
// correccion se volveria menos predecible justo cuando mas hace falta.
func TestClavesOrdenadasEsDeterminista(t *testing.T) {
	documento := []byte(`{
		"type": "object",
		"properties": {
			"zeta":  {"type": "string"},
			"alfa":  {"type": "string"},
			"medio": {"type": "string"},
			"beta":  {"type": "string"}
		}
	}`)

	schema, _, err := jsonschema.ParseSchema(documento)
	if err != nil {
		t.Fatalf("no se pudo parsear el schema: %v", err)
	}

	esperado := []string{"alfa", "beta", "medio", "zeta"}

	// Se repite porque un mapa pequeno puede "salir ordenado" por casualidad
	// en la primera iteracion y fallar en la segunda.
	for intento := 0; intento < 20; intento++ {
		obtenido := schema.ClavesOrdenadas()

		if len(obtenido) != len(esperado) {
			t.Fatalf("ClavesOrdenadas devolvio %d claves, se esperaban %d", len(obtenido), len(esperado))
		}

		for i := range esperado {
			if obtenido[i] != esperado[i] {
				t.Fatalf("iteracion %d: posicion %d = %q, se esperaba %q (orden completo: %v)",
					intento, i, obtenido[i], esperado[i], obtenido)
			}
		}
	}
}

// TestClavesOrdenadasSobreSchemaVacio cubre los casos degenerados.
//
// Un schema nil o sin propiedades no debe provocar un panic: quien llama es el
// bucle de extraccion, que no tiene por que comprobar nada.
func TestClavesOrdenadasSobreSchemaVacio(t *testing.T) {
	casos := []struct {
		nombre string
		schema *jsonschema.Schema
	}{
		{"nil", nil},
		{"sin properties", &jsonschema.Schema{Type: "object"}},
		{"properties vacio", &jsonschema.Schema{Properties: map[string]*jsonschema.Schema{}}},
	}

	for _, tt := range casos {
		t.Run(tt.nombre, func(t *testing.T) {
			if claves := tt.schema.ClavesOrdenadas(); len(claves) != 0 {
				t.Errorf("ClavesOrdenadas = %v, se esperaba vacio", claves)
			}
		})
	}
}

// TestPalabrasClaveDesconocidasInformaLoQueNoSeAplica verifica que el aviso
// senala realmente las reglas ignoradas.
//
// Una palabra clave no soportada es una validacion que NO ocurre. Saber cual es
// lo que permite decidir si el schema se endurece o se acepta tal cual.
func TestPalabrasClaveDesconocidasInformaLoQueNoSeAplica(t *testing.T) {
	documento := []byte(`{
		"type": "object",
		"properties": {
			"titulo": {
				"type": "string",
				"minLength": 3,
				"pattern": "^.{3,}$",
				"multipleOf": 2
			}
		}
	}`)

	schema, desconocidas, err := jsonschema.ParseSchema(documento)
	if err != nil {
		t.Fatalf("no se pudo parsear el schema: %v", err)
	}

	// pattern y multipleOf no forman parte del subconjunto soportado.
	for _, esperada := range []string{"pattern", "multipleOf"} {
		if !contieneTexto(desconocidas, esperada) {
			t.Errorf("ParseSchema no devolvio %q entre las palabras desconocidas: %v", esperada, desconocidas)
		}

		if !contieneTexto(schema.PalabrasClaveDesconocidas(), esperada) {
			t.Errorf("PalabrasClaveDesconocidas no devolvio %q: %v", esperada, schema.PalabrasClaveDesconocidas())
		}
	}

	// El resultado debe ser el mismo venga de donde venga, y la copia debe
	// estar realmente copiada.
	obtenidas := schema.PalabrasClaveDesconocidas()
	obtenidas[0] = "manipulado"

	if schema.PalabrasClaveDesconocidas()[0] == "manipulado" {
		t.Error("PalabrasClaveDesconocidas devuelve el slice interno: el llamante puede alterar el schema")
	}
}

// TestPalabrasClaveDesconocidasDeduplicaYOrdena verifica la estabilidad del
// aviso cuando la misma palabra aparece en varios nodos.
func TestPalabrasClaveDesconocidasDeduplicaYOrdena(t *testing.T) {
	documento := []byte(`{
		"type": "object",
		"properties": {
			"a": {"type": "string", "pattern": "x", "const": 1},
			"b": {"type": "string", "pattern": "y"},
			"c": {"type": "array", "items": {"type": "string", "pattern": "z"}}
		},
		"pattern": "global"
	}`)

	schema, desconocidas, err := jsonschema.ParseSchema(documento)
	if err != nil {
		t.Fatalf("no se pudo parsear el schema: %v", err)
	}

	vistos := map[string]int{}
	for _, clave := range desconocidas {
		vistos[clave]++
	}

	for clave, veces := range vistos {
		if veces > 1 {
			t.Errorf("la palabra %q aparece %d veces; el aviso debe deduplicarse", clave, veces)
		}
	}

	if vistos["pattern"] == 0 {
		t.Errorf("se esperaba detectar pattern en nodos anidados: %v", desconocidas)
	}

	ordenadas := schema.PalabrasClaveDesconocidas()
	for i := 1; i < len(ordenadas); i++ {
		if ordenadas[i-1] > ordenadas[i] {
			t.Errorf("PalabrasClaveDesconocidas no viene ordenada: %v", ordenadas)
			break
		}
	}
}

// TestPalabrasClaveDesconocidasSinReglasNoSoportadas comprueba que un schema
// totalmente soportado no produce ruido.
func TestPalabrasClaveDesconocidasSinReglasNoSoportadas(t *testing.T) {
	documento := []byte(`{
		"type": "object",
		"required": ["titulo"],
		"properties": {
			"titulo": {"type": "string", "minLength": 3, "description": "el titulo"}
		},
		"additionalProperties": false
	}`)

	schema, desconocidas, err := jsonschema.ParseSchema(documento)
	if err != nil {
		t.Fatalf("no se pudo parsear el schema: %v", err)
	}

	if len(desconocidas) != 0 {
		t.Errorf("un schema sin reglas no soportadas devolvio %v", desconocidas)
	}

	if claves := schema.PalabrasClaveDesconocidas(); len(claves) != 0 {
		t.Errorf("PalabrasClaveDesconocidas = %v, se esperaba vacio", claves)
	}

	// nil tambien: un schema nil no debe provocar un panic.
	var ausente *jsonschema.Schema
	if claves := ausente.PalabrasClaveDesconocidas(); claves != nil {
		t.Errorf("sobre un schema nil devolvio %v, se esperaba nil", claves)
	}
}

// TestValidarNombraElTipoEsperadoEnLosMensajes comprueba que los mensajes de
// error son utilizables por una persona y por el prompt de autocorreccion.
//
// El mensaje debe conter DOS datos: el tipo que el schema exige, con su nombre
// JSON, y el tipo que llego. "se esperaba string y se recibio numero" es
// accionable; "valor invalido" no lo seria.
func TestValidarNombraElTipoEsperadoEnLosMensajes(t *testing.T) {
	documento := []byte(`{
		"type": "object",
		"required": ["titulo", "prioridad", "puntos", "etiquetas"],
		"properties": {
			"titulo":    {"type": "string"},
			"prioridad": {"type": "string", "enum": ["HIGH", "MEDIUM", "LOW"]},
			"puntos":    {"type": "integer"},
			"etiquetas": {"type": "array"}
		}
	}`)

	schema, _, err := jsonschema.ParseSchema(documento)
	if err != nil {
		t.Fatalf("no se pudo parsear el schema: %v", err)
	}

	casos := []struct {
		nombre   string
		valor    string
		esperado string
		recibido string
	}{
		{
			nombre:   "numero donde se espera texto",
			valor:    `{"titulo": 42, "prioridad": "HIGH", "puntos": 1, "etiquetas": []}`,
			esperado: "string",
			recibido: "numero",
		},
		{
			nombre:   "decimal donde se espera entero",
			valor:    `{"titulo": "x", "prioridad": "HIGH", "puntos": 1.5, "etiquetas": []}`,
			esperado: "integer",
			recibido: "numero",
		},
		{
			nombre:   "arreglo donde se espera texto",
			valor:    `{"titulo": [], "prioridad": "HIGH", "puntos": 1, "etiquetas": []}`,
			esperado: "string",
			recibido: "arreglo",
		},
		{
			nombre:   "objeto donde se espera texto",
			valor:    `{"titulo": {}, "prioridad": "HIGH", "puntos": 1, "etiquetas": []}`,
			esperado: "string",
			recibido: "objeto",
		},
		{
			nombre:   "booleano donde se espera texto",
			valor:    `{"titulo": true, "prioridad": "HIGH", "puntos": 1, "etiquetas": []}`,
			esperado: "string",
			recibido: "booleano",
		},
		{
			nombre:   "null donde se espera texto",
			valor:    `{"titulo": null, "prioridad": "HIGH", "puntos": 1, "etiquetas": []}`,
			esperado: "string",
			recibido: "null",
		},
		{
			nombre:   "texto donde se espera entero",
			valor:    `{"titulo": "x", "prioridad": "HIGH", "puntos": "tres", "etiquetas": []}`,
			esperado: "integer",
			recibido: "texto",
		},
		{
			nombre:   "texto donde se espera arreglo",
			valor:    `{"titulo": "x", "prioridad": "HIGH", "puntos": 1, "etiquetas": "backend"}`,
			esperado: "array",
			recibido: "texto",
		},
	}

	for _, tt := range casos {
		t.Run(tt.nombre, func(t *testing.T) {
			var doc any
			if err := json.Unmarshal([]byte(tt.valor), &doc); err != nil {
				t.Fatalf("el caso de prueba no es JSON valido: %v", err)
			}

			resultado := jsonschema.Validar(doc, schema)

			if resultado.Valido() {
				t.Fatalf("se esperaba un fallo de tipo, pero el documento es valido")
			}

			mensajes := unirErrores(resultado.Errores)

			if !strings.Contains(mensajes, tt.esperado) {
				t.Errorf("el mensaje %q deberia indicar el tipo esperado %q", mensajes, tt.esperado)
			}

			if !strings.Contains(mensajes, tt.recibido) {
				t.Errorf("el mensaje %q deberia indicar el tipo recibido %q", mensajes, tt.recibido)
			}

			// Toda violacion debe apuntar a una ruta utilizable, porque la ruta
			// es lo que permite localizar el campo que hay que corregir.
			for _, e := range resultado.Errores {
				if e.Ruta == "" {
					t.Errorf("una violacion no tiene ruta: %+v", e)
				}
			}
		})
	}
}

// TestValidarEsDeterministaEnElOrdenDeLosErrores verifica que dos validaciones
// del mismo documento producen los mismos mensajes en el mismo orden.
func TestValidarEsDeterministaEnElOrdenDeLosErrores(t *testing.T) {
	documento := []byte(`{
		"type": "object",
		"required": ["titulo", "prioridad"],
		"properties": {
			"titulo":    {"type": "string"},
			"prioridad": {"type": "string", "enum": ["HIGH", "MEDIUM", "LOW"]}
		}
	}`)

	schema, _, err := jsonschema.ParseSchema(documento)
	if err != nil {
		t.Fatalf("no se pudo parsear el schema: %v", err)
	}

	var doc any
	if err := json.Unmarshal([]byte(`{"prioridad": 7}`), &doc); err != nil {
		t.Fatalf("documento de prueba invalido: %v", err)
	}

	primera := unirErrores(jsonschema.Validar(doc, schema).Errores)

	if primera == "" {
		t.Fatal("se esperaba al menos una violacion")
	}

	for intento := 0; intento < 20; intento++ {
		if obtenida := unirErrores(jsonschema.Validar(doc, schema).Errores); obtenida != primera {
			t.Fatalf("iteracion %d: los errores no son deterministas.\nprimera:  %s\nobtenida: %s",
				intento, primera, obtenida)
		}
	}
}

// TestValidarConSchemaNilNoRevienta documenta el borde.
//
// Devolver un resultado vacio (es decir, "todo valido") puede sorprender, pero
// un panic en el validador tumbaria el proceso entero por una entrada
// inesperada; el fallo silencioso es preferible al crash.
func TestValidarConSchemaNilNoRevienta(t *testing.T) {
	var doc any
	if err := json.Unmarshal([]byte(`{"lo que sea": 1}`), &doc); err != nil {
		t.Fatalf("documento de prueba invalido: %v", err)
	}

	resultado := jsonschema.Validar(doc, nil)

	if !resultado.Valido() {
		t.Errorf("con schema nil no hay nada que validar, se esperaba un resultado valido, se obtuvo %v",
			resultado.Errores)
	}
}

// contieneTexto evita shadowing del helper homonimo de otro fichero de test
// del mismo paquete.
func contieneTexto(valores []string, buscada string) bool {
	for _, valor := range valores {
		if valor == buscada {
			return true
		}
	}
	return false
}

func unirErrores(errores []jsonschema.Error) string {
	partes := make([]string, 0, len(errores))
	for _, e := range errores {
		partes = append(partes, e.Error())
	}
	return strings.Join(partes, " | ")
}
