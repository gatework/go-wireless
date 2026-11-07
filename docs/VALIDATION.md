> 历史冻结基线验证（2026-10-27）。当前正式依赖迁移的证据见 [2026-11-07](2026-11-07-modernization.md)；以下旧 workspace/bootstrap 结果不替代本次验证。

# 交付验证记录

验证日期：2026-10-27，Asia/Hong_Kong；本机 darwin/arm64、Go 1.27.1，最低版本门禁使用下载/已缓存的 **Go 1.26.0**。冻结参考日期仍为 2026-10-26。本工作树无 HEAD/本地提交或公开制品。

本次证据索引为 `.superpowers/sdd/wireless-port/task-5-results.jsonl`；每项均保存原命令、耗时、直接进程退出码及对应 `task-5-*.log`（stdout/stderr 合并，末尾 `EXIT_CODE`）。以下路径均相对仓库根；这些审查日志在被忽略的本地交付目录中，不是源码导出的一部分。

## 最终门禁

全部 Go 实现和新增 fuzz/示例代码完成后运行一次完整 full/race/vet；随后仅修正文档示例的环境变量拼写、隔离示例验证环境及补验证文档，未修改 Go 源码。没有以 focused race 代替完整 race。

| 命令 | 结果 | 日志（目录前缀 `.superpowers/sdd/wireless-port/`） |
|---|---|---|
| `go test ./... -json -count=1` | 0；14.537s | task-5-full.log |
| `go test -race ./... -json -count=1` | 0；134.718s | task-5-race.log |
| `go vet ./...` | 0；无诊断 | task-5-vet.log |
| `env GOTOOLCHAIN=go1.26.0 go test ./... -json -count=1` | 0；16.774s | task-5-minimum.log |
| `env GOTOOLCHAIN=go1.26.0 CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...` | 0 | task-5-linux-amd64.log |
| 同上，`GOOS=linux GOARCH=arm64` | 0 | task-5-linux-arm64.log |
| 同上，`GOOS=darwin GOARCH=amd64` | 0 | task-5-darwin-amd64.log |
| 同上，`GOOS=darwin GOARCH=arm64` | 0 | task-5-darwin-arm64.log |
| `python3 scripts/test_prepare_workspace.py` | 0；15 tests | task-5-bootstrap.log |
| `python3 scripts/check-delivery.py` | 0；clean export/bootstrap/独立 consumer | task-5-delivery-final-tree.log |
| `python3 .superpowers/sdd/wireless-port/task-5-examples.py` | 0；隔离 XDG 配置目录的示例 smoke | task-5-examples-final.log |

full、race、minimum 三项各有 **139 个顶层通过项**（136 个 Test 函数、3 个 Fuzz seed suite）和 **739 个子测试通过项**，合计 878 个 pass 事件；`TestRubyChangeInterop` 在普通测试中明确 skip，单独执行见下。数据集行数与 Go 测试函数数不同。cmd 和 library 示例包无独立测试但纳入所有构建。

## 有界 fuzz

使用 Go 1.27.1、`go test . -run=^$ -fuzz=^<名称>$ -fuzztime=5s -parallel=2`，每项均退出 0。首次编译和 seed 收集时间不属于 fuzztime；持久 Go fuzz cache 可增加 baseline seeds，不能把 seeds 数当作固定源码用例数。

| 名称 | 实际执行输入 | 日志 |
|---|---:|---|
| FuzzRuntimeParserEvidence | 22,407 | task-5-fuzz-runtime.log |
| FuzzConfigurationSource | 45,277 | task-5-fuzz-configuration.log |
| FuzzJSONCanonicalRoundtrip | 69,448 | task-5-fuzz-canonical.log |

第三项新增 decoder→canonical→decoder 的稳定字节性质；成功解码但超出 canonical 可表示域的历史值允许被 canonical 明确拒绝。未发现 crash 或新失败 corpus；这不是无限输入空间的完备证明。

## 冻结源、独立消费与许可

`check-delivery.py` 校验 dependencies/SHA256SUMS、归档精确文件白名单、regular-file 类型、路径和许可证，以及 34 spec/46 lib 的实际实现和测试定义、两组 oracle fixture 哈希。冻结 go-expect 原始 11 文件/补丁后 15 文件；go-net-connector 原始 82 文件/补丁后 83 文件。归档不含 `.env`、captures、backups、reports、Git 数据、测试、二进制或私有备份。源/补丁/补丁后每个文件 SHA256 均由 bootstrap 检查。

clean export 排除 `.work`、go.work、go.work.sum、.git、.superpowers、运行报告和 Python cache，在全新临时目录从 bundled frozen archive 两次 bootstrap 并比较所有重建源文件哈希；构建完整 CLI、运行库示例，再从独立 consumer workspace 导入运行。`go list -m -json all` 验证三个 Gatework 模块目录均在该 export 内，没有兄弟目录或缓存替身；`go list -deps .` 验证核心包不依赖 connector/expect。consumer 不访问原参考 Ruby 或兄弟 Go 项目。

第三方库（yaml、x/sys、x/term 及 native 的正常传递依赖）仍依赖模块缓存或外部下载。本次允许使用现有缓存，未清空缓存制造额外下载，不宣称网络隔离/全离线构建。当前 Gatework `v0.0.0` 是未发布占位版本，冻结工作源+补丁才是已验证构建基线。

Bootstrap 15 测试含损坏 manifest/archive/member、路径穿越、链接、重复/缺失文件、补丁歧义、发布失败、回滚失败原件保留；新增组合负例验证已经有效的 workspace 不因损坏输入而被清空。根 MIT LICENSE 完整沿用参考项目的两行署名；冻结依赖保留 MIT 与 Apache-2.0 LICENSE，fpconv.go 保留 Boost Software License 1.0。

## 显式只读 Ruby 对照

普通测试和 runtime 均无 Ruby 执行要求。只有显式开发门禁设置 `WIRELESS_RUBY_SOURCE`，读取参考 bundle；所有重生输出都写入本 Go 树的 `.superpowers/sdd/wireless-port/task5-oracle/`，不写参考仓库。

`python3 .superpowers/sdd/wireless-port/task-5-reference.py` 退出 0（task-5-reference.log）：前后核对参考 **94 文件**（含 46 lib、34 spec）、ctx_parser **19 文件**与 3 项核心 corpus 哈希，以及 Git HEAD/status。全 RSpec 再执行为 **310 examples, 0 failures**。重生 parser/source/field、fpconv、recommendations 与冻结 fixture 做 JSON 语义相等；只排除 gzip 封装头，不放宽事实、时钟或来源路径。

当前实际数据为 **296 parser cases、其中 119 source/capture cases、291 field cases、16,138 finite binary64 cases、14 advice cases**。这些数由文件读取确认；它们不是 17k 个独立 Go 测试函数。逐文件映射见 COMPATIBILITY.md 与两个 JSON 矩阵，所有条目引用现存实现和具体测试，没有占位 TODO。

```sh
WIRELESS_RUBY_SOURCE=/Users/wenwu.yan/Works/ruby/wireless \
WIRELESS_RUBY=/opt/homebrew/opt/ruby/bin/ruby \
  go test ./cli -run '^TestRubyChangeInterop$' -v -count=1
```

该命令退出 0（task-5-interop.log）：Ruby 4.0.7 / JSON 3.1.0 复核 Go collect 报告，验证完整配置指纹和命令，真实本地 PTY 确认后由注入 session 应用 Go 计划，篡改拒绝。反向由普通 Go 测试读取原 Ruby 报告与计划，在 injected/PTY apply 中执行，另有 collect→review→apply 完整指纹 roundtrip。完整配置指纹为 `294aea47c2657702a541a4d791b3376d5a96c3a9bcadbda1a606d64545d5d573`。

## CLI 工作流证据

以下具体测试均在最终 full/race/minimum 日志中执行：

| 工作流 | 具体测试及验证边界 |
|---|---|
| help/version、直接 collect 参数、stdin parse、plan | cli/TestSubprocessCLI；编译并运行真实可执行文件 |
| init/show-config/分层配置/无覆盖 | cli/TestInitializePrivateNoOverwriteAndInheritance、TestInitializationPreservesPublicSchemaDefaults、TestInformationalAndMissingConfigurationDoNotConsumeInput、TestLayeredConfigurationPathsAndSecrets |
| collect共享查询、partial、取消/调度/资源失败 | core/TestCollectorSharedQueriesFirstFactsAndNoSession、TestCollectorIndependentFailurePreparationAndClose；cli/TestCollectBothPlatformsPrivateReportsAndReuse、TestStoreFailureCancelsSiblingAndRetainsStartedEvidence、TestUserCancellationJoinsAndKeeps130OnSummaryFailure |
| parse stdin/file、取消输入/用户文件所有权 | cli/TestParseOfflineAndNumericIntent、TestParseEvidenceFileIsNotSubjectToConfigFileBudget，cli/cancellation_review_test.go 的六项 TestReview*（包括真实 SIGINT/open stdin） |
| Netdisco清单/分页/规则/cache | cli/TestHTTPInventoryThroughNativePaginationAndControllerRules、TestCacheOnlyAndRefreshFallbackUseNativeVerifiedCache；HTTP 为本地 httptest，PostgreSQL 只测配置与凭据边界 |
| H3C AP LLDP / native 执行 | adapter/TestNativeAPDialoguePaginationAndExactReturn、TestNativeAPCancellationRetiresInsideLease 等；真实 native parser/lease + net.Pipe 注入字节流，非真实 SSH/telnet 服务 |
| review Ruby/Go批次、变更完整指纹 | report/TestReviewRubyFullConfiguration、cli/TestDefaultOfflineReviewAndNoOutputOnInvalidInput、TestCollectReviewApplyFullFingerprintRoundtrip |
| apply TTY负例、精确token、取消、无重放、intent/readback | cli/TestApplyRejectsTTYAndTamperingBeforeFactory、TestApplyRealTTYConfirmationAndCancellation、TestApplyDurableIntentReadbackAndNoReplay、TestApplyFailureBoundaries；本地真实 PTY或注入输入 |
| native apply一租约、拒绝/partial、独立安全诊断 | adapter/TestNativeApplyOneLeaseCompleteReadback、TestNativeApplyRejectedOrPartialReadRetires；cli/TestApplyRetainsIndependentSafeDiagnostics |
| 发布失败/late cancellation/恢复 | report/TestFinalizePhaseFailuresNeverLeaveCompleteSummary、TestExecutionFinalPublicationFailurePreservesCheckpoint、TestExecutionLateCancellationCannotLeaveVerifiedPublication、cli/TestReviewStdoutRecoveryInterruptRetains130AndCheckpoint |

## 发现及限制

- 初次 delivery 仅因 macOS `/var` 与 `/private/var` 同一临时目录的字符串比较失败；对临时路径统一 resolve 后通过，未改产品语义（task-5-delivery.log，exit 1；后续成功日志保留）。
- 额外示例 smoke 初次自动读取已有用户配置并被其 schema 错误阻止。随后在临时 `XDG_CONFIG_HOME` 中验证合成样本，全部通过（task-5-examples.log / task-5-examples-diagnostic.log，exit 1；task-5-examples-final.log，exit 0）。不会改用户配置；显式 --config 仍会校验发现的配置层，不能绕过错误的已有层。
- task-5-preflight.log 是新增 seed/关键 parser/配置的先行 focused 证据，不代替最终门禁。Task4 fix1 与独立 rereview 的结论通过当前 full/race 验证，历史 focused race 不代替最终完整 race。
- 无真实设备、外部 HTTP/PostgreSQL 服务、真实 SSH/telnet 服务、部署或发布验证。跨平台只有 CGO0 构建，未在三种其他目标系统运行测试。库/CLI 与冻结基线的公共功能映射齐全，但不宣称支持基线之外的新厂商、固件语法或写动作。
