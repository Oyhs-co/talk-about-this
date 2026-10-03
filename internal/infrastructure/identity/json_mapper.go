package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"unicode"

	"talkaboutthis/internal/domain"
)

// ErrTablaMapeoInvalida indica que mappings.json no se pudo cargar o no tiene
// una forma utilizable.
var ErrTablaMapeoInvalida = errors.New("la tabla de mapeo de identidades no es valida")

// PoliticaDesconocido define que hacer cuando un nombre no aparece en la tabla.
//
// Es una decision operativa, no una regla de negocio: la misma extraccion puede
// publicarse de tres maneras legitimas segun lo que quiera el equipo.
type PoliticaDesconocido string

const (
	// PoliticaFallar detiene el proceso. Es la opcion segura y la que conviene
	// por defecto: publicar un item sin asignar es peor que no publicar.
	PoliticaFallar PoliticaDesconocido = "fail"

	// PoliticaAsignarSinAsignado deja el item sin responsable y sigue. Util
	// cuando el tablero tiene una columna de "Sin asignar".
	PoliticaAsignarSinAsignado PoliticaDesconocido = "assign_unassigned"

	// PoliticaOmitir descarta el item. Util cuando el equipo solo quiere lo
	// que tiene responsable conocido.
	PoliticaOmitir PoliticaDesconocido = "skip"
)

// PoliticasValidas enumera los valores aceptados en el archivo de configuracion.
var PoliticasValidas = []PoliticaDesconocido{
	PoliticaFallar,
	PoliticaAsignarSinAsignado,
	PoliticaOmitir,
}

// Configuracion es el contenido de mappings.json.
//
// No es sensible: son identidades de trabajo, no credenciales. Aun asi el
// archivo real esta en .gitignore y solo se versiona el ejemplo.
type Configuracion struct {
	Version  string     `json:"version"`
	Defaults DefaultCfg `json:"defaults"`
	Mappings []Mapping  `json:"mappings"`
}

// DefaultCfg son los valores por defecto de la tabla.
type DefaultCfg struct {
	// DefaultHandle es el handle asignado cuando la politica es
	// assign_unassigned y no hay un responsible concreto.
	DefaultHandle string `json:"default_handle"`
	// UnknownAssigneePolicy es la politica ante nombres no mapeados.
	UnknownAssigneePolicy PoliticaDesconocido `json:"unknown_assignee_policy"`
}

// Mapping asocia varios nombres de texto libre con un handle de plataforma.
type Mapping struct {
	RawNames  []string `json:"raw_names"`
	Handle    string   `json:"handle"`
	Platforms []string `json:"platforms"`
	Email     string   `json:"email"`
}

// JSONIdentityMapper resuelve nombres de una minuta a handles de plataforma.
//
// Implementa domain.IdentityMapper (RF-04).
//
// La resolucion es EXACTA pero tolerante a variacion de escritura, que es el
// problema real: en una minuta el mismo nombre aparece como "Omar Hernández",
// "omar hernandez", "Omar H." o "Omar", y todos son la misma persona.
//
// # INDICES Y ALIAS
//
// El indice no guarda solo los nombres literales. De cada nombre completo se
// derivan alias:
//
//   - El nombre completo normalizado.
//   - "Nombre + inicial del apellido" ("omar h"), para menciones abreviadas.
//   - El nombre de pila solo ("omar"), pero UNICAMENTE si no hay otra persona
//     con ese nombre en la tabla.
//
// Esa ultima condicion es importante: sin ella, un "Ana" ambiguo se resolveria
// al azar, y publicar una tarea bajo la persona equivocada es un fallo silencioso
// muy costoso. Ante la duda, el mapper devuelve un error y el usuario decide.
type JSONIdentityMapper struct {
	indice     map[string]string
	politica   PoliticaDesconocido
	porDefecto string

	// Solo lectura tras la construccion: la consulta concurrente de varios
	// items es segura sin necesidad de mutex.
	mu sync.RWMutex
}

// NuevoJSONIdentityMapper carga una tabla desde una ruta.
//
// El archivo se lee completo en memoria y se indexa una sola vez. Consultar el
// archivo en cada nombre seria una lectura de disco por item, y el mapper se
// usa justo en la ruta caliente (la etapa de despacho).
func NuevoJSONIdentityMapper(ruta string) (*JSONIdentityMapper, error) {
	archivo, err := os.Open(ruta)
	if err != nil {
		return nil, fmt.Errorf("%w: no se pudo abrir %q: %w", ErrTablaMapeoInvalida, ruta, err)
	}
	defer func() {
		_ = archivo.Close()
	}()

	datos, err := io.ReadAll(archivo)
	if err != nil {
		return nil, fmt.Errorf("%w: no se pudo leer %q: %w", ErrTablaMapeoInvalida, ruta, err)
	}

	return NuevoJSONIdentityMapperDesdeBytes(datos)
}

// NuevoJSONIdentityMapperDesdeBytes construye el mapper a partir del JSON.
//
// Existe separado del constructor por ruta para que los tests puedan usar la
// misma logica de indexado sin escribir archivos en disco.
func NuevoJSONIdentityMapperDesdeBytes(datos []byte) (*JSONIdentityMapper, error) {
	var config Configuracion

	if err := json.Unmarshal(datos, &config); err != nil {
		return nil, fmt.Errorf("%w: JSON invalido: %w", ErrTablaMapeoInvalida, err)
	}

	if len(config.Mappings) == 0 {
		return nil, fmt.Errorf("%w: el archivo no contiene ningun mapeo", ErrTablaMapeoInvalida)
	}

	m := &JSONIdentityMapper{
		indice:     make(map[string]string),
		politica:   normalizarPolitica(config.Defaults.UnknownAssigneePolicy),
		porDefecto: config.Defaults.DefaultHandle,
	}

	m.construirIndice(config.Mappings)

	return m, nil
}

// Politica devuelve la politica configurada ante nombres desconocidos.
func (m *JSONIdentityMapper) Politica() PoliticaDesconocido {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.politica
}

// HandlePorDefecto devuelve el handle usado por la politica assign_unassigned.
func (m *JSONIdentityMapper) HandlePorDefecto() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.porDefecto
}

// construirIndice puebla el mapa de alias a handle.
//
// Se construye en dos pasadas porque el alias de nombre de pila solo puede
// anadirse si no colisiona con otra persona, y eso no se sabe hasta haber leido
// todas las entradas.
func (m *JSONIdentityMapper) construirIndice(mappings []Mapping) {
	// pila -> conjunto de handles que registran ese nombre de pila.
	// Hace falta un conjunto, y no un unico handle, para poder distinguir
	// "nadie mas lo registro" de "otro lo registro con otro handle".
	pilas := make(map[string]map[string]bool)

	for _, mapping := range mappings {
		handle := strings.TrimSpace(mapping.Handle)
		if handle == "" {
			// Una entrada sin handle no sirve para nada y solo introduciria
			// colisiones. Se ignora en lugar de romper toda la carga.
			continue
		}

		for _, nombre := range mapping.RawNames {
			clave := NormalizarNombre(nombre)
			if clave == "" {
				continue
			}

			m.indice[clave] = handle

			partes := strings.Fields(clave)
			if len(partes) < 2 {
				continue
			}

			// Alias abreviados: nombre de pila + inicial de CADA token
			// siguiente.
			//
			// Se generan con todos, no solo con el ultimo, porque un nombre
			// puede tener dos apellidos: "Ana Maria Ruiz" debe resolver tanto
			// "Ana M." (inicial del segundo token) como "Ana R." (del tercero).
			// Limitarlo al ultimo haria fallar las menciones mas habituales.
			for j := 1; j < len(partes); j++ {
				inicial := partes[j]
				if len(inicial) > 0 {
					m.indice[partes[0]+" "+inicial[:1]] = handle
				}
			}

			// Candidato de nombre de pila.
			if _, visto := pilas[partes[0]]; !visto {
				pilas[partes[0]] = make(map[string]bool)
			}
			pilas[partes[0]][handle] = true
		}
	}

	// Segunda pasada: el nombre de pila solo se registra si lo reclama
	// exactamente UN handle.
	for pila, handles := range pilas {
		if len(handles) != 1 {
			// Es ambiguo. Resolverlo al azar publicaria tareas bajo la persona
			// equivocada, que es el peor fallo posible: nadie se entera.
			continue
		}

		for handle := range handles {
			m.indice[pila] = handle
		}
	}
}

// ResolveHandle implementa domain.IdentityMapper.
//
// Devuelve el handle SIN el prefijo "@": es lo que exigen los campos de assignee
// de GitHub, y anteponerlo alli produce un error de la API. El "@" de la
// especificacion es notacion de la documentacion, no parte del valor.
//
// Un nombre no mapeado devuelve un error que envuelve
// domain.ErrIdentidadNoResuelta, para que la politica de la capa de aplicacion
// pueda aplicarlo.
func (m *JSONIdentityMapper) ResolveHandle(ctx context.Context, rawName string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	m.mu.RLock()
	handle, existe := m.indice[NormalizarNombre(rawName)]
	m.mu.RUnlock()

	if existe && handle != "" {
		return handle, nil
	}

	return "", fmt.Errorf("%w: %q no figura en la tabla de aliases", domain.ErrIdentidadNoResuelta, rawName)
}

// ConoceInforma si un nombre tiene handle asignado, sin devolverlo.
//
// Permite a la capa de aplicacion aplicar la politica sin capturar un error que
// no es realmente un fallo.
func (m *JSONIdentityMapper) ConoceInforma(rawName string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	handle, existe := m.indice[NormalizarNombre(rawName)]
	return existe && handle != ""
}

// HandlesConocidos devuelve los handles registrados, ordenados.
//
// Existe para diagnostico: ayuda a un usuario que no entiende por que un nombre
// no se resuelve.
func (m *JSONIdentityMapper) HandlesConocidos() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	vistos := make(map[string]bool)
	for _, handle := range m.indice {
		vistos[handle] = true
	}

	salida := make([]string, 0, len(vistos))
	for handle := range vistos {
		salida = append(salida, handle)
	}

	sort.Strings(salida)
	return salida
}

// Asercion de compilacion del puerto.
var _ domain.IdentityMapper = (*JSONIdentityMapper)(nil)

// NormalizarNombre lleva un nombre a la forma en que se indexa.
//
// Las reglas responden a como se escribe un nombre de verdad en una minuta:
//
//   - Minusculas: evita que "Omar" y "omar" sean entradas distintas.
//   - Sin acentos: "Hernández" y "Hernandez" son la misma persona, y ningun
//     usuario escribe el acento de forma consistente al dictar.
//   - Sin puntos: permite que "Omar H." y "Omar H" colisionen a proposito.
//   - Espacios colapsados: "Omar  Hernández" (dos espacios) es un error tipografico
//     comun, no otra persona.
//
// Go estandar no trae normalizacion Unicode (NFC/NFD), asi que los acentos se
// resuelven con una tabla explicita del rango latino, que es lo que aparece en
// nombres y aliases reales.
func NormalizarNombre(nombre string) string {
	minusculas := strings.ToLower(strings.TrimSpace(nombre))

	var b strings.Builder
	b.Grow(len(minusculas))

	espacioPendiente := false

	for _, r := range minusculas {
		// Los separadores se tratan como un unico espacio y se emiten despues:
		// asi "Omar   Hernández" y "Omar Hernández" convergen.
		if unicode.IsSpace(r) || r == '.' || r == ',' {
			espacioPendiente = b.Len() > 0
			continue
		}

		if espacioPendiente {
			b.WriteRune(' ')
			espacioPendiente = false
		}

		b.WriteRune(sinAcento(r))
	}

	return b.String()
}

// sinAcento sustituye las vocales acentuadas y la enye por su forma simple.
func sinAcento(r rune) rune {
	switch r {
	case 'á', 'à', 'ä', 'â':
		return 'a'
	case 'é', 'è', 'ë', 'ê':
		return 'e'
	case 'í', 'ì', 'ï', 'î':
		return 'i'
	case 'ó', 'ò', 'ö', 'ô':
		return 'o'
	case 'ú', 'ù', 'ü', 'û':
		return 'u'
	case 'ñ':
		// La enye se convierte en n: "Hernández" y "Hernandez" deben ser la
		// misma persona.
		return 'n'
	case 'ç':
		return 'c'
	}
	return r
}

// normalizarPolitica valida y corrige la politica configurada.
//
// Una politica desconocida cae a "fail", que es la opcion segura: ante un valor
// mal escrito, lo correcto es detenerse, no publicar tareas al azar.
func normalizarPolitica(politica PoliticaDesconocido) PoliticaDesconocido {
	if politica == "" {
		return PoliticaFallar
	}

	normalizada := PoliticaDesconocido(strings.ToLower(strings.TrimSpace(string(politica))))

	for _, valida := range PoliticasValidas {
		if normalizada == valida {
			return normalizada
		}
	}

	// Politica desconocida: se cae a la opcion segura. Publicar sin asignar es
	// un fallo silencioso caro; detenerse es visible y el usuario lo corrige.
	return PoliticaFallar
}
