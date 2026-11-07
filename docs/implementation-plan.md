# Go Wireless implementation plan

Approved source: conversation 2026-10-26; user selected independent go-wireless and full functional/data compatibility, then requested implementation.

## Goal and constraints
- New /Users/wenwu.yan/Works/go/go-wireless, module github.com/gatework/go-wireless, library wireless and cmd/wireless binary.
- Pure Go runtime. Ruby source /Users/wenwu.yan/Works/ruby/wireless is read-only development oracle; baseline HEAD b328e1fba414ecf09ce891190b0eb375269dcae3 plus exact source/spec/lockfile SHA256.
- Preserve all sibling workspaces. No commits, publication, deployment or real-device operations. Do not treat stages as complete delivery.
- Go minimum 1.26; Linux/macOS amd64/arm64. CLI collect, parse, review, apply; machine data compatible with Ruby reports and plans.
- All H3C dimensions: access_points, radios, bsses, clients, configuration, ap_groups, service_templates, dot1x_connections, mab_connections, blocked_clients, client_ip_conflicts, ap_models, lldp. Huawei AP/client only; unsupported explicit.
- Observation, Snapshot, Projection, Coverage, Config, RuntimeParser, NeighborParser, Collector, Recommendations, ChangePlan semantics preserved. Core package wireless, vendor responsibility split into h3c_*/huawei_* files; connector-free parser library. No cyclic dependencies or broad parser frameworks.
- Internal evidence ownership/private fields, deep-copy ingress/egress; safe String/GoString/errors; explicit JSON export with stable keys, absence/null/empty arrays, numeric meaning and list order.
- Configuration precedence CLI > process environment > YAML > .env > defaults. Match Ruby discovery, source-relative paths, shared/dedicated config, strict validation, no overwrite initialization; reuse connector public primitives where compatible, not its different CLI schema wholesale.
- Static + Netdisco HTTP/Postgres/cache inventory, filtering not scanning, controller parallelism and sequential per-controller queries; shared command collected/parsed once; independent dimensional completeness.
- LLDP explicitly requested only. Independent lazy AP credentials. Nested AP telnet via controller, one non-reentrant lease per full dialogue, quit only from confirmed AP, uncertain owned connection closes, busy borrowed connection untouched. No enabling remote console.
- CLI streams and exit codes preserved. SIGINT 130; context cancellation stops dispatch, cancels in-flight operations, joins workers, finishes checkpoint once preserving completed/pending.
- Reports 0700 directories/0600 files, atomic publication and fsync receipts, incomplete checkpoint before completion, output/storage failures cannot replay network operations.
- review candidate/blocked/investigate rules, commented suggested/rollback files and read-only verification scripts; Ruby/Go reports and plans mutually consumable.
- Canonical fingerprint bytes match Ruby sorted map keys, array order, escaping and numbers. Source SHA256 uses original bytes.
- apply only known H3C wireless_security removal of TKIP/WPA with CCMP/RSN and explicit enabled state. Exact real-TTY APPLY IP SHA-prefix confirmation; strict hostkeys; typed render and command comparison before connection; configuration fingerprint and prerequisites before writes; persist intent before each command, receipt after; verify readback; uncertain on ambiguous effects; no retry/reconnect/automatic rollback/config save.
- Dependencies are unpublished and dirty siblings. Freeze allowlisted source snapshots + SHA manifest in dependencies/, restore to .work/ via deterministic bootstrap relative go.work; exclude device captures/backups/credentials/logs; never claim public go get works absent published dependencies.

## Tasks
1. Baseline, core domain, all configuration/runtime/neighbor parsers, field/terminal semantics, Projection/Coverage, Ruby differential corpus and tests.
2. Frozen dependencies, session collection and go-net-connector adapter including AP nested dialogue and cancellation; fake/PTY verification.
3. CLI configuration/discovery, static/dynamic inventory, collect/parse, progress/report atomic publishing; complete compatible flags and failure tests.
4. Recommendations, review, typed plans and interactive apply; Ruby interoperability, canonical hashes and write-boundary tests.
5. Final cross-component review, race/vet/fuzz/build/isolated consumer and platform checks, Chinese docs, compatibility matrix and validation evidence.

## Acceptance
Source/spec coverage matrix with every public feature mapped to implementation/tests; all goldens exact semantic match (only clock/path normalization), independent literal assertions and negative mutations; no dropping unknown syntax to manufacture complete. Tests include conflict/count/zero/malformed controls, memory ownership/log secrecy, session reentry/cleanup/lease/cancellation, report write/rename/fsync failures, input/plan tampering, no retry and interop in both directions. Report exact verified/offline/live boundaries.
