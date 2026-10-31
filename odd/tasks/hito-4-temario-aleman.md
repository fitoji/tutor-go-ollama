# Temario de alemán A1–C2 (síntesis para el hito 4)

Qué es: el contenido que va a alimentar el `LevelProfile` alemán de `internal/conv/levels.go`. Un idioma nuevo necesita **su** gramática, no la inglesa traducida.

**Fuentes y honestidad**: intenté leer las páginas del Goethe-Institut y las *Wortlisten* oficiales; el sitio devuelve 403 y los PDF no se pueden leer como texto. Lo de abajo es una **síntesis** armada con el marco del MCER y la progresión estándar de *Profile deutsch* (la referencia que usan Goethe, telc y ÖSD), más el vocabulario aproximado de las listas oficiales que sí se citan en los exámenes. **Pendiente de validar por alguien que enseñe alemán** antes de escribirlo en código. No es un currículum oficial.

## A1 — principiante

- **Puede**: presentarse, preguntar y responder sobre datos personales, comprar, pedir en un restaurante, entender instrucciones simples.
- **Gramática**: Präsens (regulares e irregulares: *sein, haben, werden*), pronombres personales, Nominativ y Akkusativ, artículos determinado/indeterminado, negación (*nicht / kein*), posesivos, verbos separables en presente, modales (*können, müssen, wollen, möchten*), preguntas W- y de sí/no, imperativo (du/Sie), verbo en posición 2, *es gibt*, *gern / lieber*.
- **Temas**: familia, vivienda, comida y bebida, compras, trabajo (básico), tiempo libre, la hora y la rutina, países e idiomas.
- **Vocabulario**: ~650 palabras (lista A1 de Goethe).

## A2 — elemental

- **Puede**: describir el pasado y los planes, manejarse en viajes, hablar de salud y de experiencias, entender textos cortos cotidianos.
- **Gramática**: Perfekt (*haben/sein* + participio), Präteritum de *sein, haben* y modales, Dativ, pronombres en dativo, preposiciones de lugar con acusativo/dativo (*Wechselpräpositionen*), subordinadas con *weil, dass, wenn*, modales en perfecto, comparativo y superlativo, declinación del adjetivo (nominativo/acusativo), verbos reflexivos (básico), conectores (*und, aber, oder, denn*), temporales (*als, wenn*), verbos con preposición (básico).
- **Temas**: viajes y vacaciones, salud y médico, buscar vivienda, fiestas, ropa, clima, tráfico, medios (básico).
- **Vocabulario**: ~1.300 palabras.

## B1 — intermedio

- **Puede**: contar experiencias y planes con detalle, argumentar una opinión simple, resolver trámites, entender lo esencial de un texto estándar.
- **Gramática**: Präteritum narrativo, Konjunktiv II de cortesía y deseos, pasiva en presente y pretérito, oraciones de relativo (nominativo/acusativo), genitivo (básico), declinación completa del adjetivo, verbos con preposición, conectores (*deshalb, trotzdem, außerdem, sonst*), subordinadas (*obwohl, damit, während*), infinitivo con *zu*, temporales (*bevor, nachdem, seit*), preguntas indirectas, sustantivación de verbos (básico).
- **Temas**: trabajo y candidatura, formación, medio ambiente, sociedad, oficinas y trámites, consumo, salud, medios.
- **Vocabulario**: ~2.400 palabras.

## B2 — intermedio alto

- **Puede**: argumentar con matices, seguir una discusión, entender textos especializados de su área, escribir textos estructurados.
- **Gramática**: Konjunktiv II irreal (presente y pasado), pasiva con modales y en pasado, estilo indirecto con Konjunktiv I (básico), relativos en dativo/genitivo y con preposición, construcciones participiales (básico), nominalización, conectores formales (*dennoch, allerdings, daher, sodass*), correlaciones (*entweder… oder, sowohl… als auch, je… desto*), Futur I/II (básico), adjetivos sustantivados, preposiciones con genitivo (*wegen, trotz, während*), orden del medio (TeKaMoLo).
- **Temas**: política, economía, ciencia, cultura, mundo laboral, política ambiental, ética, crítica de medios.
- **Vocabulario**: ~4.000–5.000 palabras (aproximado: el MCER no fija cifras).

## C1 — avanzado

- **Puede**: hablar con fluidez y espontaneidad, usar el idioma con flexibilidad para fines sociales, académicos y profesionales, entender textos largos y exigentes.
- **Gramática**: estilo nominal (*Nominalstil*), *Funktionsverbgefüge* (*in Frage stellen, zur Verfügung stellen*), construcciones participiales completas, Konjunktiv I en estilo indirecto, formas sustitutivas de la pasiva (*lassen sich, sein + zu + Infinitiv*), conectores formales (*sofern, soweit, indem, wohingegen*), orden marcado e inversión, atributos en genitivo, formación de palabras (prefijos, sufijos, composición), partículas modales (*halt, eben, doch*).
- **Temas**: sociedad, ciencia, derecho, filosofía, economía, cultura, migración, tecnología.
- **Vocabulario**: ~8.000 palabras (aproximado).

## C2 — maestría

- **Puede**: entender con facilidad todo lo que lee o escucha, expresarse con precisión y matiz, captar la ironía y el registro.
- **Gramática**: control estilístico del registro, expresiones idiomáticas y *Redewendungen*, estructuras poco frecuentes, matices irónicos, textos literarios y científicos, arcaísmos y figuras retóricas.
- **Temas**: cualquiera, incluido el humor, la ironía y el debate especializado.
- **Vocabulario**: ~16.000 palabras (aproximado).

## Cómo se usa

En `levels.go`, cada nivel alemán necesita `Name`, `Vocab`, `Length`, `Can`, `Grammar` y `Topics`. El `Length` (frases por respuesta) se mantiene igual al inglés: A1-A2 1-2, B1-B2 2-3, C1-C2 3-5. Lo que cambia es `Grammar` y `Topics`, que salen de este documento.

## Lo que falta antes de codificar

1. Validación por un docente de alemán (o al menos por alguien B2+ que haya hecho el Goethe B2/C1).
2. Decidir si el vocabulario se expresa en cifras (como ahora) o en bandas.
3. Un test que verifique que el perfil alemán **no** contiene gramática inglesa (`present perfect`, `phrasal verbs`, `articles`): el error más probable al copiar el perfil de un idioma a otro.
