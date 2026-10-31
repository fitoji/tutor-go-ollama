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
- **S6** — **Ollama Cloud como opción de modelo grande sin RAM local**: `--provider custom --api-base https://ollama.com/v1 --api-key-env OLLAMA_API_KEY --llm <modelo>` ya funciona sin código (su API es compatible OpenAI: streaming, JSON mode y `response_format`; no expone `num_ctx`). La medición de S5 se hace también contra un modelo de nube.
- **S7** — Un solo proveedor: `--review-llm` comparte `--provider`/`--api-base`/`--api-key-env` con `--llm`. Mezclar charla local + revisión en la nube exige además `--review-api-base` y `--review-api-key-env`; si no se agregan, el hito solo permite dos modelos **del mismo proveedor**.
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
| T7 | S7 | inline: decidir si `--review-llm` suma proveedor propio o queda atado al de la charla | pendiente |

## Log

**L1** (usuario, textual): «dejalo apuntado para un feature futuro,» — sobre separar `--llm` de un `--review-llm` y medir si un modelo más grande mejora la experiencia.

**L2** — Evidencia de esta sesión con qwen3.5:9b que motiva el hito: escribió *"welcher Film hast du geschaut"* (debía ser **welchen**), corrigió dentro de la respuesta contra lo que pide el prompt, generó un mnemónico flojo (*"el verbo go se rompe"*) y una vez inventó un hecho copiando el ejemplo del prompt. Son fallas de seguir instrucciones y de precisión, justo lo que la pasada de revisión necesita.

**L3** — Lo que **no** se toca: memoria, niveles, personas y perfiles. El modelo solo cambia qué tan bien se ejecutan los prompts. Tampoco se promete que sea más rápido: un 70B remoto genera más tokens por segundo que un 9B local, pero suma el viaje de red.

**L4** — Antes de escribir código conviene la medición (T5): si el 70B no mejora las correcciones de forma visible, el hito se descarta y se ahorra la complejidad.

**L5** (usuario, textual): «ok apunta para el hito 7» — sobre poder usar la versión en nube de Ollama con modelos más grandes.

**L6** — Verificado en la doc de compatibilidad de Ollama: `https://ollama.com/v1` es compatible OpenAI y soporta streaming, `response_format` (JSON mode), `max_tokens` y `temperature`; **no** expone `num_ctx`. O sea que el modelo grande para medir no requiere integración nueva: alcanza con `--provider custom --api-base https://ollama.com/v1 --api-key-env OLLAMA_API_KEY --llm <modelo>`. Los nombres salen de `https://ollama.com/api/tags`; en la app/CLI se usa `modelo:cloud`, pero para la API va el nombre pelado.

**L7** — Consecuencia de diseño que quedó anotada como S7: hoy el proveedor es global, así que `--review-llm` solo puede elegir un modelo distinto **dentro del mismo proveedor**. Para charla local con `qwen3.5:9b` y revisión en Ollama Cloud hacen falta dos flags más (`--review-api-base`, `--review-api-key-env`) y resolver el cliente por tarea.
