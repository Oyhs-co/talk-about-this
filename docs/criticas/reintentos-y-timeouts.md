# Reintentos y timeouts

**RNF-03**

## El compromiso

> **Solo se reintenta lo que puede mejorar con la espera, y toda espera respeta el
> contexto.**

## Los dos reintentos, que no son el mismo

```mermaid
flowchart TB
    subgraph alto["Capa ALTA — el Retry Loop (ExtractBacklog)"]
        A1["¿La respuesta cumple<br/>el esquema?"]
        A2["Envía los errores<br/>al prompt"] --> A1
        A1 -->|3 intentos| A3["❌ ErrReintentosAgotados"]
    end

    subgraph bajo["Capa BAJA — el Cliente HTTP"]
        B1["¿La petición falló?"]
        B2["backoff"] --> B1
        B1 -->|agotado| B3["Error con mensaje útil"]
    end

    P["ExtractBacklog"] -->|"NO delega"| A1
    Q["Cliente HTTP"] --> B1
    P -.->|"un fallo del proveedor<br/>NO se reintenta aquí"| Q

    style A1 fill:#fff3cd
    style B1 fill:#d1ecf1
    style P fill:#e9ecef
```

| | Retry loop (alto) | Cliente HTTP (bajo) |
|---|---|---|
| **Reintenta** | Respuesta que no cumple el schema | 429, 5xx, fallo de red |
| **No reintenta** | Error del proveedor | 4xx |
| **Espera** | 400 ms fijos | Backoff exponencial |
| **Al agotarse** | `ErrReintentosAgotados` + último error | Error con código HTTP |
| **Modifica el prompt** | **Sí** | No |

## Por qué un error del proveedor no se reintenta arriba

```mermaid
sequenceDiagram
    participant E as ExtractBacklog
    participant L as Adaptador
    participant C as Cliente HTTP

    E->>L: GenerateStructuredOutput
    L->>C: POST
    C-->>L: 401 Unauthorized
    C-->>C: "¿4xx? → no reintento"
    L-->>E: error
    Note over E: "propago INMEDIATAMENTE<br/>no gasto un intento"
```

Repetir la misma llamada con la misma credencial inválida da el mismo error tres
veces, consume tres esperas de 400 ms y tres intentos del presupuesto, y vuelve a
fallar.

**Lo transitorio lo sabe mejor el cliente HTTP**, que sabe distinguir un 503 de un
401. La separación es deliberada: cada capa reintenta lo que ella entiende.

## El retry loop: cómo funciona

```mermaid
stateDiagram-v2
    [*] --> Intento1 : Construir(transcript, schema)

    Intento1 --> Valido
    Valido --> [*] : éxito

    Intento1 --> Acumular : errores
    Acumular --> Espera : 400 ms
    Espera --> Correccion : ConstruirCorreccion(·, ·, errores)
    Correccion --> Valido
    Correccion --> Acumular : más errores

    Acumular --> Agotado : intento == 3
    Agotado --> [*] : ErrReintentosAgotados

    note right of Espera
        dormirRespetandoContexto:
        select { case <-ctx.Done(): case <-timer.C: }
    end note
```

| Parámetro | Valor | Por qué ese valor |
|---|---|---|
| `MaxIntentosExtraccion` | **3** | Uno original y dos correcciones |
| `EsperaEntreIntentos` | **400 ms** | Reiniciar el sampling es parte de la corrección |
| Los errores | **Acumulados** | Un reintento puede corregir varios problemas |
| El último error concreto | **Conservado** | Para que el usuario sepa QUÉ falló |

### Por qué 400 ms y no 0

La mayoría de los fallos de formato son **transitorios**: el mismo prompt con el
error adjunto produce una respuesta distinta en el siguiente muestreo. Sin pausa,
los tres intentos serían casi la misma llamada tres veces.

Y por qué no más: a partir de unos segundos, esperar cuesta más que devolver el
error y dejar que el usuario decida. Tres es el punto donde seguir reintentando
cuesta más que devolver el error.

### Por qué se acumulan los errores

```mermaid
flowchart LR
    subgraph sub["Sustituir"]
        A1["intento 1: 'falta meeting_summary'"] --> A2["intento 2: solo ve<br/>«priority inválido»"] --> A3["intento 3: solo ve eso"]
    end

    subgraph acum["Acumular"]
        B1["intento 1: 'falta meeting_summary'"] --> B2["intento 2: ve los DOS:<br/>'falta meeting_summary'<br/>'priority inválido'"] --> B3["intento 3: corrige<br/>de golpe ✅"]
    end

    style B3 fill:#d4edda
```

## La espera respeta el contexto

```go
func dormirRespetandoContexto(ctx context.Context, d time.Duration) error {
    select {
    case <-ctx.Done():
        return ctx.Err()
    case <-time.After(d):
        return nil
    }
}
```

```mermaid
sequenceDiagram
    participant E as ExtractBacklog
    participant D as dormir

    E->>D: dormir(400 ms)
    alt el contexto se cancela
        U->>E: Ctrl-C
        D-->>E: ctx.Err()
        E-->>U: sale con 130 ✅
    else pasan 400 ms
        D-->>E: nil
        E->>E: siguiente intento
    end
```

Un `time.Sleep(400 * time.Millisecond)` a secas haría que un Ctrl-C tardara medio
segundo en notarse. Con `--timeout 1s` y tres intentos, casi tres segundos.

`dormir` además es **inyectable**: los tests no pagan esperas reales.

## El presupuesto de RNF-03: 15 minutos

```mermaid
gantt
    dateFormat X
    axisFormat %s

    section Escenario normal
    Ingesta                    :0, 1
    Extracción 1 intento       :1, 12
    Espera                     :13, 1
    Extracción 2 intento       :14, 12
    Identidades                :26, 1
    Publicación 20 items       :27, 3

    section Peor caso (3 intentos)
    Extracción 3 intentos      :0, 40
    Publicación 20 items       :40, 5
```

| Tramo | Típico | Peor caso |
|---|---|---|
| Ingesta | 50 ms | 500 ms |
| Una extracción | 2–15 s | 60 s |
| Tres intentos | 6–45 s | 180 s |
| Publicación de 20 items | 2 s | 30 s |
| **Total** | **10–60 s** | **~4 min** |

El presupuesto de 15 minutos es un **techo de seguridad**, no una restricción: existe
para detectar un LLM colgado. Los 400 ms entre intentos son ruido a esa escala.

## Los timeouts

```mermaid
flowchart LR
    CLI["--timeout 1m"] --> EF["timeoutEfectivo()"]
    EF --> SOLO{"¿< 5 s?"}
    SOLO -->|sí| PISO["max(valor, 5 s)"]
    SOLO -->|no| OK["el valor dado"]
    PISO --> AP["aplicado por petición"]
    OK --> AP

    style PISO fill:#fff3cd
```

**El suelo de 5 s.** `--timeout 1ms` no significa «quiero fallar rápido»: significa
«quiero esperar poco». Sin suelo, un valor accidentalmente pequeño produce una
batería de fallos instantáneos en vez de una espera, y el diagnóstico apunta al
problema equivocado.

El timeout se aplica **por petición**, no por flujo, y lo hereda el contexto: un
`ctx` con deadline de 5 s corta tanto la espera como la llamada.

## `EsReintentable`

```mermaid
flowchart TD
    E["error de la API"] --> K{"¿qué status?"}
    K -->|429| Y["✅ reintentable<br/><i>límite de cuota</i>"]
    K -->|500-599| Y
    K -->|red / timeout| Y
    K -->|401| N["❌ no reintentable<br/><i>credencial</i>"]
    K -->|403| N
    K -->|404| N
    K -->|422| N

    style Y fill:#d4edda
    style N fill:#f8d7da
```

Esperar a un 401 no lo convierte en 200. Y reintentar un 403 tres veces solo gasta
tiempo antes de decir lo mismo.

## Verificación

| Compromiso | Test |
|---|---|
| Los transitorios se reintentan | `TestClienteReintentaErroresTransitorios` |
| Los no transitorios no | `TestClienteNoReintentaErroresNoTransitorios` |
| `EsReintentable` | `TestEsReintentable` |
| El timeout se respeta | `TestClienteRespetaTimeout` |
| El contexto cancela | `TestClienteCancelaConContexto` |
| Contexto cancelado en el parser | `TestTxtRespetaContextoCancelado` |
| Contexto cancelado en la extracción | `TestPublishRespetaContextoCancelado` |
| Contexto cancelado en el tablero | `TestConsultarProjectIDRespetaContextoCancelado` |
| Contexto cancelado en la CLI | `TestContextoCancelado` |
| Tres intentos y se rinde | `TestReintentosAgotadosDevuelveCodigoDeExtraccion` |
| El prompt lleva los errores | `TestPromptDeCorreccionIncluyeErrores` |
| El prompt por defecto también | `TestPromptPorDefectoConstruyeCorreccion` |
| El rate limit se identifica | `TestRateLimitSeIdentifica` |
| La duración se mide | `TestPipelineDryRunCompleto` |

```bash
go test ./internal/infrastructure/llm/ -run 'Reintenta|Timeout|Cancela' -v
go test ./internal/application/ -run 'Reintentos|PromptDeCorreccion' -v
```

### El fallo parcial no se reintenta

Un item que falla al publicar **no se reintenta**: `PublishBacklog` propaga el error
del tablero y el resumen registra el fallo. Reintentar una creación de issue tiene
un riesgo real de duplicado: si la primera llamada creó la tarjeta y la respuesta se
perdió, un reintento crea una segunda.

## Documentos relacionados

- [Flujo de extracción](../architecture/flujos/02-extraccion.md)
- [Manejo de errores](manejo-de-errores.md)
- [RFC-03 en `INIT.md`](../../INIT.md)