# Dploy Documentation

This documentation is built using [Docusaurus](https://docusaurus.io/).

## Installation

```bash
cd docs
npm install
```

## Local Development

```bash
npm start
```

This command starts a local development server and opens up a browser window. Most changes are reflected live without having to restart the server.

## Build

```bash
npm run build
```

This command generates static content into the `build` directory and can be served using any static contents hosting service.

## Structure

```
docs/
├── docs/                    # Documentation markdown files
│   ├── intro.md            # Getting started
│   ├── installation.md     # Installation guide
│   ├── configuration.md    # Configuration reference
│   ├── environments.md     # Environments config
│   ├── development.md      # Development guide
│   ├── architecture.md     # Architecture overview
│   ├── api/                # API reference
│   │   ├── overview.md
│   │   └── endpoints.md
│   └── guide/              # User guides
│       └── web-ui.md
├── src/                    # React components
│   ├── components/
│   ├── css/
│   └── pages/
├── static/                 # Static assets
└── docusaurus.config.ts    # Docusaurus configuration
```

## Deployment

The documentation is deployed automatically via GitHub Pages when changes are pushed to the `main` branch.

Manual deployment:

```bash
GIT_USER=<Your GitHub username> npm run deploy
```
