# Hito 7 — dos modelos: charla rápida, revisión precisa

Objetivo: separar el modelo que conversa del que corrige, y medir con datos si un modelo más grande mejora al tutor. Hoy `--llm` hace las cuatro cosas (charla, correcciones, resumen y extracción de hechos).
Branch: `main`. Delivery: work-unit commits. Runner: `go test -race ./...`. Estado: **solo planificado**.

## Por qué

Con API remota no hay costo de memoria por cargar dos modelos, así que la restricción que obligaba a uno solo ("nunca dos modelos a la vez", que sí vale para Ollama local) no aplica. Y las dos tareas tienen prioridades opuestas:

| Tarea | Lo que importa | Modelo ideal |
|---|---|---|
| Charla | latencia hasta el primer token | chico y rápido |
| Revisión (✎, hechos, mnemónicos, `↻`) | precisión y respetar el schema JSON | grande |

## Specs

- **S1** — `--review-llm <modelo>` (o `SPEAKEASY_REVIEW_LLM`): modelo de la pasada de revisión. Vacío = usa `--llm`, así el comportamiento actual no cambia.
- **S2** — La revisión usa `--review-llm` para: correcciones, hechos, mnemónicos y `rewrite`. El resumen rodante y la nota de `/progress` siguen en `--llm` (son texto libre, no schema).
- **S3** — Si `--review-llm` es distinto de `--llm` y el proveedor es **Ollama local**, avisar al arrancar: son dos modelos en 16 GB y `prepare` va a descargar uno para cargar el otro. No bloquear; solo advertir.
- **S4** — `--selftest` prueba el modelo de revisión en la comprobación de correcciones (la que hoy usa el de chat).
- **S5** — Protocolo de medición documentado: las mismas 10 frases con errores conocidos en A1, B1 y C1, con dos modelos, comparando: correcciones útiles vs inventadas, respeto de "no corregir en la respuesta" y de las 45 palabras, formato del mnemónico, y tiempo hasta el primer token.

## Tasks

| ID | Specs | Ruta | Estado |
|---|---|---|---|
| T1 | S1 | inline: flag + `Settings.ReviewModel` + `settingsFor` | pendiente |
| T2 | S2 | inline: `correctionRequest` usa el modelo de revisión | pendiente |
| T3 | S3 | inline: aviso cuando difieren con Ollama local | pendiente |
| T4 | S4 | inline: el probe de correcciones usa el de revisión | pendiente |
| T5 | S5 | inline: `odd/tasks/hito-7-medicion.md` con las 10 frases y la tabla de resultados | pendiente |
| T6 | — | docs: README (flags) | pendiente |

## Log

**L1** (usuario, textual): «dejalo apuntado para un feature futuro,» — sobre separar `--llm` de un `--review-llm` y medir si un modelo más grande mejora la experiencia.

**L2** — Evidencia de esta sesión con qwen3.5:9b que motiva el hito: escribió *"welcher Film hast du geschaut"* (debía ser **welchen**), corrigió dentro de la respuesta contra lo que pide el prompt, generó un mnemónico flojo (*"el verbo go se rompe"*) y una vez inventó un hecho copiando el ejemplo del prompt. Son fallas de seguir instrucciones y de precisión, justo lo que la pasada de revisión necesita.

**L3** — Lo que **no** se toca: memoria, niveles, personas y perfiles. El modelo solo cambia qué tan bien se ejecutan los prompts. Tampoco se promete que sea más rápido: un 70B remoto genera más tokens por segundo que un 9B local, pero suma el viaje de red.

**L4** — Antes de escribir código conviene la medición (T5): si el 70B no mejora las correcciones de forma visible, el hito se descarta y se ahorra la complejidad.
