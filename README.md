# speakeasy

Practica inglés hablando con un LLM, desde la terminal. Pensado para un Mac mini M4 de 16 GB: un solo binario en Go, sin dependencias externas. La voz es local (Phonon, vía Ollama); el chat puede ser una API económica (por defecto Groq, con plan gratuito) o un modelo local.

Estado: hitos 1 y 2 (conversación por texto con respuesta hablada, memoria acotada, correcciones). Falta el hito 3, la entrada por voz (ver más abajo).

## Uso rápido (Groq, para testear)

```sh
export GROQ_API_KEY=...        # clave gratuita desde la consola de Groq
ollama pull phonon             # solo si quieres voz

go build -o speakeasy ./cmd/speakeasy
./speakeasy                    # chat en Groq + voz local
./speakeasy --mute             # solo texto: no hace falta Ollama
```

Escribes en inglés (o en español; el tutor siempre responde en inglés). Mientras suena la respuesta se calculan las correcciones de tu mensaje. Si escribes otra cosa mientras habla, se interrumpe. `Ctrl-C` corta el turno; en el prompt, sale.

Al arrancar te pregunta quién sos (`usuario:`); con Enter mantiene el último. Cada nombre guarda su propio nivel y sus propios errores, así que dos personas pueden compartir la máquina sin pisarse el perfil (ver [Un perfil por persona](#un-perfil-por-persona)).

Comandos: `/reset`, `/level B2`, `/topic cine`, `/forget city`, `/status`, `/quit`.

## Proveedores

| `--provider` | Chat | Clave | Modelo por defecto |
|---|---|---|---|
| `groq` (por defecto) | `api.groq.com/openai/v1` | `GROQ_API_KEY` | `llama-3.3-70b-versatile` |
| `xai` | `api.x.ai/v1` | `XAI_API_KEY` | ninguno: pasa `--llm` |
| `custom` | el de `--api-base` (DeepSeek, OpenRouter...) | `LLM_API_KEY` o `--api-key-env` | pasa `--llm` |
| `ollama` | local, `qwen3:8b` | no hace falta | `qwen3:8b` |

Ejemplos:

```sh
./speakeasy --llm llama-3.1-8b-instant                     # más rápido, menos preciso
./speakeasy --provider xai --llm <modelo>
./speakeasy --provider custom --api-base https://api.deepseek.com/v1 --llm <modelo> --api-key-env DEEPSEEK_API_KEY
./speakeasy --provider ollama                              # todo local
```

Con un proveedor remoto **tus mensajes salen a un servidor externo** (la app lo recuerda al arrancar). Usa modelos sin razonamiento largo: los que «piensan» gastan el límite de tokens antes de contestar. Si el modelo emite bloques `<think>...</think>`, se descartan.

Otras opciones (flag o variable): `--user` / `SPEAKEASY_USER` (nombre del alumno; si falta, lo pregunta), `--tts` / `SPEAKEASY_TTS`, `--voice`, `--num-ctx` (4096 remoto, 8192 Ollama), `--keep-alive` (solo Ollama), `--notes` (idioma de las correcciones, Spanish), `--mem-limit` (MiB para este proceso), `--think` (solo Ollama), `--corrections=false` (oculta los `✎`; la pasada de revisión sigue guardando datos), `--state` (archivo exacto, sin separar por usuario), `--host` (el Ollama que sirve la voz), `--selftest` (ver abajo).

### Antes de la primera sesión: `--selftest`

```sh
./speakeasy --selftest            # chat + correcciones + voz, según lo configurado
./speakeasy --selftest --mute     # sin Ollama, solo chat y correcciones
```

Hace las mismas peticiones que la app en un turno real —versión de Ollama, modelo de voz presente, síntesis que devuelva PCM, un chat de una línea y una corrección en JSON— e imprime una línea por comprobación. Sale con código 1 si algo falla, así que sirve en un script. No toca tu conversación guardada ni descarga modelos de Ollama.

```
[ok]    ollama:       0.40.13 at http://127.0.0.1:11434
[ok]    voice:        phonon:latest is pulled
[ok]    speech:       2.8 s of PCM (134400 bytes)
[ok]    chat:         "ready" (19 prompt tokens)
[ok]    corrections:  1 corrections, first "Yesterday I go" -> "Yesterday I went" (past simple tense)
```

La comprobación de correcciones manda una frase con un error obvio (*"Yesterday I go to the cinema with my friend"*); si el modelo no devuelve ninguna corrección, cuenta como fallo: el formato JSON se está ignorando y la función no sirve en la práctica.

## Cómo se cuida la memoria

Lo que mató a LinguaLoop fue perder contexto y cargar varios modelos a la vez. Aquí:

- **Con chat remoto, en tu Mac solo vive Phonon (~120 MB).** Con Ollama local, como mucho dos modelos: el LLM y Phonon. No hay selector de modelos en la app; se cambia con `--llm` y se reinicia. Al arrancar, `speakeasy` descarga de Ollama todo lo que no sea suyo (`/api/ps` + `keep_alive: 0`).
- **El resumen y las correcciones usan el mismo modelo que la conversación.** Nunca se carga un segundo modelo para eso.
- **Ollama: `num_ctx` fijo e idéntico en todas las peticiones.** Si cambia entre peticiones, Ollama recarga el modelo; y si no se indica, el valor por defecto es pequeño y trunca en silencio el principio del prompt (el system prompt incluido).
- **La app es dueña de la memoria, no el modelo.** Cada petición se reconstruye: reglas del tutor + perfil (nivel, temas, errores recurrentes) + resumen rodante + últimos turnos que quepan en el presupuesto (`--num-ctx`). Cuando los turnos guardados pasan del 45 % del presupuesto, los más antiguos se resumen en segundo plano (≤120 palabras). La estimación de tokens es pesimista (3 caracteres por token). En remoto el presupuesto es de 4096 para que cada petición sea pequeña y quepa en los límites por minuto del plan gratuito.
- **Audio con memoria acotada:** como mucho ~3 clips existen a la vez (sonando, en espera, sintetizándose). Si el reproductor se atasca, el flujo del LLM se frena en vez de acumular.
- **Estado:** un JSON pequeño **por alumno** (ver abajo), con escritura atómica, máximo 200 turnos, 30 categorías de error, 5 temas y 40 datos personales.
- **Este proceso:** ~10 MB de RSS medido contra servidores simulados; `debug.SetMemoryLimit` configurable.

### Presupuesto estimado (no medido) en 16 GB con Ollama local

| Pieza | Aprox. |
|---|---|
| qwen3:8b (cuantizado) | ~5 GB |
| KV cache a 8K de contexto, fp16 | ~1,1 GB (36 capas × 8 cabezas KV × 128 × 2 × 2 B por token) |
| Phonon 31M | ~0,12 GB |
| speakeasy | ~0,01 GB |

Con Groq te ahorras los ~6 GB del LLM. `/status` muestra lo que Ollama tiene cargado.

### Ajustes recomendados en el servidor Ollama

```sh
OLLAMA_MAX_LOADED_MODELS=2 OLLAMA_NUM_PARALLEL=1 ollama serve
# opcionales, comprueba que tu versión los soporta:
# OLLAMA_FLASH_ATTENTION=1 OLLAMA_KV_CACHE_TYPE=q8_0   (KV cache a la mitad)
```

## Un perfil por persona

El nivel y los errores recurrentes son de quien practica, no de la máquina. Al arrancar la app pregunta el nombre (sin contraseña, sin cuentas) y con eso elige el archivo de estado:

```
~/.speakeasy/
├── state.json                 # perfil viejo, compartido (solo se lee una vez)
├── last-user                  # el último nombre usado, para ofrecerlo con Enter
└── users/
    ├── ana/state.json         # nivel, temas, errores, resumen y turnos de Ana
    └── beto/state.json
```

- **`--user Ana`** (o `SPEAKEASY_USER=Ana`) se salta la pregunta: sirve para scripts y para atajos en el shell.
- **Sin terminal** (stdin por caño, `< /dev/null`, o fd 0 cerrado) no pregunta nada: usa el último nombre, o `default` si no hay ninguno. Nunca se queda esperando ni ensucia la primera línea de la salida.
- **El nombre se normaliza** a minúsculas y guiones para el directorio: `Ana`, `ana` y ` Ana ` son la misma persona; `Ana María` va a `users/ana-mar-a/`.
- **`--state <archivo>`** sigue significando *ese archivo exacto*, sin separar por usuario y sin escribir un `last-user` al lado. Útil para probar con un perfil descartable.
- **Migración:** la primera vez, si existe el `state.json` viejo y todavía no hay ningún alumno, se **copia** (no se mueve) al primer perfil que arranque. Un segundo nombre nunca hereda ese perfil: arranca limpio.
- `/status` muestra el nombre y el archivo en uso, que es la forma rápida de comprobar que no estás pisando el perfil de otro:

```
usuario: Ana · /Users/vos/.speakeasy/users/ana/state.json
chat: groq · llama-3.3-70b-versatile
conversación: 12 turnos, resumen de 87 palabras, nivel B2
```

## Lo que el tutor recuerda de vos

Además de tu nivel y tus errores, el tutor guarda **datos personales** para que la charla tenga contexto. En la primera sesión pregunta, de a una por turno: tu nombre, de dónde sos, dónde vivís y qué te gusta hacer. Con eso arranca el tema y deja de preguntar.

Después sigue aprendiendo: si contás que trabajás de algo, que tenés hermanos o que el mes que viene viajás, también lo guarda. La app es la que recuerda, no el modelo: cada turno reconstruye el system prompt con lo que sabe.

```
lo que sabe de vos: city=Madrid · country=Argentina · hobby=cooking · name=Fito
```

- Se guarda en tu perfil (`users/<nombre>/state.json`, campo `facts`), así que cada alumno tiene los suyos.
- **`/forget city`** borra un dato. Sin eso, un nombre mal escrito quedaría pegado para siempre.
- `/status` muestra todo lo que sabe; `/reset` borra la conversación pero **no** los datos.
- Límites: 40 datos, clave de hasta 40 caracteres, valor de hasta 200. Los datos salen solo de lo que escribís: si el modelo devuelve un placeholder o algo que no está en tu mensaje, se descarta.
- Los datos se extraen en la **pasada de revisión** (la misma que corrige), con salida JSON. `--corrections=false` oculta los `✎` pero la pasada sigue corriendo: es la que guarda los datos.

## Correcciones y perfil del alumno

Cada mensaje tuyo son **dos peticiones**, con papeles separados:

| | Tutor | Correcciones |
|---|---|---|
| Qué recibe | reglas + perfil + resumen + últimos turnos | solo tu mensaje |
| Temperatura | 0.7 | 0 |
| Salida | texto libre en streaming | JSON con schema |
| Cuándo | primero | después, mientras suena el audio |

El tutor **nunca corrige en voz alta**: el system prompt le pide que use la forma correcta como si nada. Lo explícito sale por los `✎`, que son la segunda petición.

Cada corrección trae una `category` ("past simple", "articles"...). Esas categorías se acumulan en el perfil, y las **tres más frecuentes** entran al system prompt de cada turno como *recurring trouble spots*. Por eso, si repetís el mismo error tres o cuatro veces, el tutor empieza a usar esa estructura por su cuenta y a hacer preguntas que te fuerzan a practicarla.

`/status` muestra exactamente esas tres, con su contador:

```
errores recurrentes: past simple ×4 · articles ×2
```

Límites que conviene saber:

- Un error aislado no cambia nada: entran solo las tres categorías más frecuentes.
- La categoría la inventa el modelo, no hay taxonomía fija: "past simple" y "past simple tense" cuentan por separado, así que el contador se diluye. Con modelos chicos pasa más.
- La pasada de correcciones no ve la conversación, solo tu mensaje: una respuesta elíptica correcta ("So do I") puede marcarse como error.
- `/reset` borra la conversación pero **no** el perfil: los trouble spots sobreviven. Para borrarlos hay que eliminar el archivo de estado.

El parser tolera las formas en que los modelos rompen el contrato: una lista desnuda en vez del objeto, `[]` cuando no hay nada que corregir, y comillas dobles sin escapar dentro de un valor —esta última era la que producía `could not read corrections`—. Todo cubierto con tests.

## Límites del plan gratuito

Cada turno son dos peticiones (respuesta y corrección) y de vez en cuando una tercera (resumen). Si Groq responde 429, la app espera una vez si el `Retry-After` es corto (≤15 s) y, si no, te dice cuánto esperar. Los límites cambian: confírmalos en la consola de Groq.

## Estructura

```
cmd/speakeasy/       CLI: arranque, comandos, bucle
internal/config/     flags, entorno y proveedores
internal/learner/    quién practica y dónde vive su estado (un archivo por nombre)
internal/llm/        interfaz neutral de chat (Request, Stats, Chatter)
internal/openai/     cliente de chat/completions con SSE (Groq, xAI, ...)
internal/ollama/     cliente de Ollama: chat, voz, ps/unload/load
internal/conv/       memoria: perfil, resumen, ventana de turnos, JSON en disco
internal/tutor/      un turno: prompt, streaming, resumen rodante, correcciones
internal/selftest/   comprobaciones de `--selftest` contra los servicios reales
internal/speech/     frases -> síntesis -> reproducción (afplay en macOS)
```

`go test -race ./...` cubre todo salvo el bucle interactivo de `run` y la reproducción real por `afplay`. Incluye `cmd/speakeasy`: `prepare`, los comandos y `/status` se prueban contra un Ollama simulado.

## Próximo: hito 3, entrada por voz

Push-to-talk (mantener una tecla, grabar, transcribir, enviar), sin VAD ni interrupción por voz al principio. Groq también ofrece transcripción por API (Whisper), que podría servir como STT de pruebas; la alternativa local es whisper.cpp o algo basado en MLX.

## Sin verificar

Se desarrolló con servidores simulados (Ollama y compatible con OpenAI). **Verificado en macOS arm64 con Ollama 0.40.13 y phonon**: `--selftest` pasa las cinco comprobaciones contra los servicios reales y una sesión con voz funciona. Queda sin probar contra Groq y contra xAI/custom. Puntos a confirmar:

- Que Groq acepta `max_tokens`, el modo JSON (`response_format: json_object`) con tu modelo y el streaming tal como lo implementa el cliente (el uso de tokens se lee de `usage` o `x_groq.usage`; si no llega, `/status` lo indica).
- Que el modelo elegido cumple el formato JSON de las correcciones. Ya no rompe la sesión: el parser acepta lista desnuda, `[]` y comillas sin escapar, pero un modelo que no corrija nada útil igual te deja sin `✎`.
- xAI y `custom` comparten el mismo cliente que Groq, pero no se han probado contra esas APIs.
