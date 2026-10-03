---
titulo: Reunión de sincronización — Sprint 42
fecha: 2026-09-28
asistentes: [Omar Hernández, Ana María Ruiz, Luis Cabrera]
---

# Reunión de sincronización — Sprint 42

**Fecha:** 28 de septiembre de 2026
**Asistentes:** Omar Hernández, Ana María Ruiz, Luis Cabrera

## Acuerdos

1. **Migrar** el módulo de autenticación fuera del contexto global.
   Hay que hacerlo antes del cierre de Sprint 43.
2. *Ana* revisará el contrato de la API de pagos, especially los webhooks.
   - Subtask: documentar los reintentos.
   - Subtask: agregar tests de idempotencia.
3. Luis se encarga de reducir el bundle inicial > 300 KB.

> **Nota:** si el parche se retrasa, se escala al Sprint 43.

## Bloqueos

- El token de GitHub caduca el viernes; hay que renovarlo.
- `CREATE INDEX` sobre `transcript_items` bloquea la tabla en producción.
