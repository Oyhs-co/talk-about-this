// Package jsonschema implementa un validador minimo de JSON Schema.
//
// # ALCANCE DELIBERADO
//
// No es una implementacion completa de draft-07. Cubre exactamente el subconjunto
// que usa docs/specifications/backlog_schema.json: type, properties, required,
// additionalProperties, items, enum, minLength, maxLength, minimum, maximum y
// minItems.
//
// Existe en lugar de una dependencia externa (por ejemplo santhosh-tekuri/jsonschema)
// por dos razones concretas:
//
//  1. Mantiene el objetivo de INIT.md seccion 1 de un binario estatico y ligero:
//     go.mod sigue sin ninguna dependencia.
//  2. El subconjunto necesario es pequeno y su semantica es estable. Anadir un
//     validador completo traeria cientos de lineas de codigo que nunca se
//     ejecutarian.
//
// # LIMITACION CONOCIDA
//
// Si el schema llegara a usar una palabra clave fuera de este subconjunto (por
// ejemplo if/then, $ref, patternProperties, anyOf), el validador la ignoraria en
// silencio en lugar de rechazarla. EsquemasMas ricos no estan soportados.
//
// Para que esa limitacion no pase desapercibida, ParseSchema cuenta las palabras
// clave desconocidas y las expone en PalabrasClaveDesconocidas, de modo que el
// llamador pueda decidir si le afectan.
package jsonschema

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Schema representa el subconjunto soportado de un JSON Schema.
type Schema struct {
	Title                string             `json:"title"`
	Type                 string             `json:"type"`
	Properties           map[string]*Schema `json:"properties"`
	Required             []string           `json:"required"`
	Items                *Schema            `json:"items"`
	Enum                 []any              `json:"enum"`
	MinLength            *int               `json:"minLength"`
	MaxLength            *int               `json:"maxLength"`
	Minimum              *float64           `json:"minimum"`
	Maximum              *float64           `json:"maximum"`
	MinItems             *int               `json:"minItems"`
	MaxItems             *int               `json:"maxItems"`
	AdditionalProperties *bool              `json:"additionalProperties"`
	Description          string             `json:"description"`

	// desconocidas recuerda las palabras clave del schema original que este
	// validador ignora. No lleva etiqueta JSON a proposito: no es parte del
	// schema, es metadato del parseo, y Codificar no debe emitirlo.
	desconocidas []string
}

// palabrasClaveConocidas delimita el subconjunto soportado.
var palabrasClaveConocidas = map[string]bool{
	"$schema":              true,
	"$id":                  true,
	"title":                true,
	"description":          true,
	"type":                 true,
	"properties":           true,
	"required":             true,
	"items":                true,
	"enum":                 true,
	"minLength":            true,
	"maxLength":            true,
	"minimum":              true,
	"maximum":              true,
	"minItems":             true,
	"maxItems":             true,
	"additionalProperties": true,
	"default":              true,
}

// PalabrasClaveDesconocidas devuelve las palabras clave del schema que este
// validador ignora.
//
// Un resultado no vacio no es un error, pero debe tractarse como aviso: una
// palabra clave ignorada es una regla de validacion que NO se aplica.
func (s *Schema) PalabrasClaveDesconocidas() []string {
	if s == nil || len(s.desconocidas) == 0 {
		return nil
	}

	// Se devuelve una copia: el schema es compartido por todos los intentos de
	// extraccion y el llamante no debe poder alterar su estado.
	copia := make([]string, len(s.desconocidas))
	copy(copia, s.desconocidas)

	return copia
}

// Error describe una violacion concreta del schema.
//
// Ruta es una referencia al punto exacto del documento, al estilo JSON Pointer
// ("action_items[2].priority"), para que el mensaje sea utilizable tanto por una
// persona como por el prompt de autocorreccion del LLM.
type Error struct {
	// Ruta apunta a la ubicacion del problema dentro del documento.
	Ruta string
	// Mensaje explica que se esperaba.
	Mensaje string
}

// Error implementa error para que un Error isolado pueda Comparable con errors.Is.
func (e Error) Error() string {
	if e.Ruta == "" {
		return e.Mensaje
	}
	return e.Ruta + ": " + e.Mensaje
}

// Resultado agrupa los errores de una validacion.
type Resultado struct {
	Errores []Error
}

// Valido informa si la validacion fue exitosa.
func (r Resultado) Valido() bool { return len(r.Errores) == 0 }

// Err devuelve un error agregado, o nil si la validacion fue exitosa.
//
// El mensaje incluye TODOS los fallos encontrados, no solo el primero: informar
// de un unico error por intento obliga al LLM a corregirlos de uno en uno y
// agota los tres intentos disponibles.
func (r Resultado) Err() error {
	if r.Valido() {
		return nil
	}

	mensajes := make([]string, 0, len(r.Errores))
	for _, e := range r.Errores {
		mensajes = append(mensajes, e.Error())
	}

	return fmt.Errorf("el documento no cumple el JSON Schema (%d errores): %s",
		len(r.Errores), strings.Join(mensajes, "; "))
}

// Validar comprueba un documento JSON (ya decodificado a any) contra el schema.
//
// Los errores se acumulan en lugar de cortarse en el primero, para que el
// Retry Loop pueda corregirlos todos de una vez.
func Validar(documento any, schema *Schema) Resultado {
	var v validador
	v.recorrer(documento, schema, "")
	return Resultado{Errores: v.errores}
}

type validador struct {
	errores []Error
}

func (v *validador) errorf(ruta, formato string, args ...any) {
	if ruta == "" {
		ruta = "(raiz)"
	}
	v.errores = append(v.errores, Error{
		Ruta:    ruta,
		Mensaje: fmt.Sprintf(formato, args...),
	})
}

// recorrer aplica el schema a un valor.
func (v *validador) recorrer(valor any, schema *Schema, ruta string) {
	if schema == nil {
		return
	}

	if !coincideTipo(valor, schema.Type) {
		v.errorf(ruta, "se esperaba tipo %q y se recibio %s", schema.Type, tipoDe(valor))
		// Si el tipo no coincide, seguir validando las reglas de ese tipo daria
		// errores derivados y confusos. Se detiene la recursion.
		return
	}

	v.validarEnum(valor, schema, ruta)

	switch schema.Type {
	case "string":
		v.validarTexto(valor, schema, ruta)
	case "number", "integer":
		v.validarNumero(valor, schema, ruta)
	case "array":
		v.validarArreglo(valor, schema, ruta)
	case "object":
		v.validarObjeto(valor, schema, ruta)
	}
}

func (v *validador) validarEnum(valor any, schema *Schema, ruta string) {
	if len(schema.Enum) == 0 {
		return
	}

	for _, permitido := range schema.Enum {
		if fmt.Sprintf("%v", permitido) == fmt.Sprintf("%v", valor) {
			return
		}
	}

	// Se enumeran los valores admitidos: el mensaje viaja al prompt de
	// autocorreccion, y un LLM puede corrigirse mucho mejor si sabe las
	// opciones exactas que si solo se le dice "valor invalido".
	opciones := make([]string, 0, len(schema.Enum))
	for _, permitido := range schema.Enum {
		opciones = append(opciones, fmt.Sprintf("%v", permitido))
	}

	v.errorf(ruta, "el valor %q no es valido; se esperaba uno de: %s",
		fmt.Sprintf("%v", valor), strings.Join(opciones, ", "))
}

func (v *validador) validarTexto(valor any, schema *Schema, ruta string) {
	texto, ok := valor.(string)
	if !ok {
		return
	}

	// Se cuenta en RUNES, no en bytes: el schema declara maxLength en
	// caracteres, y contar en bytes rechazaria textos con acentos o emoji.
	longitud := len([]rune(texto))

	if schema.MinLength != nil && longitud < *schema.MinLength {
		v.errorf(ruta, "el texto tiene %d caracteres y el minimo es %d", longitud, *schema.MinLength)
	}
	if schema.MaxLength != nil && longitud > *schema.MaxLength {
		v.errorf(ruta, "el texto tiene %d caracteres y el maximo es %d", longitud, *schema.MaxLength)
	}
}

func (v *validador) validarNumero(valor any, schema *Schema, ruta string) {
	numero, ok := valor.(float64)
	if !ok {
		return
	}

	if schema.Minimum != nil && numero < *schema.Minimum {
		v.errorf(ruta, "el valor %s es menor que el minimo %s",
			formatearNumero(numero), formatearNumero(*schema.Minimum))
	}
	if schema.Maximum != nil && numero > *schema.Maximum {
		v.errorf(ruta, "el valor %s excede el maximo %s",
			formatearNumero(numero), formatearNumero(*schema.Maximum))
	}
}

func (v *validador) validarArreglo(valor any, schema *Schema, ruta string) {
	arreglo, ok := valor.([]any)
	if !ok {
		return
	}

	if schema.MinItems != nil && len(arreglo) < *schema.MinItems {
		v.errorf(ruta, "el arreglo tiene %d elementos y el minimo es %d", len(arreglo), *schema.MinItems)
	}
	if schema.MaxItems != nil && len(arreglo) > *schema.MaxItems {
		v.errorf(ruta, "el arreglo tiene %d elementos y el maximo es %d", len(arreglo), *schema.MaxItems)
	}

	if schema.Items == nil {
		return
	}

	// Se valida cada elemento. El indice forma parte de la ruta para que el
	// error diga exactamente que item falla.
	for i, elemento := range arreglo {
		v.recorrer(elemento, schema.Items, fmt.Sprintf("%s[%d]", ruta, i))
	}
}

func (v *validador) validarObjeto(valor any, schema *Schema, ruta string) {
	objeto, ok := valor.(map[string]any)
	if !ok {
		return
	}

	for _, requerida := range schema.Required {
		if _, existe := objeto[requerida]; !existe {
			v.errorf(ruta, "falta la clave obligatoria %q", requerida)
		}
	}

	// additionalProperties: false rechaza claves no declaradas.
	//
	// Esto importa mas de lo que parece: el LLM tiende a "mejorar" la respuesta
	// anadiendo campos ("notes", "confidence") que la plataforma destino no
	// sabe interpretar. Rechazarlos aqui es la primera linea de defensa.
	rechazaExtras := schema.AdditionalProperties != nil && !*schema.AdditionalProperties

	for clave, valorCampo := range objeto {
		definido, conocido := schema.Properties[clave]

		if !conocido {
			if rechazaExtras {
				v.errorf(ruta, "la clave %q no esta definida en el schema", clave)
			}
			continue
		}

		prop := ruta
		if prop == "" {
			prop = clave
		} else {
			prop = prop + "." + clave
		}

		v.recorrer(valorCampo, definido, prop)
	}
}

// coincideTipo comprueba el tipo declarado.
//
// Para "integer" se acepta un float64 sin parte decimal, porque encoding/json
// decodifica TODOS los numeros como float64: el entero 3 llega como 3.0 y un
// chequeo estricto lo rechazaria.
func coincideTipo(valor any, tipo string) bool {
	if tipo == "" {
		return true
	}

	switch tipo {
	case "object":
		_, ok := valor.(map[string]any)
		return ok
	case "array":
		_, ok := valor.([]any)
		return ok
	case "string":
		_, ok := valor.(string)
		return ok
	case "boolean":
		_, ok := valor.(bool)
		return ok
	case "null":
		return valor == nil
	case "number":
		switch valor.(type) {
		case float64, int, int64:
			return true
		}
		return false
	case "integer":
		switch t := valor.(type) {
		case float64:
			return t == float64(int64(t))
		case int, int64:
			return true
		}
		return false
	}

	// Tipo desconocido: no se puede afirmar que no coincide. Se acepta para no
	// producir falsos positivos con una palabra clave no soportada.
	return true
}

// tipoDe devuelve el nombre del tipo Go de un valor decodificado, para los
// mensajes de error.
func tipoDe(valor any) string {
	switch valor.(type) {
	case map[string]any:
		return "objeto"
	case []any:
		return "arreglo"
	case string:
		return "texto"
	case float64:
		return "numero"
	case int:
		return "entero"
	case bool:
		return "booleano"
	case nil:
		return "null"
	default:
		return "desconocido"
	}
}

// formatearNumero evita notacion cientifica en los mensajes, que el LLM
// interpreta peor que un decimal simple.
func formatearNumero(n float64) string {
	return strconv.FormatFloat(n, 'f', -1, 64)
}

// ClavesOrdenadas devuelve las claves de properties en orden alfabetico.
//
// Existe para que los mensajes de error sean deterministas: iterar un mapa de Go
// produce un orden aleatorio, y un LLM que recibe la lista de errores en orden
// distinto en cada intento es menos probable que corrija de forma consistente.
func (s *Schema) ClavesOrdenadas() []string {
	if s == nil || s.Properties == nil {
		return nil
	}

	claves := make([]string, 0, len(s.Properties))
	for clave := range s.Properties {
		claves = append(claves, clave)
	}

	sort.Strings(claves)
	return claves
}

// ParseSchema decodifica un JSON Schema y devuelve el subconjunto soportado
// junto con las palabras clave que este validador ignora.
//
// Las palabras clave desconocidas se devuelven en vez de descartarse en
// silencio: una regla que no se aplica es peor que no existir, porque el schema
// aparenta proteger algo que nadie comprueba.
func ParseSchema(documento []byte) (*Schema, []string, error) {
	var schema Schema

	if err := json.Unmarshal(documento, &schema); err != nil {
		return nil, nil, fmt.Errorf("el JSON Schema no es JSON valido: %w", err)
	}

	var crudo map[string]any
	if err := json.Unmarshal(documento, &crudo); err != nil {
		return nil, nil, fmt.Errorf("el JSON Schema no es un objeto JSON: %w", err)
	}

	desconocidas := palabrasClaveDesconocidas(crudo)

	// El mismo schema puede repetir una palabra clave en varios nodos. Se
	// deduplica y ordena para que el aviso sea estable entre ejecuciones y
	// para que no dependa del orden de iteracion de un mapa.
	schema.desconocidas = unicasYOrdenadas(desconocidas)

	return &schema, schema.desconocidas, nil
}

// palabrasClaveDesconocidas recorre el schema buscando palabras clave fuera del
// subconjunto soportado.
//
// La recursion distingue dos sitios con reglas distintas:
//
//   - Un nodo de esquema ({"type": ..., "minLength": ...}): sus claves son
//     palabras clave del schema.
//   - El interior de "properties": sus claves son NOMBRES de campo del
//     documento, no palabras clave. Confundirlas haria que "titulo" o
//     "action_items" se reportaran como reglas no soportadas, que es ruido y
//     oculta las ausencias reales. Por eso se baja al VALOR de cada propiedad
//     (su definicion) y no a su nombre.
func palabrasClaveDesconocidas(nodo any) []string {
	var resultado []string

	switch t := nodo.(type) {
	case map[string]any:
		for clave, valor := range t {
			switch clave {
			case "properties":
				// Cada entrada es nombreDeCampo -> definicion. Solo la
				// definicion contiene reglas que revisar.
				definiciones, ok := valor.(map[string]any)
				if !ok {
					continue
				}
				for _, definicion := range definiciones {
					resultado = append(resultado, palabrasClaveDesconocidas(definicion)...)
				}

			case "enum", "required", "default", "description", "title",
				"$schema", "$id", "additionalProperties":
				// Valores de datos o metadatos: no contienen reglas.

			default:
				if !palabrasClaveConocidas[clave] {
					resultado = append(resultado, clave)
				}

				// "items" es la unica palabra clave cuyo valor es otro esquema.
				if clave == "items" {
					resultado = append(resultado, palabrasClaveDesconocidas(valor)...)
				}
			}
		}

	case []any:
		for _, elemento := range t {
			resultado = append(resultado, palabrasClaveDesconocidas(elemento)...)
		}
	}

	sort.Strings(resultado)
	return unique(resultado)
}

// unique elimina duplicados preservando el orden.
func unique(valores []string) []string {
	vistos := make(map[string]bool, len(valores))
	salida := make([]string, 0, len(valores))

	for _, v := range valores {
		if vistos[v] {
			continue
		}
		vistos[v] = true
		salida = append(salida, v)
	}

	return salida
}

// unicasYOrdenadas deduplica y ordena una lista de palabras clave.
func unicasYOrdenadas(valores []string) []string {
	if len(valores) == 0 {
		return nil
	}

	vistos := make(map[string]bool, len(valores))
	resultado := make([]string, 0, len(valores))

	for _, valor := range valores {
		if vistos[valor] {
			continue
		}
		vistos[valor] = true
		resultado = append(resultado, valor)
	}

	sort.Strings(resultado)

	return resultado
}
