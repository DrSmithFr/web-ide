# `web`

The front end: SolidJS and TypeScript, built by Vite into `pod/webdist/dist`, which the pod embeds. The code is in [src/](src/README.md).

## Components

- `src/`: the application, one folder per concern: [src/README.md](src/README.md).
- `index.html`, `vite.config.ts`: entry and build. `base: './'` keeps the paths relative, because the page also runs under `/_lan/pod/<key>/` of a gateway; in development Vite proxies `/ws`, `/auth` and the model files to the pod (`POD=http://127.0.0.1:4433`).
- `public/`: the icons and the manifest of the installable app.
- `scripts/i18n-check.mjs`: fails when a text passed to `t()` is missing from a catalog (`src/i18n/<id>.json`, one per language of `src/i18n/languages.json`), or the catalogs differ.

## Commands

```
npm run dev     # Vite on :5173 with hot reload (make dev also starts a dev pod)
npm run build   # tsc --noEmit, then vite build into ../pod/webdist/dist
npm run check   # tsc --noEmit and the catalog check (part of make test)
```

## Invariants and pitfalls

- Every text shown goes through `t()`; Markdown is sanitized (DOMPurify) before it is rendered.
- The page must work under a path prefix and over a tunnel: never build an absolute URL from `location.origin` alone, use `app/base.ts`.
- Pitfalls of the editor, Solid and the DOM: [src/README.md](src/README.md#invariants-and-pitfalls).

## Tests

`cd web && npm run check`, then the browser suites of [../e2e/README.md](../e2e/README.md) (`make build` first: they run the built front end).
