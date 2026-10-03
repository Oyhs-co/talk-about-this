# Funcionalidades críticas

Los compromisos que **no se pueden romper** sin romper el proyecto.

No es documentación de módulos. `docs/architecture/` dice cómo está construido;
este directorio dice **qué tiene que seguir siendo cierto**, y qué se rompe cuando
deja de serlo.

## Índice

| Documento | La pregunta que responde |
|---|---|
| [seguridad-y-secretos.md](seguridad-y-secretos.md) | ¿Qué pasa con un token filtrado? |
| [observabilidad.md](observabilidad.md) | ¿Cómo se depura una ejecución que ya terminó? |
| [manejo-de-errores.md](manejo-de-errores.md) | ¿Qué pasa cuando algo falla? |
| [reintentos-y-timeouts.md](reintentos-y-timeouts.md) | ¿Cuándo se reintenta y cuándo no? |
| [concurrencia.md](concurrencia.md) | ¿Qué se ejecuta en paralelo y por qué es seguro? |

## Por qué estos cinco

```mermaid
flowchart LR
    subgraph 기능["Funcionalidad"]
        F["Extraer 20 tareas<br/>y publicarlas"]
    end

    subgraph crit["Lo que no puede fallar"]
        C1["🔒 La credencial<br/>no se filtra"]
        C2["👁️ Se puede saber<br/>qué pasó"]
        C3["⚠️ El fallo es<br/>acotado y accionable"]
        C4["🔄 No se reintenta<br/>lo inútil"]
        C5["⚡ No hay carreras<br/>ni duplicados"]
    end

    F --> C1 & C2 & C3 & C4 & C5

    style F fill:#d1ecf1,stroke-width:2px
    style C1 fill:#f8d7da
    style C2 fill:#fff3cd
    style C3 fill:#fff3cd
    style C4 fill:#fff3cd
    style C5 fill:#fff3cd
```

Cada uno de estos cinco compromisos se rompe de una forma característica:

| Compromiso | Cómo se rompe | Cómo se detecta |
|---|---|---|
| Seguridad | Alguien pone el token en un mensaje de error | Un test que busca el token en los errores |
| Observabilidad | Una etapa deja de registrar su `job_id` | La mitad de las líneas sin correlación |
| Errores | Un error pierde su causa al envolverlo | Un `errors.Is` que devuelve `false` |
| Reintentos | Se reintenta un 401 | Tres llamadas idénticas y un fallo |
| Concurrencia | Se quita la doble comprobación de la caché | 32 llamadas idénticas a la API |

Ninguno se rompe de forma visible. Por eso tienen su propio documento y sus
propios tests.

## Cada uno tiene un compromiso verificable

```mermaid
flowchart TD
    C["Un compromiso"] --> T["Un test que lo vigila"]
    T --> F["Si el código lo incumple,<br/>el test falla"]
    F --> R["Y el mensaje dice<br/>QUÉ se rompió"]

    style F fill:#d4edda
    style R fill:#d4edda
```

Un compromiso que solo está en un documento **se erosiona**. Uno que está en un
test, no.

## Documentos relacionados

- [`docs/specs/`](../specs/) — qué debe hacer.
- [`docs/architecture/`](../architecture/) — cómo está construido.
- [`docs/adr/`](../adr/) — por qué está construido así.
- [`AGENTS.md`](../../AGENTS.md) — reglas operativas para agentes.