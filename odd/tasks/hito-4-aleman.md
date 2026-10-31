# Hito 4 — alemán como idioma extra

Objetivo: practicar **alemán** además de inglés, con el mismo tutor, las mismas correcciones y la misma voz. Solo alemán por ahora: francés, italiano y portugués quedan para después (italiano arrastra el problema de que Phonon no tiene voz).

El hito arrastra dos mejoras de voz que no son específicas del alemán pero se implementan acá porque tocan el mismo pipeline: **VAD por energía en Go** (corta al callarte, sin segundo Enter) y **`say` como backend de TTS** (macOS, sin Ollama; es lo que destraba el italiano y sirve de fallback).
Branch: `main`. Delivery: work-unit commits. Runner: `go test -race ./...`. Estado: **solo planificado, sin código**.

## Specs

- **S1** — `--target en|de`, default `en`. El idioma target decide en qué idioma responde el tutor, corrige la revisión, resume la memoria, transcribe el micrófono y habla la voz. Los flags explícitos (`--stt-lang`, `--tts`) ganan sobre el default derivado.
- **S2** — El estado pasa a ser **por alumno y por idioma**: `users/<nombre>/<idioma>/state.json`. Cada idioma es un tutor nuevo: nivel, errores, mnemónicos, resumen, turnos **y datos de la persona** (nombre, país, ciudad, hobby) arrancan vacíos, y el onboarding se repite. Migración: el `users/<nombre>/state.json` actual se **copia** a `users/<nombre>/en/state.json` la primera vez (nunca se mueve ni se borra), así el perfil inglés sobrevive; el alemán arranca de cero.
- **S3** — El tutor en alemán: "You are a friendly **German** conversation partner" y "Always answer in **German**" (hoy dice English, `internal/conv/prompt.go:20,25`). El resumen dice "memory of a German tutor".
- **S4** — La revisión corrige alemán: "You are a **German** teacher", y las categorías usan términos alemanes (`akkusativ`, `wortstellung`, `perfekt`). Las notas siguen en el idioma del alumno (`--notes`, default Spanish).
- **S5** — Perfil MCER **alemán** por nivel, documentado en [`hito-4-temario-aleman.md`](hito-4-temario-aleman.md) (gramática, temas y vocabulario A1–C2). No es traducción del inglés: *present perfect* no existe en alemán. El temario es una síntesis a validar por un docente.
- **S6** — Voz: `phonon:de` (141 MB, **una sola voz `default`**). Con `--target de` el default de `--tts` pasa a `phonon:de`; los alias `femenina`/`masculina` **no aplican** (no hay catálogo de voces) y `/voice` debe decirlo en vez de fallar raro. Licencia: los 40M son CC BY-NC 4.0, no comercial.
- **S7** — Micrófono: con `--target de`, `--stt-lang` default `de`. Para `--stt whisper` hace falta el modelo **multilingüe** (`ggml-small.bin`), no el `.en`: si el modelo configurado termina en `.en`, avisar al arrancar que ese modelo no transcribe alemán.
- **S8** — Lo que **no** cambia: personas (`--persona`, `/persona`), mnemónicos, `/forget`, `/level`, silencio, Enter vacío, perfiles por persona, `/status`. La persona de slang americano es solo para inglés: las de alemán son opt-in.
- **S9** — **VAD por energía, en Go.** La grabación termina sola tras `--vad <ms>` de silencio (default `800`, `0` lo apaga) **después** de haber detectado voz; el segundo Enter sigue funcionando como corte manual. Reusa `voice.Silent` (RMS, ya escrito en Go): el recorder pasa de juntar todo el stdout a leerlo por bloques de 100 ms. Sin modelos, sin Python, sin dependencias nuevas. No incluye *barge-in* (interrumpir al tutor hablando): eso necesita cancelar el eco de los parlantes y queda afuera.
- **S10** — **`say` como backend de TTS.** `--tts-backend ollama|say` (default `ollama`); con `say` la voz sale de macOS, sin Ollama y sin red. `--say-voice` elige la voz del sistema (default: la del idioma target). No hace falta para alemán (`phonon:de` existe), pero es lo que permite italiano y lo que mantiene la app usable si Ollama está caído. Contra: suena más robótico que Phonon y es solo macOS. El detalle de formato (archivo temporal → PCM que ya consume el reproductor) se confirma al implementar.

## Tasks

| ID | Specs | Ruta | Estado |
|---|---|---|---|
| T1 | S2 | inline: ruta por idioma + copia del perfil actual a `en/` (riesgo: datos guardados) | **hecho** |
| T2 | S1, S3, S4 | inline: `--target` atravesando tutor, revisión, resumen | **hecho** |
| T3 | S5 | inline: perfiles MCER de alemán, desde el temario documentado | **hecho** |
| T4 | S6, S7 | inline: default de voz y STT por idioma + validaciones | **hecho** |
| T5 | S8 | docs: README, `/help`, una persona de ejemplo en alemán | **hecho** |
| T6 | S9 | inline: VAD por bloques en `internal/voice` + `--vad` + tests con PCM sintético | **hecho** |
| T7 | S10 | inline: `SaySynth` en `internal/speech` + `--tts-backend`/`--say-voice` | **diferido** |

## Log

**L1** (usuario, textual): «dejalo anotado como hito 4, pero por ahora solo el idioma aleman como extra»

**L2** — Contexto: la pregunta previa fue si se podían reutilizar los harness para francés, italiano o alemán. Análisis verificado: lo hardcodeado a inglés son 4 prompts (`tutorRules`, `summaryPrompt`, la revisión y el perfil MCER), el modelo local de whisper (`.en`) y los defaults de idioma; Phonon tiene `:de` con una sola voz, y los checkpoints 40M son CC BY-NC.

**L3** — Decisiones de diseño, pendientes de aprobación: el estado se separa por idioma **dentro del mismo archivo** (no `users/<nombre>/de/state.json`) para no preguntar dos veces el nombre; los datos de la persona quedan compartidos. Esa migración es lo único con riesgo real de datos, por eso T1 va primero y sola.

**L4** — Supuestos a confirmar: el contenido de S5 (gramática alemana por nivel) es una propuesta, no un currículum validado; conviene que lo revise alguien que enseñe alemán antes de escribirlo en el código.

**L5** (usuario, textual): «con t1, quiero que se empiece de 0 con todas las preguntas, ya que seria como encontrarme con un nuevo tutor que no sabe nada de mi y con el punto 2, podemos averiguar el temario de aprendizaje del aleman y documentar»

**L6** — Cambio de decisión: **nada se comparte entre idiomas**. Se descarta el mapa `languages` dentro de un archivo (que era más barato de migrar pero obligaba a separar datos personales de perfil). Cada idioma es un tutor nuevo, con su onboarding completo, en `users/<nombre>/<idioma>/state.json`. Eso simplifica el código y hace la migración trivial: copiar el archivo actual a `en/`.

**L7** — Temario documentado en `hito-4-temario-aleman.md`. Fuentes oficiales no legibles (Goethe 403, *Wortlisten* en PDF), así que es una síntesis con el marco MCER / *Profile deutsch*, marcada para validar. Incluye un test propuesto para que el perfil alemán no arrastre gramática inglesa.

**L8** (usuario, textual): «anotalo para el hito 4 junto con say»

**L9** — El VAD va **en Go**, no en Python: su `vad.py` ya es energía RMS sin modelo, y `voice.Silent` es la misma cuenta. El cambio real es que el recorder lea el stdout de ffmpeg por bloques en vez de juntarlo entero. `say` entra como segundo backend de TTS porque es lo que destraba el italiano y el fallback sin Ollama, aunque para alemán no sea necesario.

**L10** — T1 hecho: `users/<nombre>/<idioma>/state.json`, `--target en|de`, y migración del perfil sin idioma a `en/` (copia, el original queda). Verificado a mano: arrancar con `--target de` sobre un perfil viejo lo migra a `en/` y crea `de/` vacío. Los prompts siguen en inglés: eso es T2.

**L11** — T2 hecho: el idioma atraviesa las reglas del tutor, el resumen y la revisión (`conv.LanguageName`, plantillas en `tutorRules`/`summaryPrompt`, "a German teacher" en la revisión). Con `--target de` el perfil MCER inglés **no** se inyecta (solo el largo de respuesta) para no enseñar gramática inglesa: eso llega en T3. Verificado con qwen3.5:9b: responde en alemán.

**L12** — T3 hecho: `levelProfiles` pasó a ser por idioma (`en`, `de`), con los seis niveles alemanes del temario y la gramática en términos alemanes (Präsens, Perfekt, Akkusativ, Konjunktiv II). Un idioma sin perfil cae al nivel pelado más el largo de respuesta, nunca a la gramática inglesa. Test nuevo: el perfil alemán existe en los seis niveles y no contiene gramática inglesa.

**L13** — T4 hecho: `--target de` deriva `--tts phonon:de` y `--stt-lang de` (los flags explícitos ganan), los modelos de una sola voz avisan en vez de mandar un alias inexistente, y `--stt whisper` con un `.en` se niega a grabar en otro idioma. Además el probe de `--selftest` ahora usa una frase alemana con error (`Gestern ich gehe…` → `Gestern ging ich…`). `phonon:de` descargado (122 MB) y selftest alemán en verde: voz, chat y correcciones.

**L14** — T5 hecho, con un agregado pedido por el usuario: en **A1 alemán el tutor da una lección, no una charla**. El modo lo activa el nivel (no un flag ni una persona): una palabra o frase por turno, la dice, pide repetirla (`Sprich nach: …`) y avanza por saludos, «Ich heiße…», números, días y «Ich komme aus…». Sin gramática y sin slang, y solo en alemán (la voz es el checkpoint alemán). Verificado con qwen3.5:9b: dos turnos seguidos de lección. Persona de ejemplo en `personas/deutsch.md`.

**L15** — T6 hecho: `--vad <ms>` (default 800, 0 apaga), VAD por energía sobre bloques de 100 ms, y no corta hasta haber escuchado voz. `loud()` se separó de `Silent()` porque el mínimo de 0.2 s hace que todo bloque corto parezca silencio. El recorder pasó de juntar stdout a leerlo por bloques y **cerrar el pipe** al terminar: un nieto que lo retiene colgaba la sesión (lo descubrió un test con un ffmpeg falso, que saqué).

**L16** (usuario, textual): «deja esto para mas adelante», sobre T7.

**L17** — El hito 4 se cierra con T1–T6 hechos y **T7 diferido**: `say` no hace falta para inglés ni alemán (`phonon:de` existe) y solo destraba el italiano o el uso sin Ollama. Se retoma cuando el italiano entre en el horizonte, o como parte del hito que lo agregue. El plan queda como está: cuando se implemente, es un archivo nuevo en `internal/speech` más dos flags.

**L18** — Idea anotada para el futuro: pasar a la revisión la **confianza del STT**. Hoy solo le decimos si el turno vino del micrófono; con un número (Groq `avg_logprob` en `verbose_json`; whisper.cpp puede dar probabilidades por token con `-oj`) la nota podría volverse neutra cuando la transcripción es dudosa, en vez de arriesgar una corrección sobre una palabra que el alumno nunca dijo. No es una línea: hay que normalizar dos backends distintos.
