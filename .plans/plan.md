# Plan de Ejecución — TalkAboutThis

> Plan derivado de `INIT.md` §9 (Hitos 1–5) y de la matriz de evaluación §8 (TC-01 … TC-07).
> Cada fase es independiente y verificable. **No se avanza de fase hasta cumplir el criterio de salida.**
> Contrato de desarrollo aplicable: [`AGENTS.md`](../AGENTS.md).

**Estado actual:** producto completo. **Fases 1 a 5: COMPLETADAS.**
**Estado:** 310 tests en verde en 11 paquetes, cero dependencias externas, cobertura global 93,4%, dominio al 100%. Documentación completa en `docs/` (4 familias, 39 documentos, Mermaid embebido).

---

## Mapa de dependencias

```text
        Fase 1 (Dominio + Contratos)
                    │
        ┌───────────┴───────────┐
        ▼                       ▼
   Fase 2 (Parsers)     Fase 3 (LLM + Retry Loop)
        └───────────┬───────────┘
                    ▼
            Fase 4 (Identidad + GitHub Projects)
                    │
                    ▼
              Fase 5 (CLI + Orquestación)
```

Las fases 2 y 3 pueden desarrollarse **en paralelo**: solo dependen de los contratos de la Fase 1.

---

## Fase 1 — Estructura base y dominio ✅ COMPLETADA

**Objetivo:** dejar el núcleo compilable y los contratos cerrados. Ningún pixel de infraestructura todavía.

### Resultado

| Entregable | Archivo | Estado |
| --- | --- | --- |
| Errores sentinela | `internal/domain/errors.go` | Hecho (10 sentinelas) |
| Entidades e invariantes | `internal/domain/models.go` | Hecho (100% cubierto) |
| 4 puertos | `internal/domain/ports.go` | Hecho |
| Entidades de Ingestión | `internal/domain/transcript.go` | Hecho |
| Guardas arquitectónicas | `internal/domain/architecture_test.go` | Hecho (3 guardas) |
| Sincronía SDD | `internal/domain/schema_sync_test.go` | Hecho (4 tests) |
| Schema embebido | `docs/specifications/embed.go` | Hecho |
| Composition root | `cmd/talkaboutthis/main.go` | Hecho (CLI real en Fase 5) |

**Verificación:** `gofmt` limpio, `go vet` limpio, `go build` OK, **dominio al 100% de cobertura**, total del repo 93.2%.

**Decisiones tomadas en esta fase:**
- `go:embed` no alcanza `docs/` desde `cmd/`, asi que el embed vive en `docs/specifications/embed.go` y expone `BacklogSchema()`/`Validar()`. Evita mantener una copia del schema que pueda divergir.
- Las guardas arquitectónicas usan `go/parser` en lugar de `go/build`: este ultimo falla con rutas POSIX en Windows y no hace falta resolver paquetes para leer imports.
- Se decidio que una prioridad desconocida produce error, nunca `MEDIUM` por defecto. Cubierto por `TestPrioridadInvalidaNoSeSustituyeEnSilencio`.
- Se elimino un `TranscriptPort` que se habia añadido sin uso: codigo muerto en el nucleo es peor que su ausencia.

### Tareas

1. `internal/domain/models.go` — `Priority`, `ActionItem`, `MeetingBacklogExtraction`, `PublishResult`, `ExtractionJob`. ✅
2. `internal/domain/ports.go` — las 4 interfaces de `INIT.md` §5.1. ✅
3. `internal/domain/errors.go` — errores sentinela con `errors.New`. ✅
4. `docs/specifications/backlog_schema.json` — existe y validado contra los struct tags. ✅
5. Test de guarda de la regla de imports (§1 de `AGENTS.md`). ✅
6. `cmd/talkaboutthis/main.go` mínimo que compila. ✅

### Tests

| ID | Qué se verifica | Resultado |
| --- | --- | --- |
| TC-07 (parcial) | `domain` no importa `infrastructure` | ✅ 3 guardas, verificadas por inyección de un import prohibido |
| RNF-02 | Tags `json` coinciden con el schema campo a campo | ✅ |
| RNF-02 | Enum de `priority` del schema == constantes del dominio | ✅ verificado por desincronización |
| RNF-02 | `maxLength` del schema == `MaxTituloLen` == tag `validate` | ✅ los tres sitios verificados |
| — | `Priority` rechaza valores fuera del enum | ✅ |
| — | `domain` solo usa stdlib y no hace I/O de red | ✅ |

### Criterio de salida — cumplido

```bash
go build ./...   # OK
go vet ./...     # limpio
go test ./...    # ok en los 3 paquetes
gofmt -l .       # sin salida
go tool cover -func=coverage.out | awk '$3 != "100.0%"'  # domain 100%
```

---

## Fase 2 — Parsers e ingesta ✅ COMPLETADA

**Objetivo:** cualquier `.md` / `.txt` / `.docx` → un `Transcript` limpio. (RF-01, TC-01)

### Resultado

| Entregable | Archivo | Estado |
| --- | --- | --- |
| Lectura y limpieza compartida | `internal/infrastructure/parsers/text.go` | Hecho |
| Parser texto plano | `parsers/txt.go` | Hecho |
| Parser Markdown | `parsers/markdown.go` | Hecho |
| Parser DOCX (OOXML) | `parsers/docx.go` | Hecho |
| Registro de parsers | `parsers/registry.go` | Hecho |
| Caso de uso de ingesta | `internal/application/ingest.go` | Hecho |
| Fixtures | `testdata/{reunion-equipo.md,notas-rapidas.txt,minuta.docx}` | Hecho |
| Generador del fixture .docx | `scripts/make_docx_fixture.py` | Hecho |
| Tests | `parsers_test.go`, `application/ingest_test.go` | Hecho |

**Verificación:** `gofmt` limpio, `go vet` limpio, **137 tests en verde**, parsers al 93.1%, application al 85.2%, **cero dependencias externas**.

### Decisiones tomadas en esta fase

- **DOCX con `archive/zip` + `encoding/xml`, sin librerias de terceros.** Para este subconjunto de OOXML la dependencia externa no compensaba, y mantiene el objetivo de binario ligero de `INIT.md` §1. `go.mod` sigue sin un solo `require`.
- **Los bloques de codigo Markdown se preservan intactos.** Una minuta que cita SQL o comandos contiene la especificacion exacta de la tarea; aplanarla produciria items inutiles.
- **Los guiones bajos simples no se eliminan.** `snake_case` es codigo real y aparece mas que el cursivo en una transcripcion tecnica. Decision consciente, documentada en el codigo y protegida por test.
- **La deteccion por contenido va ANTES que la extension.** Este orden es contraintuitivo y resulto ser la correccion mas importante de la fase (ver abajo).
- **`<w:br/>` se conserva como salto de linea**, no como espacio. Aplanarlo producia frases sin puntuacion ("decision tomada Se descarta...") que el LLM interpreta peor.
- **El fixture `.docx` se genera con un script**, no se versiona opaco: el XML que se parsea queda visible y auditable en `scripts/make_docx_fixture.py`.

### Bugs reales encontrados y corregidos durante la fase

1. **La deteccion por contenido nunca se ejecutaba.** `seleccionarParser` consultaba primero la extension y solo caia al contenido si la extension fallaba. Pero el caso que motiva la funcion —un `.docx` exportado desde Word guardado como `.txt`— tiene una extension PERFECTAMENTE VALIDA, asi que el parser de texto plano ganaba y devolvia bytes comprimidos como prosa. Se invirtio el orden: primero contenido, luego extension. Cubierto por `TestIngestaDocxConExtensionIncorrecta`.
2. **Cursiva Markdown asimetrica.** La heuristica de asteriscos simples quitaba la apertura de `*Ana*` y dejaba el cierre, produciendo `Ana* revisara`. Se reescribio con deteccion de limites de palabra, simetrica para apertura y cierre, sin tocar `2*3`. Cubierto por `TestMarkdownCursivaSimetrica`.
3. **`zip.NewReader` con `io.LimitReader`** no compila: el primero exige `io.ReaderAt` y el segundo solo devuelve `io.Reader`. Se leen los bytes con techo y se pasa un `bytes.Reader`.

### Tests

| ID | Qué se verifica | Estado |
| --- | --- | --- |
| TC-01 | `.docx` real → texto UTF-8 limpio, sin marcado XML residual | ✅ |
| TC-01 | Sin fuga de descriptores (200 documentos seguidos) | ✅ |
| — | `.md`: front-matter eliminado, formato quitado, codigo preservado | ✅ |
| — | `.txt`: BOM eliminado, CRLF normalizado, UTF-8 invalido tolerado | ✅ |
| — | Extension desconocida → `ErrFormatoNoSoportado` con formatos soportados listados | ✅ |
| — | `.docx` renombrado a `.txt` → detectado por contenido | ✅ |
| TC-07 | Registrar un parser nuevo sin tocar codigo de produccion | ✅ |
| RNF-03 | Ingesta dentro del presupuesto de tiempo | ✅ |

### Criterio de salida — cumplido

```bash
go build ./...   # OK
go vet ./...     # limpio
go test ./...    # ok en los 5 paquetes
gofmt -l .       # sin salida
```

---

## Fase 3 — Proveedores de LLM y Retry Loop ✅ COMPLETADA

**Objetivo:** extracción estructurada confiable, con auto-corrección. (RF-02, RF-03, RF-07, TC-02, TC-03)

### Resultado

| Entregable | Archivo | Estado |
| --- | --- | --- |
| Validador de JSON Schema | `internal/infrastructure/jsonschema/validator.go` | Hecho (subconjunto draft-07, sin dependencias) |
| Cliente HTTP compartido | `internal/infrastructure/llm/client.go` | Hecho (timeout, tope de tamaño, backoff) |
| Provider Ollama | `internal/infrastructure/llm/ollama.go` | Hecho |
| Provider OpenAI | `internal/infrastructure/llm/openai.go` | Hecho |
| Provider Anthropic | `internal/infrastructure/llm/anthropic.go` | Hecho (vía tools) |
| Provider Gemini | `internal/infrastructure/llm/gemini.go` | Hecho (schema simplificado) |
| Constructor de prompts | `internal/infrastructure/llm/prompt.go` | Hecho |
| Retry Loop con autocorrección | `internal/application/extract_backlog.go` | Hecho |

**Verificación:** `gofmt` limpio, `go vet` limpio, **193 tests en verde**, llm al 91.7%, application al 87.5%, **cero dependencias externas**.

### Corrección respecto al plan original

El plan ubicaba el Retry Loop en `internal/infrastructure/llm/retry.go`. Se implementó en **`internal/application/extract_backlog.go`** porque en la Fase 1 el contrato de `domain.LLMProvider` ya establecía que la validación y el Retry Loop son responsabilidad de la capa de aplicación (el adaptador solo devuelve bytes). Implementarlo en `llm` habría roto la dirección de imports documentada.

Por el mismo motivo, el constructor de prompts se implementa en `llm` pero se consume en `application` mediante la interfaz `ConstructorDePrompt`, inyectada desde el composition root. Es el mismo patrón que `SelectorDeParser` en la Fase 2.

### Decisiones tomadas en esta fase

- **Validador de JSON Schema propio (subconjunto draft-07).** Una dependencia como `santhosh-tekuri/jsonschema` habría traido cientos de líneas para un uso de ~200. `go.mod` sigue sin un solo `require`. `ParseSchema` además reporta las palabras clave que **no** aplica, para que una regla no verificada no pase por verificada.
- **La validación tiene tres capas**, porque cada una atrapa un fallo distinto: (1) sintaxis JSON, (2) forma según el schema, (3) invariantes del dominio. La tercera existe porque el schema no puede expresar que un título en blanco no es una tarea.
- **Se acumulan TODOS los errores de validación**, no solo el primero: corregirlos de uno en uno agota los tres intentos.
- **Un fallo del proveedor NO se reintenta con el mismo prompt.** Un error de red o de credencial no se arregla reformulando la petición; se propaga de inmediato y lo reintenta la política de red del cliente HTTP.
- **Anthropic usa `tools`, no `response_format`.** Su API no expone json_schema; la salida estructurada llega en el bloque `tool_use`.
- **Gemini normaliza el schema.** Su `responseSchema` acepta un subconjunto reducido y descarta en silencio lo que no entiende, así que se convierte antes de enviarlo. La validación fuerte sigue ocurriendo en la capa de aplicación.
- **El backoff es configurable, no una constante de paquete.** Con 500 ms fijos, tres tests de reintento pagaban 4,5 s de reloj. Configurarlo bajo también sirve en producción contra servidores lentos.

### Bugs reales encontrados y corregidos

1. **La detección de palabras clave desconocidas reportaba los nombres de los campos del documento** (`action_items`, `meeting_summary`) como si fueran reglas del schema, y en cambio no bajaba al interior de las definiciones. Ahora distingue "clave de un nodo de esquema" de "nombre de campo". Lo detectó `TestSchemaRealDelProyectoValidaEs`.
2. **Suite de `llm` tardaba 9,4 s** portests que pagaban el backoff real. Corregido haciendo `EsperaBase` configurable (suite a 3,0 s).

### Tests

| ID | Qué se verifica | Estado |
| --- | --- | --- |
| TC-02 | Ollama devuelve JSON que deserializa sin error de `json.Unmarshal` | ✅ |
| TC-02 | El schema viaja en la petición (restricted decoding) | ✅ |
| TC-03 | JSON corrupto → reintento con el error de sintaxis adjunto | ✅ |
| TC-03 | Máximo 3 intentos, con `ErrReintentosAgotados` y la causa concreta | ✅ |
| RF-07 | Los errores se acumulan entre intentos | ✅ |
| RF-07 | Un fallo de proveedor no se reintenta con el mismo prompt | ✅ |
| RNF-03 | Timeout por petición; el contexto cancela los reintentos | ✅ |
| RNF-05 | Backoff exponencial en 5xx/429; no reintenta 4xx | ✅ |
| RNF-04 | La credencial de Gemini no aparece en los errores | ✅ |
| — | `additionalProperties: false` rechaza claves inventadas por el LLM | ✅ |
| — | El schema real del proyecto no usa palabras clave sin aplicar | ✅ |

### Criterio de salida — cumplido

```bash
go build ./...   # OK
go vet ./...     # limpio
go test ./...    # ok en los 7 paquetes
gofmt -l .       # sin salida
# ningún test sale a la red: todo con httptest o proveedores falsos
```

---

## Fase 4 — Mapeo de identidades y adaptador GitHub Projects ✅ COMPLETADA

**Objetivo:** resolver menciones a handles reales y publicar el backlog. (RF-04, RF-05, TC-04, TC-06)

### Resultado

| Entregable | Archivo | Estado |
| --- | --- | --- |
| JSONIdentityMapper | `internal/infrastructure/identity/json_mapper.go` | Hecho |
| Cliente GraphQL | `internal/infrastructure/adapters/graphql.go` | Hecho |
| Adaptador Projects v2 | `internal/infrastructure/adapters/github_graphql.go` | Hecho |
| Adaptador vía CLI (`gh`) | `internal/infrastructure/adapters/github_cli.go` | Hecho |
| Caso de uso de despacho | `internal/application/publish_backlog.go` | Hecho |

**Verificación:** `gofmt` limpio, `go vet` limpio, **292 tests en verde**, adapters 89.1%, identity 95.7%, **cero dependencias externas**.

### Decisiones tomadas en esta fase

- **El cliente GraphQL NO reutiliza el de los proveedores de LLM.** No es duplicación: GraphQL devuelve los errores **dentro del cuerpo con HTTP 200**, en un arreglo `errors`. Un cliente que solo mira el código de estado daría por buena una tarjeta que nunca se creó, que es el peor fallo posible.
- **Crear el issue y asociarlo al tablero son dos operaciones.** En Projects v2 el tablero no contiene issues: contiene *items*. Si la asociación falla, el issue existe pero no está en el tablero, y el `PublishResult` lo dice explícitamente.
- **Un fallo parcial no es un error global.** Cinco de seis tarjetas creadas se reportan como resumen con `exitos=5, fallos=1`, no como operación fallida: si no, el usuario creería que no se publicó nada.
- **El handle se devuelve SIN `@`.** Es lo que exigen los campos `assignee` de GitHub; anteponerlo produce un error de la API. El `@` de la especificación es notación documental.
- **Normalización sin acentos.** `strings` de Go no trae normalización Unicode, así que hay una tabla explícita del rango latino. Sin esto, "Hernández" y "Hernandez" serían dos personas distintas.
- **El nombre de pila solo se resuelve si es único.** Con dos "Omar" en la tabla, `Omar` queda sin resolver: adivinar publicaría tareas bajo la persona equivocada, un fallo silencioso que nadie descubre hasta semanas después.
- **Concurrencia limitada a 4 items simultáneos** contra la API de GitHub. Publicar veinte mutaciones a la vez es la vía rápida a un 403 por rate limit.

### Bugs reales encontrados y corregidos

1. **La caché de IDs sufría *cache stampede*.** Al publicar en paralelo, todos los items consultaban a la vez el `node_id` del repositorio: 3 items → 3 consultas, que es justo lo que la caché debía evitar. Corregido con un **lock por clave** y doble comprobación, de modo que solo los items que necesitan el mismo id esperan. Lo detectó `TestCacheDeIdentificadores`.
2. **El índice de alias nunca registraba nombres de pila únicos.** `pilaEsUnica` consultaba claves de una sola palabra que todavía no existían en el mapa, así que devolvía siempre `false` y "Luis" o "Ana" jamás se resolvían. Rehecho con un conjunto de handles por nombre de pila en dos pasadas.
3. **Las abreviaturas solo usaban el último apellido.** "Ana María Ruiz" generaba `ana r` pero no `ana m`, así que la mención más habitual ("Ana M.") fallaba. Ahora se genera un alias por cada token posterior.
4. **Un token ausente se reportaba como fallo del primer item.** Ahora se valida al inicio de `PublishBacklog`, porque es un error de configuración, no un fallo por elemento.

### Garantía TC-05 verificada por inyección

La prueba de que el dry-run no hace red se verificó **rompiendo el código a propósito**: moví la comprobación del modo después de resolver identidades y confirmé que el test falla con `SE RESOLVIERON IDENTIDADES EN MODO DRY-RUN`. Restaurado, pasa. Un test que nunca falla no protege nada.

### Tests

| ID | Qué se verifica | Estado |
| --- | --- | --- |
| TC-04 | "Omar Hernández" → `omarhernan`, insensible a mayúsculas | ✅ |
| TC-04 | Tolera acentos ausentes y abreviaturas ("Omar H.", "Ana M.") | ✅ |
| TC-04 | Nombre de pila ambiguo **no** se resuelve | ✅ |
| TC-05 | Dry-run: cero llamadas al tablero **y** al mapper | ✅ verificado por inyección de regresión |
| TC-06 | `createIssue` + `addIssueToProject` con variables correctas | ✅ |
| TC-06 | Los IDs de GraphQL se devuelven y se cachean | ✅ |
| TC-06 | Errores in-band de GraphQL → error de Go | ✅ |
| TC-07 | GraphQL y CLI cumplen el mismo `ProjectBoardAdapter` | ✅ |
| RNF-04 | El token viaja solo en la cabecera `Authorization` | ✅ |
| RNF-05 | Reintentos solo ante fallos transitorios (rate limit, red) | ✅ |
| — | Concurrencia limitada y fallos parciales tolerados | ✅ |
| — | La CLI se prueba sin `gh` instalado (proceso-ayudante) | ✅ |

### Criterio de salida — cumplido

```bash
go build ./...   # OK
go vet ./...     # limpio
go test ./...    # ok en los 9 paquetes
gofmt -l .       # sin salida
# ningún test sale a la red real
```

---

## Fase 5 — CLI y orquestación ✅ COMPLETADA

**Objetivo:** unir todo en un binario utilizable. (RF-06, TC-05)

### Resultado

| Entregable | Archivo | Estado |
| --- | --- | --- |
| Composition root puro | `cmd/talkaboutthis/main.go` | Hecho |
| Configuración flags + entorno | `cmd/talkaboutthis/config.go` | Hecho |
| Render JSON y tabla legible | `cmd/talkaboutthis/output.go` | Hecho |
| Orquestación de etapas | `internal/application/pipeline.go` | Hecho |
| Logger con Job ID | `internal/infrastructure/logging/logging.go` | Hecho |
| Logger en contexto (sin cruzar capas) | `internal/application/contexto.go` | Hecho |
| Tests de CLI y end-to-end | `cmd/talkaboutthis/{main,fixtures,e2e}_test.go` | Hecho |

**Verificación:** `gofmt` limpio, `go vet` limpio, **390 tests en verde** en 10 paquetes, cmd 90.7%, **cero dependencias externas**, binario de 7,3 MB.

### Corrección respecto al plan original

Dos banderas del plan no se implementaron, a propósito:

| Bandera del plan | Por qué no | Qué hace en su lugar |
| --- | --- | --- |
| `--config` | Un archivo de config duplicaría flags + entorno con dos fuentes de verdad | Solo flags y variables de entorno (el flag manda) |
| `--schema` | El schema embebido es el contrato; permitir otro abre la puerta a un modelo desincronizado | `docs/specifications.BacklogSchema()` con validación cruzada en tests |

Y dos banderas que el plan no preveía sí se añadieron: `--adapter` (`graphql` o `cli`) y `--assignee-policy` (`fail`, `assign_unassigned`, `skip`), necesarias para que la Fase 4 sea alcanzable desde la línea de comandos.

### Decisiones tomadas en esta fase

- **El composition root no orquesta nada.** La secuencia ingesta → extracción → despacho vive en `application.Pipeline`. La razón es que un `main.go` con lógica necesita tests que capturen `os.Exit` y las señales del proceso; al mover la secuencia a `application`, `main.go` se reduce a cablear dependencias y el test e2e invoca `ejecutar(ctx, args, stdout, stderr)` como una función normal.
- **Dry-run es el modo por defecto.** Publicar en el tablero de un equipo es una acción con efecto externo: que ocurra por omisión sería una sorpresa inaceptable. Sin `--dry-run` ni `--publish` se asume `--dry-run`; darlos juntos es error de uso, no un "gana el último".
- **Las banderas contradictorias se guardan fuera de `Configuracion`.** `dryRunFlag` y `publishFlag` son variables de estado, no campos: si vivieran en la configuración, el valor final ya habría perdido la información de que el usuario pidió los dos, y el error sería indetectable.
- **La validación depende del modo.** En dry-run no se exige token, `owner` ni `repo`: pedir credenciales que no se van a usar es una barrera falsa.
- **Un fallo parcial de publicación no es un error global.** Si 5 de 6 tarjetas se crearon, el proceso lo informa por el resumen y sale con 6, pero el JSON ya documenta qué se publicó y qué no.
- **Los códigos de salida siguen `sysexits.h`.** 0 ok · 1 uso · 2 config · 3 ingesta · 4 extracción · 5 identidad · 6 publica · 130 interrumpido. Lo que hace útil la CLI en CI es poder *reintentar por red* (4) pero *no por un archivo mal escrito* (3).
- **`SIGINT` y `SIGTERM` se capturan** con `signal.NotifyContext`, de modo que Ctrl-C sale con 130 y no con un error genérico.

### Bugs reales encontrados y corregidos

1. **`slog.Logger` no tiene `WithContext`** (era `slog.Default().With(...)`). Creado `internal/application/contexto.go` con `conLoggerEnContexto` / `loggerDe`, para poder propagar el Job ID sin que `application` importe `infrastructure/logging` — lo que habría roto la regla de imports del `AGENTS.md` §1.
2. **`LLM_PROVIDER` se ignoraba en silencio.** La condición era `cfg.Proveedor == "ollama"`, que nunca se cumplía porque el flag ya traía el valor por defecto. Corregido a `cfg.Proveedor == "" || cfg.Proveedor == ProveedorPorDefecto`, que además cubre una `Configuracion` construida a mano que nunca pasó por `registrarFlags`.
3. **Alarma falsa en la tabla del dry-run.** La columna de responsable marcaba siempre `(SIN RESOLVER)`, porque en dry-run el mapper no se consulta por diseño (TC-05). Ahora el marcador solo aparece en modo publish, que es el único caso en que significa algo.
4. **Un `--timeout 1ms` hacía fallar todo con un error desconcertante.** `timeoutEfectivo` impone un suelo de 5 s: por debajo, el LLM no tiene tiempo de responder y el mensaje no explica la causa real.
5. **Job IDs colisionables si se usara `time.Now()`.** Se generan con 6 bytes de `crypto/rand` en hexadecimal (`job-3f9a…`), que además no filtran nada del reloj del sistema.

### Garantía TC-05 verificada de nuevo

`TestTC05LaCLICompletaNoTocaGitHub` levanta un servidor `httptest` con la forma de respuesta de Ollama y ejecuta la CLI real contra él. El test pasa cuando: el backlog se extrae, la salida se imprime, el proceso sale con 0, **y ninguna petición llega a GitHub**. La verificación fue bidireccional: primero se confirmó que el test falla si se reintroduce una llamada al tablero, y después se restauró el código.

### Tests

| ID | Qué se verifica | Estado |
| --- | --- | --- |
| RF-06 | La CLI extrae y muestra el backlog en modo dry-run | ✅ |
| TC-05 | CLI completa: **cero** tráfico a GitHub (servidor httptest) | ✅ |
| — | `--dry-run` + `--publish` juntos → error de uso (código 1) | ✅ |
| — | Sin banderas de modo se asume dry-run | ✅ |
| — | Publicar sin `--project` → error de uso, no de config | ✅ |
| — | Publicar sin token → mensaje accionable, nunca imprime el token | ✅ |
| — | `--output table` y `--output json` renderizan el mismo backlog | ✅ |
| — | En publish, un responsable sin resolver se marca; en dry-run, no | ✅ |
| — | `SIGINT` → código 130 y mensaje de cancelación | ✅ |
| — | `--help`, `--version` y subcomando inexistente | ✅ |
| — | Job ID presente en los logs en formato JSON y texto | ✅ |
| RNF-04 | El token no aparece en la salida, ni en logs ni en errores | ✅ |
| — | Schema embebido legible sin archivos sueltos (binario autocontenido) | ✅ |

### Criterio de salida — cumplido

```bash
gofmt -l .         # sin salida
go vet ./...       # limpio
go build ./...     # OK
go test ./...      # ok en los 10 paquetes, 390 tests
./bin/talkaboutthis ingest --file testdata/reunion-equipo.md --dry-run --provider ollama
# imprime el backlog, sale con 0 y no sale a la red
# ningún test sale a la red real
```

### Deuda cerrada al terminar la fase

La cobertura de `internal/application` cayó de 90,7% a **60,6%** al entrar `pipeline.go` y `contexto.go`, que no tenían tests directos: solo se ejercitaban a través de la CLI. Se cerró añadiendo `internal/application/pipeline_test.go` (dobles de extractor y tablero, fallo por etapa, propagación del Job ID a los logs) y tests de la precedencia flag/entorno en la CLI: **93,5%**, con la suite entera en **90,7%**.

### Bugs reales encontrados al cerrar el proyecto

Ocho, y ninguno lo había detectado la suite: aparecieron al escribir el README contra el código real y al ejecutar el binario compilado contra un servidor con la forma de Ollama.

1. **`LOG_LEVEL` y `LOG_FORMAT` estaban documentados en `.env.example` pero nunca se leían.** El usuario los ajustaba y no pasaba nada. Ahora ambas se aplican, y los flags `--log-level` / `--log-format` tienen en ellas el relevo.
2. **La precedencia flag/entorno estaba invertida.** `primeroNoVacio(os.Getenv(...), cfg.X)` daba prioridad al entorno: un `GITHUB_OWNER` exportado en la sesión pisaba un `--owner` escrito a propósito. Invertido en todos los campos.
3. **Comparar el valor con el default no detecta un flag explícito.** `--log-format` solo admite `text` y `json`, y `text` es el de por defecto, así que el flag nunca podía ganar. Añadido `registrarFlagsEnviadas`, que usa `flag.FlagSet.Visit` (que solo recorre las banderas escritas) y registra cuáles aparecieron antes de `completar()`.
4. **`conLoggerEnContexto` entraba en panic con un `ctx` nil.** `loggerDe` ya lo toleraba, así que la asimetría era un crash esperando. Ahora se sustituye por `context.Background()`.
5. **Un carácter CJK corrupto en un comentario** (`seconsideraronaron`) en `publish_backlog.go`, resto de una escritura anterior. Barrido de todo el árbol.
6. **La tabla de identidades era obligatoria en dry-run.** El archivo `mappings.json` tiene que existir para arrancar, aunque en dry-run no se consulta ni un solo nombre (TC-05). El modo por defecto quedaba bloqueado para cualquiera que no hubiera copiado el ejemplo, y el error salía con el código 5 (identidad) cuando lo que fallaba era una lectura. Ahora la tabla es obligatoria **solo al publicar**: en seco se usa `identity.MapperVacio()`.
7. **`job_id` salía duplicado en cada línea de log** (`job_id=job-abc job_id=job-abc`). La CLI inyecta su logger con el job_id puesto y el pipeline lo volvía a añadir. El pipeline ahora respeta el logger que encuentra en el contexto en lugar de enriquecerlo. Verificado rompiendo el código a propósito: el test falla con «el job_id aparece 2 veces en una linea».
8. **Un LLM que responde `"high"` en vez de `"HIGH"` agotaba los tres reintentos y fallaba.** El schema de `INIT.md` exige mayúsculas, pero el prompt viaja entero hasta el modelo y allí las mayúsculas no sobreviven. El fallo era por un detalle de caja que nunca cambia el significado del dato. Se normaliza el enum en la frontera con el modelo, antes de validar, y **sin inventar**: un "urgente" se rechaza igual que antes. Al hacerlo apareció un segundo bug oculto: la Capa 3 deserializaba de los bytes originales, así que validaba un valor y guardaba otro.

Los tres últimos se encontraron ejecutando el **binario real** contra un servidor con la forma de Ollama, no leyendo el código: la suite los daba por buenos porque sus dobles usaban siempre mayúsculas.

---

## Verificación final — ejecutada

```bash
gofmt -l .           # sin salida
go vet ./...         # limpio
go test ./...        # ok en los 10 paquetes, 390 tests
go test ./... -coverprofile=coverage.out && go tool cover -func=coverage.out
go build -o bin/talkaboutthis ./cmd/talkaboutthis
```

Matriz TC-01 … TC-07 en verde, sin ningún test que dependa de red o credenciales reales.

| Paquete | Cobertura |
| --- | --- |
| `internal/domain` | 100,0% |
| `internal/application` | 93,5% |
| `cmd/talkaboutthis` | 90,7% |
| `infrastructure/identity` | 90,0% |
| `infrastructure/llm` | 91,7% |
| `infrastructure/parsers` | 93,1% |
| `infrastructure/adapters` | 89,1% |
| `infrastructure/logging` | 93,8% |
| `infrastructure/jsonschema` | 78,0% |
| `docs/specifications` | 75,0% |
| **Total** | **90,7%** |

---

## Riesgos y mitigaciones

| Riesgo | Impacto | Mitigación |
| --- | --- | --- |
| Respuestas del LLM con JSON inconsistente | Alto | Retry Loop con auto-corrección (RF-07) + validación estricta |
| Limitaciones de rate limit en GitHub | Medio | Backoff exponencial + `WaitGroup` con semáforo de 4 (sin `errgroup`: RNF-01) |
| Fuga de secretos en logs | Alto | Tokens fuera de `slog`; tests que escanean la salida en busca de `ghp_` |
| `.docx` con XML malformado | Medio | Validar antes de parsear; error claro en lugar de panic |
| Acoplamiento accidental del dominio | Alto | Test de imports en Fase 1 + revisión de `AGENTS.md` §1 |
| Dependencias que engordan el binario | Medio | Preferencia explícita por stdlib; auditar `go mod tidy` en cada fase |

---

# Sesión de cierre: tests que faltaban y documentación

## 1. Tests añadidos

Se cerró la cobertura por función, no por paquete, buscando lo que estaba por
debajo del 80 %.

| Fichero | Qué cubre |
| --- | --- |
| `internal/application/casos_de_borde_test.go` | Prompt por defecto, reloj inyectado, `ErrorEtapa.Unwrap`, `ExtensionDe` |
| `internal/application/extensibilidad_test.go` | **TC-07**: un `JiraAdapter` externo compila y se enchufa |
| `internal/infrastructure/jsonschema/bordes_test.go` | Orden determinista, palabras no soportadas, nombres de tipo en los mensajes |
| `internal/infrastructure/adapters/github_graphql_interno_test.go` | `consultarProjectID`, `resolverNodeID` sin cache stampede |
| `internal/infrastructure/identity/mapper_vacio_test.go` | `MapperVacio` (dry-run), tabla completa de acentos |
| `internal/infrastructure/parsers/registry_test.go` | `ExtensionDeRuta`, selección de parser, detección por contenido |
| `internal/infrastructure/llm/nombres_test.go` | `Name()` de los cuatro + contrato del puerto |
| `cmd/talkaboutthis/output_test.go` | Formateadores, códigos de etapa, ayuda de `ingest` |
| `pkg/sdk/frontera_test.go` | Las dos reglas de la reserva |

## 2. Bugs encontrados por esos tests

Ninguno lo detectó la revisión del código. Seis:

| # | Bug | Consecuencia |
| --- | --- | --- |
| 1 | `promptPorDefecto.ConstruirCorreccion` devolvía solo el transcript | El reintento era **ciego**: tres intentos idénticos (violaba TC-03) |
| 2 | `PalabrasClaveDesconocidas()` devolvía `nil` siempre | Método público que miente; el aviso de reglas no aplicadas no existía |
| 3 | `modoDe(nil)` hacía panic | Un fallo de renderizado tumbaba el proceso **después** del trabajo |
| 4 | `tituloEnLinea` dejaba doble espacio con `\r\n` | Desalineaba la tabla |
| 5 | `salidaDespacho.Plataforma` nunca se rellenaba | La salida JSON **nunca decía dónde se publicó** |
| 6 | `TestPipelineDryRunCompleto` fallaba en Windows | `time.Since` devolvía 0 por granularidad del reloj, no por comportamiento |

## 3. Test de contrato TC-07

El caso aparecía en la matriz de `AGENTS.md` sin que nada lo demostrara. Ahora
`internal/application/extensibilidad_test.go` contiene un `JiraAdapter` completo,
heterogéneo a propósito (respuesta = hilo de comentarios, no issue), que se enchufa
en `PublicarBacklog` y en `Pipeline`.

**Verificado por inyección de regresión**: añadir un método a
`domain.ProjectBoardAdapter` rompe la compilación de ese test.

## 4. `pkg/sdk`

Decisión: dejarla como **reserva vacía con reglas vigiladas**, no poblarla.

- `pkg/sdk/README.md` — qué es, por qué, cuándo llenarla.
- `pkg/sdk/frontera_test.go` — regla 1: `pkg/sdk` no importa `internal/`.
  Regla 2: `internal/` no importa `pkg/sdk`. Ya existía la regla 3 en
  `internal/domain/architecture_test.go`.

Rellenarla ahora sería deuda permanente sin demanda real. Ver
[`docs/adr/0010`](../../docs/adr/0010-sdk-publico-en-pkg.md).

## 5. Documentación

39 documentos en `docs/`, todos los diagramas en **Mermaid embebido**:

| Familia | Documentos | Contenido |
| --- | --- | --- |
| `docs/architecture/` | 6 + 6 flujos | Las cuatro capas, cada módulo modelado, seis flujos con diagrama |
| `docs/adr/` | README + plantilla + 10 | Las decisiones no obvias, con alternativas y consecuencias malas |
| `docs/specs/` | README + 7 | Un RF por documento, más la trazabilidad completa |
| `docs/criticas/` | README + 5 | Los cinco compromisos que no se pueden romper |

Se comprobó que los **152 nombres de test citados existen** en el repositorio.

## 6. Verificación final — ejecutada

```bash
gofmt -l .                  # vacío
go vet ./...                # limpio
go test ./... -count=1      # ok en 11 paquetes, 310 tests
grep -c require go.mod      # 0
```

| Paquete | Antes | Ahora |
| --- | --- | --- |
| `internal/domain` | 100,0% | **100,0%** |
| `internal/infrastructure/identity` | 90,0% | **98,7%** |
| `internal/application` | 93,5% | **96,4%** |
| `internal/infrastructure/logging` | 93,8% | 93,8% |
| `internal/infrastructure/parsers` | 93,1% | **93,3%** |
| `internal/infrastructure/adapters` | 89,1% | **93,0%** |
| `internal/infrastructure/llm` | 91,7% | **92,7%** |
| `cmd/talkaboutthis` | 90,7% | **92,3%** |
| `internal/infrastructure/jsonschema` | 78,0% | **89,4%** |
| `docs/specifications` | 75,0% | 75,0% |
| `pkg/sdk` | — | guarda de frontera |
| **Total** | **90,7%** | **93,4%** |

Los cinco huecos que seguían en pie, y su motivo:

| Hueco | Por qué no se cierra |
| --- | --- |
| `main()` en `cmd` | Hace `os.Exit`; se prueba `ejecutar()`, que es el 90 % |
| Caminos profundos de `llm/client.go` | Requieren un servidor que simule timeouts reales |
| `docs/specifications` al 75 % | La parte sin cubrir son rutas de error de un schema que va incrustado |

---

## Definición de "hecho" (aplicable a todas las fases)

1. `make check` en verde.
2. Test asociado a cada TC que toca la fase.
3. Sin secretos ni artefactos de build en el diff.
4. `docs/specifications/backlog_schema.json` sincronizado con los structs (si hubo cambio de modelos).
5. `README.md` y `AGENTS.md` actualizados si cambió una instrucción o una bandera.