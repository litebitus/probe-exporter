# probe-exporter

An OpenMetrics exporter that owns its probes and their targets, running [blackbox_exporter](https://github.com/prometheus/blackbox_exporter)'s probers and its own.

## Features

- The configuration lists what to probe, in named target groups, so a scraper needs only the exporter's address and every target has one home.
- Probes run on their own schedule, staggered and bounded in how many runs happen at once, and their latest results are served between runs.
- Every series carries its `probe`, `target_group`, `name` and `address`.
- blackbox_exporter's probers run unchanged, imported as a library, and a `blackbox.yml` module's settings carry over as they are.
- Each probe's targets are checked against its prober, in the editor as in the exporter: a `tcp` target takes a port, an `http` target is a URL.
- A target is a name and the addresses it is checked at, so a name served by several hosts is checked at each of them.
- The `rdap` prober reads a domain's registration expiry from its registry over RDAP.
- `/results` shows each target's latest result with its log, so why a run failed stays readable.

The design, the configuration and every series are in [docs/design.md](docs/design.md).

## Usage

```sh
probe-exporter --config.file=probe-exporter.yml --web.listen-address=:9123
```

[`examples/probe-exporter.yml`](examples/probe-exporter.yml) is an example configuration.

The container image is `ghcr.io/litebitus/probe-exporter:<version>`, distroless and running as a non-root user:

```sh
docker run -v "$PWD/probe-exporter.yml:/etc/probe-exporter.yml:ro" -p 9123:9123 \
  ghcr.io/litebitus/probe-exporter:<version> \
  --config.file=/etc/probe-exporter.yml --web.listen-address=:9123
```

A scrape job reads it as any exporter:

```yaml
scrape_configs:
  - job_name: probe-exporter
    static_configs:
      - targets: [mock-host.example:9123]
```

`SIGHUP` reloads the configuration.

## Published schemas

Every release attaches the configuration's JSON Schemas as assets, for a modeline or `schema_search_paths` to name:

| Schema | Asset | In the latest release |
|---|---|---|
| a minor | `probe-exporter-v<major>.<minor>.schema.json` | [`probe-exporter-v1.0.schema.json`](https://github.com/litebitus/probe-exporter/releases/latest/download/probe-exporter-v1.0.schema.json) |
| a major, as its newest minor | `probe-exporter-v<major>.schema.json` | [`probe-exporter-v1.schema.json`](https://github.com/litebitus/probe-exporter/releases/latest/download/probe-exporter-v1.schema.json) |

A configuration naming the newest 1.x never needs its modeline edited:

```yaml
# yaml-language-server: $schema=https://github.com/litebitus/probe-exporter/releases/latest/download/probe-exporter-v1.schema.json
version: "1"
```

[`yaml-dsl.yml`](yaml-dsl.yml) defines the configuration as a YAML DSL: copied into a repo's `yaml-dsl.yml`, it lets the [YAML DSL editor](https://github.com/litebitus/vscode-yaml-dsl-editor) check a `probe-exporter.yml` as it is written. How the format is versioned is in [docs/design.md](docs/design.md).

## Development

Go builds and tests the repo.

| Target | Does |
|---|---|
| `make build` | runs the tests, then builds the exporter into `bin/` |
| `make schema` | fills the `blackbox_*` definitions of the newest minor's JSON Schema from the blackbox_exporter release the exporter builds with, keeping the rest as written |
| `make test` | `go vet`, a `gofmt` check, the Go tests with the race detector, the integration tests, a check that the committed schema's `blackbox_*` definitions are what `make schema` fills, and a check that the tests cover at least 95% of the code |

The integration tests run the exporter from a configuration file over HTTP, against local TLS listeners and RDAP registries, reloading it as `SIGHUP` does.

### Releases

The `Release` workflow, dispatched by hand, runs the tests, computes the next version, stamps `CHANGELOG.md` and `schemas/CHANGELOG.md`, tags it, and publishes the archives, the container image and the schemas.

The `Snapshot` workflow, dispatched by hand, publishes a pre-release such as `v0.0.1-snapshot.20261111175512`, with its image.

The `Prune snapshot images` workflow runs daily and deletes every snapshot image older than two weeks, with the platform images only it uses; a release's image is never deleted. Dispatched by hand, it names what it would delete unless `dry_run` is turned off.
