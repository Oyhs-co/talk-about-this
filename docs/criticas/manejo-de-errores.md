# Manejo de errores

## El compromiso

> **Todo error conserva su causa original a través de todas las capas, y se
> traduce a un código de salida que dice qué acción tomar.**

## Los tres niveles

```mermaid
flowchart TD
    A["1 · ORIGEN<br/><i>el adaptador sabe qué pasó</i><br/>«GitHub devolvió 401»"]
    B["2 · ETAPA<br/><i>el pipeline sabe dónde</i><br/>ErrorEtapa{despacho, err}"]
    C["3 · CÓDIGO<br/><i>la CLI sabe qué hacer</i><br/>6"]

    A -->|"errors.Is sigue funcionando"| B
    B -->|"errors.As encuentra la etapa"| C

    style A fill:#fff3cd
    style B fill:#d1ecf1
    style C fill:#d4edda
```

## Los errores centinela

Todos son `errors.New`, y se comparan con `errors.Is`.

```mermaid
flowchart TB
    subgraph cfg["Configuración"]
        C1["ErrConfigInvalida"]
    end

    subgraph datos["Datos"]
        D1["ErrPriorityInvalida"]
        D2["ErrTituloInvalido"]
        D3["ErrDescripcionInvalido"]
        D4["ErrAsignadoInvalido"]
        D5["ErrSchemaViolation"]
        D6["ErrFormatoNoSoportado"]
    end

    subgraph oper["Operación"]
        O1["ErrMapeoDeIdentidadInvalido"]
        O2["ErrIdentidadNoResuelta"]
        O3["ErrReintentosAgotados"]
        O4["ErrPublicacionFallida"]
    end

    cfg --> ACC
    datos --> ACC
    oper --> ACC

    ACC["¿Dónde se usan?"]

    style ACC fill:#e9ecef
```

| Familia | Se comparan con | Se convierten en |
|---|---|---|
| **Configuración** | La CLI | Código 2 |
| **Datos** | El validador → **prompt de corrección** | Reintento |
| **Operación** | La CLI y los tests | Códigos 4, 5, 6 |

Cada centinela lleva un comentario que explica **por qué existe**, no qué dice.
Un error sin contexto obliga a buscar el mensaje completo en el código.

## El envoltorio: `ErrorEtapa`

```go
type ErrorEtapa struct {
    Etapa PipelineEtapa
    Err   error
}

func (e *ErrorEtapa) Error() string { return fmt.Sprintf("fallo en la etapa de %s: %v", e.Etapa, e.Err) }
func (e *ErrorEtapa) Unwrap() error   { return e.Err }
```

### `Unwrap` es la pieza crítica

```mermaid
flowchart LR
    subgraph con["Con Unwrap"]
        C1["ErrReintentosAgotados"] --> C2["ErrorEtapa{extraccion, ·}"]
        C2 --> C3["errors.Is(err, ErrReintentosAgotados)<br/><b>true</b> ✅"]
    end

    subgraph sin["Sin Unwrap"]
        S1["ErrReintentosAgotados"] --> S2["ErrorEtapa{extraccion, ·}"]
        S2 --> S3["errors.Is(…)<br/><b>false</b> ❌"]
    end

    style C3 fill:#d4edda
    style S3 fill:#f8d7da
```

> Sin `Unwrap`, todo error de la etapa de despacho sería indistinguible, y la CLI
> no podría elegir entre código 4, 5 y 6.

### Tolera el envoltorio anidado

```mermaid
sequenceDiagram
    participant CLI
    participant E as error envuelto dos veces

    CLI->>CLI: errors.As(err, &etapa)
    E-->>CLI: ✅ encuentra ErrorEtapa<br/>aunque esté dentro de un errors.Join
    CLI->>CLI: errors.Is(err, causa)
    E-->>CLI: ✅ alcanza la causa
```

Un `errors.Join` o un envoltorio añadido por un logger no rompe nada: `errors.As`
recorre la cadena.

### Y tolera errores que no son suyos

| Entrada | `EtapaDelError` | `EsFalloDePublicacion` |
|---|---|---|
| `nil` | `""` | `false` |
| `errors.New("sin etapa")` | `""` | `false` |
| `domain.ErrConfigInvalida` | `""` | `false` |
| `ErrorEtapa{despacho}` | `"despacho"` | `true` |

La CLI llama a estas funciones con errores de configuración, que no llevan etapa.
Una función que entra en pánico ahí tumba el proceso justo cuando iba a dar un
mensaje útil.

## Los códigos de salida

```mermaid
flowchart TD
    CI{"código"} --> K4{"4"}
    K4 -->|sí| A["🔄 El modelo no colaboró"]
    K4 -->|no| K3{"3"}
    K3 -->|sí| B["🔧 El archivo falla"]
    K3 -->|no| K6{"6"}
    K6 -->|sí| C["🔄 La plataforma falla"]
    K6 -->|no| K5{"5"}
    K5 -->|sí| D["📝 Falta un mapeo"]
    K5 -->|no| K2{"2"}
    K2 -->|sí| E["⚙️ Falta configuración"]
    K2 -->|no| F["❓ Uso incorrecto"]

    style A fill:#fff3cd
    style C fill:#fff3cd
    style B fill:#d1ecf1
    style D fill:#d1ecf1
    style E fill:#d1ecf1
    style F fill:#e9ecef
```

| Código | Significado | Acción |
|---|---|---|
| 0 | Éxito, incluido el fallo parcial | Nada |
| 1 | Uso | Arreglar la línea de comandos |
| 2 | Configuración | Arreglar credenciales o ficheros |
| 3 | Ingesta | Arreglar el archivo |
| 4 | Extracción | Cambiar de modelo o de prompt |
| 5 | Identidad | Arreglar el mapeo |
| 6 | Publicación | Reintentar el comando |
| 130 | Interrumpido (Ctrl-C) | Nada |

Cada código señala una **categoría de causa** distinta, y por tanto una acción
distinta. Ese es el criterio para decidir si merece un código: **si la acción
cambia, necesita un código propio**.

### La etapa manda

```mermaid
flowchart LR
    E["ErrorEtapa{despacho,<br/>ErrQuePareceDeUso}"] --> K["codigoDeEtapa"]
    K --> R["6, no 1"]

    Note["la etapa describe DÓNDE;<br/>el error describe QUÉ.<br/>Para reintentar, importa dónde."]
```

## El fallo parcial no es un error

```mermaid
flowchart TD
    I["5 items"] --> P["4 creados, 1 fallido"]
    P --> S["ResumenDespacho:<br/>exitos=4, fallos=1,<br/>omitidos=[…]"]
    S --> C(["código 0 ✅"])

    style C fill:#d4edda
```

Devolver error haría que un pipeline de CI creyera que no se publicó nada, cuando
cuatro tarjetas existen. La información de lo que falló va en el resumen y en el
log.

## Los mensajes: para personas y para el LLM

```mermaid
flowchart LR
    subgraph persona["Para una persona"]
        P1["«no se encontró el proyecto<br/>número 7 en «organización»»"]
    end

    subgraph llm["Para el LLM"]
        L1["«action_items[0].priority:<br/>se esperaba uno de HIGH, MEDIUM, LOW;<br/>se recibió \"urgent\"»"]
    end

    style P1 fill:#d1ecf1
    style L1 fill:#fff3cd
```

Los mensajes de validación van al prompt del LLM. «Valor inválido» obligaría al
modelo a adivinar; con la ruta y el enum, sabe exactamente qué corregir.

## La higiene de los mensajes

| Regla | Motivo |
|---|---|
| Nombrar **qué** se esperaba y **qué** se recibió | Es lo que hace el mensaje accionable |
| Incluir la **ruta** (`action_items[2].priority`) | Localiza el campo |
| Nombrar el **proveedor** que falló | «el proveedor fallo» no dice cuál |
| **Truncar** los cuerpos de error | Una respuesta puede pesar megabytes |
| **Nunca** incluir credenciales | [Seguridad](seguridad-y-secretos.md) |
| Mencionar la **variable** que falta | «falta `OPENAI_API_KEY`» es accionable |

## Errores que se solapan entre paquetes

```mermaid
flowchart LR
    CLI["codigoDeError"] --> A["errors.Is(err,<br/>domain.ErrConfigInvalida)"]
    CLI --> B["errors.Is(err,<br/>adapters.ErrConfigInvalida)"]
    A --> R["código 2"]
    B --> R
```

`identity` y `adapters` definen cada uno su `ErrConfigInvalida`. No es duplicación
sin motivo: son paquetes que **no se importan entre sí**, y el del dominio está en
la frontera. Cuesta una línea en la CLI y evita un acoplamiento.

## La lista de errores está cerrada

```mermaid
flowchart TD
    ERR["error desconocido"] --> W{"¿es alguno<br/>de los conocidos?"}
    W -->|sí| CODE["código específico"]
    W -->|no| U["1 · uso"]

    style U fill:#fff3cd
```

Un error no reconocido se reporta como error de uso: es la elección conservadora,
porque significa «algo que no sabemos que hacer».

## Verificación

| Compromiso | Test |
|---|---|
| La causa sobrevive al envoltorio | `TestErrorEtapaEncadenaYExponeLaEtapa` |
| Tolera el envoltorio anidado | `TestErrorEtapaAguantaElEnvoltorioAnidado` |
| Tolera errores ajenos y `nil` | `TestEtapaDelErrorToleraErroresAjenos` |
| Cada etapa tiene su código | `TestCodigoDeEtapaMapeaCadaEtapaASuCodigo` |
| La etapa desconocida delega | `TestCodigoDeEtapaDesconocidaDelegaEnElError` |
| Los códigos son distintos | `TestCodigosDeSalidaSonDistintos` |
| Funcionan con error envuelto | `TestCodigoDeErrorEncadenadoConEtapa` |
| La tabla de errores | `TestCodigoDeError`, `TestCodigoDeErrorEncadenados` |
| `--help` devuelve 0 | `TestCodigoDeErrorDevuelveOKParaAyuda` |
| Un error desconocido da 1 | `TestCodigoDeErrorParaErroresDesconocidos` |
| El fallback parcial no es error | `TestFalloParcialNoEsErrorGlobal` |
| El error del validador es accionable | `TestValidarNombraElTipoEsperadoEnLosMensajes` |
| El último error concreto se conserva | `TestReintentosAgotadosDevuelveCodigoDeExtraccion` |

```bash
go test ./internal/application/ -run 'ErrorEtapa|EtapaDelError' -v
go test ./cmd/talkaboutthis/ -run 'Codigo' -v
```

## Documentos relacionados

- [Flujo de errores](../architecture/flujos/05-errores-y-codigos.md) — el diagrama completo.
- [Errores centinela del dominio](../architecture/domain.md#errores-centinela)
- [Reintentos y timeouts](reintentos-y-timeouts.md) — cuándo se reintenta.