# Arquitectura de TalkAboutThis

Este directorio describe **cómo está construido el sistema**: qué hace cada
capa, qué depende de qué, cómo se modela el dominio y cómo fluye una ejecución
de principio a fin.

## Índice

| Documento | Contenido |
|---|---|
| [capas.md](capas.md) | Las cuatro capas y las reglas de dependencia |
| [domain.md](domain.md) | El núcleo: modelos, invariantes y puertos |
| [application.md](application.md) | Casos de uso y orquestación |
| [infrastructure.md](infrastructure.md) | Adaptadores concretos |
| [cmd.md](cmd.md) | La CLI, el *composition root* |
| [flujos/00-vision-general.md](flujos/00-vision-general.md) | El recorrido completo, de la invocación al código de salida |
| [flujos/01-ingesta.md](flujos/01-ingesta.md) | Lectura y conversión de la transcripción |
| [flujos/02-extraccion.md](flujos/02-extraccion.md) | Retry loop con autocorrección |
| [flujos/03-identidades.md](flujos/03-identidades.md) | De «Omar Hernández» a `@omarhernan` |
| [flujos/04-publicacion.md](flujos/04-publicacion.md) | Mutations GraphQL y concurrencia |
| [flujos/05-errores-y-codigos.md](flujos/05-errores-y-codigos.md) | Traducción de fallo a código de salida |

## Vista de contexto

```mermaid
flowchart TB
    USER["👤 Persona<br/>prepara su.minuta.md"]

    subgraph CLI["Binario talkaboutthis"]
        direction TB
        ARG["Argumentos y entorno"]
        CFG["Configuración<br/><i>completion + validación</i>"]
        OUT["Presentación<br/><i>JSON o tabla</i>"]
    end

    subgraph NUCLEO["Núcleo sin conocimiento del exterior"]
        direction TB
        ING["Ingesta"]
        EXT["Extracción"]
        IDM["Identidades"]
        PUB["Publicación"]
    end

    subgraph ADAPTADORES["Adaptadores intercambiables"]
        direction LR
        PAR["Parsers<br/>md · txt · docx"]
        LLM["LLM<br/>ollama · openai<br/>anthropic · gemini"]
        MAP["Mapper de identidades"]
        BRD["Tableros<br/>GraphQL · CLI"]
    end

    USER -->|"--file"| ARG
    ARG --> CFG
    CFG --> ING
    ING --> EXT --> IDM --> PUB
    OUT <--> CFG

    ING --> PAR
    EXT --> LLM
    IDM --> MAP
    PUB --> BRD

    classDef nucleo fill:#d1ecf1,stroke:#17a2b8,stroke-width:2px
    classDef adapt fill:#fff3cd,stroke:#ffc107
    classDef borde fill:#e2e3e5,stroke:#6c757d

    class ING,EXT,IDM,PUB nucleo
    class PAR,LLM,MAP,BRD adapt
    class ARG,CFG,OUT borde
```

## La idea en una frase

> El **núcleo no sabe qué es un archivo, ni un modelo de lenguaje, ni GitHub.**
> Solo sabe qué es una transcripción, un backlog y una publicación.

Todo lo demás son adaptadores que se enchufan desde un único sitio.

## Las cuatro capas

```mermaid
flowchart TB
    subgraph L4["4 · cmd/talkaboutthis"]
        A4["Composition root<br/><i>el único que conoce las 4 capas</i>"]
    end

    subgraph L3["3 · internal/infrastructure"]
        A3["Parsers · LLM · Identity · Adapters<br/><i>implementan los puertos</i>"]
    end

    subgraph L2["2 · internal/application"]
        A2["Casos de uso<br/><i>orquesta, no sabe CÓMO</i>"]
    end

    subgraph L1["1 · internal/domain"]
        A1["Modelos, invariantes y puertos<br/><i>depende solo de la stdlib</i>"]
    end

    L4 -->|"construye"| L3
    L3 -->|"implementa"| L1
    L2 -->|"usa"| L1

    L2 -.->|"PROHIBIDO"| L3
    L3 -.->|"PROHIBIDO"| L2
    L1 -.->|"PROHIBIDO"| L2
    L1 -.->|"PROHIBIDO"| L3

    classDef ok fill:#d4edda,stroke:#28a745
    classDef mal fill:#f8d7da,stroke:#dc3545,stroke-dasharray: 5 5
    class L4,L3,L2,L1 ok
```

Las flechas punteadas son **prohibiciones**, y no son convenciones: están
vigiladas por tests que fallan si alguien las cruza.

| Prohibición | Test que la vigila |
|---|---|
| `domain` → `infrastructure`, `application`, `cmd`, `pkg/sdk` | `internal/domain/architecture_test.go` |
| `domain` → `net/http`, `net/url`, `os/exec` | `internal/domain/architecture_test.go` |
| `domain` → dependencias de terceros | `internal/domain/architecture_test.go` |
| `pkg/sdk` → `internal/` | `pkg/sdk/frontera_test.go` |
| `internal/` → `pkg/sdk` | `pkg/sdk/frontera_test.go` |

## Cero dependencias externas

`go.mod` no tiene un solo `require`. Todo sale de la biblioteca estándar:

| Necesidad | En vez de | Solución |
|---|---|---|
| Leer `.docx` | `unioffice`, `docx` | `archive/zip` + `encoding/xml` |
| Validar JSON Schema | `gojsonschema` | `internal/infrastructure/jsonschema` |
| Cliente HTTP con reintentos | `resty`, `retryablehttp` | `internal/infrastructure/llm/client.go` |
| GraphQL | `shurcooL/graphql` | Peticiones `POST` a mano sobre `net/http` |
| Logging | `zap`, `zerolog` | `log/slog` |

Motivo y consecuencias en [ADR-0002](../adr/0002-cero-dependencias-externas.md).

## Invariantes del dominio

El núcleo garantiza, y sus tests comprueban (`100 %` de cobertura):

1. Una `Priority` solo puede ser `HIGH`, `MEDIUM` o `LOW`.
2. Un `ActionItem` tiene título, descripción y responsable no vacíos.
3. Un `Transcript` vacío es un error: se detecta **antes** de gastar una
   llamada al LLM.
4. El esquema JSON y los structs Go no pueden divergir: un test los compara.
5. Los identificadores de responsable se guardan **sin `@`**.

## Documentos relacionados

- [`INIT.md`](../../INIT.md) — especificación original (RF, RNF, TC). Fuente de verdad.
- [`AGENTS.md`](../../AGENTS.md) — contrato operativo para agentes.
- [Vocabulario y glosario](domain.md#vocabulario) — los términos del dominio.
- [Trazabilidad requisitos ↔ tests](../specs/07-trazabilidad.md) — qué cubre qué.