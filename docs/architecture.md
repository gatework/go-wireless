# Package ownership and migration

The source refactor preserves command names, flags, configuration precedence,
device commands, report JSON, exact numeric fingerprints and exit semantics.
Public Go import paths change together; no forwarding compatibility packages remain.
Go 1.26 remains the minimum.

| Owner | Responsibility |
| --- | --- |
| `wireless` | Observation, Snapshot, Capture, Failure, Projection, Coverage; owned JSON values, object order and fingerprints |
| `parser` | validated Registry/Profile/Query/CollectionScope; field parsing, result accounting, runtime and AP neighbor evidence |
| `vendors/h3c`, `vendors/huawei` | actual vendor syntax and profile declarations; shared table accounting belongs to parser |
| `vendors` | explicit built-in catalog composition, with no global registration |
| `collect` | one controller acquisition, command receipts, shared-query grouping and AP reads through the selected Profile |
| `review` | historical report validation and recommendations |
| `change` | validated immutable plans, concrete H3C command rendering, eligibility and before/after verification |
| `report` | private durable artifacts, checkpoints, recovery and report text |
| `adapter/netconnector` | Connector session/receipt/cleanup adaptation |
| `internal/bootstrap` | configuration, immutable settings and lazy concrete dependency assembly |
| `internal/workflow/*` | collect/review/apply application state machines |
| `internal/cli` | Cobra commands, flags, confirmation, progress and exit/output presentation |

The root does not import domain packages or transports. Parser never imports
vendors. Collect receives a selected profile and never discovers a built-in parser;
this includes AP LLDP. Huawei does not import H3C. Change does not import review.
Workflow never imports bootstrap, CLI or Cobra. `scripts/check-architecture.py`
checks these boundaries from the compiled package graph in the normal CI pipeline.

## Library migration

| Previous API | Current API |
| --- | --- |
| `wireless.NewProfile(platform, model)` | `vendors.NewBuiltinRegistry().Fetch(platform, model)` |
| `wireless.SupportedPlatforms/SupportedDimensions` | registry `Platforms/Dimensions` |
| `wireless.NewConfig(...).Parse(text, complete)` | selected `Profile.Parse(wireless.Configuration, text, complete)` |
| root runtime parser | `parser.NewRuntimeParser(registry, model)` |
| root neighbor parser | `parser.NewNeighborParser(selectedProfile, model)` |
| root Collector/Reader/Receipt/AP types | `collect` |
| `CollectOptions.Platform/Model` | `CollectOptions.Profile` selected before admission |
| root review validation/recommendation functions | `review` |
| root ChangePlan/rendering/checking/session functions | `change` |
| `adapter` | `adapter/netconnector` |
| public `cli.Run`, `cli.LoadOptions`, `cli.Dependencies` | internal application code; embed public domain capabilities or run the executable |

```go
profile, err := vendors.NewBuiltinRegistry().Fetch("h3c", "")
if err != nil { return err }
observation, err := profile.Parse(wireless.Configuration, text, true)
```

Custom catalog callers use `parser.NewRegistry(ProfileSpec{...})`. The registry
validates command/dimension/alias uniqueness and owns declaration slices. ParseFunc
callbacks own their captured state and must be safe for concurrent reuse. Built-in
profiles capture private declarations, so caller mutations of a ProfileSpec do not
change a registered parser. Nil/zero profiles cannot parse or open sessions.

`collect.CollectOptions` explicitly pairs the selected profile with the actual
connector platform and borrowed opener. Grouped queries execute once. All reads,
including custom AP LLDP parsing, use that profile. Invalid models/names or zero
profiles fail admission; unsupported dimensions remain explicit evidence.

The removed Config wrapper added no invariant beyond selecting Configuration.
RuntimeParser retains unknown/unsupported dimension handling. NeighborParser
retains AP name/model validation and associates AP identity with detached records.
Representation helpers live in scoped internal record/text packages instead of
expanding the public SDK for mechanical sharing.

## Operational contracts

Bootstrap copies validated settings away from exported diagnostic views. Connector
owns shared inventory environment projection/validation; Wireless owns controller
selection. Inventory fetchers receive source/cache settings and return complete
cache receipts. Prefer/refresh create versioned identity entries; cache-only
requires refresh when only legacy identities exist. No legacy entry is rewritten.

Collection waits for admitted callbacks and never replays to repair publication.
Apply keeps one synchronous borrowed operation, checks caller and operation
cancellation before new work, persists intent before send and acknowledgement
after successful send, and preserves captured acknowledgements on cancellation.
Only full verified readback plus confirmed publication yields configuration_verified.
Rollback remains plan data; no retry, reconnect, rollback or save is automatic.

## Delivery boundary

`go.mod` pins published Connector v0.4.0 and Expect v0.4.1, including the shared
inventory APIs. Build and delivery checks use `GOWORK=off` and no replacements.
The external consumer exercises supported public packages and runs the built CLI
from outside the checkout; it cannot import internal application code. Ruby corpus
hashes, oracle fixtures and source-to-test mappings remain active regression inputs.

The repository carries no dependency source snapshots or workspace restoration
tooling. Module versions and checksums belong to `go.mod` and `go.sum`; old source
and implementation records remain available in Git history. Current checks and
release evidence are described in [verification](verification.md).
