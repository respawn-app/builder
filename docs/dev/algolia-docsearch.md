# Algolia DocSearch

The docs UI uses `@astrojs/starlight-docsearch`.

## Frontend Config

- `appId`: `YFIMJHUME7`
- `indexName`: `builder`
- `apiKey`: search-only key configured in `docs/scripts/site-config.mjs` and overridable via `DOCSEARCH_API_KEY`

GitHub Pages builds also accept these optional repo variables:

- `DOCSEARCH_APP_ID`
- `DOCSEARCH_API_KEY`
- `DOCSEARCH_INDEX_NAME`

## Notes

- The search-only API key is safe for client-side use. Do not use an Algolia admin key in the docs app.
- Keep `initialIndexSettings` out of the crawler until you finalize ranking/faceting. Add them later in Algolia once the first crawl is healthy.
- If the docs move off GitHub Pages or the base path changes, update the crawler `startUrls`, `sitemaps`, and `pathsToMatch` first.
