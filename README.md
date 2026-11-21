# ArcMD Plugins

Official plugin marketplace and WASM (`GOOS=wasip1 GOARCH=wasm`) plugins for [ArcMD](https://github.com/Ciao1019/ArcMD).

## Repository Layout

- `plugins/` — Official first-party plugins maintained in this repository:
  - `plugins/cloud-sync` (`dev.arcmd.cloud-sync`) — Incremental 3-way sync via S3-compatible storage (R2 / OSS / COS / MinIO) or WebDAV.
  - `plugins/image-host` (`dev.arcmd.image-host`) — Auto-upload pasted/dropped images to S3 or custom HTTP image hosts and insert remote URLs.
- `community/` — Reserved for community plugin listing manifests.
- `scripts/build_catalog.go` — Compiles all `plugins/*` into reproducible `.wasm` + `.zip` packages and generates `dist/index.json`.

## Release Architecture

Plugin binaries and marketplace assets are published exclusively via **GitHub Releases** (keeping the main ArcMD repository releases focused on desktop installers):

1. Each plugin version gets an immutable release tagged `<id>-v<version>` containing `<id>-<version>.zip`. Modifying code without bumping `"version"` is rejected by CI.
2. A rolling `catalog` release stores `index.json`, fetched by ArcMD's built-in plugin marketplace (`https://github.com/Ciao1019/arcmd-plugins/releases/download/catalog/index.json`) with SHA-256 verification before installation.

## Local Build & Test

```sh
go test -v ./...
go run ./scripts/build_catalog.go
```

Outputs are written to `dist/`:

- `dist/<id>-<version>.zip`
- `dist/index.json`
- `dist/releases.tsv`
