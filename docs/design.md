# probe-exporter

probe-exporter is an OpenMetrics exporter that probes the targets its configuration lists, on a schedule, and serves the latest result of each check. It owns its targets: a scraper needs only the exporter's address.

The configuration is written from the user's side: what is probed, and which probers check it. How a prober carries out its check is the exporter's own concern. Most probers are carried out by blackbox_exporter's probers, imported as a library; `domain_registration` is the exporter's own.

## Concepts

| Term | Meaning |
|---|---|
| prober | a kind of check, such as `certificate` or `domain_registration` |
| probe target | what is probed, named in the configuration, with the probers that check it |
| run | one check of a probe target by one of its probers, at one address, port and host where the prober takes them |
| result | what the latest run produced: its success, its series and its log |

## Configuration

The configuration is one YAML file, named by `--config.file`. [`examples/probe-exporter.yml`](../examples/probe-exporter.yml) uses every prober.

| Field | Holds |
|---|---|
| `version` | the configuration format the file is written in: a minor such as `"1.0"`, or a major such as `"1"` |
| `probers` | prober name → its settings; only the probers the targets name |
| `probe_targets` | target name → the target |
| `probers` of a target | the probers that check it |
| `addresses` | where the target is reached, each a host name or an IP address without a port |
| `serves` | the hosts the target serves, each a host name without a wildcard |

Every prober takes `interval`, how often it checks each target, and `timeout`, how long one check may take. What else it takes, and what it needs of a target:

| Prober | Its own settings | A target gives it |
|---|---|---|
| `certificate` | `ports` | `addresses`, `serves` |
| `dns` | none | `addresses`, `serves` |
| `domain_registration` | none | its name, a domain |
| `http` | `ports`, `path`, `status_codes` | `addresses`, `serves` |
| `ping` | none | `addresses` |
| `tcp` | `ports` | `addresses` |

- A target gives `addresses` and `serves` only when one of its probers takes them.
- Every field is required where its prober takes it. A value is refused, never corrected.
- A configuration is held to the schema of the version it states before anything in it is read, then to the rules a schema cannot state: an `interval` and a `timeout` above zero, every prober a target names given its settings under `probers`, and a target checked by `domain_registration` named by its domain.
- Every problem is reported at once, each at its line and column with the lines around it, as a compiler reports one, and with the schema rule it breaks where it breaks one.

| Flag | Holds |
|---|---|
| `--config.file` | the configuration file |
| `--web.listen-address` | an address to listen on; repeatable |
| `--web.config.file` | the exporter-toolkit web configuration; optional |
| `--probes.max-concurrent` | how many runs happen at once, across every prober |
| `--log.level`, `--log.format` | the log's level and format |

The configuration is read at start and again on `SIGHUP`. A configuration that fails to load is refused and the running one kept; one that fails at start stops the exporter.

## Versions

The configuration format and the exporter carry separate versions. The format's version is what a configuration states in `version`, and its JSON Schema is `schemas/probe-exporter-v<major>.<minor>.schema.json`. The exporter's version is the release.

| Change | Format version | Exporter version |
|---|---|---|
| a breaking format change | a new major, `2.0` | a new major |
| an additive format change, such as a new prober | a new minor, `1.1` | a new minor |
| the exporter's code only | none | a new patch |

- A minor's schema is a file of its own, written by hand and frozen once released.
- A major floats to its newest minor: `version: "1"` is read as the newest 1.x the exporter knows, and `version: "1.0"` as 1.0.
- A version the exporter does not know is refused, naming the versions it reads.
- Each release attaches every minor's schema, and each major's as its newest minor.

`yaml-dsl.yml` defines the configuration as a YAML DSL, for the [YAML DSL editor](https://github.com/litebitus/vscode-yaml-dsl-editor) and every reader of the [YAML DSL standard](https://github.com/litebitus/yaml-dsl-spec).

## Schedule

- A target's probers each check it: at every address, on every port of the prober's `ports`, for every host it `serves`, where the prober takes them. Each such check is a run, repeating once per its prober's `interval` on its own schedule.
- Runs are staggered across the interval by a hash of their target, prober, address, port and host, so they spread out instead of starting together.
- A run is bounded by its prober's `timeout`.
- At most `--probes.max-concurrent` runs happen at once; a run waits for a free slot.
- A run first happens within its first interval after the configuration loads. It has no result until then.

On reload, a run still configured with the same settings keeps its result and its place in the schedule; a run no longer configured loses its result.

## Results

A run's result is replaced when its next run ends. Between runs it is served as it stands.

A result keeps its run's log, the lines the prober wrote, so why a run failed stays readable until the next run replaces it. The log is bounded; past its bound, the earliest lines are dropped.

Every series of a result carries:

| Label | Value |
|---|---|
| `probe_target` | the target's name |
| `prober` | the prober |
| `address` | the address checked; for `domain_registration`, the domain |
| `port` | the port checked, where the prober takes ports |
| `host` | the host checked for, where the prober takes the hosts a target serves |

Each result also has:

| Series | Value |
|---|---|
| `probe_success` | `1` if the run succeeded, else `0` |
| `probe_duration_seconds` | how long the run took |
| `probe_last_run_timestamp_seconds` | when the run ended |

A prober carried out by blackbox_exporter serves its series as blackbox names them.

## Serving

| Path | Answers |
|---|---|
| `/metrics` | every run's result, and the exporter's own series; OpenMetrics when the scraper asks for it, the Prometheus text format otherwise |
| `/results` | every run's latest result: its target, prober, address, port and host, whether it succeeded, when it ran, how long it took, and its log |
| `/-/healthy` | `200` while the process runs |
| `/-/ready` | `200` once a configuration has loaded |

`--web.config.file` takes the exporter-toolkit web configuration, so the endpoints can serve TLS and require authentication, as blackbox's do.

The exporter's own series:

| Series | Value |
|---|---|
| `probe_exporter_config_last_reload_successful` | `1` if the last load of the configuration succeeded, else `0` |
| `probe_exporter_config_last_reload_success_timestamp_seconds` | when a configuration last loaded |

## Probers

A prober is one interface: it checks one run's place within a deadline, adds its series to the result, and answers whether the run succeeded.

| Prober | A run | Carried out by |
|---|---|---|
| `certificate` | asks the address and port for the host's certificate, verified against the system's certificate authorities | blackbox's `tcp` prober with TLS, the host as the TLS server name |
| `dns` | asks the address, a DNS server, for the host's `A` record | blackbox's `dns` prober |
| `domain_registration` | reads when the domain's registration expires | the exporter's RDAP prober |
| `http` | requests `path` from the address and port over HTTPS, as the host, and succeeds on one of `status_codes` | blackbox's `http` prober, the host as the `Host` header and the TLS server name |
| `ping` | pings the address | blackbox's `icmp` prober |
| `tcp` | connects to the address and port | blackbox's `tcp` prober |

A blackbox prober runs exactly as blackbox's own handler would, with a module the exporter builds from the prober's settings. Each module resolves a host name to IPv4 first, falling back to IPv6.

### domain_registration

The `domain_registration` prober reads a domain's registration from its registry over RDAP (RFC 9083), the HTTPS protocol that replaces WHOIS for registration data.

- The domain is the target's name.
- The registry is found through IANA's RDAP bootstrap for DNS (RFC 9224), which maps a top-level domain to its registry's RDAP base URLs, by the domain's longest suffix the bootstrap names. Only HTTPS base URLs are used.
- The bootstrap is read when first needed and again at most once a day; a failed read keeps the last one.
- A run asks the registry for the domain, `GET <base URL>domain/<name>`, with `Accept: application/rdap+json`.
- A run succeeds when the answer is `200` and holds an `expiration` event.

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
