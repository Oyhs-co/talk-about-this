# AGENTS.md — Contrato de Desarrollo para Agentes

> Documento normativo para cualquier agente de IA o desarrollador humano que trabaje en **TalkAboutThis**.
> Fuente de verdad funcional: [`INIT.md`](./INIT.md). Si este documento y `INIT.md` discrepan, prevalece `INIT.md`.

## 0. Qué es TalkAboutThis

CLI y motor de automatización en **Go** que transforma transcripciones, notas y minutas de reunión
(`.md`, `.txt`, `.docx`) en **ítems accionables de backlog** dentro de **GitHub Projects (v2)**
y otras plataformas de gestión de proyectos, usando modelos de lenguaje **locales** (Ollama / LM Studio)
o **APIs SaaS** (OpenAI, Anthropic, Gemini).

- Módulo Go: `talkaboutthis`
- Versión de Go: **1.27+**
- Licencia: MIT
- Documento de especificación original: [`INIT.md`](./INIT.md)
- Plan de ejecución por fases: [`.plans/plan.md`](./.plans/plan.md)

---

## 1. Arquitectura: Hexagonal + DDD (obligatorio)

El proyecto usa **Arquitectura Hexagonal (Puertos y Adaptadores)** y **Domain-Driven Design**.
Esta no es una preferencia estética: es un requisito verificado por los tests (TC-07).

```text
cmd/talkaboutthis/        Entrada CLI. Solo debe cablear dependencias (composition root).
        │
        ▼
internal/application/     Casos de uso. Orquesta el flujo. CONOCE interfaces del dominio.
        │
        ▼
internal/domain/          Entidades, Value Objects, Errores y Puertos (interfaces).
        │                 NO importa nada de infrastructure. CERO dependencias externas.
        ▲
        │  (inversión de dependencias: la infrastructure IMPLEMENTA los puertos)
        │
internal/infrastructure/  Adaptadores concretos: parsers, llm, identity, adapters, logging.
```

### Regla de oro: la dirección de los imports

```text
cmd  ──►  application  ──►  domain
                               ▲
                               │
              infrastructure ─┘   (implementa, nunca es importado por el core)
```

| Paquete | Puede importar | NUNCA debe importar |
| --- | --- | --- |
| `internal/domain` | stdlib (`context`, `time`, `io`) | `internal/infrastructure`, `internal/application`, cualquier SDK de terceros |
| `internal/application` | `internal/domain`, stdlib | `internal/infrastructure` |
| `internal/infrastructure/*` | `internal/domain`, stdlib, librerías | nada del core de negocio salvo contratos |
| `cmd/talkaboutthis` | todo | lógica de negocio inline |

> **Cómo verificarla:** `go list -deps ./internal/domain | grep infrastructure` debe devolver vacío.
> Este comando forma parte de la revisión de código.

---

## 2. Puertos del dominio (`internal/domain/ports.go`)

Las interfaces son **pequeñas y enfocadas**. No las modifiques sin actualizar `INIT.md` §5 y el TC-07.

```go
type DocumentParser interface {
    CanParse(extension string) bool
    Parse(ctx context.Context, reader io.Reader) (string, error)
}

type LLMProvider interface {
    Name() string
    GenerateStructuredOutput(ctx context.Context, prompt string, schemaJSON string) ([]byte, error)
}

type IdentityMapper interface {
    ResolveHandle(ctx context.Context, rawName string) (string, error)
}

type ProjectBoardAdapter interface {
    PlatformName() string
    PublishBacklog(ctx context.Context, projectRef string, items []ActionItem) ([]PublishResult, error)
}
```

### Añadir un adaptador nuevo (regla Open-Closed)

Para soportar Jira, Linear, Trello o GitLab:

1. Crear el archivo dentro de `internal/infrastructure/adapters/`.
2. Implementar `domain.ProjectBoardAdapter`. **No modificar `internal/domain`.**
3. Registrarlo en el composition root (`cmd/talkaboutthis`).
4. Añadir un test que verifique la interfaz en tiempo de compilación:
   `var _ domain.ProjectBoardAdapter = (*JiraAdapter)(nil)`.

---

## 3. Modelos de dominio (`internal/domain/models.go`)

```go
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
    MeetingSummary string        `json:"meeting_summary" validate:"required"`
    ActionItems    []ActionItem `json:"action_items" validate:"required,dive"`
}
```

### Invariantes que un agente DEBE respetar

- `Title` ≤ 100 caracteres. Si el LLM devuelve algo más largo, truncar o fallar el esquema. Nunca dejar que se cuele.
- `Priority` solo admite `HIGH | MEDIUM | LOW`. Cualquier otro valor → error de validación, no default silencioso.
- `RawAssignee` se llena en la fase de extracción; `MappedHandle` **solo** en la fase de identidad.
- No agregar campos al struct sin actualizar **simultáneamente** `docs/specifications/backlog_schema.json`
  (RNF-02: el schema y el struct son el mismo contrato, en dos formatos).

---

## 4. Spec-Driven Development (RNF-02)

El JSON Schema es la **fuente de verdad** del contrato de intercambio con el LLM.

- Ubicación: `docs/specifications/backlog_schema.json`
- Regla: **cualquier cambio en los modelos Go requiere el cambio espejo en el schema, en el mismo commit.**
- El schema se entrega al LLM en el prompt (constraint), no se valida a posteriori en la mayoría de los casos.
- Además se valida la respuesta tras recibirla (defensa en profundidad, ver RF-07).

---

## 5. Flujo de ejecución obligatorio (INIT.md §4.2)

Cualquier agente que implemente funcionalidad nueva debe respetar estas etapas y su orden:

```text
[Inicio CLI]
  │
  ├─ 1. INGESTA        → cargar archivo → validar formato → texto plano → entidad Transcript
  │
  ├─ 2. INFERENCIA IA  → prompt + schema al LLMProvider
  │      ├─ ¿La respuesta valida contra el schema?
  │      │    ├─ SÍ → continuar
  │      │    └─ NO → reintentar con prompt de corrección (máx. 3 intentos)
  │      └─ retorna MeetingBacklogExtraction
  │
  ├─ 3. IDENTIDADES    → IdentityMapper: Assignee → MappedHandle
  │
  └─ 4. DECISIÓN       → ¿--dry-run?
         ├─ SÍ → imprimir JSON/tabla en stdout → exit 0
         └─ NO → 5. DESPACHO: ProjectBoardAdapter publica → resumen → exit 0
```

**Puntos de no-returnro:**
- `--dry-run` **nunca** puede disparar una llamada de red hacia la API destino (TC-05). Es un requisito de test, no una recomendación.
- El retry de extracción está acotado a 3 intentos y cada intento debe respetar `context.WithTimeout`.

---

## 6. Estándares de código Go

### Obligatorio

- **Errores idiomáticos:** siempre `fmt.Errorf("contexto de la operación: %w", err)`. Nunca `errors.New` con el error concatenado a mano, nunca `fmt.Sprintf` para componer errores.
- **Errores de dominio** declarados con `errors.New` en `internal/domain/errors.go` y comparados con `errors.Is`.
- **Contexto como primer parámetro** en toda función que haga I/O. Respetar el `ctx` recibido; nunca usar `context.Background()` dentro de la lógica de negocio.
- **Timeouts explícitos** en toda llamada de red: `context.WithTimeout(ctx, 60*time.Second)`. Sin excepción.
- **Logging con `log/slog`** (stdlib). JSON en producción. Incluir el **Job ID** como atributo en cada log.
- **`defer file.Close()`** en cuanto se abre un archivo (TC-01 comprueba fuga de descriptores).
- Sin variables globales mutables. La configuración se inyecta desde el composition root.
- Cero secretos en disco. Todo por env vars o flags (RNF-04).

### Evitar

- Paquetes globales de estado mutable.
- `interface{}` / `any` salvo en serialización JSON real.
- Ignorar errores con `_ =`. Si se ignora a propósito, comentario explicando por qué.
- Magic numbers sin constante con nombre.
- Dependencias externas cuando stdlib resuelve (JSON Schema, HTTP, logging).

### Concurrencia (RNF-03)

Cuando se paralelice (múltiples archivos, múltiples ítems):

- **No** usar `golang.org/x/sync/errgroup`: el proyecto no tiene dependencias
  externas (INIT.md §1). La cancelación se hace con `context.WithCancel` y la
  agregación de errores con `sync.WaitGroup`, como en
  `adapters.GitHubGraphQL.PublishBacklog`.
- Limitar la concurrencia con un **semáforo** (`chan struct{}` con capacidad),
  nunca con un `go` por item: veinte mutaciones simultáneas contra GitHub son
  la vía rápida a un 403 por rate limit. El límite actual es 4.
- **Nunca** crear goroutine sin `defer wg.Done()`.
- El primer error debe cancelar el contexto de los demás: quien lanza el
  `cancel` es quien recibe el primer fallo.

---

## 7. Comandos del proyecto

```bash
make help          # lista de targets
make build         # compila bin/talkaboutthis (estático, con -trimpath -ldflags "-s -w")
make run FILE=...  # ejecución en dry-run
make test          # go test ./...
make cover         # cobertura -> coverage.html
make check         # fmt + vet + test  <-- puerta de calidad antes de dar cualquier tarea por terminada
make lint          # golangci-lint si está disponible, si no cae a go vet
make schema-check  # valida el JSON Schema
```

### Regla de oro del agente

> **Una tarea no está terminada hasta que `make check` pasa en verde.**
> Si no puedes ejecutar los comandos, dilo explícitamente en tu respuesta de cierre. No declares éxito sin verificar.

---

## 8. Matriz de evaluación (TC-01 … TC-07)

Cada cambio debe poder asociarse a al menos un caso de esta matriz. Al añadir funcionalidad, considera añadir su TC.

| Test | Categoría | Criterio de éxito en Go |
| --- | --- | --- |
| **TC-01** | Ingesta DOCX | Retorno de `string` UTF-8 limpio, sin panics ni fuga de descriptores |
| **TC-02** | Adapter Ollama | `MeetingBacklogExtraction` deserializada sin error de `json.Unmarshal` |
| **TC-03** | Retry Loop | Reintento con contexto de error; `context.WithTimeout` respetado |
| **TC-04** | Identity Map | `"Omar Hernández"` → `@omarhernan`; mapeo exacto e insensible a mayúsculas |
| **TC-05** | Dry-Run | Salida en stdout, **cero** llamadas de red hacia la API de GitHub |
| **TC-06** | GitHub Project v2 | Mutación GraphQL: crear Issue y agregarlo al tablero; devuelve ID |
| **TC-07** | Extensibilidad | `JiraAdapter` compila **sin modificar** `internal/domain` |

### Cómo testear sin red

- **No** escribas tests que llamen a APIs reales. Usar `httptest.NewServer` para simular Ollama y la API de GitHub.
- Fixtures en `testdata/`.
- Inyectar dependencias por constructor para poder sustituir con dobles de prueba.

---

## 9. Configuración y secretos (RNF-04)

| Variable | Para qué |
| --- | --- |
| `LLM_PROVIDER` | `ollama` \| `openai` \| `anthropic` \| `gemini` |
| `OLLAMA_BASE_URL`, `OLLAMA_MODEL` | Motor local |
| `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `GEMINI_API_KEY` | APIs SaaS |
| `GITHUB_TOKEN`, `GITHUB_OWNER`, `GITHUB_REPO`, `GITHUB_PROJECT_ID` | GitHub Projects v2 |
| `ASSIGNEE_POLICY` | Política ante responsables no mapeados: `fail` (por defecto), `assign_unassigned`, `skip` |
| `LOG_LEVEL`, `LOG_FORMAT`, `REQUEST_TIMEOUT` | Observabilidad |

**Precedencia: el flag gana siempre al entorno.** El entorno solo rellena lo que
el usuario no escribió en la línea de comandos. Para decidirlo no se compara el
valor con el de por defecto (con `--log-format text` no hay forma de saber si el
usuario lo escribió), sino con `flag.FlagSet.Visit`, que solo recorre las
banderas presentes. No inventes atajos: comparar con el default es un bug
silencioso.

- Plantilla en [`.env.example`](./.env.example). El `.env` real está en `.gitignore`.
- El PAT de GitHub requiere scope `project`.
- **Nunca** imprimas un token en logs, ni siquiera en nivel `debug`.

---

## 10. Checklist antes de dar por cerrada una tarea

- [ ] ¿El código nuevo respeta la dirección de imports de la §1?
- [ ] ¿Se añadió el caso a `internal/domain`, o se resolvió con un adaptador nuevo sin tocar el dominio? (TC-07)
- [ ] ¿El schema JSON se actualizó junto a los structs? (RNF-02)
- [ ] ¿Los errores usan `%w` y se envuelven con contexto?
- [ ] ¿Toda llamada de red tiene timeout explícito?
- [ ] ¿`--dry-run` sigue siendo 100% offline?
- [ ] ¿Hay tests para el caso nuevo de la matriz TC?
- [ ] ¿`make check` pasa en verde?
- [ ] ¿El dry-run sigue sin tocar `IdentityMapper` ni el adaptador de tablero?
- [ ] ¿Si tocaste la precedencia flag/entorno, hay test para los dos lados?
- [ ] ¿Ningún secreto quedó en el diff?

---

## 11. Reglas de contribución

- Commits pequeños y con un propósito. Mensaje en imperativo, explicando el *porqué*.
- Un commit = un hito o una parte coherente de un hito.
- Nada de dependencias masivas en runtime: el objetivo es un binario estático único (INIT.md §1).
- Si una decisión de diseño se aparta de `INIT.md`, **documenta el porqué** en `docs/` y actualiza `INIT.md` antes que dejarlo implícito.