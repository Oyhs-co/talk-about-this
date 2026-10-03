# Documento de Especificación y Contrato de Desarrollo: TalkAboutThis

**TalkAboutThis** es una CLI y motor de automatización de alto rendimiento escrito en **Go (Golang)** que transforma transcripciones, notas y minutas de reuniones (`.md`, `.txt`, `.docx`) en ítems accionables de backlog dentro de **GitHub Projects (v2)** y otras plataformas de gestión de proyectos, utilizando modelos de lenguaje locales (Ollama/LM Studio) o APIs SaaS (OpenAI, Anthropic, Gemini).

---

## 1. Visión Arquitectónica (Go-Native & Extensible)

El proyecto está diseñado bajo una **Arquitectura Hexagonal (Puertos y Adaptadores)** y los principios de **Domain-Driven Design (DDD)**. Aprovecha las ventajas nativas de Go:

* **Cero dependencias pesadas de runtime:** Compilación a un binario estático único y ligero.
* **Procesamiento concurrente:** Parsing e Invocación paralela de adaptadores vía *Goroutines* y *Channels*.
* **Extensibilidad por Contrato:** Definición estricta de comportamientos mediante **Interfaces de Go**, lo que permite agregar nuevos parsers (ej. PDF, Audio-to-Text), proveedores de LLM o gestores de tareas (Jira, Linear, Trello, Azure DevOps) sin tocar la lógica del dominio (*Open-Closed Principle*).

---

## 2. Requerimientos del Sistema

### 2.1. Requerimientos Funcionales (RF)

| ID        | Requerimiento                                 | Descripción                                                                                                                                                                |
| --------- | --------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **RF-01** | **Ingesta Multiformato**                      | Extraer texto plano y metadatos de archivos `.md`, `.txt` y `.docx`. La arquitectura debe ser extensible para futuros lectores (`.pdf`, `.json`).                          |
| **RF-02** | **Soporte Híbrido y Extensible de LLM**       | Conexión agnóstica con motores locales (Ollama) o APIs externas (OpenAI, Anthropic, Gemini) mediante contratos de interfaz.                                                |
| **RF-03** | **Extracción Estructurada**                   | El LLM debe procesar el contexto y generar un schema estricto conteniendo: Título, Descripción, Asignado (`github_username` / `alias`), Prioridad, Etiquetas y Estimación. |
| **RF-04** | **Mapeo Dinámico de Identidades**             | Traducción de nombres reales o menciones en la reunión a handles de usuario válidos mediante un `IdentityMapper`.                                                          |
| **RF-05** | **Adaptador Pluggable de Proyectos**          | Despacho de tareas a **GitHub Projects (v2)** vía GraphQL API o CLI (`gh`). Extensible para soportar Jira, Linear, Trello o GitLab.                                        |
| **RF-06** | **Modo Simulador (Dry-Run)**                  | Permitir la inspección previa del backlog extraído (formato JSON o tabla en consola) sin mutar las plataformas destino.                                                    |
| **RF-07** | **Validación y Auto-Corrección (Retry Loop)** | Si la respuesta del LLM violara el JSON Schema, el sistema debe re-ejecutar la petición adjuntando el error de sintaxis para auto-corrección.                              |

### 2.2. Requerimientos No Funcionales (RNF)

| ID         | Criterio                                | Especificación Técnica                                                                                                                                                      |
| ---------- | --------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **RNF-01** | **Desacoplamiento Absoluto (SOLID)**    | La capa del dominio solo interactúa con contratos (`type Provider interface`). Ningún componente concreto de infraestructura se importa en el Core.                         |
| **RNF-02** | **Spec-Driven Development (SDD)**       | Los esquemas de intercambio de datos se definen mediante JSON Schema formal e instanciaciones de `struct` Go codificadas bajo Go Struct Tags (`json:"..." validate:"..."`). |
| **RNF-03** | **Alto Rendimiento y Concurrencia**     | Ingesta, parseo y mapeo en tiempo < 50ms. Manejo de I/O bloqueante (red/LLMs) orquestado con `context.Context` con timeouts explícitos.                                     |
| **RNF-04** | **Seguridad & Credenciales**            | Inyección de API Keys y Tokens (GitHub PAT) mediante variables de entorno o flags CLI seguros. Cero persistencia de secretos en disco.                                      |
| **RNF-05** | **Resiliencia & Tolerancia a Fallos**   | Manejo de errores idiomático en Go (`fmt.Errorf("...: %w", err)`), combinando patrones de *Retry with Exponential Backoff* para peticiones HTTP.                            |
| **RNF-06** | **Trazabilidad & Logging Estructurado** | Logs formateados en JSON utilizando la librería nativa `log/slog` de Go, incluyendo IDs de correlación por trabajo (*Job ID*).                                              |

---

## 3. Arquitectura de Dominio (DDD) & Bounded Contexts

```text
                      [ Entradas CLI / API / Webhook ]
                                     │
                                     ▼
 ┌────────────────────────────────────────────────────────────────────────┐
 │                      BOUNDED CONTEXT: INGESTION                        │
 │  Entities: Transcript, DocumentMetadata                                │
 │  Value Objects: FilePath, RawContent, FileExtension                    │
 │  Ports: DocumentParser interface                                       │
 └───────────────────────────────────┬────────────────────────────────────┘
                                     │ Domain Event: TranscriptParsedEvent
                                     ▼
 ┌────────────────────────────────────────────────────────────────────────┐
 │                      BOUNDED CONTEXT: EXTRACTION                       │
 │  Aggregates: ExtractionJob                                             │
 │  Entities: ActionItem                                                  │
 │  Value Objects: TaskTitle, TaskDescription, Assignee, Priority         │
 │  Ports: LLMProvider interface                                          │
 └───────────────────────────────────┬────────────────────────────────────┘
                                     │ Domain Event: ActionItemsExtractedEvent
                                     ▼
 ┌────────────────────────────────────────────────────────────────────────┐
 │                  BOUNDED CONTEXT: BACKLOG MANAGEMENT                   │
 │  Aggregates: BoardReference                                            │
 │  Entities: BacklogCard                                                 │
 │  Ports: ProjectBoardAdapter interface, IdentityMapper interface        │
 └────────────────────────────────────────────────────────────────────────┘

```

---

## 4. Definición de Módulos y Flujo de Trabajo (Contrato de Ejecución)

Este apartado establece el **contrato operacional** de cómo la aplicación debe procesar los datos de extremo a extremo. Todo desarrollo o agente de IA debe ceñirse estrictamente a estas etapas y límites de responsabilidad.

### 4.1. Responsabilidad por Módulo

```text
┌───────────────────────────────────────────────────────────────────────────────────┐
│                                TALKABOUTTHIS CLI                                  │
└─────────┬───────────────────────────────┬──────────────────────────────┬──────────┘
          │                               │                              │
          ▼                               ▼                              ▼
┌──────────────────┐           ┌──────────────────┐           ┌──────────────────┐
│   1. INGESTION   │           │ 2. EXTRACTION    │           │  3. DISPATCH     │
│     MODULE       │──────────>│    & LLM ENGINE  │──────────>│     MODULE       │
│                  │           │                  │           │                  │
│  - File Reader   │           │  - Schema Validate│           │  - Identity Map  │
│  - Docx/MD/TXT   │           │  - LLM Provider  │           │  - Board Adapter │
│  - Text Cleanup  │           │  - Retry Loop    │           │  - Dry-Run Check │
└──────────────────┘           └──────────────────┘           └──────────────────┘

```

1. **Módulo Ingestor (`internal/infrastructure/parsers`)**:

* **Entrada**: Ruta del archivo (`.docx`, `.md`, `.txt`) o un `io.Reader`.
* **Responsabilidad**: Determinar el parser adecuado según la extensión o cabecera, extraer el texto plano, remover ruido de formato y entregar una entidad `Transcript` homogénea.

1. **Módulo de Extracción e IA (`internal/infrastructure/llm` + `internal/application`)**:

* **Entrada**: Contenido del `Transcript` + Contrato JSON Schema (`backlog_schema.json`).
* **Responsabilidad**: Invocación al proveedor configurado (Ollama, OpenAI, Gemini). Aplicar el *Retry Loop* con auto-corrección si la IA responde con formato corrupto. Validar la respuesta contra el schema Pydantic/Go Struct.

1. **Módulo Mapeador de Identidades (`internal/infrastructure/identity`)**:

* **Entrada**: Lista de `ActionItem` extraídos con nombres en texto libre (ej. "Omar").
* **Responsabilidad**: Consultar la tabla de aliases (`mappings.json`) y traducir los nombres reales a usernames/handles válidos de la plataforma (ej. `@omarhernan`).

1. **Módulo Despachador de Proyectos (`internal/infrastructure/adapters`)**:

* **Entrada**: Lista final de `ActionItem` procesados + banderas de ejecución (`--dry-run`, `--publish`).
* **Responsabilidad**: Si `--dry-run` está activo, imprime el backlog estructurado en stdout. Si `--publish` está activo, orquesta las mutaciones de GraphQL hacia GitHub Projects v2 (o plataformas alternativas).

### 4.2. Pipeline del Flujo de Trabajo Paso a Paso

```tree
[Inicio CLI] 
    │
    ├── 1. Ingesta: Cargar archivo de contexto (.docx/.md/.txt)
    │      └── Valida formato -> Extrae Texto Plano -> Retorna `Transcript`
    │
    ├── 2. Inferencia IA: Enviar prompt estructurado a `LLMProvider`
    │      ├── Intento 1: ¿Respuesta cumple con `backlog_schema.json`?
    │      │     ├── SÍ ──> Continuar a etapa 3
    │      │     └── NO ──> Re-intentar con prompt de corrección (Máx. 3 intentos)
    │      └── Retorna `MeetingBacklogExtraction`
    │
    ├── 3. Resolución de Identidades: Ejecutar `IdentityMapper`
    │      └── Mapea `Assignee` de cada tarea -> Actualiza `MappedHandle`
    │
    ├── 4. Decisión de Salida (`Dry-Run` vs `Publish`)
    │      ├── ¿Flag `--dry-run` activo?
    │      │     ├── SÍ ──> Imprimir JSON/Tabla en STDOUT -> Fin (Exit 0)
    │      │     └── NO ──> Continuar a etapa 5
    │      │
    │      └── 5. Despacho: Invocar `ProjectBoardAdapter`
    │            ├── Crear Issues en Repositorio GitHub
    │            ├── Asociar Issues a columnas del GitHub Project v2
    │            └── Retornar resumen de tarjetas creadas -> Fin (Exit 0)
    │
[Fin / Log Output]

```

---

## 5. Interfaces Core en Go (Abierto a Implementaciones)

La extensibilidad del proyecto se garantiza mediante interfaces pequeñas y enfocadas en el paquete `internal/domain`.

### 5.1. Puertos de Dominio (Interfaces)

```go
package domain

import (
	"context"
	"io"
)

// DocumentParser define el contrato para extraer texto plano de cualquier formato.
type DocumentParser interface {
	CanParse(extension string) bool
	Parse(ctx context.Context, reader io.Reader) (string, error)
}

// LLMProvider define el contrato para comunicarse con cualquier motor de IA.
type LLMProvider interface {
	Name() string
	GenerateStructuredOutput(ctx context.Context, prompt string, schemaJSON string) ([]byte, error)
}

// IdentityMapper mapea nombres hallados en la reunión con handles de la plataforma destino.
type IdentityMapper interface {
	ResolveHandle(ctx context.Context, rawName string) (string, error)
}

// ProjectBoardAdapter define el contrato para publicar ítems en cualquier gestor de proyectos.
type ProjectBoardAdapter interface {
	PlatformName() string
	PublishBacklog(ctx context.Context, projectRef string, items []ActionItem) ([]PublishResult, error)
}

```

### 5.2. Modelos y Schemas SDD (`internal/domain/models.go`)

```go
package domain

import "time"

type Priority string

const (
	PriorityHigh   Priority = "HIGH"
	PriorityMedium Priority = "MEDIUM"
	PriorityLow    Priority = "LOW"
)

type ActionItem struct {
	Title        string   `json:"title" validate:"required,max=100"`
	Description  string   `json:"description" validate:"required"`
	RawAssignee  string   `json:"assignee_name" validate:"required"`
	MappedHandle string   `json:"mapped_handle,omitempty"`
	Priority     Priority `json:"priority" validate:"required,oneof=HIGH MEDIUM LOW"`
	Labels       []string `json:"labels"`
	StoryPoints  int      `json:"story_points,omitempty"`
}

type MeetingBacklogExtraction struct {
	MeetingSummary string       `json:"meeting_summary" validate:"required"`
	ActionItems    []ActionItem `json:"action_items" validate:"required,dive"`
}

type PublishResult struct {
	TaskTitle  string
	ExternalID string
	URL        string
	Success    bool
	Error      error
}

type ExtractionJob struct {
	ID        string
	CreatedAt time.Time
	Status    string
	RawText   string
	Result    *MeetingBacklogExtraction
}

```

---

## 6. Especificación Driven Development (SDD)

### JSON Schema de Extracción (`docs/specifications/backlog_schema.json`)

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "MeetingBacklogExtraction",
  "type": "object",
  "properties": {
    "meeting_summary": { "type": "string" },
    "action_items": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "title": { "type": "string", "maxLength": 100 },
          "description": { "type": "string" },
          "assignee_name": { "type": "string" },
          "priority": { "type": "string", "enum": ["HIGH", "MEDIUM", "LOW"] },
          "labels": { 
            "type": "array", 
            "items": { "type": "string" } 
          },
          "story_points": { "type": "integer" }
        },
        "required": ["title", "description", "assignee_name", "priority", "labels"]
      }
    }
  },
  "required": ["meeting_summary", "action_items"]
}

```

---

## 7. Estructura Estándar del Proyecto Go (`Standard Go Project Layout`)

```tree
talk-about-this/
├── cmd/
│   └── talkaboutthis/         # Punto de entrada principal (main.go)
├── docs/
│   └── specifications/        # Esquemas JSON / OpenRPC / OpenAPI
│       └── backlog_schema.json
├── internal/                  # Código privado de la aplicación
│   ├── domain/                # Entidades, Value Objects e Interfaces (Puertos)
│   │   ├── models.go
│   │   ├── ports.go
│   │   └── errors.go
│   ├── application/           # Casos de uso (Orquestador / Servicios)
│   │   ├── extract_backlog.go
│   │   └── publish_backlog.go
│   └── infrastructure/        # Adaptadores Concretos
│       ├── parsers/           # Implementaciones de DocumentParser
│       │   ├── markdown.go
│       │   ├── docx.go
│       │   └── txt.go
│       ├── llm/               # Implementaciones de LLMProvider
│       │   ├── ollama.go
│       │   ├── openai.go
│       │   ├── anthropic.go
│       │   └── gemini.go
│       ├── identity/          # Implementación de IdentityMapper
│       │   └── json_mapper.go
│       └── adapters/          # Implementaciones de ProjectBoardAdapter
│           ├── github_graphql.go
│           ├── github_cli.go
│           ├── jira.go        # (Extensión futura)
│           └── linear.go      # (Extensión futura)
├── pkg/                       # Librerías reutilizables por terceros (opcional)
│   └── sdk/
├── testdata/                  # Fixtures y transcripciones de prueba
├── go.mod
├── go.sum
├── Makefile
└── README.md

```

---

## 8. Matriz de Evaluación para Agentes de IA (Benchmarking Grid)

Esta matriz permite validar si el agente de IA genera código Go idiomático, robusto y conforme a la especificación:

| Test Code | Categoría           | Entrada / Escenario                                               | Resultado Esperado                                                | Criterio de Éxito en Go                                                                      |
| --------- | ------------------- | ----------------------------------------------------------------- | ----------------------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| **TC-01** | *Ingesta DOCX*      | Lectura de un archivo `.docx` con párrafos y listas.              | Retorno de un `string` limpio UTF-8.                              | Integración con parser sin panics ni fuga de descriptores de archivo (`defer file.Close()`). |
| **TC-02** | *Ollama Adapter*    | Envío de prompt a Ollama local (`llama3` / `qwen2.5`).            | Estructura `MeetingBacklogExtraction` deserializada exitosamente. | Cero errores al ejecutar `json.Unmarshal`.                                                   |
| **TC-03** | *Retry Loop*        | Respuesta simulada de LLM con sintaxis JSON corrupta.             | Re-intento automático con contexto de error antes de fallar.      | Manejo de contexto (`context.WithTimeout`) respetado.                                        |
| **TC-04** | *Identity Map*      | Nombre real "Omar Hernández" en la minuta.                        | Traducción al handle `@omarhernan` usando `JSONIdentityMapper`.   | Mapeo case-insensitive exacto.                                                               |
| **TC-05** | *Dry-Run Mode*      | Ejecución de CLI con el flag `--dry-run`.                         | Salida formateada en stdout de las tareas procesadas.             | Cero llamadas de red realizadas hacia la API de GitHub.                                      |
| **TC-06** | *GitHub Project v2* | Petición de mutación GraphQL hacia la API v2 de GitHub Projects.  | Creación de Issue y agregado del `item` al tablero.               | Retorno con código de éxito e ID GraphQL generado.                                           |
| **TC-07** | *Extensibilidad*    | Adición de un `JiraAdapter` que implemente `ProjectBoardAdapter`. | Compilación exitosa del sistema usando el nuevo adaptador.        | Cumplimiento estricto de la interfaz sin modificar `internal/domain`.                        |

---

## 9. Plan de Ejecución e Hitos para la IA

1. **Hito 1 (Estructura Base y Dominio)**: Inicializar el módulo Go (`go mod init talkaboutthis`), implementar los modelos e interfaces en `internal/domain` y definir `docs/specifications/backlog_schema.json`.
2. **Hito 2 (Parsers e Ingesta)**: Desarrollar los lectores `.md`, `.txt` y `.docx` dentro de `internal/infrastructure/parsers`.
3. **Hito 3 (LLM Providers & Retry Loop)**: Desarrollar los clientes HTTP para Ollama y OpenAI con validación contra el JSON Schema.
4. **Hito 4 (Mapeo y Adaptador GitHub Projects)**: Implementar el resolvedor de identidades y el cliente GraphQL para la API v2 de GitHub Projects.
5. **Hito 5 (CLI & Orquestación)**: Unificar los componentes en la CLI (`cmd/talkaboutthis`), garantizando soporte completo para banderas como `--dry-run`, `--config`, y `--provider`.
