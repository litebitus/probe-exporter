# Configuration schema changelog

Each version is the format a configuration names in its `version`, and the schema file of that name. A version not yet released is headed `## <version> — unreleased`; the release workflow stamps it with the probe-exporter release that ships it and the date.

## 1.0 — unreleased

- The first format: `probes`, each a prober with its settings, `interval` and `target_groups`, and `max_concurrent_probes`.
