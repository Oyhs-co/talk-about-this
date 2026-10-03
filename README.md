# TalkAboutThis

**Automatiza la creación de backlogs en GitHub Projects (v2) a partir de minutas de reunión**, usando modelos de lenguaje locales (Ollama / LM Studio) o APIs en la nube (OpenAI, Anthropic, Gemini).

Transforma transcripciones, notas y documentos `.md`, `.txt` y `.docx` en **ítems accionables de backlog** —con título, descripción, responsable, prioridad, etiquetas y estimación— y los publica en tu tablero.

> CLI en **Go**. Arquitectura **Hexagonal + DDD**. Un solo binario estático, sin runtime pesado.

---

## Estado del proyecto

En desarrollo. Consulta el **plan de ejecución por fases** en [`.plans/plan.md`](./.plans/plan.md) y el **contrato de desarrollo** en [`AGENTS.md`](./AGENTS.md). La especificación funcional completa está en [`INIT.md`](./INIT.md).

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

También disponible una imagen multi-stage:

```bash
make docker-build
docker run --rm -v "$PWD/testdata:/work" talkaboutthis:latest \
  ingest --file /work/ejemplo.md --dry-run --provider ollama
```

---

## Configuración

Copia la plantilla y rellena los valores que necesites:

```bash
cp .env.example .env
```

| Variable | Descripción |
| --- | --- |
| `LLM_PROVIDER` | `ollama` \| `openai` \| `anthropic` \| `gemini` |
| `OLLAMA_BASE_URL` | Endpoint del motor local (por defecto `http://localhost:11434`) |
| `OLLAMA_MODEL` | Modelo local a usar, p. ej. `llama3`, `qwen2.5` |
| `OPENAI_API_KEY` | Credencial de OpenAI (solo si lo usas) |
| `ANTHROPIC_API_KEY` | Credencial de Anthropic |
| `GEMINI_API_KEY` | Credencial de Gemini |
| `GITHUB_TOKEN` | PAT de GitHub — requiere scope `project` |
| `GITHUB_OWNER` / `GITHUB_REPO` | Repositorio destino de los issues |
| `GITHUB_PROJECT_ID` | ID del GitHub Project (v2) |
| `LOG_LEVEL` / `LOG_FORMAT` | `debug\|info\|warn\|error` y `json\|text` |
| `REQUEST_TIMEOUT` | Timeout por llamada de red (por defecto `60s`) |

> **Ningún secreto se escribe en disco.** El archivo `.env` real está en `.gitignore`; solo se versiona `.env.example`.
> Verifica los permisos de tu PAT: `project` en tokens classic, o *project permissions* en tokens fine-grained.

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

La búsqueda es **exacta e insensible a mayúsculas**. Consulta `configs/mappings.example.json` para ver la política aplicada cuando un nombre no está mapeado.

---

## Uso

```bash
# 1. Inspeccionar qué se extraería, sin tocar nada (100% offline)
talkaboutthis ingest --file notas.md --dry-run --provider ollama

# 2. Ver el resultado como tabla legible en vez de JSON
talkaboutthis ingest --file notas.md --dry-run --output table

# 3. Publicar de verdad en GitHub Projects v2
talkaboutthis ingest --file notas.md --publish \
  --project PROJECT_ID --owner OWNER --repo REPO
```

### Flags principales

| Flag | Descripción |
| --- | --- |
| `--file` | Ruta de la transcripción (`.md`, `.txt`, `.docx`) |
| `--dry-run` | Imprime el backlog y sale. **Nunca** hace llamadas de red |
| `--publish` | Publica el backlog en el tablero destino |
| `--provider` | `ollama` \| `openai` \| `anthropic` \| `gemini` |
| `--output` | `json` \| `table` |
| `--mappings` | Ruta a la tabla de alias |
| `--schema` | Ruta a un JSON Schema alternativo |
| `--log-level` / `--log-format` | Configuración de `slog` |
| `--timeout` | Timeout por llamada de red |

---

## Cómo funciona

```text
1. INGESTA     → .md / .txt / .docx  →  texto plano limpio  →  Transcript
2. EXTRACCIÓN  →  Transcript + JSON Schema  →  LLM  →  MeetingBacklogExtraction
                 (con Retry Loop: hasta 3 intentos de auto-corrección)
3. IDENTIDAD   →  "Omar Hernández"  →  @omarhernan
4. DECISIÓN    →  ¿--dry-run?  →  imprimir  |  publicar en el tablero
```

El intercambio con el modelo está gobernado por un JSON Schema formal:
[`docs/specifications/backlog_schema.json`](./docs/specifications/backlog_schema.json).

---

## Arquitectura

```text
cmd/talkaboutthis/        Composition root: solo cablea dependencias
internal/domain/          Entidades, value objects, errores y puertos (interfaces)
internal/application/     Casos de uso que orquestan el flujo
internal/infrastructure/  Adaptadores: parsers/, llm/, identity/, adapters/, logging/
docs/specifications/       Contratos JSON Schema
configs/                  Mapeo de identidades
testdata/                 Fixtures de prueba
```

El dominio **no conoce** ningún adaptador concreto: se apoya en interfaces (`DocumentParser`, `LLMProvider`, `IdentityMapper`, `ProjectBoardAdapter`). Añadir Jira, Linear o Trello es escribir un adaptador nuevo **sin tocar `internal/domain`**.

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

---

## Licencia

MIT — ver [`LICENSE`](./LICENSE).