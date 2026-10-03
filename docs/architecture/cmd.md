# `cmd/talkaboutthis` — la CLI

**Composition root.** El único sitio del programa que conoce las cuatro capas a
la vez. Aquí se decide qué adaptador se usa y con qué valores.

**Cobertura: 92,3 %.**

## Por qué existe esta capa

Sin un composition root, cada caso de uso tendría que crear sus propias
dependencias y el flujo no existiría como algo ejecutable y testeable. Aquí:

```mermaid
flowchart LR
    subgraph main_["main.go"]
        A["main()<br/>os.Exit(ejecutar(...))"]
        B["ejecutar()<br/>dispatcher de subcomandos"]
        C["ejecutarIngest()<br/><i>todo el flujo</i>"]
        D["construirPipeline()<br/>construirProveedor()<br/>construirTablero()"]
        E["reportar()<br/>codigoDeEtapa()<br/>codigoDeError()"]
    end

    subgraph cfg_["config.go"]
        F["registrarFlags()<br/>registrarFlagsEnviadas()"]
        G["completar()<br/><i>entorno → formato → modo → rutas → schema</i>"]
        H["aplicarEntorno()<br/><i>flag primero</i>"]
        I["validar()<br/>cargarMappings()<br/>timeoutEfectivo()"]
    end

    subgraph out_["output.go"]
        J["renderizar()<br/>construirSalidaJSON()<br/>renderizarTabla()"]
    end

    A --> B --> C
    C --> D
    C --> E
    C --> F --> G
    G --> H --> I
    C --> J

    classDef raiz fill:#d4edda,stroke:#28a745
    class A,B,C,D,E,F,G,H,I,J raiz
```

Los tres ficheros tienen una responsabilidad cada uno, y ninguno solapa:

| Fichero | Responsabilidad |
|---|---|
| `main.go` | Dispatcher, construcción del grafo, códigos de salida |
| `config.go` | Banderas, entorno, validación, valores por defecto |
| `output.go` | Presentación: JSON o tabla |

## El ciclo de `ejecutarIngest`

```mermaid
sequenceDiagram
    participant U as Usuario
    participant M as main
    participant CFG as config.go
    participant L as logging
    participant P as Pipeline
    participant O as output.go

    U->>M: ingest --file notas.md --provider ollama
    M->>CFG: registrarFlags(fs, &cfg)
    Note over CFG: "registrarFlagsEnviadas ANTES de completar()"
    M->>CFG: fs.Parse(os.Args[2:])

    CFG->>CFG: aplicarEntorno() "flag gana"
    CFG->>CFG: completar()
    CFG->>CFG: validar()
    CFG->>CFG: cargarMappings(ruta, modo)
    CFG->>CFG: timeoutEfectivo() "suelo 5s"
    CFG-->>M: cfg

    M->>L: Nuevo(...)
    L-->>M: logger
    M->>M: ctx = ConLoggerEnContexto(ctx, logger)

    M->>M: construirPipeline(cfg)
    M->>P: Ejecutar(ctx, cfg)

    alt éxito
        P-->>M: resultado
        M->>O: renderizar(stdout, resultado, formato)
        M-->>U: salida + código 0
    else fallo
        P-->>M: error con etapa
        M->>M: reportar(stderr, err)
        M-->>U: mensaje + código 3/4/5/6
    end
```

El orden de `ejecutarIngest` no es arbitrario, y dos de sus pasos parecen
invertidos hasta que se entiende por qué.

### `registrarFlagsEnviadas` antes de `completar()`

`completar()` rellena los campos vacíos con valores por defecto y del entorno.
Si se registrara qué banderas se enviaron **después**, ya no habría forma de
saber si `--log-format text` lo puso el usuario o el default.

```mermaid
flowchart LR
    subgraph mal["Si se hiciera después"]
        M1["Parse"] --> M2["completar()<br/>rellena con default"] --> M3["registrarFlagsEnviadas()<br/>¿--log-format? NO, ya es 'text'"]
        M3 --> M4["❌ el flag nunca puede ganar"]
    end

    subgraph bien["Lo que hay"]
        B1["Parse"] --> B2["registrarFlagsEnviadas()<br/>flag.Visit → envía"] --> B3["completar()<br/>solo rellena vacíos"]
        B3 --> B4["✅ --log-format=json gana"]
    end

    classDef mal_ fill:#f8d7da,stroke:#dc3545
    classDef bien_ fill:#d4edda,stroke:#28a745
    class M1,M2,M3,M4 mal_
    class B1,B2,B3,B4 bien_
```

El global `envios map[string]bool` se reinicia en cada invocación de
`ejecutarIngest`, porque `main()` es una función de biblioteca desde el punto de
vista de los tests.

## Precedencia flag → entorno → default

```mermaid
flowchart TD
    START["Un valor, tres posibles orígenes"] --> FLAG{"¿se envió<br/>el flag?"}
    FLAG -->|sí| F["usar el flag<br/><b>gana siempre</b>"]
    FLAG -->|no| ENV{"¿hay variable<br/>de entorno?"}
    ENV -->|sí| E["usar la variable"]
    ENV -->|no| D["usar el default"]

    F --> RESULT["valor final"]
    E --> RESULT
    D --> RESULT

    classDef gana fill:#d4edda,stroke:#28a745,stroke-width:2px
    class F gana
```

> **Bug real, encontrado al cerrar el proyecto.** La implementación era
> `primeroNoVacio(os.Getenv(...), cfg.Campo)`: el entorno se consultaba
> **primero**. Un `GITHUB_OWNER` exportado en la sesión de trabajo pisaba un
> `--owner` explícito, y el usuario que passeran horas arreglando un token sin
> descubrir por qué se ignoraba su bandera.

## Variables de entorno

| Variable | Flag equivalente |
|---|---|
| `LLM_PROVIDER` | `--provider` |
| `OLLAMA_BASE_URL`, `OLLAMA_MODEL` | — (configuración del motor) |
| `OPENAI_API_KEY`, `OPENAI_BASE_URL`, `OPENAI_MODEL` | — |
| `ANTHROPIC_API_KEY`, `ANTHROPIC_MODEL` | — |
| `GEMINI_API_KEY`, `GEMINI_MODEL` | — |
| `GITHUB_TOKEN` | — (credencial) |
| `GITHUB_OWNER` | `--owner` |
| `GITHUB_REPO` | `--repo` |
| `GITHUB_PROJECT_ID` | `--project` |
| `ASSIGNEE_POLICY` | `--assignee-policy` |
| `REQUEST_TIMEOUT` | `--timeout` |
| `LOG_LEVEL` | `--log-level` |
| `LOG_FORMAT` | `--log-format` |

> **Bug real.** `LOG_LEVEL` y `LOG_FORMAT` estaban documentados en
> `.env.example` desde el principio y **nunca se leían**. Documentar una variable
> que el programa ignora es peor que no documentarla: el usuario la ajusta, no
> ocurre nada, y pierde la confianza en todo lo demás.

## Modo: `--dry-run` y `--publish`

```mermaid
stateDiagram-v2
    [*] --> SinModo

    state SinModo {
        [*] --> DryRun : "modo por defecto"
    }

    SinModo --> DryRun : --dry-run
    SinModo --> Publish : --publish
    SinModo --> Rechazado : --dry-run --publish

    DryRun --> [*] : código 0
    Publish --> [*] : código 0 o 6

    Rechazado --> [*] : código 1 (error de uso)

    note right of DryRun
        CERO llamadas de red.
        Es el default: la
        forma más segura de
        probar el comando
        nuevo es no hacer
        nada.
    end note
```

`--dry-run` es el default **por diseño**: la primera ejecución de un comando
nuevo contra un tablero real no debería poder crear veinte issues.

## `cargarMappings(ruta, modo)` — la tolerancia del dry-run

```mermaid
flowchart TD
    C["cargarMappings(ruta, modo)"] --> EX{"existe el fichero?"}
    EX -->|sí| LOAD["NuevoJSONIdentityMapper(ruta)"]
    EX -->|no| M{"modo"}

    M -->|dry-run| EMPTY["identity.MapperVacio()"]
    M -->|publish| ERR["error<br/>código 2"]

    LOAD --> J{"válido?"}
    J -->|no| M
    J -->|sí| OK["mapper con aliases"]

    EMPTY --> OK2["el dry-run avanza"]
    ERR --> FIN["aborta antes de publicar"]

    classDef bien fill:#d4edda,stroke:#28a745
    classDef mal fill:#f8d7da,stroke:#dc3545
    class OK,OK2 bien
    class ERR mal
```

> **Bug real.** La tabla de mapeo era **obligatoria también en dry-run**, y el
> error salía con **código 5 (identidad)** cuando el problema era de
> configuración. Dos fallos en uno: una dependencia inexistente en el camino que
> promete cero dependencias, y un código de salida que mentía sobre la causa.

La clave es que el error distingue dos situations que antes compartían ruta:

- **Falta el fichero en dry-run** → `MapperVacio()`, se sigue.
- **Falta el fichero en publish** → error, código 2. Sin tabla, cualquier
  nombre quedaría sin responsable.

## Códigos de salida

```mermaid
flowchart LR
    E1["1 · uso<br/><i>flag contradictorio,<br/>--file ausente</i>"]
    E2["2 · configuración<br/><i>credencial o tabla<br/>incompleta</i>"]
    E3["3 · ingesta<br/><i>archivo ilegible,<br/>formato desconocido</i>"]
    E4["4 · extracción<br/><i>3 intentos agotados</i>"]
    E5["5 · identidad<br/><i>responsable sin<br/>resolver (fail)</i>"]
    E6["6 · publicación<br/><i>fallo de la<br/>plataforma destino</i>"]
    E0["0 · éxito"]
    E130["130 · interrumpido<br/><i>128 + SIGINT</i>"]

    classDef ok fill:#d4edda,stroke:#28a745
    class E0 ok
```

Son distintos a propósito. Un proceso de CI tiene que poder responder:

```mermaid
flowchart TD
    CODE{"código de salida"} --> C6{"¿6?"}
    C6 -->|sí| A["reintentar: es la plataforma,<br/>quizá un rate limit"]
    C6 -->|no| C4{"¿4?"}
    C4 -->|sí| B["reintentar con otro modelo:<br/>el LLM no colaboró"]
    C4 -->|no| C3{"¿3?"}
    C3 -->|sí| D["revisar el ARCHIVO:<br/>cambiarlo no arregla un LLM"]
    C3 -->|no| E["revisar la configuración"]
```

Cada código apunta a una categoría de causa distinta. Un único código de error
—el diseño habitual— obligaría a leer el mensaje para decidir, y en un log de CI
el mensaje es la primera cosa que se pierde.

Detalle completo en [flujo de errores](flujos/05-errores-y-codigos.md).

## `output.go` — la presentación

```mermaid
flowchart TD
    R["renderizar(w, resultado, formato)"] --> F{"--output"}
    F -->|json| J["construirSalidaJSON()<br/>→ json.MarshalIndent"]
    F -->|table| T["renderizarTabla()"]
    F -->|otro| ERR["error de uso (código 1)"]

    J --> E1["campos errores y publicacion"]
    T --> E2["columnas:<br/>PRIORIDAD · TÍTULO · RESPONSABLE"]

    classDef err fill:#f8d7da,stroke:#dc3545
    class ERR err
```

La salida JSON es una estructura **estable**, pensada para que un script la
consuma: `job_id`, `modo`, `duracion_ms`, `etapas`, `archivo`, `resumen_reunion`,
`items[]`, `despacho`.

Tres cosas que se arreglaron aquí:

| Antes | Ahora | Por qué |
|---|---|---|
| `errores` a `null` | Siempre `[]` | En JSON, `null` y `[]` no son lo mismo y un consumidor riguroso falla con `null` |
| `plataforma` nunca rellenada | Se propaga desde `ResumenDespacho` | El campo estaba declarado y vacío: la salida no decía **dónde** se publicó |
| `modoDe(nil)` hacía panic | Comprueba `nil` | Un fallo de renderizado tumba el proceso **después** de haber hecho el trabajo bien |

### La tabla, y por qué no marca en dry-run

```mermaid
flowchart TD
    H["renderizarTabla()"] --> ROW{"por cada item"}
    ROW --> P["prioridadOrdenada()<br/>ALTA / MEDIA / BAJA"]
    ROW --> R{"responsableLegible()"}

    R -->|"MappedHandle vacío"| M{"¿dry-run?"}
    M -->|sí| N["solo el nombre<br/><b>sin marca</b>"]
    M -->|no| U["nombre + (SIN RESOLVER)"]

    R -->|con handle| H2["el handle"]

    N --> SAL["fila"]
    U --> SAL
    H2 --> SAL

    classDef ok fill:#d4edda,stroke:#28a745
    classDef aviso fill:#fff3cd,stroke:#ffc107
    class N ok
    class U aviso
```

> **Bug real.** En dry-run **no** se consultan identidades (es la garantía de
> TC-05), así que el handle siempre está vacío. Marcando «SIN RESOLVER» en cada
> fila, la tabla levantaba una alarma que no correspondía: en dry-run no se
> intentó resolver nada, y el usuario ya sabe que no se publicó nada. Era ruido
> que enseñaba a ignorar la marca donde sí importa.

## Ficheros

| Fichero | Líneas | Contenido |
|---|---|---|
| `main.go` | ~430 | Dispatcher, construcción, códigos de salida, uso |
| `config.go` | ~400 | Banderas, entorno, validación, defaults |
| `output.go` | ~340 | Renderizado JSON y tabla, formateadores |
| `main_test.go` | | Comportamiento de la CLI por flags |
| `e2e_test.go` | | Flujo completo contra un servidor Ollama falso |
| `output_test.go` | | Formateadores, códigos de etapa, ayuda |

## Relacionado

- [Flujo completo](flujos/00-vision-general.md)
- [ADR-0007 — Precedencia flag sobre entorno](../adr/0007-precedencia-flag-sobre-entorno.md)
- [Errores y códigos de salida](flujos/05-errores-y-codigos.md)