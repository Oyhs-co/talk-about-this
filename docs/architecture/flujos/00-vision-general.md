# Flujo de trabajo general

Un comando, de principio a fin.

```mermaid
flowchart TD
    START(["👤 talkaboutthis ingest --file reunion.md"]) --> A1

    subgraph fase1["1 · Preparación"]
        A1["Parsear flags"] --> A2["Registrar cuáles se enviaron"]
        A2 --> A3["Aplicar entorno<br/><i>flag &gt; env &gt; default</i>"]
        A3 --> A4["completar()"]
        A4 --> A5["validar()"]
        A5 --> A6["cargarMappings(ruta, modo)"]
        A6 --> A7["Construir logger"]
        A7 --> A8["logger → contexto"]
    end

    A8 --> B1

    subgraph fase2["2 · Ingesta"]
        B1["os.Stat(ruta)"] --> B2{"extensión<br/>conocida?"}
        B2 -->|sí| B3["elegir parser"]
        B2 -->|no| B4["leer cabecera<br/>y detectar"]
        B4 --> B5{"reconocido?"}
        B5 -->|no| BX["❌ código 3"]
        B5 -->|sí| B3
        B3 --> B6["Parsear → texto plano"]
        B6 --> B7{"vacío?"}
        B7 -->|sí| BX
        B7 -->|no| B8["Transcript"]
    end

    B8 --> C1

    subgraph fase3["3 · Extracción"]
        C1["Construir prompt"] --> C2["Llamar al proveedor"]
        C2 --> C3{"respuesta<br/>válida?"}
        C3 -->|sí| C7["Backlog"]
        C3 -->|no| C4["Acumular errores"]
        C4 --> C5{"¿3 intentos?"}
        C5 -->|no| C6["Prompt de corrección<br/>+ dormir 400 ms"]
        C6 --> C2
        C5 -->|sí| CX["❌ código 4"]
    end

    C7 --> D1

    subgraph fase4["4 · Identidades"]
        D1{"modo"}
        D1 -->|dry-run| D9["Sin tocar nada"]
        D1 -->|publish| D2["Normalizar cada nombre"]
        D2 --> D3{"¿en la tabla?"}
        D3 -->|sí| D4["MappedHandle"]
        D3 -->|no| D5{"política"}
        D5 -->|fail| DX["❌ código 5"]
        D5 -->|assign_unassigned| D6["sin responsable"]
        D5 -->|skip| D7["a omitidos"]
        D6 --> D8["items preparados"]
        D7 --> D8
        D4 --> D8
    end

    D8 --> E1

    subgraph fase5["5 · Publicación"]
        E1["cache: repo y tablero"] --> E2["Crear issues<br/><i>máx. 4 en paralelo</i>"]
        E2 --> E3["Añadir al tablero"]
        E3 --> E4["Asignar responsables"]
        E4 --> E5["Contar éxitos y fallos"]
    end

    D9 --> OUT
    E5 --> OUT["📄 JSON o tabla"] --> FIN(["código 0"])

    classDef ok fill:#d4edda,stroke:#28a745
    classDef mal fill:#f8d7da,stroke:#dc3545
    classDef etapa fill:#d1ecf1,stroke:#17a2b8

    class FIN,OUT ok
    class BX,CX,DX mal
    class A1,C1,D1,E1 etapa
```

## Diagrama de secuencia

```mermaid
sequenceDiagram
    autonumber
    actor U as Usuario
    participant CLI as cmd/talkaboutthis
    participant CFG as config.go
    participant LOG as logging
    participant PIPE as Pipeline
    participant ING as IngestTranscriptor
    participant PAR as parsers.Registry
    participant EXT as ExtractBacklog
    participant LLM as llm.Adapter
    participant VAL as jsonschema
    participant PUB as PublicarBacklog
    participant MAP as identity.Mapper
    participant BRD as adapters.GitHubGraphQL

    U->>CLI: ingest --file reunion.md
    CLI->>CFG: registrarFlags + Parse
    CLI->>CFG: aplicarEntorno → completar → validar
    CLI->>CFG: cargarMappings
    CLI->>LOG: Nuevo(level, format)
    CLI->>PIPE: ctx = ConLoggerEnContexto(ctx, logger)

    PIPE->>ING: DesdeRuta(ctx, ruta)
    ING->>PAR: ParserParaExtension(".md")
    PAR-->>ING: MarkdownParser
    ING->>PAR: Parse(ctx, reader)
    PAR-->>ING: "texto plano"
    ING-->>PIPE: Transcript

    PIPE->>EXT: Ejecutar(ctx, transcript)
    loop hasta 3 intentos
        EXT->>LLM: GenerateStructuredOutput(prompt, schema)
        LLM-->>EXT: bytes
        EXT->>VAL: Validar(documento, schema)
        VAL-->>EXT: errores (o ninguno)
    end
    EXT-->>PIPE: MeetingBacklogExtraction

    alt modo dry-run
        PIPE->>PUB: Ejecutar(ctx, ref, items, dry-run)
        PUB-->>PIPE: items sin tocar
    else modo publish
        PIPE->>PUB: Ejecutar(ctx, ref, items, publish)
        PUB->>MAP: ResolveHandle(ctx, nombre)
        MAP-->>PUB: handle o error
        PUB->>BRD: PublishBacklog(ctx, ref, preparados)
        BRD-->>PUB: []PublishResult
        PUB-->>PIPE: ResumenDespacho
    end

    PIPE-->>CLI: PipelineResultado
    CLI-->>U: salida + código de salida
```

## Línea de tiempo de una ejecución

```mermaid
gantt
    dateFormat X
    axisFormat %s

    section Preparación
    Flags y entorno          :0, 2
    Validación y mappings    :2, 3

    section Ingesta
    Stat y selección parser  :5, 5
    Lectura del documento    :10, 15

    section Extracción
    Intento 1                :25, 800
    Espera 400 ms            :825, 1
    Intento 2                :826, 800
    Espera 400 ms            :1626, 1
    Intento 3                :1627, 800

    section Publicación
    Resolver identidades     :2427, 5
    Crear issues (paralelo)  :2432, 400
```

**El tiempo lo domina el LLM**, no el código. Para una meeting de una hora, la
extracción tarda de 2 a 15 segundos; la publicación de 20 items, unos 2 segundos
con concurrencia 4.

RNF-03 fija un presupuesto de **15 minutos**, muy por encima: el límite existe
para detectar un LLM colgado, no para ser una restricción real.

## Los cuatro puntos donde el flujo puede morir

```mermaid
flowchart LR
    subgraph P1["Etapa: ingesta"]
        A1["archivo ausente"]
        A2["formato desconocido"]
        A3["documento vacío"]
    end

    subgraph P2["Etapa: extracción"]
        B1["proveedor caído"]
        B2["credencial inválida"]
        B3["3 intentos sin respuesta válida"]
        B4["timeout"]
    end

    subgraph P3["Etapa: despacho"]
        C1["token sin scope project"]
        C2["tablero no encontrado"]
        C3["rate limit"]
        C4["fallo parcial"]
    end

    P1 --> R1["código 3"]
    P2 --> R2["código 4"]
    P3 --> R3["código 6"]
    P3 --> R4["código 0 con<br/>resumen de fallos"]

    classDef mal fill:#f8d7da,stroke:#dc3545
    classDef ok fill:#d4edda,stroke:#28a745
    class R1,R2,R3 mal
    class R4 ok
```

**El fallo parcial es el único que devuelve 0.** Y es deliberado: si se
publicaron tres de cinco tarjetas, el proceso *ha tenido éxito* y el resumen
informa de lo que falta. Devolver error haría que un pipeline de CI creyera que
no se publicó nada.

## Próximos documentos

| Flujo | Documento |
|---|---|
| Ingesta | [01-ingesta.md](01-ingesta.md) |
| Extracción | [02-extraccion.md](02-extraccion.md) |
| Identidades | [03-identidades.md](03-identidades.md) |
| Publicación | [04-publicacion.md](04-publicacion.md) |
| Errores | [05-errores-y-codigos.md](05-errores-y-codigos.md) |