# Flujo: publicación

**RF-05, RF-06** · **TC-06** · `internal/application/publish_backlog.go` ·
`internal/infrastructure/adapters/`

De `[]ActionItem` a issues en un GitHub Project v2.

## Dos rutas

```mermaid
flowchart TB
    S(["PublicarBacklog(items)"]) --> AD{"--adapter"}

    AD -->|graphql, default| G["GitHubGraphQL"]
    AD -->|cli| C["GitHubCLI"]

    G --> G1["api.github.com/graphql<br/><i>POST con token</i>"]
    C --> C1["os/exec: gh project item-create<br/><i>gh manage la autenticación</i>"]

    G1 --> OK(["issues en el tablero"])
    C1 --> OK

    classDef cli fill:#fff3cd,stroke:#ffc107
    class C,C1 cli
```

| | GraphQL (por defecto) | CLI |
|---|---|---|
| **Autenticación** | `GITHUB_TOKEN` en el entorno | La sesión de `gh auth` |
| **Latencia** | Una llamada HTTP por operación | Un proceso por operación |
| **Control de errores** | Total: se ve el mensaje de la API | Depende de la salida de `gh` |
| **Requiere instalar** | Nada | El binario `gh` |
| **Uso típico** | CI, automatización | Station, donde no se quiere un PAT en el entorno |

La ruta CLI existe por una razón concreta: en muchas máquinas de empresa el
token **no se puede exportar a un entorno**, pero `gh auth login` ya está hecho.

## La garantía de dry-run

Antes de nada, lo más importante del fichero:

```mermaid
flowchart TD
    S["PublicarBacklog.Ejecutar"] --> CHK{"modo == ModoDryRun?"}
    CHK -->|sí| RETURN["return items<br/><b>primera instrucción útil</b>"]
    CHK -->|no| KNOWN{"modo == ModoPublish?"}

    KNOWN -->|no| ERR["ErrConfigInvalida"]
    KNOWN -->|sí| RES["resolverIdentidades()"]

    RETURN --> NADA["ni identidades<br/>ni tablero<br/>ni red"]
    NADA --> ZERO(["cero llamadas de red"])

    classDef ok fill:#d4edda,stroke:#28a745
    classDef err fill:#f8d7da,stroke:#dc3545
    class ZERO ok
    class ERR err
```

El modo se comprueba **antes de tocar identidades**. La diferencia importa:

| Diseño | En dry-run |
|---|---|
| Comprobar al final | Se resuelven 20 identidades y luego se descarta todo |
| **Comprobar al principio** | No se resuelve nada |

El primero hace llamadas de red en un modo que promete cero red, y para un
mapper respaldado por una API sería exactamente eso.

### Cómo se demuestra

```mermaid
sequenceDiagram
    participant T as Test
    participant P as PublicarBacklog
    participant B as tableroQueFalla

    Note over B: "PublishBacklog llama a<br/>t.Fatal() si se invoca"
    T->>P: Ejecutar(ctx, ref, items, dry-run)
    P-->>T: items sin tocar
    Note over T,B: "el test pasa ⇔ el tablero no se invocó"
```

No hace falta contar llamadas de red: basta con que la más perjudicial de todas
—un adaptador que entra en panic— tumbe el test si se toca.

## Las operaciones GraphQL

```mermaid
sequenceDiagram
    autonumber
    participant P as PublicarBacklog
    participant G as GitHubGraphQL
    participant C as cacheIDs
    participant API as api.github.com

    Note over G,C: "resolverNodeID usa lock por clave"

    P->>G: PublishBacklog(ctx, projectRef, items)
    G->>C: resolverNodeID("repositorio:org/repo")
    alt ya cacheado
        C-->>G: R_kgDO
    else primera vez
        C->>API: query repository { id }
        API-->>C: R_kgDO
        C-->>G: R_kgDO (cacheado)
    end

    G->>C: resolverNodeID("tablero:" + projectRef)
    alt projectRef empieza por PVT_
        C-->>G: projectRef (sin llamada)
    else es un número
        C->>API: query organization { projectV2 { id } }
        API-->>C: PVT_kwDO...
        C-->>G: PVT_kwDO... (cacheado)
    end

    Note over G: "AHORA, y solo si modo publish"

    loop por cada item (máx. 4 en paralelo)
        G->>API: createIssue(input: {...})
        API-->>G: { id, url }
        G->>API: addIssueToProjectV2ItemById
        API-->>G: { item { id } }
        G->>API: addAssigneesToAssignable
        API-->>G: { issue { id } }
    end

    G-->>P: []PublishResult
```

### `projectRef` acepta las dos formas

```mermaid
flowchart LR
    subgraph a["Node ID"]
        A1["PVT_kwDOABCDEF"] --> A2["se usa tal cual<br/><b>sin llamada a la API</b>"]
    end

    subgraph b["Número de tablero"]
        B1["7"] --> B2["consultar la API<br/>→ PVT_kwDOABCDEF"]
    end

    subgraph c["Inválido"]
        C1["«abc»"] --> C2["error: 'no es un node_id<br/>ni un número válido'"]
    end

    classDef ok fill:#d4edda,stroke:#28a745
    classDef err fill:#f8d7da,stroke:#dc3545
    class A2,B2 ok
    class C2 err
```

Aceptar las dos evita que el usuario tenga que descubrir un identificador opaco
a mano. Y si ya lo tiene, no se desperdicia una consulta.

### El `400` silencioso

> La API de GitHub responde con **`200` y datos vacíos** cuando el tablero no
> existe o no es visible para el token. No es un error HTTP: es un `data:
> {organization: {projectV2: null}}`.
>
> Sin la comprobación explícita del `id` vacío, el adaptador devolvería una
> cadena vacía y el fallo aparecería más tarde, como un error de mutación
> desconectado de su causa. El mensaje que sí es accionable es
> «no se encontró el proyecto número 7 en «organización»».

## La caché de identificadores

Publicar 20 items sin caché haría 20 búsquedas del **mismo** `node_id` del
repositorio. Y sin un lock por clave, peor.

```mermaid
sequenceDiagram
    autonumber
    participant I1 as item 1
    participant I2 as item 2
    participant I3 as item 3
    participant L as lock["repositorio:org/repo"]
    participant API as API

    par tres items piden lo mismo
        I1->>L:.Lock()
        I3->>L: .Lock()
        I2->>L: .Lock()
    end

    Note over I1,L: I1 gana el lock
    I1->>API: query repository { id }
    API-->>I1: R_kgDO
    I1->>I1: cache.guardar("repositorio:org/repo", "R_kgDO")
    I1->>L: .Unlock()

    Note over I2: "segunda comprobación<br/>tras acquiring el lock"
    I2->>I2: ¿ya está en caché? SÍ
    I2-->>I2: R_kgDO (sin llamada)
    I3->>I3: ¿ya está en caché? SÍ
    I3-->>I3: R_kgDO (sin llamada)
```

**Sin la segunda comprobación**, los tres items esperarían el lock y luego
lanzarían su propia consulta: tres llamadas idénticas. Es el
**cache stampede**, y la doble comprobación lo elimina.

**Con un mutex global**, resolver `usuario:ana` bloquearía la resolución del
repositorio. El lock es **por clave**, y solo se serializan quienes piden lo
mismo.

```mermaid
flowchart TD
    ASK["resolverNodeID(clave, resolver)"] --> C1{"caché[clave]?"}
    C1 -->|sí| HIT(["devolver"])
    C1 -->|no| LOCK["lockDe(clave).Lock()"]
    LOCK --> C2{"<b>segunda comprobación</b><br/>caché[clave]?"}
    C2 -->|sí| HIT2(["devolver"])
    C2 -->|no| RES["resolver(ctx)"]
    RES --> ERR{"¿error?"}
    ERR -->|sí| PROP(["propagar<br/><b>sin cachear</b>"])
    ERR -->|no| SAVE["cache.guardar(clave, valor)"]
    SAVE --> DONE(["devolver"])
    LOCK --> UNLOCK["defer Unlock()"]

    classDef clave fill:#d4edda,stroke:#28a745
    class C2 clave
```

> **El error no se cachea.** Cachear un fallo convertiría un 503 transitorio en
> un fallo permanente durante toda la vida del proceso: el usuario tendría que
> relanzar la herramienta para volver a intentarlo.
>
> `TestResolverNodeIDPropagaElErrorYNoLoCachea` lo comprueba.

## La concurrencia

```mermaid
flowchart TB
    subgraph limite["Semáforo de 4"]
        direction LR
        A["item 1"] --> B["item 2"] --> C["item 3"] --> D["item 4"]
    end

    E["item 5"] -.->|"espera"| A
    F["item 6"] -.->|"espera"| A

    subgraph porItem["Cada item, en serie"]
        P1["createIssue"] --> P2["addIssueToProjectV2ItemById"] --> P3["addAssigneesToAssignable"]
    end

    classDef esp fill:#fff3cd,stroke:#ffc107
    class E,F esp
```

| Decisión | Elección | Motivo |
|---|---|---|
| Paralelismo | 4 items a la vez | La API de GitHub limita por segundo; más rápido = más rate limits |
| Dentro de un item | En serie | El `addIssueToProject` necesita el `id` del issue |
| Estructura | `WaitGroup` + `chan struct{}` | `errgroup` es una dependencia externa (RNF-01) |

`TestPublicacionParalelaRespetaElLimite` comprueba que nunca hay más de 4
simultáneos.

## El fallo parcial

```mermaid
flowchart TD
    ITEMS["5 items"] --> P["4 se crean<br/>1 falla"]

    P --> RES["PublishResult por item:<br/>{Success: true} × 4<br/>{Success: false, Error: ...} × 1"]

    RES --> CONT["exitos=4, fallos=1"]
    CONT --> DEC{"¿devolver error?"}

    DEC -->|sí| MAL["❌ el usuario cree que<br/>no se publicó nada"]
    DEC -->|no| BIEN(["✅ resumen: 4 creados, 1 fallido<br/><b>código de salida 0</b>"])

    classDef bien fill:#d4edda,stroke:#28a745
    classDef mal fill:#f8d7da,stroke:#dc3545
    class BIEN bien
    class MAL mal
```

Es la razón por la que `PublishBacklog` devuelve `[]PublishResult` en vez de un
`error`: el llamante necesita saber **cuántas** tarjetas se crearon.

La salida JSON distingue las dos listas:

```mermaid
flowchart LR
    J["despacho"] --> PUB["publicados[]<br/>{titulo, exito, url, external_id}"]
    J --> OMIT["omitidos[]<br/>{titulo, raw_assignee}"]
    J --> N["exitos · fallos · plataforma"]
```

Un fallo parcial **no** es un error de la operación. Devolver error haría que un
pipeline de CI creyera que no se publicó nada, cuando cinco tarjetas existen.

## Errores reintentables

```mermaid
flowchart TD
    ERR["error de la API"] --> K{"¿status?"}

    K -->|429| R1["rate limit → reintentar<br/>con espera mayor"]
    K -->|5xx| R2["servidor caído → reintentar"]
    K -->|"error de red"| R3["timeout / reset → reintentar"]
    K -->|401 / 403| F1["❌ token sin scope o caducado<br/><b>no reintentar: no arregla</b>"]
    K -->|404| F2["❌ tablero o repo no existe"]
    K -->|422| F3["❌ contenido rechazado por la API"]

    classDef reint fill:#fff3cd,stroke:#ffc107
    classDef fatal fill:#f8d7da,stroke:#dc3545
    class R1,R2,R3 reint
    class F1,F2,F3 fatal
```

Los mensajes de error reintentables y los no reintentables se distinguen
explícitamente: esperar a un 401 no lo convierte en 200.

## TC-06

> **TC-06 — GitHub Project v2.** Petición de mutación GraphQL → creación de
> Issue y agregado del `item` al tablero → retorno con código de éxito e ID
> GraphQL generado.

| Criterio | Test |
|---|---|
| Crea issue y lo agrega al tablero | `TestTC06CreaIssueYAgregaAlTablero` |
| Las variables enviadas son las correctas | `TestTC06EmiteLasVariablesCorrectas` |
| Errores in-band → error de Go | `TestErroresInBandSeConviertenEnErrorDeGo` |
| Fallo parcial deja constancia | `TestFalloAlAgregarAlTableroDejaConstancia` |
| Límite de concurrencia respetado | `TestPublicacionParalelaRespetaElLimite` |
| Caché de identificadores | `TestCacheDeIdentificadores`, `TestResolverNodeIDEvitaElStampede` |
| El token nunca aparece en errores | `TestCredencialNoApareceEnErrores` |
| Token ausente falla antes de la red | `TestTokenAusenteFallaAntesDeLaRed` |

Ningún test toca la API real: todos usan `httptest` con un servidor local que
imita las respuestas GraphQL.

## Relacionado

- [Flujo general](00-vision-general.md)
- [Identidades](03-identidades.md)
- [Concurrencia](../../criticas/concurrencia.md)