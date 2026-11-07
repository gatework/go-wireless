# Core API contract

Module `github.com/gatework/go-wireless`, package `wireless`; Go 1.26 minimum.
This document freezes the Task 1 interface for collector, CLI, and review/apply.
The package has no network operations, file operations, or Ruby runtime dependency.

## Names and evidence types

```go
type Record = map[string]any
type Dimension string
type Status string
```

Dimension constants: `AccessPoints`, `Radios`, `BSSes`, `Clients`, `Configuration`,
`APGroups`, `ServiceTemplates`, `Dot1xConnections`, `MABConnections`,
`BlockedClients`, `ClientIPConflicts`, `APModels`, `LLDP`, `Collection`.
Their wire values are the corresponding Ruby snake_case names.
Status constants: `Complete`, `Partial`, `Failed`, `Unsupported`, `NotRequested`.

Records use string keys, explicit `nil` for known unknowns, and absent keys where
Ruby omitted facts. Arrays preserve source order. Integer fields use integers;
measurements use finite floating-point numbers. Large controller integers use
`json.Number` without rounding; ordinary controller integers use `int64`. JSON deserialization preserves
numbers using `json.Number`. Do not coerce absent or unknown values to zero.
Constructors copy all nested maps/slices. Accessors return independent copies.

## Observation and Snapshot

```go
type ObservationOptions struct {
    Name Dimension
    Status Status
    Records, Issues, Evidence []Record
    StartedAt, CompletedAt *time.Time
}
func NewObservation(ObservationOptions) (Observation, error)
func (Observation) Name() Dimension
func (Observation) Status() Status
func (Observation) Records() []Record
func (Observation) Issues() []Record
func (Observation) Evidence() []Record
func (Observation) StartedAt() *time.Time
func (Observation) CompletedAt() *time.Time
func (Observation) Complete() bool
func (Observation) Partial() bool
func (Observation) Available() bool
func (Observation) ToMap() Record

type SnapshotOptions struct {
    Target Record
    Dimensions map[Dimension]Observation
    ObservedAt time.Time
}
func NewSnapshot(SnapshotOptions) (Snapshot, error)
func (Snapshot) Target() Record
func (Snapshot) Dimensions() map[Dimension]Observation
func (Snapshot) Dimension(Dimension) Observation
func (Snapshot) Records(Dimension) []Record
func (Snapshot) ObservedAt() time.Time
func (Snapshot) Complete() bool
func (Snapshot) ToMap() Record
```

`failed`, `unsupported`, and `not_requested` observations cannot carry records.
A missing snapshot dimension returns a `not_requested` observation. A snapshot
requires a nonempty supported dimension map, matching observation names, a target
object, and an explicit nonzero clock time. Completeness excludes `not_requested`
and requires at least one selected observation.

Both types implement `json.Marshaler` and `json.Unmarshaler`. Shared
`MarshalJSON(any)` / `MarshalJSONIndent(any, prefix, indent)` preserve numeric
kinds in nested fact maps/slices: integral float measurements retain `.0`,
negative zero remains `-0.0`, and json.Number decimal/exponent tokens and
arbitrary integer precision remain exact. Observation/Snapshot (also Coverage
and Projection) use this boundary. Use it for explicit ToMap exports, and
Decoder.UseNumber when reading generic records. This ordinary wire encoder is
separate from the exact Ruby canonical fingerprint encoder supplied by Task 4.
 Observation wire
keys are `name,status,records,issues,evidence,started_at,completed_at`, including
null timestamps and empty arrays. Snapshot keys are `target,dimensions,observed_at`.
Timestamps serialize to RFC 3339 with six fractional digits. `String` and
`GoString` expose only safe metadata.

To attach collector metadata, reconstruct an observation using the original
`Name`, `Status`, `Records`, `Issues` accessors and the collector's `Evidence`,
`StartedAt`, `CompletedAt`; this does not mutate parser facts.

## Registry and query declarations

```go
func SupportedPlatforms() []string
func SupportedDimensions() []Dimension
func NewProfile(platform, model string) (*Profile, error)
func (*Profile) Platform() string
func (*Profile) Model() string
func (*Profile) PreparationCommand() string
func (*Profile) Dimensions() []Dimension
func (*Profile) Commands() map[Dimension]string
func (*Profile) Command(Dimension) (string, error)
type Query struct { Command string; Dimensions []Dimension }
func (*Profile) Queries([]Dimension) ([]Query, error)
func (*Profile) Parse(Dimension, string, bool) (Observation, error)
func (*Profile) ParseMany([]Dimension, string, bool) (map[Dimension]Observation, error)
```

Platforms normalize case/outer whitespace; `h3c_wireless` aliases `h3c`.
Unsupported platforms return an error without including caller data. Models
normalize case/outer whitespace; the frozen Ruby profiles have no model-specific
overrides. Query order follows selected dimension order and groups shared commands
once. `Dimensions` returns an owned slice in profile declaration order, including
LLDP; collectors must exclude LLDP from defaults unless explicitly requested.
`Queries` accepts an empty scope, rejects duplicate/unsupported dimensions,
and returns owned slices. `ParseMany` requires a nonempty unique scope covered by
one command; unrelated command bodies must remain separate.

H3C commands:

| Dimensions | Command |
|---|---|
| client_ip_conflicts | display wlan statistics client-ip-conflict |
| ap_models | display wlan ap-model all |
| access_points, radios | display wlan ap all verbose |
| bsses | display wlan bss all verbose |
| clients | display wlan client verbose |
| configuration, ap_groups, service_templates | display current-configuration |
| dot1x_connections | display dot1x connection |
| mab_connections | display mac-authentication connection |
| blocked_clients | display wlan client-security block-mac |
| lldp | display lldp neighbor-information verbose |

Huawei supports `access_points` with `display ap all` and `clients` with
`display station all`. Preparation commands are `screen-length disable` (H3C)
and `screen-length 0 temporary` (Huawei).

## Runtime and configuration parsing

```go
type ParseOptions struct { Platform string; Complete bool }
func NewRuntimeParser(model string) (*RuntimeParser, error)
func (*RuntimeParser) Parse(Dimension, string, ParseOptions) (Observation, error)
func (*RuntimeParser) ParseMany([]Dimension, string, ParseOptions) (map[Dimension]Observation, error)
func NewConfig(platform, model string) (*Config, error)
func (*Config) Parse(content string, complete bool) (Observation, error)
func NewNeighborParser(platform, model string) (*NeighborParser, error)
func (*NeighborParser) Parse(content, apName string, complete bool) (Observation, error)
```

An empty runtime/config/neighbor platform selects H3C. Runtime parsing returns
`unsupported` for dimensions outside the platform. Config's configuration view
must be supported by its profile. Neighbor parsing validates AP identity then
returns `unsupported` if LLDP is unsupported. Invalid UTF-8/NUL is failed evidence;
terminal damage is partial evidence with no runtime records. Complete capture
never masks missing syntax, invalid totals, conflicts, or radio/port coverage.
Configuration keeps unrelated usable facts and marks damaged source lines invalid.
All parsers can be reused concurrently; each call owns its parsing state.

## Configuration source evidence

```go
func ParseConfigurationSource(content string, complete bool) (*ConfigurationSource, error)
func (*ConfigurationSource) Observation() Observation
func (*ConfigurationSource) Content() string
func (*ConfigurationSource) Checksum() string
func (*ConfigurationSource) Lines() []SourceLine
func (*ConfigurationSource) States() []string
func (*ConfigurationSource) State(index int) (string, error)
func (*ConfigurationSource) Issues() []Record
func (*ConfigurationSource) Captures() []ConfigurationCapture
func (*ConfigurationSource) CapturesFor(path []any) []ConfigurationCapture
func (*ConfigurationSource) UnparsedContent() string
func (SourceLine) Index() int
func (SourceLine) Number() int
func (SourceLine) Raw() string
func (SourceLine) Text() string
func (SourceLine) Indent() int
func (ConfigurationCapture) Data() any
func (ConfigurationCapture) Path() []any
func (ConfigurationCapture) FirstLine() int
func (ConfigurationCapture) LastLine() int
func (ConfigurationCapture) Content() string
func (ConfigurationCapture) Complete() bool
```

This is the H3C controller configuration parser with explicit read-only source
tracking, corresponding to Ruby's parser cursor/document/capture interfaces.
It preserves exact source bytes/newline boundaries and SHA256, tracks ignored,
parsed, unsupported, invalid, and pending states, and snapshots facts at each
scope's completion. Source ranges are inclusive; indexes are zero-based, source
line numbers are one-based. Paths contain string keys, integer array indexes,
and nil for Ruby's unnamed virtual AP group. Unnamed virtual group JSON map keys
serialize as the empty string, as in Ruby. Unsupported/invalid byte inputs have no
source document. Source objects and captures have safe `String`, `GoString`, and
metadata-only JSON. Raw source is exposed only through explicit content accessors.

## Projection and Coverage

```go
func NewProjection(dimensions Record) (Projection, error)
func (Projection) Dimensions() Record
func (Projection) Complete() bool

type CoverageOptions struct {
    Observations, Projection Record
    Dimensions []Dimension
    Now time.Time
    StaleAfter float64 // seconds
}
func NewCoverage(CoverageOptions) (Coverage, error)
func (Coverage) ToMap() Record
```

Projection entries require a status; only `complete` entries yield completeness.
An empty projection is complete. Nil observation history yields inventory/untracked;
an empty history yields CLI/unknown. Coverage checks latest status, valid clock,
staleness, matching projection timestamp, and successful projection independently.
Freshness is finite and nonnegative. Coverage copies input history before retaining
it. Both expose metadata-only formatters and JSON serialization.

## Field and terminal helpers

```go
type NewlinePolicy string // PreserveNewlines, LFNewlines, DeleteCRNewlines
func NormalizeText(string, preserveNewlines bool) (string, error)
func NormalizeTextWithPolicy(string, NewlinePolicy) (string, error)
func NormalizeValue(string) *string
func ParseMAC(*string) (*string, error)
func ParseIP(*string) (*string, error)
func ParseInteger(*string, max *int64) (*int64, error)
func ParseIntegerExact(*string, maximum *big.Int) (*big.Int, error)
func ParseVLAN(*string) (*int64, error)
func ParseNumber(*string, unit string) (*float64, error)
func ParseDuration(*string) (*int64, error)
func ParseDurationExact(*string) (*big.Int, error)
func ParseRatesMbps(*string) (*float64, *float64, error)
func ParseChannel(*string) (*int64, error)
func ParseChannelWidth(*string) *int64
func ParseChannelWidthExact(*string) *big.Int
func FrequencyBand(string) *string
func OnlineStatus(string) string
func TerminalUnsupported(string) bool
func TerminalInvalid(string) bool
func TerminalIssue(string) Record
```

Nil optional scalar pointers remain unknown; invalid syntax returns safe errors.
The convenience scalar helpers use int64 and reject overflow. The `Exact` helpers
return caller-owned `math/big.Int` values; parser records retain arbitrary integer
and duration precision as Ruby does. Frequency band requires explicit evidence. Configured
power is separate from measured power. Authorization URLs and credentials do not
enter normalized facts; known URL fields retain presence booleans.

Canonical fingerprints, typed security changes, generic historical report
validation and recommendation APIs are documented in `docs/change-api.md`.
