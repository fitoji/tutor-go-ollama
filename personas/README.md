# Personas

Cada `nombre.md` es la **voz** del tutor; `nombre-review.md` es el **tono de las correcciones**. Todo lo que está arriba de una línea `---` es documentación: el modelo no lo ve.

| Archivo | Qué hace |
|---|---|
| `slang-americano.md` | tutor que habla slang americano todo el tiempo |
| `slang-americano-review.md` | correcciones con chicana, sin dejar de nombrar el error |
| `slang-ny.md` | variante neoyorquina: Brooklyn, grupo de chat, hip-hop |
| `deutsch.md` | tutor de alemán con voz de Berlín |

## Cómo se usan

```sh
# al arrancar
./speakeasy --persona personas/slang-americano.md \
            --review-persona personas/slang-americano-review.md

# en plena sesión (los busca en ~/.speakeasy/personas/)
/persona slang-americano
/persona ./personas/otro.md
/persona off          # vuelve al tutor de fábrica
```

Sin `--persona` ni `~/.speakeasy/tutor.md`, usa el tutor de fábrica (neutro).

## Cómo escribir una

1. Copiá uno de los dos ejemplos.
2. Arriba de `---`, explicá para qué sirve (para vos).
3. Abajo de `---`, escribí el prompt **en inglés**: es el idioma en que trabaja el modelo.
4. No hace falta tocar código ni reiniciar si la cambiás con `/persona`.

Las reglas fijas —texto plano para el TTS, no corregir dentro de la respuesta, responder en inglés, 45 palabras— no se pueden pisar desde acá: si se pudieran, un archivo mal escrito rompería el audio o las correcciones.
