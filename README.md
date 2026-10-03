# TalkAboutThis

**Automatiza la creación de backlogs en GitHub Projects (v2) a partir de minutas de reunión**, usando modelos de lenguaje locales (Ollama / LM Studio) o APIs en la nube (OpenAI, Anthropic, Gemini).

Transforma transcripciones, notas y documentos `.md`, `.txt` y `.docx` en **ítems accionables de backlog** (con título, descripción, responsable, prioridad, etiquetas y estimación) y los publica en tu tablero.

> CLI en **Go**. Arquitectura **Hexagonal + DDD**. Un solo binario estático, **cero dependencias externas**: todo con la biblioteca estándar.

---

## Estado del proyecto

**Completo.** Las cinco fases del plan están implementadas y verificadas: 390 tests en verde, cobertura global del 90,7%, dominio al 100% y `go.mod` sin un solo `require`.

| Documento | Para qué |
| --- | --- |
| [`.plans/plan.md`](./.plans/plan.md) | Plan por fases, decisiones tomadas y bugs encontrados en cada una |
| [`AGENTS.md`](./AGENTS.md) | Contrato de desarrollo: regla de imports, puertos, invariantes |
| [`INIT.md`](./INIT.md) | Especificación funcional original (RF-01…RF-07, RNF-01…RNF-06, TC-01…TC-07) |

---

## Instalación

Requisitos: **Go 1.27+**.

```bash
git clone <url-del-repo>
cd talk-about-this

make build          # genera bin/talkaboutthis
```

O directamente con Go:

```bash
go build -o bin/talkaboutthis ./cmd/talkaboutthis
```

También hay una imagen multi-stage:

```bash
make docker-build
docker run --rm -v "$PWD/testdata:/work" talkaboutthis:latest \
  ingest --file /work/reunion-equipo.md --dry-run --provider ollama
```

El binario es **autocontenido**: el JSON Schema de extracción va embebido con `go:embed`, así que no necesita ningún archivo suelto a su lado.

---

## Configuración

Copia la plantilla y rellena lo que necesites:

```bash
cp .env.example .env
```

### Variables de entorno

Cada una tiene su bandera equivalente. **El flag siempre gana**: si escribes `--provider anthropic`, la variable `LLM_PROVIDER` se ignora. El entorno solo rellena lo que no dijiste en la línea de comandos.

| Variable | Equivalente | Descripción |
| --- | --- | --- |
| `LLM_PROVIDER` | `--provider` | `ollama` (por defecto), `openai`, `anthropic` o `gemini` |
| `OLLAMA_BASE_URL` | — | Endpoint del motor local (por defecto `http://localhost:11434`) |
| `OLLAMA_MODEL` | — | Modelo local, p. ej. `llama3`, `qwen2.5` |
| `OPENAI_API_KEY`, `OPENAI_BASE_URL`, `OPENAI_MODEL` | — | Credencial, endpoint y modelo de OpenAI |
| `ANTHROPIC_API_KEY`, `ANTHROPIC_MODEL` | — | Credencial y modelo de Anthropic |
| `GEMINI_API_KEY`, `GEMINI_MODEL` | — | Credencial y modelo de Gemini |
| `GITHUB_TOKEN` | — | PAT de GitHub; requiere scope `project` |
| `GITHUB_OWNER` | `--owner` | Propietario del repositorio (org o usuario) |
| `GITHUB_REPO` | `--repo` | Repositorio donde se crean los issues |
| `GITHUB_PROJECT_ID` | `--project` | ID del GitHub Project (v2) |
| `ASSIGNEE_POLICY` | `--assignee-policy` | `fail` (por defecto), `assign_unassigned` o `skip` |
| `LOG_LEVEL` | `--log-level` | `debug`, `info` (por defecto), `warn` o `error` |
| `LOG_FORMAT` | `--log-format` | `text` (por defecto) o `json` |
| `REQUEST_TIMEOUT` | `--timeout` | Timeout por llamada de red (por defecto `60s`, con suelo de `5s`) |

> **Ningún secreto se escribe en disco.** El `.env` real está en `.gitignore`; solo se versiona `.env.example`. El token viaja únicamente en la cabecera `Authorization` y nunca aparece en los logs ni en los mensajes de error (RNF-04).
>
> Verifica los permisos de tu PAT: scope `project` en tokens classic, o *project permissions* en tokens fine-grained.

### Mapeo de identidades

Los nombres que aparecen en la minuta ("Omar", "Ana María") no son handles válidos. El `IdentityMapper` los traduce usando una tabla de alias:

```bash
cp configs/mappings.example.json configs/mappings.json
```

```json
{
  "raw_names": ["Omar Hernández", "Omar Hernan"],
  "handle": "omarhernan",
  "platforms": ["github"]
}
```

La búsqueda tolera **mayúsculas y acentos ausentes** ("Omar H." y "Hernandez" resuelven igual), y un nombre de pila solo se resuelve si es **único** en la tabla: con dos "Omar", el nombre corto queda sin resolver en lugar de adivinar.

---

## Uso

```bash
# 1. Ver qué se extraería, sin tocar nada (100% offline)
talkaboutthis ingest --file notas.md --dry-run --provider ollama

# 2. El mismo resultado como tabla legible
talkaboutthis ingest --file notas.md --dry-run --output table

# 3. Publicar de verdad en GitHub Projects v2
talkaboutthis ingest --file notas.md --publish \
  --project 5 --owner mi-org --repo mi-repo
```

> **El dry-run es el modo por defecto.** Sin `--dry-run` ni `--publish` no se publica nada. Publicar en el tablero de un equipo es una acción con efecto externo: que ocurra por omisión sería una sorpresa inaceptable. Darlos **juntos** es un error de uso.

### Banderas del subcomando `ingest`

| Bandera | Por defecto | Descripción |
| --- | --- | --- |
| `--file <ruta>` | — | Transcripción `.md`, `.txt` o `.docx`. **Obligatoria** |
| `--provider <id>` | `ollama` | `ollama`, `openai`, `anthropic` o `gemini` |
| `--timeout <dur>` | `60s` | Timeout de las operaciones de red (suelo: `5s`) |
| `--dry-run` | activo | Muestra el backlog sin publicar nada |
| `--publish` | — | Publica el backlog en el tablero destino |
| `--output <fmt>` | `json` | `json` (para máquinas) o `table` (legible) |
| `--project <id>` | — | GitHub Project v2 destino. Obligatorio con `--publish` |
| `--owner <org>` | — | Propietario del repositorio |
| `--repo <nombre>` | — | Repositorio donde se crean los issues |
| `--adapter <id>` | `graphql` | `graphql` (API) o `cli` (binario `gh`) |
| `--mappings <ruta>` | `configs/mappings.json` | Tabla de aliases |
| `--assignee-policy <p>` | `fail` | `fail`, `assign_unassigned` o `skip` |
| `--log-level <nivel>` | `info` | `debug`, `info`, `warn` o `error` |
| `--log-format <fmt>` | `text` | `text` o `json` |

Y en la raíz: `-h` / `--help`, `--version`.

### Códigos de salida

Cada clase de fallo tiene su propio código, siguiendo la convención de `sysexits.h`. Es lo que hace la CLI utilizable en un proceso automático: **reintentar por un problema de red tiene sentido; reintentar porque el archivo no existe, no.**

| Código | Significado |
| --- | --- |
| `0` | Todo correcto |
| `1` | Uso incorrecto: banderas contradictorias o faltan parámetros |
| `2` | Configuración incompleta: credenciales o proveedor desconocido |
| `3` | Ingesta: archivo ilegible o formato no soportado |
| `4` | Extracción: el LLM no produjo un backlog válido |
| `5` | Identidad: responsable sin resolver en modo estricto |
| `6` | Publicación: fallo (o fallo parcial) contra la plataforma destino |
| `130` | Cancelado por el usuario (`Ctrl-C`) |

---

## Cómo funciona

```text
1. INGESTA     → .md / .txt / .docx  →  texto plano limpio  →  Transcript
2. EXTRACCIÓN  →  Transcript + JSON Schema  →  LLM  →  MeetingBacklogExtraction
                 (con Retry Loop: hasta 3 intentos de auto-corrección)
3. IDENTIDAD   →  "Omar Hernández"  →  omarhernan
4. DECISIÓN    →  ¿--dry-run?  →  imprimir  |  publicar en el tablero
```

Las etapas viven en `internal/application.Pipeline`; `cmd/talkaboutthis/main.go` se limita a cablear dependencias. Por eso el flujo se puede probar entero sin lanzar un proceso.

El intercambio con el modelo está gobernado por un JSON Schema formal:
[`docs/specifications/backlog_schema.json`](./docs/specifications/backlog_schema.json), embebido en el binario y validado en tres capas (forma, esquema y coherencia de campos).

---

## Arquitectura

```text
cmd/talkaboutthis/        Composition root: solo cablea dependencias
internal/domain/          Entidades, value objects, errores y puertos (interfaces)
internal/application/     Casos de uso: ingesta, extracción, despacho, pipeline
internal/infrastructure/  Adaptadores: parsers/, llm/, identity/, adapters/, logging/
docs/specifications/      Contratos JSON Schema (embebidos)
configs/                  Mapeo de identidades
testdata/                 Fixtures de prueba
```

El dominio **no conoce** ningún adaptador concreto: se apoya en interfaces (`DocumentParser`, `LLMProvider`, `IdentityMapper`, `ProjectBoardAdapter`). Añadir Jira, Linear o Trello es escribir un adaptador nuevo **sin tocar `internal/domain`**.

Esa regla no se pide por buena voluntad: `internal/domain/architecture_test.go` la verifica con `go/parser` en cada `go test`. Importar `infrastructure` desde el dominio, usar I/O de red o añadir una dependencia de terceros **hacen fallar la suite**.

Detalle completo en [`AGENTS.md`](./AGENTS.md).

---

## Desarrollo

```bash
make help      # lista todos los targets
make test      # go test ./...
make cover     # cobertura -> coverage.html
make check     # fmt + vet + test  (puerta de calidad)
make build     # binario estático en bin/
make lint      # golangci-lint si está disponible
```

Si no tienes `make`, los comandos son directos: `gofmt -l .`, `go vet ./...`, `go test ./...`, `go build -o bin/talkaboutthis ./cmd/talkaboutthis`.

| Paquete | Cobertura | Qué lo sostiene |
| --- | --- | --- |
| `internal/domain` | **100,0%** | El núcleo, completo |
| `internal/infrastructure/identity` | 98,7% | Mapper de dry-run y tabla de acentos |
| `internal/application` | 96,4% | Prompt por defecto, reloj, `ErrorEtapa`, TC-07 |
| `internal/infrastructure/logging` | 93,8% | Formatos, niveles, `job_id` |
| `internal/infrastructure/parsers` | 93,3% | Registro y `ExtensionDeRuta` |
| `internal/infrastructure/adapters` | 93,0% | `consultarProjectID`, `cacheIDs` sin stampede |
| `internal/infrastructure/llm` | 92,7% | `Name()` de los cuatro, contrato del puerto |
| `cmd/talkaboutthis` | 92,3% | Formateadores, códigos de etapa, ayuda |
| `internal/infrastructure/jsonschema` | 89,4% | Orden determinista, palabras no soportadas |
| `docs/specifications` | 75,0% | Solo `embed.go`; el schema va incrustado |
| `pkg/sdk` | — | Guarda de frontera (sin código de producción) |
| **Total** | **93,4%** | 310 tests en 11 paquetes |

Ningún test sale a la red real ni necesita credenciales: el LLM se simula con un servidor `httptest` y GitHub con dobles de `ProjectBoardAdapter`.

---

## Documentación

La documentación completa vive en [`docs/`](./docs/README.md) y se organiza en
cuatro familias:

| Familia | Responde | Índice |
| --- | --- | --- |
| **Especificaciones** | Qué debe hacer el sistema | [`docs/specs/`](./docs/specs/README.md) |
| **Arquitectura** | Cómo está construido, módulo a módulo | [`docs/architecture/`](./docs/architecture/README.md) |
| **ADR** | Por qué se decidió así, con sus alternativas | [`docs/adr/`](./docs/adr/README.md) |
| **Funcionalidades críticas** | Qué compromisos no se pueden romper | [`docs/criticas/`](./docs/criticas/README.md) |

Todos los diagramas son **Mermaid embebido** (` ```mermaid `), y se renderizan en
GitHub, GitLab y cualquier editor con el plugin.

Si quieres entender el proyecto, empieza por:

1. [`docs/architecture/README.md`](./docs/architecture/README.md) — la vista general.
2. [`docs/architecture/flujos/00-vision-general.md`](./docs/architecture/flujos/00-vision-general.md) — el recorrido completo.
3. [`docs/specs/07-trazabilidad.md`](./docs/specs/07-trazabilidad.md) — qué cubre qué.

---

## Licencia

MIT — ver [`LICENSE`](./LICENSE).
