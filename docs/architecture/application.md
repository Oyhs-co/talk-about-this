# `internal/application` — los casos de uso

La capa que **sabe el QUÉ** y no sabe el CÓMO. Orquesta; no implementa.

**Cobertura: 96,4 %.**

## Los tres casos de uso y el pipeline

```mermaid
flowchart TB
    subgraph cli["cmd/talkaboutthis"]
        RUN["ejecutarIngest()"]
    end

    subgraph app["internal/application"]
        PIPE["Pipeline<br/><i>orquesta las tres etapas</i>"]

        ING["IngestTranscriptor<br/><i>DesdeRuta · DesdeReader</i>"]
        EXT["ExtractBacklog<br/><i>3 intentos + autocorrección</i>"]
        PUB["PublicarBacklog<br/><i>identidades + tablero</i>"]

        CTX["contexto.go<br/><i>logger en el contexto</i>"]
    end

    RUN --> PIPE
    PIPE --> ING
    PIPE --> EXT
    PIPE --> PUB

    PIPE -.-> CTX
    ING -.-> CTX
    EXT -.-> CTX
    PUB -.-> CTX

    classDef usecase fill:#d1ecf1,stroke:#17a2b8
    classDef orquestador fill:#d4edda,stroke:#28a745
    class PIPE orquestador
    class ING,EXT,PUB usecase
```

`Pipeline.Ejecutar` es el **único** sitio donde importa los tres casos de uso.
Por eso el orden de las etapas y el punto de fallo de cada una se prueban sin
lanzar un proceso, y por eso `cmd/` no tiene lógica de orquestación que probar.

## `Pipeline`

```go
type PipelineEtapa string

const (
    EtapaIngesta    PipelineEtapa = "ingesta"
    EtapaExtraccion PipelineEtapa = "extraccion"
    EtapaDespacho   PipelineEtapa = "despacho"
)
```

### Máquina de estados

```mermaid
stateDiagram-v2
    [*] --> Inicio

    Inicio --> Ingesta: Ejecutar(ctx, cfg)

    Ingesta --> Extraccion: transcripción válida
    Ingesta --> Fallo: archivo ilegible / formato desconocido / vacío

    Extraccion --> Identidades: backlog válido
    Extraccion --> Fallo: 3 intentos agotados

    Identidades --> Despacho: modo publish
    Identidades --> FinDryRun: modo dry-run

    Despacho --> FinPublish: tarjetas creadas (con o sin fallos parciales)
    Despacho --> Fallo: error de la plataforma

    Fallo --> [*]: code 3 / 4 / 5 / 6
    FinDryRun --> [*]: code 0
    FinPublish --> [*]: code 0

    note right of Identidades
        En dry-run NO se consultan
        identidades: es la garantía
        de TC-05 (cero red).
    end note
```

`EtapasCompletadas()` devuelve las etapas que se pasaron con éxito, y es lo que
la salida JSON reporta como `etapas`. Con eso, un fallo en la tercera etapa
distingue "no leyó el archivo" de "el LLM no collaboró".

### `ErrorEtapa`

```go
type ErrorEtapa struct {
    Etapa PipelineEtapa
    Err   error
}

func (e *ErrorEtapa) Unwrap() error { return e.Err }
```

`Unwrap` es lo que hace útil el tipo. Sin él, `errors.Is(err,
domain.ErrPublicacionFallida)` sería `false` dentro del envoltorio, y la CLI no
podría distinguir "el tablero devolvió 422" de "el token caducó".

Dos funciones de consulta, usadas por la CLI para elegir el código de salida:

```go
func EsFalloDePublicacion(err error) bool
func EtapaDelError(err error) PipelineEtapa
```

Ambas toleran `nil` y errores ajenos: devuelven `false` y `""` respectivamente.
La CLI las llama con errores de configuración, que no llevan etapa.

## `IngestTranscriptor`

Dos puntos de entrada, porque son problemas distintos:

| Método | Quién es dueño del archivo | Contexto |
|---|---|---|
| `DesdeRuta(ctx, ruta)` | El caso de uso: `defer f.Close()` | Ruta real |
| `DesdeReader(ctx, r)` | El llamante | Cualquier fuente |

`DesdeRuta` cierra el archivo **inmediatamente** después de abrirlo, no al
terminar el proceso. Procesar cientos de documentos sin cerrarlos agota los
descriptores del proceso, y el fallo aparece en un archivo *distinto* al que lo
provocó.

### Detección: extensión primero, contenido después

```mermaid
flowchart TD
    IN["DesdeRuta(ctx, ruta)"] --> STAT{"os.Stat"}
    STAT -->|error| E1["Err de lectura → etapa ingesta"]
    STAT -->|ok| EX["ExtensionDe(ruta)"]

    EX --> REG{"ParserParaExtension(ext)"}
    REG -->|"encontrado"| USE["usa ese parser"]
    REG -->|"no encontrado"| CAB["abre y lee cabecera"]

    CAB --> DET{"DetectarPorContenido"}
    DET -->|"reconocido"| USE
    DET -->|"nada (nil, nil)"| E2["se devuelve el error<br/>de extensión"]

    E1 --> FIN["ErrorEtapa{ingesta}"]
    E2 --> FIN

    classDef err fill:#f8d7da,stroke:#dc3545
    class E1,E2,FIN err
```

El orden importa: **la extensión válida gana**. Si se invirtiera y la detección
por contenido se ejecutara siempre primero, un `.txt` que empiece por
`PK\x03\x04` se interpretaría como `.docx`. La detección por contenido existe
para cuando la extensión **miente**, no para competir con ella.

`DetectarPorContenido` devuelve `(nil, nil)` cuando nada encaja, y eso **no** es
un error: significa "prueba con la extensión". El error real es "la extensión no
la conozco tampoco".

## `ExtractBacklog`

El corazón del retry loop con autocorrección.

```mermaid
flowchart TD
    S["Ejecutar(ctx, transcript)"] --> C{"ctx cancelado?"}
    C -->|sí| E0["return ctx.Err()"]
    C -->|no| I1["intento 1"]

    I1 --> P1["prompt = Construir(transcript, schema)"]
    P1 --> LLM["GenerateStructuredOutput"]
    LLM -->|"error"| PROP["propaga: reintentar<br/>no lo arregla el prompt"]
    LLM -->|ok| V["validarRespuesta(crudo)"]

    V --> VAL{"sin errores?"}
    VAL -->|sí| OK["return extracción"]
    VAL -->|no| ACC["acumular errores"]

    ACC --> ESP{"intentos agotados?"}
    ESP -->|no| P2["intento n+1:<br/>ConstruirCorreccion(transcript, schema, errores)"]
    P2 --> ESP2{"dormir 400 ms<br/>respetando ctx"}
    ESP2 --> LLM
    ESP -->|sí| AGOT["ErrReintentosAgotados<br/>+ último error concreto"]

    classDef ok fill:#d4edda,stroke:#28a745
    classDef err fill:#f8d7da,stroke:#dc3545
    class OK ok
    class E0,PROP,AGOT err
```

### Tres reglas de este bucle

**1. Un error del proveedor no se reintenta.** Si la red cayó o la credencial es
inválida, repetir la misma llamada dará el mismo error. Se propaga de inmediato y
lo reintenta la política de red del propio cliente HTTP, que sí sabe distinguir
un 503 de un 401.

**2. Los errores se acumulan, no se sustituyen.** Un reintento puede corregir
varios problemas de golpe, así que el prompt lleva la lista completa de lo que
falló, no solo el último.

**3. La espera entre intentos no es decorativa.** La mayoría de los fallos de
formato son transitorios: el mismo prompt con el error adjunto produce una
respuesta distinta en el siguiente muestreo. Reiniciar el sampling **es** parte
del mecanismo de corrección.

```mermaid
sequenceDiagram
    participant U as Usuario
    participant E as ExtractBacklog
    participant M as Modelo
    participant V as Validador

    U->>E: extrae("Acta de la reunión...")
    E->>M: prompt con schema
    M-->>E: {"action_items": [...]}<br/>sin meeting_summary
    E->>V: validar
    V-->>E: ["falta meeting_summary"]
    E->>E: dormir 400 ms
    E->>M: prompt + ["falta meeting_summary"]
    M-->>E: {"meeting_summary": "...", "action_items": [...]}
    E->>V: validar
    V-->>E: sin errores
    E-->>U: extracción válida
```

### Las tres capas de validación

| Capa | Qué hace | Qué detecta |
|---|---|---|
| 1 | `jsonschema.Validar` | Tipos, campos obligatorios, longitudes, enumeraciones |
| 2 | `normalizarEnums` | `"high"` en vez de `"HIGH"` — el LLM no siempre recuerda las mayúsculas |
| 3 | `json.Unmarshal` al dominio | Invariantes de Go que el schema no puede expresar |

**La normalización es una decisión deliberada.** Solo actúa sobre lo que el
schema declara como `enum`, y solo convierte mayúsculas/minúsculas. Un
`"urgente"` se sigue rechazando: inventar valores sería hacer que el sistema
aceptase prioridades que nadie pidió.

El motivo es concreto y frecuente: el error más común de un LLM con salida
estructurada es el detalle de las mayúsculas. Gastar los tres reintentos en eso
es absurdo.

> Hay un segundo bug escondido aquí: la capa 3 deserializaba del documento
> **crudo**, no del ya normalizado, de modo que normalizar no arreglaba nada. Se
> descubrió al arreglar el primero.

## `PublicarBacklog`

```mermaid
flowchart TD
    S["Ejecutar(ctx, projectRef, items, modo)"] --> M{"modo"}

    M -->|dry-run| DRY["return items SIN tocar<br/>ni identidades ni tablero"]
    M -->|otro| CFG{"modo conocido?"}
    CFG -->|no| ERR["ErrConfigInvalida"]
    CFG -->|sí publish| RES["resolverIdentidades"]

    RES --> POL{"política por item"}
    POL -->|fail| F1["error si alguien no resuelve"]
    POL -->|assign_unassigned| U1["item sin responsable, sigue"]
    POL -->|skip| SK["item a omitidos"]
    U1 --> PREP["preparados"]

    PREP --> VAC{"preparados vacío?"}
    VAC -->|sí| RES0["resumen con omitidos, sin publicar"]
    VAC -->|no| PUB["tablero.PublishBacklog"]

    PUB --> PAR{"resultados parciales"}
    PAR --> R["resumen: éxitos, fallos, omitidos"]

    classDef seco fill:#fff3cd,stroke:#ffc107
    classDef mal fill:#f8d7da,stroke:#dc3545
    class DRY seco
    class ERR,F1 mal
```

### El orden dry-run / publish

```mermaid
sequenceDiagram
    participant P as PublicarBacklog
    participant TAB as ProjectBoardAdapter

    Note over P: "PRIMERA instrucción útil"
    alt modo == dry-run
        P-->>P: return items tal cual
        Note over P,TAB: "nunca se llama al tablero"
    else modo == publish
        P->>P: resolverIdentidades
        P->>TAB: PublishBacklog(...)
    end
```

El modo se comprueba **antes de tocar identidades**. Es la diferencia entre «no
se resuelve nada» y «se resuelve todo y luego no se publica», y la segunda
haría llamadas de red en un modo que promete cero red.

**Cómo se demuestra:** `tableroQueFalla` es un adaptador cuyo `PublishBacklog`
llama a `t.Fatal`. Si el dry-run lo tocara, el test fallaría con la pila dentro
del adaptador. No hace falta contar llamadas de red: basta con que la más
perjudicial de todas tumbe el test.

### Fallo parcial ≠ error

Si se crean tres de cinco tarjetas, `PublicarBacklog` **no devuelve error**:
devuelve un resumen con `éxitos: 3, fallos: 2`. Devolver error haría que el
usuario creyera que no se publicó nada, cuando cinco tarjetas existen.

## `contexto.go` — el logger en el contexto

```mermaid
flowchart LR
    subgraph cli["cmd"]
        A["ConLoggerEnContexto(ctx, logger)"]
    end

    subgraph app["internal/application"]
        B["loggerDe(ctx)"]
        C["hayLoggerEnContexto(ctx)"]
    end

    A --> B
    B --> D["slog con job_id,<br/>etapa, items"]

    A -.->|"si no, el pipeline<br/>no lo enriquece"| E["job_id duplicado<br/>en cada línea"]
```

Tres reglas:

1. **El pipeline no enriquece el logger.** Si lo hiciera, y la CLI ya lo hubiera
   hecho, el `job_id` aparecería dos veces en cada línea.
2. **`ConLoggerEnContexto` sustituye un `ctx` nil por `context.Background()`.**
   `context.WithValue` con padre nil entra en pánico, y lo hacía.
3. **Un fallo de inyección no se propaga.** Si el contexto no lleva logger, se
   usa el global. Perder la correlación de un `job_id` no justifica abortar una
   publicación ya empezada.

## Ficheros

| Fichero | Responsabilidad |
|---|---|
| `pipeline.go` | `Pipeline`, `ErrorEtapa`, consultas de etapa |
| `ingest.go` | Lectura, selección de parser, marcas de tiempo |
| `extract_backlog.go` | Retry loop, validación en tres capas, normalización |
| `publish_backlog.go` | Identidades, política, despacho, garantía de dry-run |
| `contexto.go` | Propagar el logger por el contexto |

## Relacionado

- [Flujo de extracción](flujos/02-extraccion.md)
- [Flujo de publicación](flujos/04-publicacion.md)
- [ADR-0006 — El dry-run se decide en el caso de uso](../adr/0006-dry-run-por-defecto.md)