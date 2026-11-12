# probe-exporter

probe-exporter is an OpenMetrics exporter that runs probes on a schedule and serves the latest result of each. It owns its target lists: its configuration states what to probe, and a scraper needs only the exporter's address.

Its probers are blackbox_exporter's, imported as a library and run unchanged, and its own, for the checks blackbox does not make. A blackbox prober behaves here exactly as it does in blackbox.

## Concepts

| Term | Meaning |
|---|---|
| prober | a kind of check, such as `tcp` or `rdap` |
| probe | a prober with its settings, how often it runs and its targets, named in the configuration |
| target | a name and the addresses it is checked at |
| run | one check of a target's name at one of its addresses |
| result | what the latest run produced: its success and its series |

## Configuration

The configuration is one YAML file, named by `--config.file`. [`examples/probe-exporter.yml`](../examples/probe-exporter.yml) is one.

| Field | Holds |
|---|---|
| `version` | the configuration format the file is written in: a minor such as `"1.0"`, or a major such as `"1"` |
| `probes` | probe name → the probe |
| `prober` | the probe's prober |
| `timeout` | how long a run may take |
| `interval` | how often each of the probe's runs repeats |
| `dns`, `grpc`, `http`, `icmp`, `tcp`, `unix`, `websocket` | the settings of the probe's prober, the one key its `prober` names; optional |
| `target_groups` | group name → the group: its targets and the labels they share |
| `targets` | the group's targets |
| `name` | a target's name, a host name without a wildcard |
| `addresses` | where a target's runs connect, one run per address, each in the form its probe's prober takes |
| `labels` | labels every series of the group's targets carries; optional, and never one the exporter sets |
| `max_concurrent_probes` | how many runs happen at once, across every probe |

What a target's name is to its run, and the form of its addresses, follow its probe's prober:

| Prober | The name | An address |
|---|---|---|
| `tcp`, `grpc` | the TLS server name | a host and its port, such as `www.example.com:443` or `[2001:db8::1]:443` |
| `http` | the `Host` header | an `http` or `https` URL |
| `dns` | the name asked | a DNS server, its port optional |
| `websocket` | a label only | a `ws` or `wss` URL |
| `icmp` | a label only | a host without a port |
| `unix` | a label only | an absolute path to a socket |
| `rdap` | a label only | a domain name |

- A blackbox prober's settings are written as blackbox writes a module's, so a `blackbox.yml` module's settings carry over as they are, less what a target's name stands for: a `tcp` or `grpc` probe's `tls_config` states no `server_name`, an `http` probe's `headers` no `Host`, and a `dns` probe's settings no `query_name`.
- Every field is required except where the table says optional. A value is refused, never corrected.
- A configuration is held to the schema of the version it states before anything in it is read, then to the rules a schema cannot state: an `interval` and a `timeout` above zero, a name checked at an address once in a group, and blackbox's own reading of each probe's settings.
- The schema holds each probe to its prober: its settings key, and the form of its targets' addresses.
- Every problem is reported at once, each at its line and column with the lines around it, as a compiler reports one, and with the schema rule it breaks where it breaks one.

| Flag | Holds |
|---|---|
| `--config.file` | the configuration file |
| `--web.listen-address` | an address to listen on; repeatable |
| `--web.config.file` | the exporter-toolkit web configuration; optional |
| `--log.level`, `--log.format` | the log's level and format |

The configuration is read at start and again on `SIGHUP`. A configuration that fails to load is refused and the running one kept; one that fails at start stops the exporter.

## Versions

The configuration format and the exporter carry separate versions. The format's version is what a configuration states in `version`, and its JSON Schema is `schemas/probe-exporter-v<major>.<minor>.schema.json`. The exporter's version is the release.

| Change | Format version | Exporter version |
|---|---|---|
| a breaking format change | a new major, `2.0` | a new major |
| an additive format change, such as a new optional field | a new minor, `1.1` | a new minor |
| the exporter's code only | none | a new patch |

- A minor's schema is a file of its own, frozen once released.
- A schema is written by hand, except its `blackbox_*` definitions: the generator fills those in the newest minor's schema from blackbox_exporter's Go types, each field described by blackbox's configuration reference, both read from the blackbox_exporter release the exporter builds with. A change to blackbox's settings is a schema diff.
- A major floats to its newest minor: `version: "1"` is read as the newest 1.x the exporter knows, and `version: "1.0"` as 1.0.
- A version the exporter does not know is refused, naming the versions it reads.
- Each release attaches every minor's schema, and each major's as its newest minor.

`yaml-dsl.yml` defines the configuration as a YAML DSL, for the [YAML DSL editor](https://github.com/litebitus/vscode-yaml-dsl-editor) and every reader of the [YAML DSL standard](https://github.com/litebitus/yaml-dsl-spec).

## Schedule

- Every run repeats once per its probe's interval: each of a target's addresses is checked every interval, each on its own schedule.
- Runs are staggered across the interval by a hash of their probe, group, name and address, so a group's runs spread out instead of starting together.
- A run is bounded by its probe's `timeout`.
- At most `max_concurrent_probes` runs happen at once; a run waits for a free slot.
- A run first happens within its first interval after the configuration loads. It has no result until then.

On reload, a run still configured keeps its result and its place in the schedule; a run no longer configured loses its result.

## Results

A run's result is replaced when its next run ends. Between runs it is served as it stands.

A result keeps its run's log, the lines the prober wrote, so why a run failed stays readable until the next run replaces it. The log is bounded; past its bound, the earliest lines are dropped.

Every series of a result carries:

| Label | Value |
|---|---|
| `probe` | the probe |
| `target_group` | the name of the target's group |
| `name` | the target's name |
| `address` | the address the run connected to |
| each of `labels` | its value |

Each result also has:

| Series | Value |
|---|---|
| `probe_success` | `1` if the run succeeded, else `0` |
| `probe_duration_seconds` | how long the run took |
| `probe_last_run_timestamp_seconds` | when the run ended |

A blackbox prober's series are named as blackbox names them.

## Serving

| Path | Answers |
|---|---|
| `/metrics` | every run's result, and the exporter's own series; OpenMetrics when the scraper asks for it, the Prometheus text format otherwise |
| `/results` | every run's latest result: its probe, group, name and address, whether it succeeded, when it ran, how long it took, and its log |
| `/-/healthy` | `200` while the process runs |
| `/-/ready` | `200` once a configuration has loaded |

`--web.config.file` takes the exporter-toolkit web configuration, so the endpoints can serve TLS and require authentication, as blackbox's do.

The exporter's own series:

| Series | Value |
|---|---|
| `probe_exporter_config_last_reload_successful` | `1` if the last load of the configuration succeeded, else `0` |
| `probe_exporter_config_last_reload_success_timestamp_seconds` | when a configuration last loaded |

## Probers

| Prober | From |
|---|---|
| `dns`, `grpc`, `http`, `icmp`, `tcp`, `unix`, `websocket` | blackbox_exporter |
| `rdap` | probe-exporter |

A prober is one interface: it checks a target's name at one address within a deadline, adds its series to the result, and answers whether the run succeeded. Each blackbox prober is held behind it with its probe's settings, and runs exactly as blackbox's own handler would against the address, with the name set in a copy of the settings where the table above says it is sent.

### rdap

The `rdap` prober reads a domain's registration from its registry over RDAP (RFC 9083), the HTTPS protocol that replaces WHOIS for registration data.

- An address is a domain name, the domain asked about.
- The registry is found through IANA's RDAP bootstrap for DNS (RFC 9224), which maps a top-level domain to its registry's RDAP base URLs, by the domain's longest suffix the bootstrap names. Only HTTPS base URLs are used.
- The bootstrap is read when first needed and again at most once a day; a failed read keeps the last one.
- A run asks the registry for the domain, `GET <base URL>domain/<name>`, with `Accept: application/rdap+json`.
- A run succeeds when the answer is `200` and holds an `expiration` event.

An `rdap` probe takes no settings beyond `prober`, `timeout`, `interval` and `target_groups`.

| Series | Value |
|---|---|
| `probe_domain_registration_expiry_timestamp_seconds` | when the domain's registration expires, from the `expiration` event |
| `probe_http_status_code` | the registry's answer |

A failed run logs why: no registry for the domain's top-level domain, the registry's answer, or an answer without an `expiration` event.

## Distribution

- Binaries for Linux and macOS, `amd64` and `arm64`, attached to each release.
- A container image per release, built on a distroless base, running as a non-root user.

## Open questions

- Serving blackbox's `/probe` endpoint too, so a setup where the scraper drives the probes can move over without rewriting its scrape job.
- Pushing results over OTLP as a second output, beside the scraped endpoint.
