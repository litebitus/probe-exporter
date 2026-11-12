# Changelog

## Unreleased

- Exporter
  - Probes the targets its configuration lists, in named target groups, on its own schedule, and serves each probe's latest result.
  - Labels every series with its probe's `module`, `target_group`, `target`, `hostname` and `query_name`.
  - Runs blackbox_exporter's probers unchanged, from modules in blackbox's format.
  - Serves `/results`, each probe's latest result with its log.
- Probers
  - `rdap`: a domain's registration expiry, read from its registry over RDAP.
