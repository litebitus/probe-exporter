# Changelog

## Unreleased

- Exporter
  - Probes the targets its configuration lists, in named target groups, on its own schedule, and serves each probe's latest result.
  - Labels every series with its `probe`, `target_group`, `name` and `address`.
  - Runs blackbox_exporter's probers unchanged, their settings written as blackbox writes them.
  - Serves `/results`, each probe's latest result with its log.
- Configuration
  - `schemas/probe-exporter-v1.0.schema.json`, attached to each release with `probe-exporter-v1.schema.json` as its newest 1.x.
  - `yaml-dsl.yml`, the configuration as a YAML DSL, kept in the repository.
  - `version` states the format, a major floating to its newest minor, and a configuration is validated against that version's schema before it is read.
  - Every problem is reported at its line and column, as a compiler reports one.
  - The schema types and describes every blackbox_exporter setting, generated from the blackbox_exporter release the exporter builds with.
- Probers
  - `rdap`: a domain's registration expiry, read from its registry over RDAP.
