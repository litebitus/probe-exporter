# probe-exporter

An OpenMetrics exporter that owns its targets: its configuration names what is probed and the probers that check it, and a scraper needs only the exporter's address.

## Features

- Each probe target lists its probers: `certificate`, `dns`, `domain_registration`, `http`, `ping` and `tcp`.
- A prober's settings are stated once and overridden by a target where it differs.
- Ports name the protocol spoken on them, as `443/tcp`, `80/http`, `53/udp` or `8/icmp`.
- A target that serves several hosts at several addresses is checked for each host at each address, written once.
- Checks run on their own schedule, staggered, and their latest results are served between runs.
- Everything is bounded: the configuration's size, each check's time and response body, each result's log, and how many checks are in flight at once.
- Every series carries its `probe_target`, `prober`, `address`, and its `port`, `host` and `record_type` where the prober takes them.
- blackbox_exporter's probers carry out most checks, imported as a library and run unchanged.
- `domain_registration` reads a domain's registration expiry from its registry over RDAP.
- The configuration is checked against its version's JSON Schema, in the editor as in the exporter, each problem reported at its line and column.
- `/results` shows each check's latest result with its log, so why a check failed stays readable.

The design, the configuration and every series are in [docs/design.md](docs/design.md).

## Usage

```sh
probe-exporter --config.file=probe-exporter.yml --web.listen-address=:9123
```

[`examples/probe-exporter.yml`](examples/probe-exporter.yml) is an example configuration using every prober.

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
| `make test` | `go vet`, a `gofmt` check, the Go tests with the race detector, the integration tests, and a check that the tests cover at least 95% of the code |

The integration tests run the exporter from a configuration file over HTTP, against a local HTTPS listener and RDAP registry, reloading it as `SIGHUP` does.

### Releases

The `Release` workflow, dispatched by hand, runs the tests, computes the next version, stamps `CHANGELOG.md` and `schemas/CHANGELOG.md`, tags it, and publishes the archives, the container image and the schemas.

The `Snapshot` workflow, dispatched by hand, publishes a pre-release such as `v0.0.1-snapshot.20261111175512`, with its image.

The `Prune snapshot images` workflow runs daily and deletes every snapshot image older than two weeks, with the platform images only it uses; a release's image is never deleted. Dispatched by hand, it names what it would delete unless `dry_run` is turned off.
