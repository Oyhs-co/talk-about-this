# Flujo: ingesta

**RF-01** · **TC-01** · `internal/application/ingest.go` ·
`internal/infrastructure/parsers/`

De un archivo en disco a un `Transcript`.

## Diagrama

```mermaid
flowchart TD
    START(["DesdeRuta(ctx, ruta)"]) --> CTX{"ctx cancelado?"}
    CTX -->|sí| E1["return ctx.Err()"]
    CTX -->|no| STAT["os.Stat(ruta)"]

    STAT -->|error| E2["«no se pudo leer el archivo»"]
    STAT -->|ok| INFO["info: ruta, nombre, tamaño"]

    INFO --> EXT["ExtensionDe(ruta)"]
    EXT --> SEL["selector.ParserParaExtension(ext)"]

    SEL --> HIT{"¿parser?"}
    HIT -->|sí| PARSE
    HIT -->|no| FALLBACK

    subgraph FALLBACK["La extensión miente"]
        F1["os.Open"] --> F2["bufio.Reader"]
        F2 --> F3["Lee hasta N bytes<br/><i>cabecera</i>"]
        F3 --> F4["selector.DetectarPorContenido(cabecera)"]
        F4 --> F5{"¿parser?"}
        F5 -->|sí| PARSE
        F5 -->|nil, nil| E3["Devuelve el error<br/>de EXTENSIÓN"]
    end

    subgraph PARSE["Parseo"]
        P1["parser.Parse(ctx, reader)"] --> P2{"¿error?"}
        P2 -->|sí| E4["propagar"]
        P2 -->|no| P3["texto plano"]
    end

    P3 --> VAC{"EsVacio()?"}
    VAC -->|sí| E5["Err: transcripción vacía"]
    VAC -->|no| OK(["✅ Transcript"])

    E1 --> ERR["ErrorEtapa{ingesta}"]
    E2 --> ERR
    E3 --> ERR
    E4 --> ERR
    E5 --> ERR

    classDef ok fill:#d4edda,stroke:#28a745
    classDef mal fill:#f8d7da,stroke:#dc3545
    class OK ok
    class E1,E2,E3,E4,E5,ERR mal
```

## Dos puntos de entrada

| Método | El archivo lo cierra | Uso |
|---|---|---|
| `DesdeRuta(ctx, ruta)` | **El caso de uso** | La CLI |
| `DesdeReader(ctx, r)` | El llamante | Tests, streams, `io.Reader` arbitrario |

### Por qué el `defer Close` va al principio

```mermaid
sequenceDiagram
    autonumber
    participant I as IngestTranscriptor
    participant OS as Sistema

    I->>OS: os.Open(ruta)
    OS-->>I: archivo
    Note over I: "defer f.Close()<br/>← AQUÍ, no al final"
    I->>I: Stat, extensión, cabecera
    I->>I: Parse(ctx, f)
    I-->>I: return transcript
    Note over I,OS: "el defer se ejecuta al salir"

    Note over OS: "TC-01 lo exige:<br/>«sin fugas de descriptores»"
```

Procesar cientos de documentos sin cerrarlos agota los descriptores del
proceso, y el fallo aparece en un archivo **distinto** al que lo provocó: es un
`too many open files` en el documento número 300 cuando el culpable fue el 12.

`TestTC01NoFugaDescriptores` abre y procesa muchas veces seguidas y verifica que
el número de descriptores no crece.

## Detección: extensión primero

```mermaid
flowchart LR
    subgraph A["Ruta A · extensión válida"]
        A1["notas.md"] --> A2["MarkdownParser"]
    end

    subgraph B["Ruta B · extensión no válida"]
        B1["minuta.txt"] --> B2["no hay parser"]
        B2 --> B3["leer cabecera"]
        B3 --> B4["firma PK\\x03\\x04"]
        B4 --> B5["DocxParser"]
    end

    classDef ok fill:#d4edda,stroke:#28a745
    classDef rutaB fill:#fff3cd,stroke:#ffc107
    class A2 ok
    class B2,B3,B4,B5 rutaB
```

**El orden importa.** Una implementación donde la detección por contenido se
ejecuta *siempre primero* interpretaría un `.txt` que empieza por `PK\x03\x04`
como un `.docx`. La detección existe para cuando la extensión **miente**, no
para competir con ella.

> Este fue el bug de la fase 2: la detección se ejecutaba, pero la extensión
> válida ganaba siempre, así que en la práctica el camino B **nunca se
 recorría**. El test de TC-01 pasaba y la función era código muerto.

## Qué hace cada parser

### `.txt` — `TextParser`

```mermaid
flowchart LR
    B["bytes"] --> BOM["Quitar BOM UTF-8"]
    BOM --> LF["Normalizar CRLF<br/>y CR a LF"]
    LF --> BAD["Sustituir UTF-8 inválido<br/>por U+FFFD"]
    BAD --> R["string"]

    R -.->|"len == 0"| E["error: vacío"]
```

### `.md` — `MarkdownParser`

```mermaid
flowchart TD
    M["markdown"] --> F{"¿front matter<br/>al principio?"}
    F -->|sí| DROP["eliminar bloque ---"]
    F -->|no| SCAN
    DROP --> SCAN["recorrer línea a línea"]

    SCAN --> INCODE{"¿dentro de<br/>código cercado?"}
    INCODE -->|sí| LITERAL["copiar LITERAL"]
    INCODE -->|no| CLEAN["limpiar marcas"]
    LITERAL --> SCAN
    CLEAN --> SCAN

    SCAN --> OUT["texto plano"]
```

**Lo que se conserva a propósito:**

| Elemento | Por qué |
|---|---|
| Bloques de código cercados | Su contenido es el requisito técnico; quitarlo lo destruye |
| `código inline` entre acentos graves | Igual |
| Reglas `---` que no están al principio | Un acta separa secciones así |

**Lo que se quita:** encabezados `#`, énfasis `**`/`*`, comillas `>`, viñetas
iniciales, y los enlaces se quedan como su texto.

> La cursiva `*a* *b*` es **simétrica**. Una implementación que empareja el
> primer `*` con el último convierte dos cursivas en otra cosa. Se resolvió con
> límites de palabra: una cursiva solo empieza tras un separador, y solo termina
> si el siguiente carácter vuelve a ser un separador.

### `.docx` — `DocxParser`

Un `.docx` es un ZIP con XML dentro.

```mermaid
flowchart TB
    F["archivo .docx"] --> SIGMA{"¿firma PK\\x03\\x04?"}
    SIGMA -->|no| ERR1["«no es un .docx»"]
    SIGMA -->|sí| LIMIT["leerBytesLimitados()<br/><i>tope de tamaño</i>"]
    LIMIT --> ZIP["zip.NewReader(ReaderAt, tamaño)"]

    ZIP --> FIND{"¿word/document.xml?"}
    FIND -->|no| ERR2["«ZIP sin word/document.xml»"]
    FIND -->|sí| OPEN["abrirParte()"]
    OPEN --> DEC{"xml.NewDecoder"}

    DEC --> P{"elemento"}
    P -->|"w:t"| T["texto"]
    P -->|"w:p"| NL["nueva línea"]
    P -->|"w:br"| NL
    P -->|"w:tab"| TAB["tabulador"]
    P -->|otro| SKIP["ignorar"]

    T --> ACC["acumular"]
    NL --> ACC
    TAB --> ACC
    ACC --> DEC

    classDef err fill:#f8d7da,stroke:#dc3545
    class ERR1,ERR2 err
```

#### Tres trampas encontradas aquí

**1. `zip.NewReader` no acepta `io.LimitReader`.**

Su firma exige `io.ReaderAt` **y un tamaño**, porque el índice central del ZIP se
lee de forma aleatoria. Un `LimitReader` no lo es. La solución fue un
`io.SectionReader` sobre un lector acotado, es decir, un `ReaderAt` con tamaño
conocido.

**2. Un `.docx` corrupto puede ser enorme.** Sin tope de tamaño, un fichero
abierto a mano con una cabecera ZIP válida puede hacer que el proceso consuma
memoria hasta morir.

**3. `encoding/xml.Decoder` token a token.** Construir el árbol entero de
`document.xml` —que puede tener varios megabytes— para extraer texto es
desperdicio. El decoder es un `io.Reader`: se avanza elemento a elemento.

## La marca de tiempo y RNF-03

```mermaid
sequenceDiagram
    participant P as Pipeline
    participant I as IngestTranscriptor

    Note over I: "ahora = time.Now<br/>inyectable"
    P->>I: DesdeRuta(ctx, ruta)
    I->>I: transcript.IngestedAt = ahora()
    I-->>P: Transcript
    Note over P: "duración total = time.Since(inicio)"
```

`NuevaIngestTranscriptorConReloj` permite fijar el reloj en los tests, que es lo
que hace comprobable el presupuesto de RNF-03 sin esperar de verdad.

Y aquí apareció un test frágil:

> `TestPipelineDryRunCompleto` afirmaba `resultado.Duracion > 0`. Con fakes, el
> pipeline entero termina en **menos de un milisegundo**, y la resolución del
> reloj de Windows devuelve `0` para `time.Since`. El test pasaba en Linux y
> fallaba en Windows, sin que hubiera ningún cambio de comportamiento.
>
> Ahora el extractor falso tiene un retardo programable de 25 ms y la aserción
> compara contra ese retardo conocido. El test verifica que **el pipeline mide**,
> no que el reloj tenga granularidad.

## Errores de esta etapa

| Situación | Error | Etapa | Código |
|---|---|---|---|
| Fichero inexistente | `os.Stat` envuelto | ingesta | 3 |
| Extensión y contenido desconocidos | `ErrFormatoNoSoportado` | ingesta | 3 |
| ZIP sin `document.xml` | error del parser | ingesta | 3 |
| Texto vacío tras parsear | `transcripción vacía` | ingesta | 3 |
| Contexto cancelado | `ctx.Err()` | ingesta | 130 |

## Lo que cubre TC-01

> **TC-01 — Ingesta DOCX.** Lectura de un `.docx` con párrafos y listas →
> retorno de un `string` UTF-8 limpio → integración sin pánicos ni fuga de
> descriptores.

| Criterio | Test |
|---|---|
| Retorno de texto UTF-8 limpio | `TestTC01IngestaDocxRetornaTextoLimpio` |
| Sin fugas de descriptores | `TestTC01NoFugaDescriptores` |
| Sin pánicos con entradas extrañas | `TestDocxRechazaArchivoQueNoEsZip`, `TestDocxRechazaZipSinWordDocument` |

## Relacionado

- [Flujo general](00-vision-general.md)
- [`internal/application`](../application.md#ingesttranscriptor)
- [`internal/infrastructure`](../infrastructure.md#parsers--de-documento-a-texto)