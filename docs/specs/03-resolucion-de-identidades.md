# Especificación 03 — Resolución de identidades

**RF-04** · **TC-04** · `internal/infrastructure/identity/json_mapper.go`

## Requisito

> El sistema debe traducir los nombres de personas presentes en la transcripción
> a los identificadores de la plataforma de destino, de forma case-insensitive y
> tolerante a acentos.

## El problema

El LLM devuelve nombres como aparecen en la minuta: `Omar Hernández`. La
plataforma exige `omarhernan`. Y el fallo silencioso es el peor resultado
posible:

```mermaid
flowchart LR
    A["Acta"] --> B["«Omar Hernández»<br/>«Ana María Ruiz»"]
    B --> C["¿Cómo los llama GitHub?"]
    C -->|sin resolver| D["❌ veinte tarjetas<br/>sin responsable"]
    C -->|resuelto| E["✅"]

    style D fill:#f8d7da
    style E fill:#d4edda
```

## El formato de mapeo

```json
{
  "defaults": {
    "unknown_assignee_policy": "fail",
    "default_handle": ""
  },
  "mappings": [
    {
      "raw_names": ["Omar Hernández", "O. Hernández", "Hdez"],
      "handle": "omarhernan",
      "platforms": ["github"]
    },
    {
      "raw_names": ["Ana María Ruiz"],
      "alias": ["Ana", "A. Ruiz", "AMR"],
      "handle": "anamaria",
      "platforms": ["github"]
    }
  ]
}
```

| Campo | Obligatorio | Qué es |
|---|---|---|
| `raw_names` | Sí | Los nombres tal como pueden aparecer en la minuta |
| `alias` | No | Formas alternativas |
| `handle` | Sí | El identificador en la plataforma, **sin `@`** |
| `platforms` | No | Filtro por plataforma |
| `defaults.unknown_assignee_policy` | No | `fail` · `assign_unassigned` · `skip` |
| `defaults.default_handle` | No | Responsable para nombres desconocidos |

`configs/mappings.example.json` es un ejemplo válido. Una tabla vacía se rechaza:
`ErrTablaMapeoInvalida`, porque una tabla sin aliases publicaría todo sin
responsable.

## El algoritmo

```mermaid
flowchart TD
    S(["ResolveHandle(ctx, 'Omar Hernández')"]) --> N["NormalizarNombre"]

    N --> N1["ToLower"]
    N1 --> N2["Quitar acentos<br/><i>tabla explícita</i>"]
    N2 --> N3["Separadores → espacio único"]
    N3 --> N4["«omar hernandez»"]

    N4 --> E{"¿exacto en el índice?"}
    E -->|sí| HIT(["omarhernan"])
    E -->|no| A{"¿token único<br/>como abreviatura?"}
    A -->|único| HIT
    A -->|varios| AMB(["⚠️ no se resuelve"])
    A -->|ninguno| DESC["nombre desconocido"]

    DESC --> POL
    AMB --> POL

    POL{"política"}
    POL -->|fail| F(["❌ código 5"])
    POL -->|assign_unassigned| U["sin responsable"]
    POL -->|skip| SK["a omitidos"]

    style HIT fill:#d4edda
    style F fill:#f8d7da
```

## La normalización

```mermaid
flowchart LR
    V1["Omar Hernández"] --> N
    V2["omar hernandez"] --> N
    V3["OMAR   HERNÁNDEZ"] --> N
    V4["Omar, Hernandez"] --> N
    V5["Omar Hernandez."] --> N

    N["NormalizarNombre"] --> R["«omar hernandez»"]

    style R fill:#d4edda,stroke-width:2px
```

| Regla | Por qué |
|---|---|
| Minúsculas | `ToLower` es de la stdlib |
| **Sin acentos** | `Hernández` y `Hernandez` son la misma persona |
| Separadores → un espacio | `Omar   Hernández` = `Omar Hernández` |
| `ñ` → `n` | Es el acento más frecuente en apellidos hispanos |
| `ç` → `c` | Idem |
| Sin espacios en los extremos | Un espacio inicial rompe la comparación con el índice |

La tabla de acentos es **explícita** porque la stdlib de Go no normaliza Unicode
(ADR-0002). Cubre el rango latino, que es lo que aparece en nombres.

## La construcción del índice

```mermaid
flowchart TB
    subgraph alias["Alias generados por entrada"]
        A1["nombre exacto<br/>«Omar Hernández» → omarhernan"]
        A2["alias declarados<br/>«Hdez» → omarhernan"]
        A3["cada token, si es único<br/>«hernández» → omarhernan"]
        A4["token inicializado<br/>«o.» → «omar»"]
    end

    A1 --> IDX["indice[clave] = handle"]
    A2 --> IDX
    A3 --> IDX
    A4 --> IDX

    style A3 fill:#fff3cd
    style A4 fill:#fff3cd
```

### Las reglas de abreviatura

**Un token genera abreviatura solo si es único en toda la tabla.**

```mermaid
flowchart LR
    U["«H.» → «hernández»<br/>único en la tabla ✅"] --> OK["se indexa"]
    A["«Ana» → 2 entradas<br/>ambiguo ❌"] --> NO["no se indexa"]

    style OK fill:#d4edda
    style NO fill:#f8d7da
```

> **Bug encontrado.** La primera versión generaba el alias de abreviatura
> **solo a partir del último apellido**: `H.` resolvía a `Hernández`, pero
> `Omar H.` no, porque el token `H.` no aparecía entre los tokens del apellido.
> La regla corregida genera un alias **por cada token** de cada nombre, con la
> condición de unicidad.

## La política

```mermaid
flowchart TB
    subgraph p["Tres políticas"]
        direction LR
        P1["fail<br/><b>default</b>"]
        P2["assign_unassigned"]
        P3["skip"]
    end

    P1 --> R1["error + código 5"]
    P2 --> R2["item sin responsable"]
    P3 --> R3["item descartado"]

    style P1 fill:#d4edda,stroke-width:2px
```

| Política | Efecto | Cuándo usarla |
|---|---|---|
| `fail` | **Error**, código 5 | Siempre, salvo que se sepa lo que se hace |
| `assign_unassigned` | Publica sin responsable, con aviso en el log | Cuando hay nombres que no son personas (un «Equipo») |
| `skip` | Descarta el item y lo lista en `omitidos` | Cuando esos items no valen |

**Por qué `fail` es el default.** Es la única política donde un nombre no
resuelto produce un fallo **visible**. Las otras dos producen trabajo a medias,
que es más difícil de detectar.

## El dry-run

```mermaid
sequenceDiagram
    participant CLI
    participant MAP as MapperVacio
    participant REAL as Mapper real

    CLI->>CLI: cargarMappings(ruta, modo)
    alt modo == dry-run
        Note over CLI: "no se exige el fichero"
        CLI->>MAP: MapperVacio()
        MAP-->>CLI: no resuelve nada
        Note over CLI: "✅ el dry-run avanza"
    else modo == publish
        Note over CLI: "el fichero es obligatorio"
        CLI->>REAL: NuevoJSONIdentityMapper(ruta)
        REAL-->>CLI: resolver por item
    end
```

`MapperVacio()` existe para el primer caso. Su documentación lleva la advertencia
en mayúsculas:

> **NO usarlo fuera de dry-run:** publicaría todo sin responsable, que es
> exactamente el fallo silencioso que TC-04 existe para evitar.

Y en dry-run la salida **no marca** «SIN RESOLVER»: no se intentó resolver nada.

## El handle no lleva `@`

```mermaid
flowchart LR
    A["«omarhernan»"] --> D["Dominio: SetMappedHandle()"]
    D --> B["almacenado: «omarhernan»"]
    B --> C["API de GitHub ✅"]

    style C fill:#d4edda
```

La normalización ocurre **en el dominio**, una sola vez, en el punto donde el
identificador entra. La infraestructura recibe lo que el dominio da y no vuelve a
decidir.

## Casos límite

| Situación | Comportamiento |
|---|---|
| El nombre no está en la tabla | Política |
| El nombre es ambiguo | Política (nunca se resuelve solo) |
| Fichero ausente en dry-run | `MapperVacio()`, sigue |
| Fichero ausente en publish | Error, código 2 |
| Tabla con `mappings: []` | `ErrTablaMapeoInvalida` |
| JSON mal formado | `ErrTablaMapeoInvalida` |
| Una entrada sin `handle` | Se ignora, con aviso |
| Nombres con espacios irregulares | Normalizados |
| Nombres con todos los acentos | Normalizados |
| Solo espacios o puntos | Nombre vacío, no resuelve |
| Contexto cancelado | `ctx.Err()`, sin tocar la tabla |

## Verificación — TC-04

> **TC-04 — Identity Map.** «Omar Hernández» en la minuta → traducción a
> `@omarhernan` usando `JSONIdentityMapper` → mapeo case-insensitive exacto.

| Criterio | Test |
|---|---|
| Nombre real → handle | `TestTC04MapeaNombreRealAHandle` |
| Case-insensitive | `TestResolveHandleCaseInsensitive` |
| Tolerante a acentos ausentes | `TestResolveHandleToleraAcentosAusentes` |
| Abreviaturas | `TestResolveHandleAceptaAbreviaturas` |
| Ambigüedad no se resuelve | `TestNombreDePilaAmbiguoNoSeResuelve` |
| Unicidad sí resuelve | `TestNombreDePilaUnicoSiSeResuelve` |
| Nombre desconocido | `TestResolveHandleNombreDesconocido` |
| Respeta el contexto | `TestResolveHandleRespetaContexto` |
| Política por defecto y configurada | `TestPoliticaPorDefecto`, `TestPoliticaConfigurada` |
| `default_handle` | `TestHandlePorDefecto` |
| Tabla vacía se rechaza | `TestTablaVaciaSeRechaza` |
| JSON inválido se rechaza | `TestTablaJSONInvalido` |
| El fichero real del proyecto es válido | `TestTablaRealDelProyectoEsValida` |
| Normalización completa | `TestNormalizarNombre`, `TestNormalizarNombreCobreTodasLasVocalesAcentuadas` |
| Separadores en espacio único | `TestNormalizarNombreConvierteSeparadoresEnEspacioUnico` |
| Seguro en concurrencia | `TestMapperEsSeguroEnConcurrencia` |
| El mapper vacío no inventa | `TestMapperVacioNoResuelveNada` |
| El mapper vacío no marca política rara | `TestMapperVacioUsaLaPoliticaDeNoAsignar` |

```bash
go test ./internal/infrastructure/identity/ -v
```

## Relacionado

- [Flujo de identidades](../architecture/flujos/03-identidades.md)
- [Publicación](04-publicacion-en-projects.md)
- [Modelo de dominio: vocabulario](../architecture/domain.md#vocabulario)