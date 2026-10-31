# Hito 6 — vocabulario con repaso

Objetivo: que el tutor te haga practicar las palabras que te cuestan. Una lista propia por alumno e idioma, con categoría, rating y repaso espaciado, enchufada al mnemónico Campayo que ya existe.
Branch: `main`. Delivery: work-unit commits. Runner: `go test -race ./...`. Estado: **solo planificado**. Depende del hito 5 solo para mostrarse en `/progress`.

## Specs

- **S1** — El estado gana una lista `words` (por alumno e idioma, como todo después del hito 4): `{term, meaning, category, rating, added, due, interval, hits, misses}`. `rating` 1–5 (cuánto te importa practicarla), `interval` en días, `due` fecha. Tope 300 palabras; al llegar, se rechaza la nueva con un mensaje claro. Aditivo.
- **S2** — Comandos: `/word <term> = <significado> [#categoría] [*rating]` agrega o actualiza (ej: `/word house = casa #home *4`); `/words [categoría]` lista con su estado (`due` o en cuántos días); `/forget word <term>` borra una.
- **S3** — Repaso espaciado con intervalos fijos: **1, 3, 7, 21 y 60 días**. Acertar avanza al siguiente intervalo y suma `hits`; fallar vuelve a 1 día y suma `misses`. `due` se recalcula siempre.
- **S4** — `/review [n]` (default 10): toma hasta `n` palabras vencidas, ordenadas por `due`, y pregunta una por vez — la palabra en el idioma target, vos escribís el significado (o al revés con `--review-reverse`). Al fallar, si hay mnemónico, lo muestra.
- **S5** — El tutor recibe en el prompt hasta **5 palabras vencidas** y las de la **categoría del tema actual**, con la instrucción de usarlas con naturalidad y **nunca listarlas**. No se agregan palabras nuevas al prompt si el tema no da.
- **S6** — El mnemónico de una palabra reusa la convención existente: `mnemonic-<palabra>` en `facts`. No se duplica ni se inventa otro formato; `/review` lo lee de ahí.
- **S7** — `/progress` (hito 5) suma "palabras repasadas en el período" leyendo los eventos `word` del registro. Si el hito 5 no está, el hito 6 funciona igual.

## Tasks

| ID | Specs | Ruta | Estado |
|---|---|---|---|
| T1 | S1 | inline: lista `words` en el estado, tope y normalización (riesgo: forma del estado) | pendiente |
| T2 | S2 | inline: `/word`, `/words`, `/forget word` | pendiente |
| T3 | S3, S4 | inline: repaso espaciado y `/review` con `now` inyectable | pendiente |
| T4 | S5, S6 | inline: bloque de vocabulario en el prompt + mnemónico reusado | pendiente |
| T5 | S7 | inline: contar repasos en los eventos del hito 5 (si existe) | pendiente |
| T6 | — | docs: README, `/help`, una persona de ejemplo que use el vocabulario | pendiente |

## Log

**L1** (usuario, textual): «ok, progress hito 5 y vocabulario hito 6»

**L2** — Decisiones: lista propia (`words`) y no `facts`, porque `facts` es un mapa plano con tope de 40 y sin campos por palabra. Intervalos fijos en vez de SM-2: menos código, predecible y suficiente para un tutor de conversación. El repaso es un comando explícito (`/review`), no automático, para que no interrumpa la charla.

**L3** — Lo que **no** entra: importar listas de afuera (Anki/CSV), audio por palabra, y calificación automática de si usaste bien una palabra durante la charla (eso último sería un tercer juez y no hay evidencia de que valga la pena).
