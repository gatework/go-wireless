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
| `wlan` | typed controller model: configuration (service templates, AP groups, AP/radio `Settings`) and runtime tables, strictly decoded from parser output; unknown fields downgrade a dimension to partial (`unmodeled_fact`) |
| `rules` | schema 3 risk catalog over `wlan.Controller` on the go-netops `assess` engine; `assessment.json` with severity, category, subject path and remediation |
| `internal/bootstrap` | configuration, immutable settings and lazy concrete dependency assembly |
| `internal/workflow/*` | collect/review/apply application state machines |
| `internal/cli` | Cobra commands, flags, confirmation, progress and exit/output presentation |

The root does not import domain packages or transports. Parser never imports
vendors. Collect receives a selected profile and never discovers a built-in parser;
this includes AP LLDP. Huawei does not import H3C. Change does not import review.
Workflow never imports bootstrap, CLI, Cobra, concrete connector, adapter or vendor
packages. Apply consumes publication state through narrow interfaces and native
diagnostics through internal/nativeerr; collect/review retain their report value
contracts. Apply does not import the concrete report implementation. `scripts/check-architecture.py`
checks these boundaries for entire package subtrees from the compiled package
graph in the normal CI pipeline. Domain infrastructure and ownership rules inspect
transitive dependencies too, so an internal helper cannot hide an inverted import.
Standard-library I/O restrictions and workflow adapter boundaries stay direct; the
workflow intentionally consumes nativeerr. Its negative and permitted-boundary controls run
under both ordinary Python and -O; path prefixes cannot bypass or broaden a rule.

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
| `netconnector.Borrow/Open(device)` | `netconnector.Borrow/Open(device, selectedProfile)` |
| `APReader.Read(ctx, record)` | `APReader.Read(ctx, collect.APRequest{AccessPoint: record, Query: scope.APQuery()})` |
| `collect.LLDPCommand` | selected `parser.APQuery.Command()`; preparation comes from APPreparation |
| `NewAPSession(device, credentials, clock)` | `NewAPSession(device, selectedProfile, credentials, clock)` |
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
profiles fail admission; unsupported dimensions remain explicit evidence. Bootstrap
selects one profile per controller and passes it through connection settings, scope,
adapter admission, parsing and evidence binding. Native controller queries are
restricted to that immutable declaration; AP-only queries are excluded. APQuery
carries the selected registry capability, so zero/foreign queries are rejected
before AP lease acquisition or writes. APPreparation is a vendor declaration. H3C
login, exit and prompt identity remain explicit protocol behavior; Huawei AP work
remains unsupported. Connector owns accepted-step validation and verified body
extraction; Wireless still forbids successful Body on a failed Capture.

The removed Config wrapper added no invariant beyond selecting Configuration.
RuntimeParser retains unknown/unsupported dimension handling. NeighborParser
retains AP name/model validation and associates AP identity with detached records.
Representation helpers live in scoped internal record/text packages instead of
expanding the public SDK for mechanical sharing.

## Operational contracts

Bootstrap copies validated settings away from exported diagnostic views. Command
contexts reach configuration/env/discovery reads through Connector config.ReadFile
and LoadContext; evidence-document reads retain their independent size policy. Connector
owns shared inventory environment projection/validation; Wireless owns controller
selection. Inventory fetchers receive source/cache settings and return complete
cache receipts. Prefer/refresh create versioned identity entries; cache-only
requires refresh when only legacy identities exist. No legacy entry is rewritten.

Collection waits for admitted callbacks and never replays to repair publication.
Apply owns typed execution progress and projects detached records only at journal
and result boundaries. It keeps one synchronous borrowed operation, checks caller and operation
cancellation before new work, persists intent before send and acknowledgement
after successful send, and preserves captured acknowledgements on cancellation.
Only full verified readback plus confirmed publication yields configuration_verified.
Rollback remains plan data; no retry, reconnect, rollback or save is automatic.

## Delivery boundary

`go.mod` pins published Connector v0.6.0 and Expect v0.5.0, including the shared
inventory and SingleAccepted/Body/ReadFile/LoadContext APIs. Formal build and
delivery checks use GOWORK=off and no replacements. Earlier workspace and frozen
local-proxy results describe the unpublished candidate stage; release verification
also requires the same-commit CI artifact and independent tagged consumers.
The external consumer exercises supported public packages and runs the built CLI
from outside the checkout; it cannot import internal application code. Ruby corpus
hashes, oracle fixtures and source-to-test mappings remain active regression inputs.

The repository carries no dependency source snapshots or workspace restoration
tooling. Module versions and checksums belong to `go.mod` and `go.sum`; old source
and implementation records remain available in Git history. Current checks and
release evidence are described in [verification](verification.md).

## Schema 3 assessment

`review` additionally publishes `assessment.json` and links `assessment`,
`risk_counts`, `risk_highest` and `assessment_incomplete` from `review.json`.
`--rules-policy FILE` enables or disables rules and overrides severity and
thresholds; unknown rules or mistyped parameters are rejected.

Rules that need a dimension declare it, and the engine skips them when the
evidence is incomplete. Configuration rules and remediations stay limited to
`h3c_wireless`; AP state is normalized (H3C `R/M`, `Run`, `R`; Huawei
`normal`, `online`) so runtime rules apply to both vendors. The TKIP/WPA fix
is the typed `change.TemplateSecurity`, the only source of applicable commands.
Review output and plans come from the typed assessment;
`rules/parity_test.go` compares the ported checks with the legacy fixtures,
and legacy fixtures in shapes the parser never emits must be reported as
incomplete evidence.

## Baseline and changes

Every review publishes `baseline-draft.yaml`: per controller, each service
template's SSID, VLAN, enable state and security (AKM, ciphers, IEs), the
majority AP firmware per model and the backup AC. The draft describes the
current state, not the correct one; operators review and trim it before
passing it back with `review --baseline FILE`, which reports drift
(`wireless.baseline_*`) from the declared intent. `defaults` apply to every
controller; `controllers.<host>` entries override per template and model.

`review --previous DIR` compares with an earlier batch (`wireless.change_*`):
running APs that disappeared or went down, LLDP uplink switch/port changes,
AP firmware changes, and service templates added, removed or changed. A
dimension incomplete in either batch infers no change; controllers present
in only one batch are listed as `appeared` / `disappeared`.
