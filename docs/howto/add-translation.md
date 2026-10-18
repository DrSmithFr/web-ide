# Add a language

Translate the interface and the pod's messages into a new language.

English is the key and the default: a text with no translation shows in English. Today the catalogs are `fr` (French) and English (no catalog). The code lists the languages in a few places, which this page walks through; `fr` is the model to copy (replace `de` below with your code).

## Prerequisites

- The sources and a build: [from the sources](../install/from-sources.md).
- Read [CONTRIBUTING.md](../../CONTRIBUTING.md) and the conventions of [AGENTS.md](../../AGENTS.md) (every text goes through `t()`; docs and code in English).

## Steps

1. **Interface catalog.** Copy `web/src/i18n/fr.json` to `web/src/i18n/de.json` and translate the values; the keys are the English texts and stay as they are. Parameters are written `{name}` and must be kept. A key may carry a context before a bar (`menu|Edit`) that is not shown in English.
2. **Register it** in `web/src/i18n/index.ts`: import the file, extend the `Lang` type, add `{ id: 'de', name: 'Deutsch' }` to `languages` and the file to `catalogs`. `locale()` and the plural rule of `tn()` have a branch for French: add yours there.
3. **The setting.** `web/src/state/settings.ts` accepts `'en'` and `'fr'` for the language setting (`raw.language === 'en' || raw.language === 'fr'`): add yours.
4. **The checker.** `web/scripts/i18n-check.mjs` lists the catalogs it checks (`const catalogs = { fr: … }`): add yours, so a missing key fails `npm run check`.
5. **Pod messages.** Copy `pod/internal/i18n/fr.json` the same way, embed it and add it to `catalogs` in `pod/internal/i18n/i18n.go` (what `Supported` reads). `TestCatalogComplete` in `i18n_test.go` checks the French catalog only: extend it to yours. A few pages written by the pod pick `fr` from the `Accept-Language` header or the language setting (`pod/internal/server/open.go`, `pod/internal/lan/update.go`, `pod/internal/preview/preview.go`, `pod/internal/server/mcp_reply.go`): add yours where the page should follow it.
6. **Dictation.** The language list of the speech model is in `web/src/llm/transcribe.ts`; add the language there if the model supports it.

## Check

```sh
cd web && npm run check      # tsc and the catalog: no "missing de" line
make test                    # includes the pod catalog test
make dev                     # then Settings > Language > pick yours
```

In the interface, the texts show in your language, a pod error (a refused git command, for example) as well, and the language survives a reload.

## Troubleshooting

| Symptom | Fix |
| --- | --- |
| `npm run check` prints `missing de: "…"` | A text used in the sources has no key in `de.json`: copy the English text exactly. |
| Some texts stay in English | They are missing from the catalog (silent fallback), or built from a variable that the checker cannot see: search the key in the sources. |
| A `{name}` shows literally | The parameter was renamed in the translation: keep it as in the key. |
| The language is not in the menu | It is missing from `languages` in `index.ts`. |
| Pod messages stay in English | The pod catalog is not registered, or the page has not told the pod its language (the pod follows each window). |

## See also

[Modify the IDE](modify-the-ide.md) · [CONTRIBUTING.md](../../CONTRIBUTING.md) · [Glossary](../../GLOSSARY.md)
