# ADR-NNNN — Título en imperativo

- **Estado**: Propuesta | Aceptada | Rechazada | Obsoleta | Reemplazada por [ADR-XXXX](…)
- **Fecha**: AAAA-MM-DD
- **Decide**: quién
- **Afecta a**: qué parte del sistema

---

## Contexto

La situación que obliga a decidir. Con sus **fuerzas en conflicto**: lo que
empuja en una dirección y lo que empuja en la contraria.

Incluye lo que ya se intentó y falló, y las restricciones que no se pueden
negociar (requisitos, plazos, herramientas disponibles).

> Escribe esto como si el lector no supiera nada. Si necesita contexto previo,
> el documento está mal: los ADR se leen en orden arbitrario.

## Decisión

Qué se hizo, **en pasado y en primera persona del plural**.

> «Se decidió…», «Se optó por…», «Se descartó…»

Una sola decisión. Si hacen falta dos, hacen falta dos ADR.

## Alternativas consideradas

**Opción B — el nombre de la opción.** Por qué no se eligió. Qué se habría
perdido.

La alternativa descartada es la parte **más útil** del documento: impide que
alguien vuelva a proponerla dentro de un año y explica por qué el código tiene
esa forma rara.

## Consecuencias

### Buenas

- …
- …

### Malas

Esta sección es obligatoria y no se puede dejar vacía. Un ADR con solo beneficios
no es una decisión, es propaganda.

- **Coste X**: …
- **Deuda Y**: …

### Neutras

- Cambia Z, que era neutro.

## Verificación

Cómo se sabe que la decisión se está cumpliendo, y qué se rompería si deja de
cumplirse. Si hay un test que la vigila, se nombra.

| Regla | Test |
|---|---|
| … | `path/al/test.go` |

## Referencias

- [Otros ADRs](…)
- [Documentos de arquitectura o especificación](…)