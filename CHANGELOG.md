# Changelog

## v0.6.0

- Upgrade the published transport dependency to net-connector v0.9.0 and shared operations to go-netops v0.4.0.
- Migrate registry contracts to platform and inventory selectors, overrides and device-rule destinations to platform names.
- Preserve scoped controller/AP queries, accepted receipts and cleanup failures; retain strict host trust and no automatic replay.

## v0.5.0

- Introduce typed WLAN models, go-netops assessments and typed template-security changes.
- Reject cyclic fingerprint inputs before UTF-8 traversal can overflow the stack.
- Adopt go-standards v1.0.2 through shared lint, architecture and `make check` gates.
- Adapt the published Connector v0.7.1 configuration, platform and inventory APIs.
- Document breaking constructor, recommendation and fingerprint migrations in [the release notes](docs/releases/v0.5.0.md).
