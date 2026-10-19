# Add a language

Translate the interface and the pod's messages into a new language.

English is the key and the default: a text with no translation shows in English. The catalogs today are French (`fr`) and Spanish (`es`). Each side keeps one list of languages that every place reads; replace `de` below with your code.

## Prerequisites

- The sources and a build: [from the sources](../install/from-sources.md).
- Read [CONTRIBUTING.md](../../CONTRIBUTING.md) and the conventions of [AGENTS.md](../../AGENTS.md) (every text goes through `t()`; docs and code in English).

## Steps

1. **Add the code to the lists.**
   - Page: an entry in `web/src/i18n/languages.json` (`id`, native `name`, `locale` for dates and numbers, `singularBelow2` for a language that uses the singular for 0 and 1, as French).
   - Pod: the code in `Languages` of `pod/internal/i18n/i18n.go`.
2. **Add the catalogs.** Copy `web/src/i18n/fr.json` to `web/src/i18n/de.json` and `pod/internal/i18n/fr.json` to `pod/internal/i18n/de.json`, then translate the values. The keys are the English texts and stay as they are; `{name}` and `%s` parameters are kept; a key may carry a context before a bar (`menu|Edit`) that is not shown in English.
3. **Relay page.** The page of the relay (`pod/internal/relay/web/bootstrap.js`) and the window after a GitHub link (`doneCatalog` in `pod/internal/relay/github.go`) have their own few words: add the code to `langs` and a third text to each `L(…)`, and a row to `doneCatalog`.

The speech list of the dictation (`web/src/plugins/ai/llm/transcribe.ts`) is the list of the models, not ours: add the language there only if the model supports it and it is missing.

## Check

```sh
cd web && npm run check      # tsc and the catalogs: no "missing de" line
make test                    # includes the pod catalog tests
make dev                     # then Settings > Language > pick yours
```

The checks loop over the lists: every text of the sources is in every catalog, the catalogs have the same keys (none missing, none extra) and a translation keeps the parameters of its key. In the interface, the texts show in your language, a pod error (a refused git command, for example) as well, and the language survives a reload.

## Troubleshooting

| Symptom | Fix |
| --- | --- |
| `npm run check` prints `missing de: "…"` | A text used in the sources has no key in `de.json`: copy the English text exactly. |
| `parameters differ in de` | A `{name}` was renamed or dropped in the translation: keep it as in the key. |
| Some texts stay in English | They are missing from the catalog (silent fallback), or built from a variable that the checker cannot see: search the key in the sources. |
| The language is not in the menu | It is missing from `languages.json`. |
| Pod messages stay in English | It is missing from `Languages`, or the page has not told the pod its language (the pod follows each window). |

## See also

[Modify the IDE](modify-the-ide.md) · [CONTRIBUTING.md](../../CONTRIBUTING.md) · [Glossary](../../GLOSSARY.md)
