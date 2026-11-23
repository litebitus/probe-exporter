# Configuration schema changelog

Each version is the format a configuration names in its `version`, and the schema file of that name. A version not yet released is headed `## <version> — unreleased`; the release workflow stamps it with the probe-exporter release that ships it and the date.

## 1.1 — probe-exporter v0.2.0, 2026-11-23

- The `stored_certificate` prober, which takes no `ports`, and a target's `environment_variables`, the variables it reads.

## 1.0 — probe-exporter v0.1.0, 2026-11-12

- The first format: `probers`, each prober's settings, and `probe_targets`, each target with its `addresses`, `serves` and probers, each overriding the prober's settings.
