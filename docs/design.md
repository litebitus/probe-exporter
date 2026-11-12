# probe-exporter

probe-exporter is an OpenMetrics exporter that probes targets on a schedule and serves the latest result of each. It owns its target lists: its configuration states what to probe, and a scraper needs only the exporter's address.

Its probers are blackbox_exporter's, imported as a library and run unchanged, and its own, for the checks blackbox does not make. A blackbox probe behaves here exactly as it does in blackbox.

## Concepts

| Term | Meaning |
|---|---|
| prober | a kind of check, such as `tcp` or `rdap` |
| module | a prober with its settings, named in the configuration |
| target | what a module probes, such as `www.example.com:443` or `example.com` |
| probe | one run of a module against one target, dialed as one hostname or asked one query name where its group states them |
| result | what the latest probe of a target produced: its success and its series |

## Configuration

The configuration is one YAML file, named by `--config.file`.

```yaml
modules:
  tls_connect:
    prober: tcp
    timeout: 10s
    tcp:
      preferred_ip_protocol: ip4
      tls: true
  domain_registration:
    prober: rdap
    timeout: 10s

probe_configs:
  - module: tls_connect
    interval: 5m
    target_groups:
      by-name:
        targets:
          - api.example.com:443
          - www.example.com:443
      mock-cdn:
        targets: [192.0.2.10:443, 192.0.2.11:443]
        hostnames: [api.example.com, www.example.com]
        labels:
          site: mock-cdn
  - module: domain_registration
    interval: 6h
    target_groups:
      registered-domains:
        targets:
          - example.com
          - example.org

max_concurrent_probes: 8
```

| Field | Holds |
|---|---|
| `modules` | module name → the module |
| `probe_configs` | each a module, how often it probes, and its targets |
| `module` | the module the targets are probed with |
| `interval` | how often each target is probed |
| `target_groups` | group name → the group: targets and the settings they share |
| `targets` | the targets |
| `hostnames` | for `http` and `tcp`, the names each target is dialed as: the `Host` header for `http`, the TLS server name for `tcp`; optional |
| `query_names` | for `dns`, the names each target is asked; required when the module states no `query_name` |
| `labels` | labels every series of the group's targets carries; optional, and never one the exporter sets |
| `max_concurrent_probes` | how many probes run at once, across every module |

- A module of a blackbox prober is written in blackbox's module format, so a `blackbox.yml` module carries over as it is.
- A module of the exporter's own prober is written in that prober's format, stated in its section below.
- Every field is required except where the table says optional. A value is refused, never corrected.
- A target is probed once per hostname and query name its group states, so three targets with two hostnames are six probes; without either, once.
- A group states `hostnames` only under a module that states no `Host` header or TLS `server_name`, and `query_names` only under one that states no `query_name`.
- A target is probed one way once: two probe configs probing the same target, in a group of the same name with the same module, are refused.

| Flag | Holds |
|---|---|
| `--config.file` | the configuration file |
| `--web.listen-address` | an address to listen on; repeatable |
| `--web.config.file` | the exporter-toolkit web configuration; optional |
| `--log.level`, `--log.format` | the log's level and format |

The configuration is read at start and again on `SIGHUP`. A configuration that fails to load is refused and the running one kept; one that fails at start stops the exporter.

## Schedule

- Each target is probed once per its interval.
- A target's probes are staggered across the interval by a hash of its module and target, so a group's probes spread out instead of starting together.
- A probe is bounded by its module's `timeout`.
- At most `max_concurrent_probes` probes run at once; a probe waits for a free slot.
- A target is first probed within its first interval after the configuration loads. It has no result until then.

On reload, a target still configured keeps its result and its place in the schedule; a target no longer configured loses its result.

## Results

A target's result is replaced when its next probe ends. Between probes it is served as it stands.

A result keeps its probe's log, the lines the prober wrote, so why a probe failed stays readable until the next probe replaces it. The log is bounded; past its bound, the earliest lines are dropped.

Every series of a result carries:

| Label | Value |
|---|---|
| `module` | the module |
| `target_group` | the name of the target's group |
| `target` | the target |
| `hostname` | the name the target is dialed as, when its group states `hostnames` |
| `query_name` | the name the target is asked, when its group states `query_names` |
| each of `labels` | its value |

Each result also has:

| Series | Value |
|---|---|
| `probe_success` | `1` if the probe succeeded, else `0` |
| `probe_duration_seconds` | how long the probe took |
| `probe_last_run_timestamp_seconds` | when the probe ended |

A blackbox prober's series are named as blackbox names them.

## Serving

| Path | Answers |
|---|---|
| `/metrics` | every target's result, and the exporter's own series; OpenMetrics when the scraper asks for it, the Prometheus text format otherwise |
| `/results` | every target's latest result: its module, group and target, whether it succeeded, when it ran, how long it took, and its probe log |
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

A prober is one interface: it probes one target within a deadline, adds its series to the result, and answers whether the probe succeeded. Each blackbox prober is held behind it with its module, and probes exactly as blackbox's own handler would, with the target's hostname or query name set in a copy of the module.

### rdap

The `rdap` prober reads a domain's registration from its registry over RDAP (RFC 9083), the HTTPS protocol that replaces WHOIS for registration data.

- A target is a domain name.
- The registry is found through IANA's RDAP bootstrap for DNS (RFC 9224), which maps a top-level domain to its registry's RDAP base URLs, by the domain's longest suffix the bootstrap names. Only HTTPS base URLs are used.
- The bootstrap is read when first needed and again at most once a day; a failed read keeps the last one.
- The probe asks the registry for the domain, `GET <base URL>domain/<name>`, with `Accept: application/rdap+json`.
- The probe succeeds when the answer is `200` and holds an `expiration` event.

Its module takes `prober: rdap` and `timeout`, nothing else.

| Series | Value |
|---|---|
| `probe_domain_registration_expiry_timestamp_seconds` | when the domain's registration expires, from the `expiration` event |
| `probe_http_status_code` | the registry's answer |

A failed probe logs why: no registry for the domain's top-level domain, the registry's answer, or an answer without an `expiration` event.

## Distribution

- Binaries for Linux and macOS, `amd64` and `arm64`, attached to each release.
- A container image per release, built on a distroless base, running as a non-root user.

## Open questions

- Serving blackbox's `/probe` endpoint too, so a setup where the scraper drives the probes can move over without rewriting its scrape job.
- Pushing results over OTLP as a second output, beside the scraped endpoint.
