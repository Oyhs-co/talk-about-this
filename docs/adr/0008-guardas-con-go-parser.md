# ADR-0008 — Guardas arquitectónicas con `go/parser`

- **Estado**: Aceptada
- **Fecha**: 2026-10-02
- **Decide**: el equipo del proyecto
- **Afecta a**: `internal/domain/architecture_test.go`, `pkg/sdk/frontera_test.go`

## Contexto

RNF-01 y RNF-02 («el dominio no depende de infraestructura») son reglas
arquitectónicas. Y una regla que depende de que un revisor la note **no es una
regla**: la presión del calendario la erosiona en dos semanas. Tiene que ser un
test.

El test tiene que responder a tres preguntas:

1. ¿`internal/domain` importa algo prohibido?
2. ¿Hace I/O de red o ejecuta procesos?
3. ¿Tiene dependencias de terceros?

Las tres se pueden contestar leyendo **las rutas declaradas en los imports**. Y
hay tres formas de leerlas, con costos muy distintos.

## Decisión

Se decidió implementarlas con **`go/parser`**, analizandro el árbol de imports de
los ficheros del paquete directamente.

```mermaid
flowchart TD
    T["TestDominioNoImportaInfrastructure"] --> WALK["filepath.Walk(internal/domain)"]
    WALK --> GOF["filtra *.go"]
    GOF --> PARSE["parser.ParseFile(fset, ruta, nil, parser.ImportsOnly)"]
    PARSE --> SPEC["spec.Path.Value"]
    SPEC --> TRIM["strings.Trim(…, '\"')"]
    TRIM --> CHECK{"¿ruta prohibida?"}
    CHECK -->|sí| FAIL["t.Errorf('VIOLACION DE ARQUITECTURA')"]
    CHECK -->|no| OK

    classDef ok fill:#d4edda,stroke:#28a745
    class OK ok
```

### Por qué no `go/build`

```mermaid
flowchart LR
    subgraph build["go/build"]
        B1["Context.ImportDir(dir, 0)"] --> B2["resuelve imports"] --> B3["necesita que las rutas<br/>del sistema coincidan"]
        B3 --> B4["❌ en Windows con rutas POSIX:<br/>'cannot import dir']"
    end

    subgraph parser["go/parser"]
        P1["ParseFile(ruta, ImportsOnly)"] --> P2["solo lee la ruta"] --> P3["✅ el sistema de ficheros<br/>no le importa"]
    end

    classDef mal fill:#f8d7da,stroke:#dc3545
    classDef bien fill:#d4edda,stroke:#28a745
    class B4 mal
    class P3 bien
```

`go/build` está pensado para compilar, y **compilar necesita resolver**. Se
tropezó con esto en la primera versión de la guarda: funcionaba en Linux y
fallaba en Windows con rutas como `/c/Users/...`. Un test que depende del sistema
de ficheros del CI es un test que fallará en algún sitio.

### Por qué no invocar el compilador

La alternativa obvia: un test que ejecute `go list -deps ./internal/domain` y
compruebe la salida. Se descartó por una razón de coste:

| | `go list` | `go/parser` |
|---|---|---|
| Velocidad | ~200 ms (proceso nuevo) | ~1 ms |
| Requiere el toolchain en el PATH | Sí | No |
| Falla en un contenedor sin Go | Sí | No |
| Aísla el test del entorno | No | Sí |

Se ejecutaba en cada `go test ./...`. Multiplicado por cada paquete, convierte la
suite en una colección de subprocesos.

### Por qué no contar como «dependencia de terceros» con la lista de la stdlib

La primera versión comparaba los imports contra una lista escrita a mano de
paquetes de la stdlib. Se descartó casi de inmediato: la lista se quedaba corta
(`archive/zip` no estaba, `iter` no existía todavía…), y **un paquete de la stdlib
que no esté en la lista se confunde con uno externo**.

La heurística elegida es mucho más simple y no se queda obsoleta:

> Si el primer segmento de la ruta contiene un **punto**, es una dependencia
> externa.

```
github.com/foo/bar     → contiene punto → externa ✅
golang.org/x/text      → contiene punto → externa ✅
gopkg.in/yaml.v3       → contiene punto → externa ✅
archive/zip            → sin punto      → stdlib   ✅
log/slog               → sin punto      → stdlib   ✅
```

No hay lista que mantener, y las rutas de la stdlib no llevan punto por
definición: el nombre de dominio de un paquete de la stdlib **es** su ruta.

### El test de confusion

La condición anterior tiene un fallo subtil: `talkaboutthis/internal/…` **no**
tiene punto, así que no se detectaría como dependencia externa. Por eso el test
comprueba dos cosas por separado: módulos prohibidos por prefijo, y «tiene
punto en el primer segmento» para lo externo. Son reglas distintas con el mismo
sintoma.

## Alternativas consideradas

**Opción B — `go/build`.** Descartada: frágil con rutas (el bug de Windows).

**Opción C — invocar `go list`.** Descartada: lenta, y depende del toolchain.

**Opción D — lista manual de la stdlib.** Descartada: se queda corta y confunde
lo que falta con lo externo.

**Opción E — no testear; confiar en la revisión.** Descartada: es exactamente el
escenario que hace que las reglas se pudran.

## Consecuencias

### Buenas

- **Rápido**: unos milisegundos, sin subprocesos.
- **Portable**: funciona en Windows, Linux y macOS sin condiciones.
- **Sin lista que mantener.**
- **El mensaje de fallo es accionable:**

  ```
  VIOLACION DE ARQUITECTURA (RNF-01): el nucleo importa
  "talkaboutthis/internal/infrastructure".
             El dominio solo depende de interfaces.
             Declara el contrato en domain/ports.go e implementalo
             en infrastructure.
  ```

### Malas

- **Los imports sin resolver no se comprueban.** `go/parser` no necesita que el
  código compile, así que un import a un paquete inexistente pasa. Para estas tres
  preguntas da igual: si la ruta importada es prohibida, lo es exista o no.
- **No detecta dependencias transitivas.** Si `parsers` importara `net/http` y
  `domain` importara `parsers`… pero esa segunda import ya está prohibida por la
  regla de módulos. Las dos guardas juntas cubren el caso.
- **El análisis es textual sobre la ruta declarada**, no sobre el grafo real. Es
  suficiente para este propósito y mucho más simple.

## Verificación

Las guardas están escritas, pero **no se han ejecutado nunca**. Un test que vigila
una regla y no se ha ejecutado es una hipótesis.

```bash
go test ./internal/domain/ -run 'Arquitectura|IO|Dependencias' -v
```

## Referencias

- [ADR-0001 — Arquitectura hexagonal](0001-arquitectura-hexagonal.md)
- [Las capas](../architecture/capas.md#reglas-de-dependencia-verificables)