# Flujo: identidades

**RF-04** · **TC-04** · `internal/infrastructure/identity/json_mapper.go` ·
`internal/application/publish_backlog.go`

De `"Omar Hernández"` a `omarhernan`.

## El problema

El LLM devuelve nombres como aparecen en la minuta. La plataforma de destino
exige identificadores. Hacer la conversión a mano es una tarea de 20 minutos por
reunión, y **el error silencioso es el peor resultado posible**: publicar veinte
tarjetas sin responsable y que nadie se entere hasta las retrospectivas.

```mermaid
flowchart LR
    A["Acta de la reunión"] --> B["«Omar Hernández»<br/>«Ana María Ruiz»"]
    B --> C["LLM: assignee_name"]
    C --> D["¿Con quién es?<br/>¿Cómo lo escribe la plataforma?"]
    D -->|"sin resolver"| E["❌ veinte tarjetas huérfanas"]
    D -->|resuelto| F["✅ con responsable"]

    classDef mal fill:#f8d7da,stroke:#dc3545
    classDef bien fill:#d4edda,stroke:#28a745
    class E mal
    class F bien
```

## El flujo

```mermaid
flowchart TD
    S(["ResolveHandle(ctx, 'Omar Hernández')"]) --> NORM["NormalizarNombre"]

    NORM --> N1["ToLower"]
    N1 --> N2["Quitar acentos<br/><i>tabla explícita</i>"]
    N2 --> N3["Separadores → espacio único"]

    N3 --> LOOK{"¿hay entrada exacta<br/>en el índice?"}

    LOOK -->|sí| HIT(["✅ handle"])
    LOOK -->|no| AMB{"¿algún token<br/>coincide como abreviatura?"}

    AMB -->|"exactamente 1"| HIT
    AMB -->|"varios"| ERR2(["⚠️ ambiguo:<br/>no se resuelve"])
    AMB -->|ninguno| MISS["nombre desconocido"]

    MISS --> POL
    ERR2 --> POL

    POL{"política configurada"}
    POL -->|fail| E5(["❌ ErrIdentidadNoResuelta<br/>código 5"])
    POL -->|assign_unassigned| SIN["item sin responsable"]
    POL -->|skip| SKIP["item a omitidos"]

    classDef ok fill:#d4edda,stroke:#28a745
    classDef aviso fill:#fff3cd,stroke:#ffc107
    classDef mal fill:#f8d7da,stroke:#dc3545

    class HIT ok
    class SIN,SKIP aviso
    class E5 mal
```

## La normalización

```mermaid
flowchart LR
    subgraph variantes["La misma persona, escrita de cinco maneras"]
        V1["Omar Hernández"]
        V2["omar hernandez"]
        V3["OMAR   HERNÁNDEZ"]
        V4["Omar, Hernandez"]
        V5["Omar Hernandez."]
    end

    V1 --> N["NormalizarNombre"]
    V2 --> N
    V3 --> N
    V4 --> N
    V5 --> N

    N --> R["«omar hernandez»"]

    classDef uno fill:#d4edda,stroke:#28a745,stroke-width:2px
    class R uno
```

### Por qué la tabla de acentos es explícita

La biblioteca estándar de Go **no normaliza Unicode**: no hay NFC ni NFD fuera de
`golang.org/x/text`, y ese es justo el paquete que RNF-01 prohíbe. La tabla cubre
el rango latino, que es lo que aparece en nombres:

| Entrada | Salida | Por qué |
|---|---|---|
| `á à ä â` | `a` | |
| `é è ë ê` | `e` | |
| `í ì ï î` | `i` | |
| `ó ò ö ô` | `o` | |
| `ú ù ü û` | `u` | |
| `ñ` | `n` | `Hernández` y `Hernandez` son la misma persona |
| `ç` | `c` | |

La enye es la más importante: es la que aparece en la mayoría de los apellidos
hispanos, y sin ella un tercio de los nombres no resolvería.

## Cómo se construye el índice

```mermaid
flowchart TB
    CFG["configs/mappings.json"] --> DEFS["defaults:<br/>unknown_assignee_policy<br/>default_handle"]
    CFG --> MAPS["mappings[]"]

    MAPS --> LOOP{"por cada entrada"}
    LOOP --> RAW["raw_names[]<br/>«Omar Hernández»"]
    LOOP --> AL["alias[]<br/>«OH», «Hdez»"]
    LOOP --> HD["handle<br/>«omarhernan»"]
    LOOP --> PL["platforms[]"]

    RAW --> IDX["índice: clave → handle"]
    AL --> IDX

    subgraph IDX["Construcción de alias"]
        A1["índice[nombre exacto] = handle"]
        A2["índice[alias] = handle"]
        A3["índice[primer token] = handle<br/><i>solo si es único</i>"]
        A4["índice[token inicializado] = handle<br/><i>«o.» → «omar»</i>"]
        A5["índice[último apellido] = handle<br/><i>«hernández»</i>"]
    end

    classDef cond fill:#fff3cd,stroke:#ffc107
    class A3,A4 cond
```

### Las reglas de abreviatura

```mermaid
flowchart TD
    R["¿cuántas entradas comparten<br/>ese token?"]

    R -->|"exactamente 1"| OK["indexar como abreviatura"]
    R -->|"2 o más"| NO["no indexar<br/><i>«Ana» no significa nada</i>"]

    OK --> MATCH["«H.» resuelve a<br/>«Hernández»"]

    classDef ok fill:#d4edda,stroke:#28a745
    classDef mal fill:#f8d7da,stroke:#dc3545
    class MATCH ok
    class NO mal
```

> **Bug encontrado.** La primera versión generaba el alias de abreviatura
> **solo a partir del último apellido**: `H.` resolvía a `Hernández`, pero
> `Omar H.` no, porque el token `H.` no aparecía entre los tokens del apellido.
> La regla corregida genera un alias **por cada token** de cada nombre, con la
> condición de unicidad.

## La unicidad, que es la parte difícil

```mermaid
flowchart TD
    subgraph unico["Nombre único"]
        U1["índice: omarhernan ← «Omar Hernández»"] --> R1["«Omar Hernández» → omarhernan<br/>«O. Hernández» → omarhernan<br/>«Hdez» → omarhernan"]
    end

    subgraph ambiguo["Nombre ambiguo"]
        A1["índice: anaruiz ← «Ana Ruiz»<br/>índice: anaiglesias ← «Ana Iglesias»"] --> R2["«Ana» → ⚠️ ambiguo<br/><i>no se resuelve</i>"]
    end

    classDef ok fill:#d4edda,stroke:#28a745
    classDef mal fill:#f8d7da,stroke:#dc3545
    class R1 ok
    class R2 mal
```

Asignar `"Ana"` a una de las dos sería una moneda al aire. **No resolver** es el
comportamiento correcto: la política decide qué hacer después, y una política
`fail` convierte un nombre ambiguo en un error visible en lugar de una tarea mal
asignada.

## La política

```mermaid
flowchart LR
    subgraph TRES["Tres políticas"]
        direction TB
        P1["fail<br/><b>por defecto</b><br/>error + código 5"]
        P2["assign_unassigned<br/>publica sin responsable<br/>+ log de advertencia"]
        P3["skip<br/>descarta el item<br/>+ lo lista en omitidos"]
    end

    P1 --> R1["ningún item sale sin responsable"]
    P2 --> R2["el item sale, sin dueño"]
    P3 --> R3["el item no sale"]

    classDef pordefecto fill:#d4edda,stroke:#28a745,stroke-width:2px
    class P1 pordefecto
```

**Por qué `fail` es el default.** Es la única política donde un nombre no
resuelto produce un fallo **visible**. Las otras dos producen trabajo hecho a
medias, que es más difícil de detectar.

Cuesta un fichero de mapeo. Se asume: publicar el backlog de una reunión sin
saber a quién se asigna no es un resultado útil.

## El dry-run y el mapper vacío

```mermaid
flowchart TD
    subgraph dry["Modo dry-run"]
        D1["Ingesta"] --> D2["Extracción"]
        D2 --> D3["PublicarBacklog(dry-run)"]
        D3 --> D4["return items<br/><b>sin consultar identidades</b>"]
        D4 --> D5["tabla: nombre en claro,<br/>sin marca"]
    end

    subgraph pub["Modo publish"]
        P1["cargarMappings(ruta, publish)"] --> P2{"¿existe el fichero?"}
        P2 -->|no| P3["❌ código 2"]
        P2 -->|sí| P4["MapperVacio no procede:<br/>mapper real"]
        P4 --> P5["ResolveHandle por item"]
    end

    classDef ok fill:#d4edda,stroke:#28a745
    classDef mal fill:#f8d7da,stroke:#dc3545
    class D4,D5 ok
    class P3 mal
```

`MapperVacio()` existe para que el dry-run pueda inyectar una identidad sin
exigir el fichero de mapeo. Su documentación es explícita sobre el límite:

> **NO usarlo fuera de dry-run:** publicaría todo sin responsable, que es
> exactamente el fallo silencioso que TC-04 existe para evitar.

El dry-run **no marca** «SIN RESOLVER» porque no intentó resolver nada. Ver
[`cmd.md`](../cmd.md#la-tabla-y-por-qué-no-marca-en-dry-run).

## La `@` no se almacena

```mermaid
flowchart LR
    A["LLM: «omarhernan»"] --> D["Dominio:<br/>SetMappedHandle()"]
    D --> B["Domain.Metadata:<br/>«omarhernan»"]
    B --> C["API de GitHub:<br/>login sin arroba ✅"]

    X["Si se guardara «@omarhernan»"] --> Y["❌ la API lo rechaza"]
    X --> Z["❌ el log y la tarjeta<br/>no coinciden"]

    classDef ok fill:#d4edda,stroke:#28a745
    classDef mal fill:#f8d7da,stroke:#dc3545
    class C ok
    class Y,Z mal
```

La normalización ocurre **en el dominio**, una sola vez, en el punto donde el
identificador entra. La infraestructura recibe lo que el dominio da y no vuelve
a decidir.

## Errores de esta etapa

| Situación | Comportamiento | Código |
|---|---|---|
| Nombre no está en la tabla | Política | 5 o 0 |
| Nombre ambiguo | Política (nunca se resuelve solo) | 5 o 0 |
| Fichero de mapeo ausente en publish | Error de configuración | 2 |
| Fichero de mapeo ausente en dry-run | `MapperVacio()`, sigue | 0 |
| JSON de mapeo inválido | Error, incluso en dry-run | 2 |

## TC-04

> **TC-04 — Identity Map.** «Omar Hernández» en la minuta → handle
> `@omarhernan` usando `JSONIdentityMapper` → mapeo case-insensitive exacto.

| Criterio | Test |
|---|---|
| Nombre real → handle | `TestTC04MapeaNombreRealAHandle` |
| Case-insensitive | `TestResolveHandleCaseInsensitive` |
| Acentos ausentes | `TestResolveHandleToleraAcentosAusentes` |
| Abreviaturas | `TestResolveHandleAceptaAbreviaturas` |
| Ambigüedad no se resuelve | `TestNombreDePilaAmbiguoNoSeResuelve` |
| Unicidad sí resuelve | `TestNombreDePilaUnicoSiSeResuelve` |
| Normalización completa | `TestNormalizarNombre`, `TestNormalizarNombreCobreTodasLasVocalesAcentuadas` |
| Tabla de mapeo real válida | `TestTablaRealDelProyectoEsValida` |
| Mapper vacío no inventa | `TestMapperVacioNoResuelveNada` |

## Relacionado

- [Modelo de dominio](../domain.md)
- [Publicación](04-publicacion.md)
- [Puertos e identidades](03-identidades.md#la-politica)