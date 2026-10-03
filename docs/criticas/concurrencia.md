# Concurrencia

**RNF-05** (parcialmente)

## El compromiso

> **Donde hay paralelismo, está acotado y protegido. Donde no lo hace falta, no lo
> hay.**

## Dónde hay concurrencia

```mermaid
flowchart TB
    subgraph paral["PARALELO — acotado"]
        P["Publicación de items<br/><b>WaitGroup + semáforo de 4</b>"]
    end

    subgraph serie["SECUENCIAL — por diseño"]
        S1["Pipeline: tres etapas<br/>en orden"]
        S2["Pipeline → ejecución"]
        S3["Operaciones de un item<br/>create → add → assign"]
    end

    subgraph seguro["ACCESO COMPARTIDO — protegido"]
        A1["cacheIDs<br/><b>mutex + lock por clave</b>"]
        A2["JSONIdentityMapper<br/><b>solo lectura tras construir</b>"]
    end

    style P fill:#fff3cd
    style S1,S2,S3 fill:#d4edda
    style A1,A2 fill:#d1ecf1
```

## La publicación en paralelo

```mermaid
flowchart TB
    subgraph antes["Sin acotar"]
        A1["20 items"] --> A2["20 goroutines"]
        A2 --> A3["❌ 20 peticiones a GitHub<br/>al mismo tiempo"]
    end

    subgraph ahora["Con semáforo de 4"]
        B1["20 items"] --> B2["chan struct{} de 4"]
        B2 --> B3["4 en vuelo"]
        B3 --> B4["cuando uno termina,<br/>otro entra"]
        B4 --> B5["✅ nunca más de 4"]
    end

    style A3 fill:#f8d7da
    style B5 fill:#d4edda
```

### Por qué 4

```mermaid
flowchart LR
    subgraph poco["1-2"]
        P1["subaprovechado:<br/>20 items tardan mucho"]
    end

    subgraph mucho["10-20"]
        M1["❌ rate limit de GitHub<br/>y reintentos, que empeoran<br/>lo que ya va mal"]
    end

    subgraph cuatro["4"]
        C1["✅ equilibrio:<br/>acelera y no llega<br/>al límite"]
    end

    style M1 fill:#f8d7da
    style C1 fill:#d4edda
```

## Por qué `WaitGroup` y un canal, y no `errgroup`

```mermaid
flowchart LR
    subgraph errgroup["errgroup (x/sync)"]
        E1["WaitGroup + primera<br/>err + cancelación"] --> E2["❌ dependencia externa<br/>RNF-01 lo prohíbe"]
    end

    subgraph stdlib["Lo que hay"]
        S1["sync.WaitGroup"] --> S3["✅ cuenta terminaciones"]
        S2["chan struct{} de 4"] --> S3
        S1 --> S4["sin dependencia"]
    end

    style E2 fill:#f8d7da
    style S4 fill:#d4edda
```

`AGENTS.md` mencionaba `errgroup` en un borrador. Se corrigió: la versión
actual usa `sync.WaitGroup` y un `chan struct{}` como semáforo. La funcionalidad es
la misma sin importar nada.

## Y por qué las operaciones de un item van en serie

```mermaid
sequenceDiagram
    participant I as item 1
    participant API

    I->>API: createIssue
    API-->>I: id = I_1
    Note over I: "necesita el id"
    I->>API: addIssueToProject(id = I_1)
    API-->>I: itemId
    Note over I: "necesita el item"
    I->>API: addAssignees(itemId)
```

`addIssueToProjectV2ItemById` necesita el `node_id` del issue, que **crea la
primera llamada**. Paralelizar dentro de un item no es posible sin magia.

El paralelismo está **entre** items, que es donde está el ganho real: crear 20
issues son 20 peticiones independientes.

## La caché: el danger real

```mermaid
sequenceDiagram
    autonumber
    participant I1 as item 1
    participant I2 as item 2
    participant I3 as item 3
    participant K as lock "repositorio"
    participant API

    Note over I1,I3: "los tres piden el mismo node_id"

    I1->>K: Lock() ✅
    I2->>K: Lock() ⏳
    I3->>K: Lock() ⏳

    I1->>API: query repository { id }
    API-->>I1: R_kgDO
    I1->>K: Unlock()

    Note over I2: "2ª comprobación: ya está ✅"
    I2-->>I2: R_kgDO (sin llamada)
    I3-->>I3: R_kgDO (sin llamada)
```

### Sin la doble comprobación: cache stampede

```mermaid
sequenceDiagram
    participant I1 as item 1
    participant I2 as item 2
    participant API

    I1->>I1: Lock ✅
    I1->>API: query { id }
    API-->>I1: R_kgDO
    I1->>I1: guardar
    I1->>I1: Unlock
    I2->>I2: Lock ✅ (esperó)
    Note over I2: "1ª comprobación: <b>NO la repite</b>"
    I2->>API: query { id } otra vez
    API-->>I2: R_kgDO
```

Tres items, tres llamadas idénticas. Es exactamente lo que la caché debía evitar, y
solo se manifiesta **bajo concurrencia**: en una ejecución con un solo item nunca
ocurre.

### Y con un mutex global, correctísimo pero lento

```mermaid
flowchart TB
    subgraph global["Mutex global"]
        G1["item 1 resuelve repo"] --> G2["item 5 espera"]
        G2 --> G3["item 5 resuelve usuario X"] --> G4["item 2 espera"]
    end

    subgraph porClave["Lock por clave"]
        C1["lock «repo»"] --> C2["lock «usuario:ana»"]
        C3["lock «usuario:omar»"] --> C4["✅ libres en paralelo"]
    end

    style G2 fill:#f8d7da
    style G4 fill:#f8d7da
    style C4 fill:#d4edda
```

Un mutex global sería **correcto**. Es serial. El lock por clave serializa solo a
quienes piden lo mismo.

## El mapper de identidades

```mermaid
flowchart LR
    subgraph construccion["Construcción (una vez)"]
        A["NuevoJSONIdentityMapper"] --> B["construirIndice()"]
    end

    subgraph lectura["Uso (muchas goroutines)"]
        C["ResolveHandle()"] --> D["lectura del mapa"]
        E["ConoceInforma()"] --> D
        F["HandlesConocidos()"] --> D
    end

    B --> D

    style A fill:#fff3cd
    style C fill:#d4edda
    style E fill:#d4edda
    style F fill:#d4edda
```

**El índice se construye una vez y después solo se lee.** Eso lo hace seguro por
construcción: un mapa que solo se lee concurrentemente no necesita mutex en Go.

La condición: **nada escribe en el índice después de construirlo**. Si algún día se
añade un método que muta, hay que añadir el lock ahí. Es la deuda que compra esta
decisión, y está documentada en el propio `json_mapper.go`.

## El patrón del semáforo

```go
semaforo := make(chan struct{}, 4)

for _, item := range items {
    wg.Add(1)

    go func(it domain.ActionItem) {
        defer wg.Done()

        semaforo <- struct{}{}        // ocupa un hueco
        defer func() { <-semaforo }()  // lo libera, incluso en panic

        // … publicar
    }(item)
}

wg.Wait()
```

El `defer` que libera el hueco es lo que evita que una salida por error deje el
semáforo permanentemente lleno y detenga el resto.

## Resumen

| Dónde | Modelo | Protección |
|---|---|---|
| Publicación de items | Paralelo, máx. 4 | `WaitGroup` + `chan struct{}` |
| Operaciones de un item | Secuencial | Dependencia de datos |
| Pipeline | Secuencial | El orden importa |
| `cacheIDs` | Compartido | `sync.Mutex` + lock por clave |
| `JSONIdentityMapper` | Compartido | Solo lectura tras construir |
| El logger | Compartido | `slog` es seguro por diseño |

## Verificación

| Compromiso | Test |
|---|---|
| Nunca más de 4 en paralelo | `TestPublicacionParalelaRespetaElLimite` |
| Sin cache stampede (32 goroutines) | `TestResolverNodeIDEvitaElStampede` |
| La caché sirve sin resolver | `TestResolverNodeIDSirveDesdeCacheSinVolverALaRed` |
| El error no se cachea | `TestResolverNodeIDPropagaElErrorYNoLoCachea` |
| Claves separadas no se bloquean | `TestCacheIDsSeparaLasClaves` |
| El valor vacío no es caché | `TestCacheIDsIgnoraValoresVacios` |
| El mapper es seguro en concurrencia | `TestMapperEsSeguroEnConcurrencia` |
| El mapper vacío también | `TestMapperVacioEsSeguroEnConcurrencia` |

```bash
go test ./internal/infrastructure/adapters/ -run 'Paralela|Stampede|CacheIDs' -v
go test ./internal/infrastructure/identity/ -run Concurrencia -v
```

### Verificado por inyección de regresión

Quitar la doble comprobación de `resolverNodeID` produce:

```
se ejecuto el resolutor 32 veces, se esperaba 1 (cache stampede)
```

El test lanza 32 goroutines esperando exactamente una llamada. Un bug de
concurrencia que no se prueba bajo concurrencia **no está probado**.

### Con `-race`

```bash
go test ./... -race -count=1
```

Detecta accesos sin sincronizar. Es la comprobación que un test funcional no
puede hacer: el comportamiento puede ser correcto en una ejecución y estar roto
en la siguiente.

## Documentos relacionados

- [ADR-0009 — Caché con lock por clave](../adr/0009-cache-con-lock-por-clave.md)
- [Flujo de publicación](../architecture/flujos/04-publicacion.md)
- [`AGENTS.md`](../../AGENTS.md) — la regla de concurrencia sin `errgroup`.