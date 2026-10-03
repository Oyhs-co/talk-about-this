# Plan de Ejecución — TalkAboutThis

> Plan derivado de `INIT.md` §9 (Hitos 1–5) y de la matriz de evaluación §8 (TC-01 … TC-07).
> Cada fase es independiente y verificable. **No se avanza de fase hasta cumplir el criterio de salida.**
> Contrato de desarrollo aplicable: [`AGENTS.md`](../AGENTS.md).

**Estado actual:** esqueleto preparado (estructura, `go.mod`, schema, configuración, tooling).
**Fases 1 y 2: COMPLETADAS** (dominio y contratos; parsers e ingesta).
**Fases restantes:** 3 (LLM + Retry), 4 (Identidad + GitHub), 5 (CLI).

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

## Fase 3 — Proveedores de LLM y Retry Loop

**Objetivo:** extracción estructurada confiable, con auto-corrección. (RF-02, RF-03, RF-07, TC-02, TC-03)

### Tareas

1. `internal/infrastructure/llm/ollama.go` — `POST /api/chat` con `format: "json"` o el schema.
2. `internal/infrastructure/llm/openai.go` — `/v1/chat/completions` con `response_format: json_schema`.
3. `internal/infrastructure/llm/anthropic.go` y `gemini.go` — stubs compilando que **sí** implementan la interfaz, para no bloquear extensibilidad.
4. `internal/infrastructure/llm/prompt.go` — construcción del prompt: instrucciones + schema embebido + transcript + regla "solo JSON".
5. `internal/infrastructure/llm/validate.go` — validación de la respuesta contra el schema.
6. `internal/infrastructure/llm/retry.go` — **Retry Loop**: máx. 3 intentos; en cada fallo de validación, reintentar adjuntando el error de parseo literal para que el modelo se autocorrija. Backoff exponencial entre intentos.
7. `internal/application/extract_backlog.go` — caso de uso que orquesta: prompt → provider → validar → retry → `MeetingBacklogExtraction`.
8. Cliente HTTP común con **timeout explícito** y una función de fábrica compartida (`NewHTTPClient`).

### Consideraciones

- Todo el cliente HTTP debe ser inyectable para tests. `httptest.NewServer` en tests, nunca red real.
- El error de auto-corrección va en el prompt del siguiente intento, no solo en el log.
- El timeout aplica **por intento**, no al total, salvo que el diseño indique lo contrario.

### Tests

| ID | Qué se verifica |
| --- | --- |
| TC-02 | Respuesta de Ollama (fake) → `MeetingBacklogExtraction` sin error de `json.Unmarshal` |
| TC-03 | JSON corrupto → reintento con contexto de error; `context.WithTimeout` respetado; máximo 3 intentos |
| — | Respuesta que viola el enum de `priority` → error de validación, no default silencioso |
| — | Cancelación del contexto propaga correctamente |

### Criterio de salida

```bash
make test
# ningún test sale a la red; todos contra httptest
```

---

## Fase 4 — Mapeo de identidades y adaptador GitHub Projects

**Objetivo:** resolver menciones a handles reales y publicar el backlog. (RF-04, RF-05, TC-04, TC-06)

### Tareas

1. `internal/infrastructure/identity/json_mapper.go` — carga `mappings.json`, índice en memoria por nombre normalizado.
   - Normalización: `strings.ToLower` + `strings.TrimSpace` + colapso de espacios.
   - Soportar también abreviaturas iniciales (`"Omar H."`).
2. Política de no-resolución configurable: `fail` | `assign_unassigned` | `skip` (leer `configs/mappings.example.json`).
3. `internal/infrastructure/adapters/github_graphql.go` — mutaciones GraphQL de Projects v2:
   - `addIssueToProject` (crear `Issue` + `addIssueToProject`).
   - Mover a columna por Status: `moveProjectCard` / `updateProjectCardFieldValue`.
4. `internal/infrastructure/adapters/github_cli.go` — backend alternativo vía binario `gh`.
5. `internal/application/publish_backlog.go` — orquestación: `IdentityMapper` sobre cada ítem → decisión dry-run vs publish.
6. `github_cli.go` y `github_graphql.go` deben registrar un `var _ domain.ProjectBoardAdapter = (*X)(nil)` de compilación.

### Consideraciones

- GraphQL v2 requiere primero resolver los `node_id` de `owner`, `repo` y `project`. Cachearlos dentro de la ejecución.
- El token viaja en el header `Authorization` y **nunca** se loguea.
- Respetar los límites de tasa de la API de GitHub: reintentos con backoff ante 5xx y errores de *rate limit*.

### Tests

| ID | Qué se verifica |
| --- | --- |
| TC-04 | `"Omar Hernández"`, `"omar hernandez"`, `"Omar H."` → `@omarhernan`; match case-insensitive |
| TC-06 | Mocks de GraphQL: se emite la mutación correcta y se devuelve el `id` generado |
| — | Nombre no mapeado → según la política configurada |
| TC-05 | Con `--dry-run`, el HTTP client de GitHub **no se instancia**; el test falla si hay una sola llamada |

### Criterio de salida

```bash
make test
# cobertura de identity y adapters >= 80 %
```

---

## Fase 5 — CLI y orquestación

**Objetivo:** unir todo en un binario utilizable. (RF-06, TC-05)

### Tareas

1. `cmd/talkaboutthis/main.go` — composition root: leer flags/env, construir parsers, provider, mapper y adapter; inyectarlos.
2. Subcomando `ingest` con flags: `--file`, `--provider`, `--dry-run`, `--publish`, `--config`, `--mappings`, `--schema`, `--project`, `--repo`, `--log-format`, `--log-level`, `--timeout`.
3. Salida de dry-run: **ambas** formas, JSON (`--output json`) y tabla legible (`--output table`).
4. `internal/infrastructure/logging/` — setup de `log/slog` (JSON o texto) con `Job ID` como atributo.
5. Códigos de salida distintos para cada clase de fallo (validación, config, proveedor, publish) para que sea usable en CI.
6. `README.md` — instalación, flags, ejemplo end-to-end, tabla de variables de entorno.
7. Verificación de que el schema se embebe con `go:embed` (binario autocontenido).

### Criterio de salida

```bash
make check
make build
./bin/talkaboutthis ingest --file testdata/ejemplo.md --dry-run --provider ollama
# debe imprimir el backlog y salir con 0 sin tocar la red
```

---

## Verificación final (tras la Fase 5)

```bash
make check          # fmt + vet + test
make cover          # cobertura total
make build          # binario estático
make lint           # golangci-lint si disponible
make docker-build   # imagen multi-stage
```

Matriz TC-01 … TC-07 en verde, sin ningún test que dependa de red o credenciales reales.

---

## Riesgos y mitigaciones

| Riesgo | Impacto | Mitigación |
| --- | --- | --- |
| Respuestas del LLM con JSON inconsistente | Alto | Retry Loop con auto-corrección (RF-07) + validación estricta |
| Limitaciones de rate limit en GitHub | Medio | Backoff exponencial + `errgroup` con límite de concurrencia |
| Fuga de secretos en logs | Alto | Tokens fuera de `slog`; tests que escanean la salida en busca de `ghp_` |
| `.docx` con XML malformado | Medio | Validar antes de parsear; error claro en lugar de panic |
| Acoplamiento accidental del dominio | Alto | Test de imports en Fase 1 + revisión de `AGENTS.md` §1 |
| Dependencias que engordan el binario | Medio | Preferencia explícita por stdlib; auditar `go mod tidy` en cada fase |

---

## Definición de "hecho" (aplicable a todas las fases)

1. `make check` en verde.
2. Test asociado a cada TC que toca la fase.
3. Sin secretos ni artefactos de build en el diff.
4. `docs/specifications/backlog_schema.json` sincronizado con los structs (si hubo cambio de modelos).
5. `README.md` y `AGENTS.md` actualizados si cambió una instrucción o una bandera.