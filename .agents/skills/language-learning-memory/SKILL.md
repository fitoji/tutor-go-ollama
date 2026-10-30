---
name: language-learning-memory
description: "Trigger: teach vocabulary, remember words, mnemonic, mnemotecnia, Campayo, learner forgets a word. Apply Ramón Campayo's association method: sound-alike keyword plus one absurd image, chained for lists."
license: Apache-2.0
metadata:
  author: "fitoji"
  version: "1.0"
---

# Language Learning Memory

## Activation Contract

Load when a learner forgets, confuses or misuses a word; when introducing new
vocabulary; when designing a tutor prompt that must make words stick; when the
user mentions mnemonic, mnemotecnia, imagen, asociación or Ramón Campayo.

Do not load for grammar explanations, pronunciation drills or translation
requests: this skill is only about making a word retrievable.

## Hard Rules

- One word per turn. Never hand out a list of mnemonics.
- The image MUST be absurd, exaggerated, moving and emotional. A logical image
  is not remembered; a ridiculous one is.
- The keyword MUST sound like the target word in the learner's native language.
  Never use etymology, a translation or a definition as the hook.
- Say the image in the learner's native language; keep the target word in the
  target language.
- Never bend grammar or invent a rule to make a word fit.
- If the word is abstract, turn it into a person, an object or an action first.

## Decision Gates

| Word | Technique |
|---|---|
| concrete noun | the object doing something impossible |
| abstract noun, verb, adjective | sound-alike keyword + a person doing it absurdly |
| list or series | chain: each image touches the next one |
| false friend or hard spelling | split into chunks, one image per chunk |
| word already learned | no mnemonic; ask for a sentence instead |

## Execution Steps

1. Say the word once with its pronunciation.
2. Pick a native-language keyword that sounds like it.
3. Build one image joining keyword and meaning: movement, exaggeration, emotion.
4. Say it in one sentence of at most 15 words, so it can be spoken aloud.
5. Ask the learner to repeat the image in their own words. If they can, it stuck.
6. Review it on the next turn, then after a day, then after a week.

## Output Contract

Return exactly: the word with its pronunciation, the keyword, one image
sentence, and one recall question. Nothing else. In speakeasy the image can be
stored as a fact with key `mnemonic-<category>` so the tutor reuses it later.

## References

- `assets/examples.md` — worked examples with Spanish keywords for English words.
