# Flujo: errores y códigos de salida

Cómo un fallo interno se convierte en un número que un proceso de CI puede usar.

## Los tres niveles

```mermaid
flowchart TD
    subgraph l1["1 · Origen"]
        A["adaptador de infraestructura<br/>«la API devolvió 401»"]
    end

    subgraph l2["2 · Etapa"]
        B["Pipeline envuelve:<br/>ErrorEtapa{Etapa, Err}"]
    end

    subgraph l3["3 · Código de salida"]
        C["CLI:<br/>codigoDeEtapa() + codigoDeError()"]
    end

    A -->|"errors.Is sigue funcionando<br/>gracias a Unwrap"| B
    B -->|"errors.As encuentra la etapa"| C
    C --> D["código 6"]

    classDef a fill:#fff3cd,stroke:#ffc107
    classDef b fill:#d1ecf1,stroke:#17a2b8
    classDef c fill:#d4edda,stroke:#28a745
    class A a
    class B b
    class C,D c
```

Los tres existen por razones distintas y **ninguno sustituye a los otros**.

## La envoltura

```go
type ErrorEtapa struct {
    Etapa PipelineEtapa
    Err   error
}

func (e *ErrorEtapa) Error() string { return fmt.Sprintf("fallo en la etapa de %s: %v", e.Etapa, e.Err) }
func (e *ErrorEtapa) Unwrap() error   { return e.Err }
```

### Por qué `Unwrap` no es opcional

```mermaid
flowchart LR
    subgraph sin["Sin Unwrap"]
        S1["error concreto:<br/>domain.ErrPublicacionFallida"] --> S2["envuelto"] --> S3["errors.Is(err, ErrPublicacionFallida)<br/><b>false</b> ❌"]
    end

    subgraph con["Con Unwrap"]
        C1["error concreto:<br/>domain.ErrPublicacionFallida"] --> C2["envuelto"] --> C3["errors.Is(err, ErrPublicacionFallida)<br/><b>true</b> ✅"]
    end

    classDef mal fill:#f8d7da,stroke:#dc3545
    classDef bien fill:#d4edda,stroke:#28a745
    class S3 mal
    class C3 bien
```

Sin `Unwrap`, `errors.Is` se detiene en el envoltorio. La CLI no podría
distinguir «el tablero devolvió 422» de «el token caducó» ni de «el modo es
inválido»: los tres serían «fallo en la etapa de despacho».

### Y las consultas de etapa

```go
func EsFalloDePublicacion(err error) bool
func EtapaDelError(err error) PipelineEtapa
```

Ambas usan `errors.As`, de modo que funcionan aunque el error de etapa se
envuelva **otra vez** más arriba —por ejemplo en un `errors.Join`. Y ambas
toleran `nil` y errores ajenos: devuelven `false` y `""`.

| `errors.As` encuentra | `EtapaDelError` | `EsFalloDePublicacion` |
|---|---|---|
| `ErrorEtapa{despacho}` | `"despacho"` | `true` |
| `ErrorEtapa{extraccion}` | `"extraccion"` | `false` |
| `ErrorEtapa` anidado en un `Join` | `"despacho"` | `true` |
| `domain.ErrConfigInvalida` | `""` | `false` |
| `nil` | `""` | `false` |

## Los códigos

```mermaid
flowchart TB
    subgraph tabla["Traducción completa"]
        direction LR
        U["1 · uso<br/><i>flag contradictorio<br/>--file ausente</i>"]
        CFG["2 · configuración<br/><i>credencial ausente<br/>mappings.json ausente<br/>modo desconocido</i>"]
        ING["3 · ingesta<br/><i>archivo ilegible<br/>formato desconocido<br/>documento vacío</i>"]
        EXT["4 · extracción<br/><i>3 intentos agotados</i>"]
        ID["5 · identidad<br/><i>responsable sin resolver</i>"]
        PUB["6 · publicación<br/><i>fallo de la plataforma</i>"]
    end

    OK["0 · éxito<br/>incluido el fallo parcial"]
    INT["130 · interrumpido<br/><i>128 + SIGINT</i>"]

    classDef ok fill:#d4edda,stroke:#28a745
    classDef mal fill:#f8d7da,stroke:#dc3545
    class OK ok
    class U,CFG,ING,EXT,ID,PUB mal
```

### Cómo se decide

```mermaid
flowchart TD
    ERR["error"] --> HASET{"errors.As(&ErrorEtapa)"}
    HASET -->|sí| PORET["codigoDeEtapa(etapa, err)"]
    HASET -->|no| SINET["codigoDeError(err)"]

    PORET --> P1{"¿ingesta?"}
    P1 -->|sí| C3["3"]
    P1 -->|extracción| C4["4"]
    P1 -->|despacho| C6["6"]
    P1 -->|etapa desconocida| SINET

    SINET --> S1{"ErrConfigInvalida?"}
    S1 -->|sí| C2["2"]
    S1 -->|no| S2{"ErrIdentidadNoResuelta<br/>o ErrMapeoDeIdentidadInvalido?"}
    S2 -->|sí| C5["5"]
    S2 -->|no| S3{"ErrFormatoNoSoportado?"}
    S3 -->|sí| C3
    S3 -->|no| S4{"ErrReintentosAgotados?"}
    S4 -->|sí| C4
    S4 -->|no| S5{"flag.ErrHelp?"}
    S5 -->|sí| C0["0"]
    S5 -->|no| C1["1 · uso"]
```

## Por qué siete códigos distintos

Un único código de error —el diseño habitual— obligaría a **leer el mensaje**
para decidir qué hacer. En un log de CI, el mensaje es la primera cosa que se
pierde: la línea que dice por qué falla hace 40 líneas más arriba.

```mermaid
flowchart TD
    CI{"llega el código"} --> K6{"¿6?"}

    K6 -->|sí| A["🔄 Reintentar.<br/>Es la plataforma: rate limit,<br/>token expirado, red.<br/><b>Reintentar el comando.</b>"]
    K6 -->|no| K4{"¿4?"}

    K4 -->|sí| B["🔄 Reintentar con otro modelo.<br/>El LLM no produjo algo válido.<br/><b>Cambiar de modelo o prompt.</b>"]
    K4 -->|no| K3{"¿3?"}

    K3 -->|sí| C["🔧 Arreglar el ARCHIVO.<br/>No existe, no se lee, o su formato<br/>no es uno de los tres.<br/><b>Cambiarlo no arregla el LLM.</b>"]
    K3 -->|no| K5{"¿5?"}

    K5 -->|sí| D["📝 Arreglar el MAPEO.<br/>Falta alguien en mappings.json.<br/><b>O cambiar la política.</b>"]
    K5 -->|no| E["⚙️ Arreglar la CONFIGURACIÓN.<br/>Token, tablero, flags.<br/><b>Nada que reintentar.</b>"]

    classDef retry fill:#fff3cd,stroke:#ffc107
    classDef fix fill:#d1ecf1,stroke:#17a2b8
    class A,B retry
    class C,D,E fix
```

Cada código señala una categoría de causa **distinta**, y por tanto una acción
distinta. Es lo que hace que un pipeline de CI pueda reaccionar sin leer el
mensaje.

`TestCodigosDeSalidaSonDistintos` verifica que los seis códigos de error son
efectivamente distintos, y `TestCodigoDeErrorEncadenadoConEtapa` que funcionan
con el error envuelto.

## Los dos casos especiales

### `--help` devuelve 0

```mermaid
flowchart LR
    HELP["--help"] --> ERR["flag.ErrHelp"]
    ERR --> COD["codigoDeError → 0"]

    classDef ok fill:#d4edda,stroke:#28a745
    class COD ok
```

`--help` no es un fallo: el proceso terminó con éxito, solo que pidió otra
cosa. Tratarlo como error rompe scripts que hacen `tool --help && haz-cosas`.

### 130, para la interrupción

`128 + SIGINT`. Es la convención de Unix, y lo que un proceso recibe al pulsar
Ctrl-C. Distinguirlo de un error real permite que un supervisor reinicie el
proceso sin alertar a nadie.

## El orden de las traducciones

```mermaid
sequenceDiagram
    participant E as ErrorEtapa
    participant C as codigoDeError
    participant CLI

    CLI->>E: errors.As(&etapa)
    E-->>CLI: etapa = "despacho"
    Note over CLI: "la ETAPA manda"
    CLI->>C: codigoDeError(err)
    C->>C: errors.Is(ErrConfigInvalida)? no
    C->>C: errors.Is(ErrIdentidadNoResuelta)? no
    C->>C: errors.Is(ErrReintentosAgotados)? no
    C-->>CLI: 1 (uso)
    Note over CLI: "pero la ETAPA dice 6"
    CLI-->>CLI: "la etapa tiene prioridad"
```

**La etapa manda.** Si el fallo ocurrió en la etapa de despacho, el código es 6,
aunque el error concreto tenga cara de «uso». La etapa describe **dónde**;
el error describe **qué**. Para decidir qué reintentar, importa más dónde.

La tabla de errores se recorre de arriba abajo y gana la primera coincidencia.
El orden importa: `ErrFormatoNoSoportado` se comprueba **después** de los de
configuración, y no antes, para que un error de configuración que lo envuelva
no se reporte como un problema de archivo.

## Errores que se solapan entre paquetes

`identity` y `adapters` definen cada uno su propio `ErrConfigInvalida`.

```mermaid
flowchart LR
    CLI["codigoDeError"] --> A["errors.Is(err,<br/>domain.ErrConfigInvalida)"]
    CLI --> B["errors.Is(err,<br/>adapters.ErrConfigInvalida)"]
    A --> R["2"]
    B --> R

    classDef ok fill:#d4edda,stroke:#28a745
    class R ok
```

No es una duplicación sin motivo: son paquetes que **no se importan entre sí**, y
el del dominio está conceptualmente en la frontera. Cuesta una línea en la CLI y
evita un acoplamiento entre `application` y `infrastructure`.

## La tabla de decisiones

| Situación | Etapa | Error | Código |
|---|---|---|---|
| `--file` ausente | — | `errFaltanParametros` | 1 |
| `--dry-run --publish` | — | `errModoAmbiguo` | 1 |
| `--format` desconocido | — | — | 1 |
| `--log-format` desconocido | — | — | 1 |
| Proveedor desconocido | — | `ErrConfigInvalida` | 2 |
| Token ausente en publish | — | `ErrConfigInvalida` | 2 |
| `mappings.json` ausente en publish | — | `ErrMapeoDeIdentidadInvalido` | 2 |
| Modo desconocido | despacho | `ErrConfigInvalida` | 2 |
| Fichero inexistente | ingesta | error de `os.Stat` | 3 |
| Extensión y contenido desconocidos | ingesta | `ErrFormatoNoSoportado` | 3 |
| Transcripción vacía | ingesta | — | 3 |
| 3 intentos sin respuesta válida | extracción | `ErrReintentosAgotados` | 4 |
| Nombre no resuelto (política `fail`) | despacho | `ErrIdentidadNoResuelta` | 5 |
| Tablero inexistente | despacho | `ErrConfigInvalida` | 2 |
| API devolvió 4xx/5xx irrecuperable | despacho | error envuelto | 6 |
| Fallo parcial | despacho | — | **0** |

## Lo que este diseño NO cubre

| Situación | Por qué no se distingue | Cómo se compensa |
|---|---|---|
| Rate limit vs 500 vs red en la etapa 6 | Los tres son código 6 | El **mensaje** los distingue, y `EsReintentable` está disponible |
| Timeouts en la etapa 4 | Todos son código 4 | El mensaje nombra el proveedor y el intento |
| Dos items con nombres desconocidos | Uno solo código 5 | El resumen lista **todos** los omitidos |

La regla es: **el código responde a «qué tipo de acción tomaría», y el mensaje
responde a «qué pasó exactamente»**. Cuando una distinción no cambia la acción,
no consume un código.

## Relacionado

- [Flujo general](00-vision-general.md)
- [Manejo de errores](../../criticas/manejo-de-errores.md)
- [Puertos e identidad](../domain.md#errores-centinela)