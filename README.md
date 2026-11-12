# probe-exporter

An OpenMetrics exporter that owns its target lists and runs probes, [blackbox_exporter](https://github.com/prometheus/blackbox_exporter)'s and its own.

## Features

- The configuration lists what to probe, in named target groups, so a scraper needs only the exporter's address and every target has one home.
- Probes run on their own schedule, staggered and bounded in how many run at once, and their latest results are served between probes.
- Every series carries its probe's `module`, `target_group` and `target`, and the `hostname` or `query_name` it was probed as.
- blackbox_exporter's probers run unchanged, imported as a library, and a `blackbox.yml` module carries over as it is.
- A target is dialed as several hostnames, or asked several DNS names, from one group.
- The `rdap` prober reads a domain's registration expiry from its registry over RDAP.
- `/results` shows each probe's latest result with its log, so why a probe failed stays readable.

The design, the configuration and every series are in [docs/design.md](docs/design.md).

## Usage

```sh
probe-exporter --config.file=probe-exporter.yml --web.listen-address=:9123
```

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

## Development

Go builds and tests the repo.

| Target | Does |
|---|---|
| `make build` | runs the tests, then builds the exporter into `bin/` |
| `make test` | `go vet`, a `gofmt` check, the Go tests with the race detector, the integration tests, and a check that the tests cover at least 95% of the code |
The integration tests run the exporter from a configuration file over HTTP, against local TLS listeners and RDAP registries, reloading it as `SIGHUP` does.

### Releases

The `Release` workflow, dispatched by hand, runs the tests, computes the next version, stamps `CHANGELOG.md`, tags it, and publishes the archives and the container image.

The `Snapshot` workflow, dispatched by hand, publishes a pre-release such as `v0.0.1-snapshot.20261111175512`, with its image.
