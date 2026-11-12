# Changelog

## Unreleased

- Exporter
  - Probes the targets its configuration lists, each with its probers, on its own schedule, and serves each check's latest result.
  - Labels every series with its `probe_target`, `prober`, `address`, and its `port` and `host` where the prober takes them.
  - `--probes.max-concurrent` bounds how many checks run at once.
  - Serves `/results`, each check's latest result with its log.
- Configuration
  - `schemas/probe-exporter-v1.0.schema.json`, attached to each release with `probe-exporter-v1.schema.json` as its newest 1.x.
  - `yaml-dsl.yml`, the configuration as a YAML DSL, kept in the repository.
  - `version` states the format, a major floating to its newest minor, and a configuration is validated against that version's schema before it is read.
  - Every problem is reported at its line and column, as a compiler reports one.
- Probers
  - `certificate`, `dns`, `http`, `ping` and `tcp`, carried out by blackbox_exporter's probers.
  - `domain_registration`: a domain's registration expiry, read from its registry over RDAP.
