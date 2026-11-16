# Changelog

## Unreleased

- Preserve typed nested VLAN and portal server facts, with URL-safe server references; see [API migration](docs/core-api.md#runtime-evidence-corrections).
- Separate radio operational and administrative state, require explicit administrative provenance, and gate runtime rules on complete evidence.
- Share published parser v0.2.1 raw source indexes and spans, admitting up to 32 MiB and 400,000 physical lines before terminal scans.
- Parse reviewed H3C load balancing, hot backup, AP MAC, client Radio slice and radio steering syntax; add synthetic fixtures and private offline corpus replay.

## v0.5.0

- Introduce typed WLAN models, go-netops assessments and typed template-security changes.
- Reject cyclic fingerprint inputs before UTF-8 traversal can overflow the stack.
- Adopt go-standards v1.0.2 through shared lint, architecture and `make check` gates.
- Adapt the published Connector v0.7.1 configuration, platform and inventory APIs.
- Document breaking constructor, recommendation and fingerprint migrations in [the release notes](docs/releases/v0.5.0.md).
