# Collection and native transport API

Package `github.com/gatework/go-wireless` adds collection without importing a
transport. Package `github.com/gatework/go-wireless/adapter` binds the pinned native
connector v0.3.0 and go-expect v0.4.0. Use `GOWORK=off`; no local workspace
or replacement is required.

```go
type Clock func() time.Time
type Session interface {
    State(context.Context) (bool, error)
    Execute(context.Context, string) (Receipt, error)
}
type OpenSession func(context.Context, func(Session) error) error
type CollectorOptions struct { Clock Clock; APFactory APFactory }
func NewCollector(CollectorOptions) *Collector
type CollectOptions struct {
    Target Record
    Platform, Model, ConnectorPlatform string
    Dimensions []Dimension
    APNames []string
    APCredentials APCredentialsSource
    Open OpenSession
}
func (*Collector) Collect(context.Context, CollectOptions) (Snapshot, error)
```

Open owns connect/callback/close. Sessions are borrowed: Execute must never
connect/retry/reenter implicitly. Open should call its callback synchronously;
repeated/concurrent callbacks are guarded and never replay commands. Collection
waits for an already admitted callback to complete. Retained callbacks invoked
after Open returns cannot submit commands. Errors become safe observations;
invalid inputs return errors before opening. Nil Dimensions selects declaration
order excluding LLDP; an explicit empty scope is invalid. LLDP adds AP inventory;
AP groups/service templates add configuration. Unsupported platform dimensions
remain explicit. The caller supplies the actual connector platform separately;
aliases normalize through the same profile registry.

```go
type ReceiptOptions struct {
    Command, Raw, Body string
    Attempted bool
    Steps int
    Success bool
    Failure *Failure
}
func NewReceipt(ReceiptOptions) Receipt
func (Receipt) Raw() string
func (Receipt) Body() string
func NewReader(Session, Clock) (*Reader, error)
func (*Reader) Read(context.Context, string) (Capture, error)
func (*Reader) Failure(context.Context) *Failure

type CaptureOptions struct {
    Command string
    Body *string
    StartedAt, CompletedAt time.Time
    Evidence []Record
    Failure *Failure
    Skipped bool
}
func NewCapture(CaptureOptions) (Capture, error)
func (Capture) Command() string
func (Capture) Body() string
func (Capture) StartedAt() time.Time
func (Capture) CompletedAt() time.Time
func (Capture) Evidence() []Record
func (Capture) Failure() *Failure
func (Capture) Success() bool
func (Capture) Skipped() bool
func (Capture) ToMap() Record
func NewFailure(code, stage string) *Failure
func (*Failure) Code() string
func (*Failure) Stage() string
func (*Failure) ToMap() Record
```

Receipts are immutable and opaque. Reader validates the requested command and
one successful step and an explicit attempt. Evidence hashes the exact receipt Raw
before decoding; unattempted commands have no byte evidence:
`command`, `sha256`, `bytes`, `observed_at` (UTC, six fractional digits). Body is
explicit and absent from formatting/JSON; failed captures cannot contain Body.
Evidence accessors return deep copies. A rejected command remains a failure and
can never acknowledge an apply command. A synchronized rejected query allows
independent later queries; closed/unknown state preserves the first failure and
skips subsequent commands. Reader rejects callback reentry without execution.
Failure vocabulary and stage are allowlisted; arbitrary error messages are never
serialized. Zero clocks are invalid. Typed interfaces eliminate Ruby's wrong-type
receipt/skipped/credential shapes; zero receipts and zero captures remain invalid.

```go
type APCredentials struct { Username, Password string }
type APCredentialsSource func(context.Context) (APCredentials, error)
type APOutcome struct {
    Result *Capture
    Error, CleanupError, CloseError *Failure
    Skipped bool
}
type APReader interface { Read(context.Context, Record) (APOutcome, error) }
type APFactory func(context.Context, Session, APCredentials) (APReader, error)
const LLDPCommand = "display lldp neighbor-information verbose"
```

AP credentials are resolved lazily only when selected inventory is nonempty and
an AP factory exists. The factory gets the actual borrowed controller as a
separate argument, which credentials cannot override. Each result must be one
valid capture for LLDP; skipped success is rejected. AP identity/command/status/
attempted/count are bound by collection, overriding adapter evidence claims.
Read, cleanup and close errors are independent, deduplicated by stable code while
successful captured neighbor facts survive cleanup errors.

```go
// package adapter
func Borrow(*connector.Device) (*Session, error)
func (*Session) CloseFailure() *wireless.Failure
func Open(*connector.Device) wireless.OpenSession
func APFactory(wireless.Clock) wireless.APFactory
func NewAPSession(*connector.Device, wireless.APCredentials, wireless.Clock) (*APSession, error)
func (*APSession) Read(context.Context, wireless.Record) (wireless.APOutcome, error)
```

Construct the native device through `vendors.NewBuiltinRegistry().Lookup`,
`Platform.Profile()` and `connector.New` (inject
`connector.WithTransportFactory` for tests). `Borrow` itself never connects or
closes. Its Execute permits only the fixed profile preparation/query declarations,
binds the exact current prompt, skips privilege escalation, protects output,
retains rejected/partial evidence and requests rejection preservation. It owns
failure retirement within that query lease and retains the first close failure for
Open (or a custom host via CloseFailure), preserving it even if a later close succeeds. AP login
uses nested telnet through the controller, bounded sensitive credentials and one
`Device.WithBorrowedOperation` around login/preparation/query/exit/retirement. AP session
must be online with a nonzero IPv4 management address and valid name. A busy lease
is untouched. Unknown prompt/cancellation retires without quit; only confirmed AP
state may send quit, and return must match the original controller prompt exactly.

The connector owns the borrowed lease, command retirement and first cleanup result.
`WithBorrowedOperation(ctx, func(context.Context, *Operation) error)` requires an
already connected device and returns operation/cleanup errors separately. Execute
uses ordinary `Operation.ExecuteCommand`; no borrowed-command alias or partial
capture option is needed. `Result.Attempts` retains every attempted raw response
in wire order; `Result.Steps` contains accepted main commands only.
`PreserveSessionOnRejection` is valid only for one borrowed command and only keeps
a synchronized, verified pure device rejection after all observers/logging succeed.
Retire works after cancellation, rejects stale/reentrant/repeated calls, and the
scope retains its first failure even if the callback ignores it. An explicit
Device.Close can retry failed cleanup. Device.Close is terminal and cancels active
work; observer Close is not treated as lease reentry.

Apply disables rejection preservation: a rejected/partial response never
acknowledges a write. Body uses verified text and removes only the exact prompt
and first matching echo, preserving the preceding newline and indentation.

The native connector uses the host SSH/telnet executables;
the wireless implementation and pinned libraries require no Ruby runtime.

Collector opener errors may use `errors.Join` for independent primary and close
failures. Collection issues retain their stable codes in original order, including
cancellation/deadline and explicit Failure codes, with duplicate codes removed.
Only a generic untyped post-callback error is inferred as session_close_failed.

Typed apply uses `adapter.Apply` and `wireless.ApplySession`, documented in
`docs/change-api.md`. It owns one `WithBorrowedOperation` lease across preparation,
precheck, writes, readback and retirement; it never nests the collection adapter.
Both Open and Apply reject busy admission without closing another caller's lease.

`internal/nativeerr` centralizes allowlisted connector diagnostics for adapter and
apply. It omits arbitrary text, Source and causes from displayed diagnostics;
apply traverses joined causes to retain independent diagnostic arrays.
