# Hito 3 — entrada por voz

Objetivo: hablarle al tutor en vez de escribir. Un Enter vacío en el prompt `tú>` graba; otro Enter corta, transcribe y manda el texto por el mismo camino que el tipeado.
Branch: `main`. Delivery: work-unit commits. Runner: `go test -race ./...`.

## Specs

- **S1** — En `tú>`, un Enter sin texto arranca la grabación y otro Enter la corta. Con `--stt off`, o sin configuración válida, el Enter vacío se ignora como hoy.
- **S2** — La grabación usa `ffmpeg` con `avfoundation`, dispositivo `--stt-device` (default `:0`), 16 kHz mono WAV, tope `--record-max` (default `30s`). Si falta `ffmpeg`, el mensaje dice cómo instalarlo.
- **S3** — La transcripción usa la API compatible OpenAI (`POST <base>/audio/transcriptions`, multipart con `model`, `language`, `response_format=json` y el archivo). Base y clave configurables (`--stt-base`, `--stt-key-env`); default Groq `whisper-large-v3-turbo` con `GROQ_API_KEY`, idioma `en` (`--stt-lang`).
- **S4** — El texto se muestra como `tú (voz)> ...` y entra al mismo turno que el tipeado: tutor, correcciones por nivel, hechos y mnemónicos.
- **S5** — Audio en silencio o sin voz: se avisa `no entendí nada` y no se manda nada al tutor.
- **S6** — `--stt groq` es el default; `--stt off` lo desactiva. Un Enter vacío nunca rompe la sesión: si algo falla, se informa y se sigue.
- **S7** — Los errores de micrófono (permiso TCC de macOS, dispositivo ocupado) se traducen a una instrucción accionable.

## Tasks

| ID | Specs | Ruta | Estado |
|---|---|---|---|
| T1 | S1, S6 | inline: config (`--stt*`) y el Enter vacío en el loop | hecho |
| T2 | S2, S7 | inline: `internal/voice` recorder ffmpeg + silencio | hecho |
| T3 | S3 | inline: `internal/voice` transcriber Groq + tests | hecho |
| T4 | S4 | inline: integración con `runTurn` | hecho |
| T5 | — | docs: README + `/help` | hecho |

## Log

**L1** (usuario, textual): «vamos por el hito 3, como seria?» … «vamos con enter vacio»

**L2** — Decisiones: trigger por Enter vacío (no modo raw); STT con Groq primero; recorder y transcriber como interfaces para sumar whisper.cpp después. Verificado en la máquina: `ffmpeg 9.0.2` con `avfoundation`; no hay `sox`, `whisper-cli` ni modelos whisper locales.

**L3** — Implementado con Enter vacío + Groq (`whisper-large-v3-turbo`). Paquete `internal/voice` (recorder ffmpeg/avfoundation, transcriber compatible OpenAI, WAV 16 kHz, detección de silencio), flags `--stt*`, `recordTurn` testeado con grabador y transcriptor falsos. Verificado sin micrófono: `--stt off` inerte, aviso cuando falta la clave, header `mic=off` en ese caso. Pendiente: una sesión hablada real.
