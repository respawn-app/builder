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

## Crawler Setup

1. Deploy the docs site and confirm the live root URL loads.
   Current default URL: `https://respawn-app.github.io/builder/docs/`
2. In Algolia, create a crawler for the docs domain.
3. Use a config shaped like this and adjust only if the public URL changes:

```js
new Crawler({
  appId: 'YFIMJHUME7',
  apiKey: process.env.ALGOLIA_ADMIN_API_KEY,
  rateLimit: 8,
  maxDepth: 10,
  startUrls: ['https://respawn-app.github.io/builder/docs/'],
  sitemaps: ['https://respawn-app.github.io/builder/sitemap-index.xml'],
  discoveryPatterns: ['https://respawn-app.github.io/builder/docs/**'],
  actions: [
    {
      indexName: 'builder',
      pathsToMatch: ['https://respawn-app.github.io/builder/docs/**'],
      recordExtractor: ({ helpers }) =>
        helpers.docsearch({
          recordProps: {
            lvl0: {
              selectors: 'main .content-panel h1',
              defaultValue: 'Builder',
            },
            lvl1: 'main .sl-markdown-content h2',
            lvl2: 'main .sl-markdown-content h3',
            lvl3: 'main .sl-markdown-content h4',
            lvl4: 'main .sl-markdown-content h5, main .sl-markdown-content h6',
            content: 'main .sl-markdown-content p, main .sl-markdown-content li',
          },
          aggregateContent: true,
          recordVersion: 'v1',
        }),
    },
  ],
});
```

4. Run the crawler once manually to backfill the empty index.
5. After records appear in the `builder` index, the deployed docs search UI will start returning results.

## Notes

- The search-only API key is safe for client-side use. Do not use an Algolia admin key in the docs app.
- The crawler needs an Algolia admin key and runs outside this repository.
- Keep `initialIndexSettings` out of the crawler until you finalize ranking/faceting. Add them later in Algolia once the first crawl is healthy.
- If the docs move off GitHub Pages or the base path changes, update the crawler `startUrls`, `sitemaps`, and `pathsToMatch` first.
