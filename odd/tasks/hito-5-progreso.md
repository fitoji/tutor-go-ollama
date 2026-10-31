# Hito 5 — progreso (`/progress`)

Objetivo: mostrar qué hiciste, no qué sabés. Un `/progress` con días activos, racha, turnos y los errores del período, más un resumen corto en lenguaje natural cuando el modelo está disponible.
Branch: `main`. Delivery: work-unit commits. Runner: `go test -race ./...`. Estado: **solo planificado**.

## Specs

- **S1** — Cada turno guarda **cuándo** pasó: `conv.Turn` gana `at` (RFC3339, `omitempty`). Los turnos viejos no lo tienen: se leen igual y quedan fuera de las cuentas por fecha. Aditivo, sin migración.
- **S2** — Los agregados no alcanzan para "esta semana" (los errores son un contador sin fecha), así que el estado gana un **registro de eventos acotado**: `events: [{at, kind, key}]` con `kind` en `mistake`, `fact`, `mnemonic` (los turnos ya están en `turns`). Tope 1000, se descartan los más viejos. Aditivo.
- **S3** — `/progress [week|month|all]` (default `week`) imprime: días activos, turnos, racha, top 3 de errores del período, datos y mnemónicos del período. Con `all`, todo lo que haya.
- **S4** — Resumen en lenguaje natural: **una** llamada al modelo, timeout 8 s, **falla en silencio** (si no hay modelo o tarda, se imprimen solo los números, sin error). Se apaga con `--progress-summary=false`.
- **S5** — Racha: días consecutivos con al menos un turno, contando desde hoy o desde ayer (si hoy todavía no hablaste, la racha de ayer sigue viva).
- **S6** — Los tests no dependen del reloj: `now` entra por parámetro. Nada de `time.Now()` escondido en la lógica de stats.

## Tasks

| ID | Specs | Ruta | Estado |
|---|---|---|---|
| T1 | S1, S2 | inline: `at` en turnos + registro de eventos acotado (riesgo: forma del estado) | pendiente |
| T2 | S3, S5, S6 | inline: cálculo de stats por período con `now` inyectable | pendiente |
| T3 | S3 | inline: comando `/progress` y su formato | pendiente |
| T4 | S4 | inline: resumen con el LLM, timeout y falla silenciosa | pendiente |
| T5 | — | docs: README + `/help` | pendiente |

## Log

**L1** (usuario, textual): «ok, progress hito 5 y vocabulario hito 6»

**L2** — Decisiones: el progreso necesita eventos fechados, no solo contadores. Se elige un log acotado (1000) en vez de fechas por contador, porque también sirve para "datos aprendidos esta semana" y para el hito 6 (palabras repasadas). El resumen del LLM es opcional y nunca puede romper el comando.

**L3** — Lo que **no** entra: gráficos, historial de sesiones y comparación entre idiomas. El progreso es por perfil (recordá que cada idioma es un tutor nuevo desde el hito 4).
