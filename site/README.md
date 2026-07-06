# Iceberg Sentry — Marketing site + Documentation

Static HTML/CSS/JS, no build step. Drop the contents of this directory
behind any static host (GitHub Pages, Vercel, Netlify, S3 + CloudFront,
Caddy, nginx).

## Layout

```
site/
├── index.html          marketing landing
├── examples.html       worked examples
├── docs/
│   ├── index.html      docs home / TOC
│   ├── install.html
│   ├── quickstart.html
│   ├── concepts.html
│   ├── audit.html
│   ├── orphans.html
│   ├── pii.html
│   ├── bench.html
│   ├── migration.html
│   ├── cost.html
│   ├── export.html
│   ├── catalogs.html
│   ├── policy.html
│   ├── ci-integration.html
│   ├── deployment.html
│   └── architecture.html
└── assets/
    ├── css/main.css    full design system
    └── js/site.js      theme toggle, sidebar, copy-buttons, tabs
```

## Run locally

```
python3 -m http.server -d site 8080
open http://localhost:8080
```

Or any equivalent static server. There is no build step.

## Editing

* The sidebar is rendered from a single JS array in `assets/js/site.js`
  (`SIDEBAR`). Update once; every docs page picks up the change.
* Code-block content is plain `<pre>` blocks. Terminal-styled blocks use
  the `.terminal` wrapper with a `.term-head` chrome bar.
* The colour palette is exposed as CSS variables in `assets/css/main.css`.
  Light theme is toggled via `data-theme="light"` on `<html>`.

## Deploying

GitHub Pages:

```
git subtree push --prefix site origin gh-pages
```

Or via the GitHub Pages settings: select `main` → `/site` as the source.
