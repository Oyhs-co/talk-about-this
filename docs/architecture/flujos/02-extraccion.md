# Flujo: extracción con LLM

**RF-02, RF-03, RF-07** · **TC-02, TC-03** · `internal/application/extract_backlog.go` ·
`internal/infrastructure/llm/`

De un `Transcript` a un `MeetingBacklogExtraction` válido.

## El bucle completo

```mermaid
flowchart TD
    S(["Ejecutar(ctx, transcript)"]) --> C0{"ctx.Err()"}
    C0 -->|cancelado| E0(["return ctx.Err()"])
    C0 -->|ok| INT["intento = 1"]

    INT --> BUILD{"intento == 1?"}
    BUILD -->|sí| PC["prompt = Construir(transcript, schema)"]
    BUILD -->|no| PF["prompt = ConstruirCorreccion(transcript, schema, erroresAcumulados)"]

    PC --> LOG["log: intento, max, proveedor, errores previos"]
    PF --> LOG

    LOG --> CALL["GenerateStructuredOutput(ctx, prompt, schemaJSON)"]

    CALL --> PROV{"¿error del proveedor?"}
    PROV -->|"y ctx no está muerto"| PROP(["⛔ propaga<br/>«el proveedor X falló»"])
    PROV -->|"y ctx está muerto"| E0
    PROV -->|ok| VALID["validarRespuesta(crudo)"]

    VALID --> RES{"¿errores?"}
    RES -->|ninguno| OK(["✅ extracción válida"])
    RES -->|algunos| ACC["erroresAcumulados = append(...)"]
    ACC --> LAST{"¿intento == 3?"}
    LAST -->|sí| AGOT(["⛔ ErrReintentosAgotados<br/>+ último error concreto"])
    LAST -->|no| DORM["dormir(400 ms)<br/>respetando ctx"]
    DORM --> INT2["intento++"]
    INT2 --> BUILD

    classDef ok fill:#d4edda,stroke:#28a745
    classDef err fill:#f8d7da,stroke:#dc3545
    classDef int fill:#fff3cd,stroke:#ffc107

    class OK ok
    class E0,PROP,AGOT err
    class INT,INT2 int
```

## Tres reglas que sostienen el bucle

### 1. Un error del proveedor NO se reintenta aquí

```mermaid
sequenceDiagram
    participant E as ExtractBacklog
    participant L as Adaptador LLM
    participant C as Cliente HTTP

    E->>L: GenerateStructuredOutput(...)
    L->>C: POST
    C-->>L: 401 Unauthorized
    L-->>E: error

    Note over E: "propaga INMEDIATAMENTE<br/>no consume un intento"
    Note over C: "el cliente HTTP sí reintenta<br/>lo que sabe que es transitorio"
```

Repetir la misma llamada con la misma credencial inválida dará el mismo error
tres veces y consumirá tres veces el presupuesto. Lo que sí es transitorio —503,
429, corte de red— lo reintenta el cliente HTTP, que sabe distinguirlo del 401.

### 2. Los errores se acumulan

```mermaid
flowchart LR
    I1["intento 1"] --> E1["['falta meeting_summary']"]
    I2["intento 2"] --> E2["['falta meeting_summary',<br/>'priority no está en enum']"]
    I3["intento 3"] --> P["prompt con los DOS errores"]

    classDef acum fill:#d4edda,stroke:#28a745
    class P acum
```

Sustituir en vez de acumular obligaría al modelo a corregir de uno en uno, con un
intento por problema. Y el tercer intento, con dos problemas ya identificados,
puede corregirlos de golpe.

### 3. La espera no es decorativa

```mermaid
sequenceDiagram
    participant E as ExtractBacklog
    participant M as Modelo

    E->>M: intento 1, prompt + schema
    M-->>E: respuesta inválida
    Note over E: "dormir 400 ms"
    Note over M: "sampling distinto<br/>en cada llamada"
    E->>M: intento 2, prompt + errores
    M-->>E: respuesta correcta
```

La mayoría de los fallos de formato son **transitorios**: el mismo prompt con el
error adjunto produce una respuesta distinta en el siguiente muestreo.
Reiniciar el sampling **es** parte del mecanismo de corrección, no una pausa para
que el servidor respire.

En los tests, `dormir` es inyectable: la suite no paga las esperas reales.

## Las tres capas de validación

```mermaid
flowchart TD
    RAW["bytes crudos<br/>del LLM"] --> J["json.Unmarshal<br/>en map[string]any"]

    J --> C1

    subgraph c1["Capa 1 · JSON Schema"]
        C1A["tipos de dato"]
        C1B["campos obligatorios"]
        C1C["minLength / maxLength"]
        C1D["enum de prioridad"]
    end

    C1 -->|errores| ERR1["lista de mensajes<br/>con ruta JSON Pointer"]
    C1 -->|válido| NORM

    subgraph norm["Capa 2 · normalizarEnums"]
        NORM{"¿el valor es un enum<br/>del schema?"}
        NORM2["comparar en minúsculas<br/>y reescribir"]
    end

    NORM2 --> REDO["volver a validar<br/><b>sobre el documento ya normalizado</b>"]
    NORM2 --> NORM
    NORM --> C1
    NORM -->|"sin cambios"| C3

    C3["Capa 3 · dominio"] --> C3A["NewActionItem()<br/>invariantes de Go"]
    C3A --> OK(["✅ MeetingBacklogExtraction"])

    classDef ok fill:#d4edda,stroke:#28a745
    classDef mal fill:#f8d7da,stroke:#dc3545
    class OK ok
    class ERR1 mal
```

### Capa 2: por qué existe

| Lo que llega | Lo que el schema exige | Sin la capa 2 |
|---|---|---|
| `"high"` | `"HIGH"` | ✗ 3 intentos agotados |
| `"High"` | `"HIGH"` | ✗ 3 intentos agotados |
| `"urgent"` | `"HIGH" \| "MEDIUM" \| "LOW"` | ✗ rechazado — **correcto** |

La normalización **solo** toca valores que el schema declara como `enum`, y
**solo** convierte mayúsculas/minúsculas. No inventa prioridades: un `"urgente"`
se sigue rechazando, porque aceptar un valor que nadie pidió sería hacer que el
sistema publicara una urgencia inventada.

> **El error más común de un LLM con salida estructurada es el detalle de las
> mayúsculas.** Gastar los tres reintentos —con esperas de 400 ms entre ellos— en
> eso es absurdo.

### El segundo bug que escondía el primero

Al arreglar la normalización se descubrió que **la capa 3 deserializaba del
documento crudo**, no del ya normalizado:

```mermaid
flowchart LR
    subgraph antes["Antes — la normalización no arreglaba nada"]
        A1["crudo"] --> A2["Capa 1: falla ('high')"] --> A3["normalizar"] --> A4["Capa 3:<br/>deserializa de CRUDO"] --> A5["❌ vuelve a fallar"]
    end

    subgraph despues["Ahora"]
        B1["crudo"] --> B2["Capa 1: falla ('high')"] --> B3["normalizar"] --> B4["json.Marshal(documento)"] --> B5["Capa 3:<br/>deserializa de ahí"] --> B6["✅"]
    end

    classDef mal fill:#f8d7da,stroke:#dc3545
    classDef bien fill:#d4edda,stroke:#28a745
    class A5 mal
    class B6 bien
```

Un test de cobertura habría pasado en ambos casos: la función `normalizarEnums`
se ejecutaba. Lo que no se mide es si su **efecto** llegaba a la capa 3.

## Los cuatro proveedores

```mermaid
flowchart TB
    E["ExtractBacklog"] --> P["LLMProvider"]

    P --> O["Ollama"]
    P --> OA["OpenAI"]
    P --> AN["Anthropic"]
    P --> GE["Gemini"]

    O --> O1["POST /api/chat<br/>{model, messages, format: schema}"]
    O1 --> O2["lee message.content"]

    OA --> OA1["POST /chat/completions<br/>response_format: json_schema"]
    OA1 --> OA2["lee choices[0].message.content"]

    AN --> AN1["POST /v1/messages<br/>+ definición de herramienta"]
    AN1 --> AN2["lee bloque tool_use"]

    GE --> GE1["POST generateContent<br/>generationConfig.responseSchema"]
    GE1 --> GE2["lee candidates[0].content.parts[0].text"]

    classDef adapter fill:#fff3cd,stroke:#ffc107
    class O,OA,AN,GE adapter
```

### Por qué no es intercambiable de verdad

El puerto `LLMProvider` es idéntico para los cuatro. Debajo, cada API habla un
idioma distinto para pedir una salida estructurada, y **ese idioma no es
negociable**:

| Proveedor | Mecanismo | Restricción del schema |
|---|---|---|
| Ollama | `format` | Acepta el schema casi entero |
| OpenAI | `response_format` | Rechaza `additionalProperties` en algunos modelos |
| Anthropic | herramienta | `$schema`, `title`, `additionalProperties` no se permiten |
| Gemini | `responseSchema` | Tampoco `additionalProperties` ni `default` |

Por eso cada adaptador tiene su `esquemaParaXxx()`: **simplificar el schema es
responsabilidad del adaptador**, porque es lo que la API de destino no acepta.

Enviarlo tal cual produce un `400` con un mensaje que no dice qué campo sobra.

### La trampa de la respuesta

> `/api/chat` de Ollama devuelve el contenido en **`message.content`**. No en un
> campo `response`.
>
> Los dobles de test se escribieron leyendo la documentación equivocada, y
> aceptaron una forma `{"response": "..."}`. El smoke test contra un servidor
> real con la forma de `/api/chat` fue lo que destapó la diferencia: TC-02
> pasaba en verde contra el doble y fallaría contra cualquier Ollama del mundo.
>
> **Regla: un doble de test que imita una API debe imitar sus rarezas, no su
> forma ideal.**

## El prompt

```mermaid
flowchart TB
    subgraph P1["PromptBuilder.Construir<br/>(primer intento)"]
        A1["Instrucciones de rol"] --> Z1
        A2["Transcripción"] --> Z1
        A3["Schema JSON literal"] --> Z1
        Z1["Prompt completo"]
    end

    subgraph P2["PromptBuilder.ConstruirCorreccion<br/>(reintentos)"]
        B1["Instrucciones de rol"] --> Z2
        B2["Transcripción"] --> Z2
        B3["Schema JSON literal"] --> Z2
        B4["❌ Errores detectados:<br/>- falta meeting_summary"] --> Z2
        Z2["Prompt corregido"]
    end

    Z1 --> M["Modelo"]
    Z2 --> M

    classDef bien fill:#d4edda,stroke:#28a745
    class B4 bien
```

### El prompt por defecto, y el bug que tenía

`NewExtractBacklog` funciona sin inyectar prompt. La primera versión:

```go
func (promptPorDefecto) ConstruirCorreccion(transcript, schemaJSON string, errores []string) string {
    return transcript   // ← solo la transcripción
}
```

Se parecía a un método con un parámetro sin usar. Era un **reintento a ciegas**:
la misma petición con distinto muestreo y ninguna pista sobre qué había fallado.

```mermaid
flowchart LR
    subgraph malo["Reintento ciego"]
        M1["intento 1: 'responde con este schema'"] --> M2["❌ priority inválida"]
        M2 --> M3["intento 2: '¿algo más?'<br/><i>misma pregunta</i>"] --> M4["❌ lo mismo"]
        M4 --> M5["intento 3: idéntico"] --> M6["❌ agotado"]
    end

    subgraph bueno["Reintento con contexto"]
        B1["intento 1"] --> B2["❌ priority inválida"]
        B2 --> B3["intento 2:<br/>'priority debe ser HIGH, MEDIUM o LOW;<br/>llegó URGENTE'"] --> B4["✅"]
    end

    classDef mal fill:#f8d7da,stroke:#dc3545
    classDef bien fill:#d4edda,stroke:#28a745
    class M6 mal
    class B4 bien
```

Es exactamente lo que TC-03 pide («re-intento automático **con contexto de
error** antes de fallar») y lo que el test comprueba ahora:
`TestPromptPorDefectoConstruyeCorreccion` exige que el prompt de corrección sea
**más largo** que el inicial, porque lleva el diagnóstico.

## Errores de esta etapa

| Situación | Comportamiento | Etapa | Código |
|---|---|---|---|
| Red caída / credencial inválida | Se propaga, sin gastar intentos | extracción | 2 o 4 |
| Respuesta no conforme × 3 | `ErrReintentosAgotados` + último error | extracción | 4 |
| Respuesta vacía | Error del adaptador → reintento | extracción | 4 |
| Timeout del cliente | `ctx.Err()` → no reintenta | extracción | 4 o 130 |
| `Name()` vacío | No ocurre: los cuatro lo implementan | — | — |

## TC-02 y TC-03

> **TC-02 — Ollama Adapter.** Envío de prompt a Ollama local → estructura
> `MeetingBacklogExtraction` deserializada sin errores.
>
> **TC-03 — Retry Loop.** Respuesta con JSON corrupto → re-intento automático
> con contexto de error antes de fallar.

| Criterio | Test |
|---|---|
| Deserialización sin errores de JSON | `TestTC02OllamaDeserializaSinErrores` |
| El schema viaja al modelo | `TestOllamaEnviaElSchema` |
| Reintento con contexto de error | `TestPromptDeCorreccionIncluyeErrores`, `TestPromptPorDefectoConstruyeCorreccion` |
| Se respetan los 3 intentos | `TestReintentosAgotadosDevuelveCodigoDeExtraccion` |
| `context.WithTimeout` respetado | `TestClienteRespetaTimeout`, `TestClienteCancelaConContexto` |
| Prompt determinista | `TestPromptEsDeterminista` |

## Relacionado

- [Flujo general](00-vision-general.md)
- [`internal/application/extract_backlog.go`](../application.md#extractbacklog)
- [Manejo de errores](../../criticas/manejo-de-errores.md)