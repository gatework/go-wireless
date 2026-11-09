# 兼容性与溯源

冻结日期 2026-10-26，交付验证日期 2026-10-27（Asia/Hong_Kong）。Ruby Wireless HEAD `b328e1fba414ecf09ce891190b0eb375269dcae3`，Ruby 4.0.7、ctx_parser 0.2.4、JSON 3.1.0。逐文件哈希见 `ruby-baseline.json`，变更/fpconv oracle 见 `change-oracle.json`。原 RSpec 34 文件含 294 个字面 example/context 块，实际展开执行 310 examples；以下是文件/功能映射，不能把数据案例数当作独立 Go 测试函数数。

## 平台与边界

| 平台 | 维度 | 处理 |
|---|---|---|
| h3c / h3c_wireless | access_points, radios, bsses, clients, configuration, ap_groups, service_templates, dot1x_connections, mab_connections, blocked_clients, client_ip_conflicts, ap_models, lldp | 按 profile 声明共享查询；LLDP 显式选择 |
| huawei | access_points, clients | 其他维度为 unsupported |
| 其他平台 | 无 | 会话前拒绝 |

`collection` 为采集生命周期诊断，不是用户可请求的设备命令。模型按原 profile 规范化，冻结基线无模型专属覆盖。h3c 是解析/采集别名；建议和可应用计划只接受原 Ruby 的精确 h3c_wireless 身份。Go 使用强类型接口取代 Ruby 动态错误形状；未提供 Rails/ActiveSupport 接口。核心包和报告结构保持 source order、未知值、精度、事实/来源边界；Go 对外显式构造错误与所有权约束见 core-api.md。

版本号属于各实现自身的包元数据：Ruby `Wireless::VERSION` 为 `0.3.1`，当前 Go 版本由根目录 `VERSION` 与 `internal/cli.Version` 定义（v0.2.0）。核心 `wireless` 包不导入 CLI，导入库不会运行 CLI。

配置指纹完全遵循 Ruby JSON 3.1.0 fpconv，不是 RFC 8785。普通 wire JSON 和 canonical fingerprint 编码是两条边界。唯一受控写动作是原 Ruby 已有的 wireless_security；其他诊断保持复核建议，不能生成自动写动作。

运行依赖使用正式 `go-net-connector v0.6.0` / `go-expect v0.5.0`，版本和校验和以 `go.mod/go.sum` 为准。构建不使用冻结源码或本地 workspace。Ruby 来源哈希和 oracle fixture 用于行为回归，不作为 Go 运行依赖；实际验证范围见 [验证说明](verification.md)。

## 全部 Ruby lib 文件

每行均对应实际 Go 实现与具体测试；`TestRubyParserCorpus` 比较原参考 spec 执行时捕获的完整输出，以及 119 个源文档/capture 结果。

| Ruby 文件 | Go 实现 | 具体测试 |
|---|---|---|
| `lib/wireless.rb` | observation.go; parser/registry.go; collect/collect.go | core_test.go:TestObservationOwnershipAndSafeFormatting |
| `lib/wireless/ap_session.rb` | adapter/netconnector/ap.go | adapter/netconnector/native_test.go:TestNativeAPDialoguePaginationAndExactReturn |
| `lib/wireless/change_plan.rb` | fingerprint.go; document.go; change/h3c.go; fpconv.go; change/apply.go | change_plan_test.go:TestTypedSecurityChange; change_plan_test.go:TestRubyFPConvCorpus |
| `lib/wireless/cli.rb` | internal/cli/runner.go; cmd/wireless/main.go | internal/cli/subprocess_test.go:TestSubprocessCLI |
| `lib/wireless/cli/apply.rb` | internal/cli/apply.go; internal/workflow/apply/diagnostics.go | internal/cli/apply_test.go:TestApplyDurableIntentReadbackAndNoReplay; internal/cli/apply_diagnostics_test.go:TestApplyRetainsIndependentSafeDiagnostics |
| `lib/wireless/cli/collect.rb` | internal/cli/runner.go | internal/cli/runner_test.go:TestCollectBothPlatformsPrivateReportsAndReuse |
| `lib/wireless/cli/connector.rb` | adapter/netconnector/session.go; adapter/netconnector/ap.go; internal/cli/runner.go | adapter/netconnector/native_test.go:TestNativeBorrowDoesNotConnectOrRunArbitraryCommands |
| `lib/wireless/cli/options.rb` | internal/bootstrap/options.go; internal/bootstrap/inventory.go; internal/bootstrap/inventory.go | internal/cli/options_test.go:TestLayeredConfigurationPathsAndSecrets; internal/cli/inventory_test.go:TestHTTPInventoryThroughNativePaginationAndControllerRules |
| `lib/wireless/cli/parse.rb` | internal/cli/parse.go; internal/cli/input.go | internal/cli/runner_test.go:TestParseOfflineAndNumericIntent; internal/cli/cancellation_review_test.go:TestReviewParseCancellationDuringBorrowedReadDiscardsFacts |
| `lib/wireless/cli/report.rb` | report/report.go | report/report_test.go:TestFinalizePhaseFailuresNeverLeaveCompleteSummary |
| `lib/wireless/cli/review.rb` | internal/cli/review.go | internal/cli/review_test.go:TestDefaultOfflineReviewAndNoOutputOnInvalidInput |
| `lib/wireless/cli/review/report.rb` | report/recommendations.go | report/recommendations_test.go:TestReviewRubyFullConfiguration |
| `lib/wireless/collector.rb` | collect/collect.go; collect/neighbors.go; capture.go | collector_test.go:TestCollectorSharedQueriesFirstFactsAndNoSession |
| `lib/wireless/config.rb` | parser/registry.go; vendors/h3c/configuration.go | parser_api_test.go:TestConfigViewsAndNeighborAPI |
| `lib/wireless/coverage.rb` | coverage.go | parser_api_test.go:TestCoverageAllReasonBranches |
| `lib/wireless/fields.rb` | parser/fields.go; parser/fields_integer.go | fields_test.go:TestRubyFieldCorpus; exact_numbers_test.go:TestExactControllerIntegers |
| `lib/wireless/h3c/authentication_fields.rb` | vendors/h3c/configuration_settings.go | core_test.go:TestRubyParserCorpus |
| `lib/wireless/h3c/command_output.rb` | parser/terminal.go; internal/parsetext/values.go | fields_test.go:TestAllTextNewlinePolicies |
| `lib/wireless/h3c/configuration_builder.rb` | vendors/h3c/configuration.go; vendors/h3c/configuration_settings.go | core_test.go:TestRubyParserCorpus |
| `lib/wireless/h3c/document.rb` | vendors/h3c/configuration_source.go | domain_boundaries_test.go:TestConfigurationSourceCapturesAreOwnedAndRedacted |
| `lib/wireless/h3c/parser.rb` | vendors/h3c/record.go; vendors/h3c/configuration.go | core_test.go:TestRubyParserCorpus |
| `lib/wireless/h3c/parsers/display_current_configuration.rb` | vendors/h3c/configuration.go | core_test.go:TestRubyParserCorpus |
| `lib/wireless/h3c/parsers/display_dot1x_connection.rb` | vendors/h3c/fields.go | core_test.go:TestRubyParserCorpus |
| `lib/wireless/h3c/parsers/display_lldp_neighbor_information_verbose.rb` | vendors/h3c/neighbors.go | core_test.go:TestRubyParserCorpus |
| `lib/wireless/h3c/parsers/display_mac_authentication_connection.rb` | vendors/h3c/fields.go | core_test.go:TestRubyParserCorpus |
| `lib/wireless/h3c/parsers/display_wlan_ap_all_verbose.rb` | vendors/h3c/access_points.go | core_test.go:TestRubyParserCorpus |
| `lib/wireless/h3c/parsers/display_wlan_ap_model_all.rb` | vendors/h3c/models.go | core_test.go:TestRubyParserCorpus |
| `lib/wireless/h3c/parsers/display_wlan_bss_all_verbose.rb` | vendors/h3c/fields.go | core_test.go:TestRubyParserCorpus |
| `lib/wireless/h3c/parsers/display_wlan_client_security_block_mac.rb` | vendors/h3c/blocked_clients.go | core_test.go:TestRubyParserCorpus |
| `lib/wireless/h3c/parsers/display_wlan_client_verbose.rb` | vendors/h3c/fields.go | core_test.go:TestRubyParserCorpus |
| `lib/wireless/h3c/parsers/display_wlan_statistics_client_ip_conflict.rb` | vendors/h3c/client_ip_conflicts.go | core_test.go:TestRubyParserCorpus |
| `lib/wireless/h3c/profile.rb` | parser/registry.go | parser_api_test.go:TestProfileQueriesAliasesAndCopies |
| `lib/wireless/h3c/record_parser.rb` | vendors/h3c/record.go | core_test.go:TestRubyParserCorpus |
| `lib/wireless/huawei/parser.rb` | vendors/huawei/access_points.go; vendors/huawei/clients.go | core_test.go:TestRubyParserCorpus |
| `lib/wireless/huawei/parsers/display_ap_all.rb` | vendors/huawei/access_points.go | core_test.go:TestRubyParserCorpus |
| `lib/wireless/huawei/parsers/display_station_all.rb` | vendors/huawei/clients.go | core_test.go:TestRubyParserCorpus |
| `lib/wireless/huawei/profile.rb` | parser/registry.go | parser_api_test.go:TestProfileQueriesAliasesAndCopies |
| `lib/wireless/neighbor_parser.rb` | parser/neighbors.go | parser_api_test.go:TestConfigViewsAndNeighborAPI |
| `lib/wireless/observation.rb` | observation.go | core_test.go:TestObservationOwnershipAndSafeFormatting |
| `lib/wireless/parser_registry.rb` | parser/registry.go | parser_api_test.go:TestProfileQueriesAliasesAndCopies |
| `lib/wireless/projection.rb` | projection.go | domain_boundaries_test.go:TestSnapshotProjectionAndCoverageOwnership |
| `lib/wireless/recommendations.rb` | review/recommendations.go | recommendations_test.go:TestRubyRecommendations |
| `lib/wireless/runtime_parser.rb` | parser/runtime.go | core_test.go:TestRuntimeInvalidRequestsAndIndependentViews |
| `lib/wireless/snapshot.rb` | snapshot.go | domain_boundaries_test.go:TestSnapshotProjectionAndCoverageOwnership |
| `lib/wireless/terminal.rb` | parser/terminal.go; internal/parsetext/values.go | fields_test.go:TestAllTextNewlinePolicies |
| `lib/wireless/version.rb` | internal/cli/runner.go | internal/cli/subprocess_test.go:TestSubprocessCLI |

## 全部 Ruby spec 文件

机器可校验版本为 [ruby-spec-matrix.json](ruby-spec-matrix.json)。它记录来源、字面块数、实现与现存测试名；交付脚本校验 34 行与测试定义。文件级映射不声称逐例一对一翻译，实际行为由 spec 来源 corpus 与显式边界测试共同验证。附加的 Go 测试覆盖取消输入、native 生命周期、发布故障和历史数值名称等修复。

| Ruby spec | 字面块 | Go 实现 | 具体测试 |
|---|---:|---|---|
| `spec/ap_radio_scopes_spec.rb` | 6 | vendors/h3c/access_points.go | core_test.go:TestRubyParserCorpus |
| `spec/ap_session_spec.rb` | 20 | adapter/netconnector/ap.go | adapter/netconnector/native_test.go:TestNativeAPDialoguePaginationAndExactReturn; adapter/netconnector/native_test.go:TestNativeAPCancellationRetiresInsideLease; adapter/netconnector/native_test.go:TestNativeAPCapturedFactsSurviveWrongExitAndCloseFailure |
| `spec/bin_script_spec.rb` | 3 | cmd/wireless/main.go; internal/bootstrap/options.go | internal/cli/subprocess_test.go:TestSubprocessCLI; internal/cli/options_test.go:TestInitializePrivateNoOverwriteAndInheritance |
| `spec/cli/apply_spec.rb` | 10 | internal/cli/apply.go; adapter/netconnector/apply.go | internal/cli/apply_test.go:TestApplyDurableIntentReadbackAndNoReplay; internal/cli/apply_test.go:TestApplyFailureBoundaries; internal/cli/apply_pty_test.go:TestApplyRealTTYConfirmationAndCancellation; internal/cli/apply_diagnostics_test.go:TestApplyRetainsIndependentSafeDiagnostics |
| `spec/cli/collect_spec.rb` | 24 | internal/cli/runner.go | internal/cli/runner_test.go:TestCollectBothPlatformsPrivateReportsAndReuse; internal/cli/runner_test.go:TestStoreFailureCancelsSiblingAndRetainsStartedEvidence; internal/cli/runner_test.go:TestCredentialAndResourcePreflightBeforeReports; internal/cli/runner_test.go:TestUserCancellationJoinsAndKeeps130OnSummaryFailure |
| `spec/cli/connector_spec.rb` | 2 | internal/cli/runner.go; adapter/netconnector/session.go | internal/cli/runner_test.go:TestCollectBothPlatformsPrivateReportsAndReuse; internal/cli/runner_test.go:TestExplicitLLDPUsesIndependentLazyAPCredentials; adapter/netconnector/native_test.go:TestNativeOpenCloseErrorKeepsControllerFacts |
| `spec/cli/inventory_spec.rb` | 6 | internal/bootstrap/inventory.go; internal/bootstrap/inventory.go | internal/cli/inventory_test.go:TestHTTPInventoryThroughNativePaginationAndControllerRules; internal/cli/runner_test.go:TestNativeInventoryRulesFilteringAndExternalSourceInjection; internal/cli/options_test.go:TestInvalidPublicConfiguration |
| `spec/cli/options_spec.rb` | 21 | internal/bootstrap/options.go | internal/cli/options_test.go:TestLayeredConfigurationPathsAndSecrets; internal/cli/options_test.go:TestSourceFieldsMatchRubyDotPathsAndEmptyOverlayKeepsOrigin; internal/cli/options_test.go:TestInitializePrivateNoOverwriteAndInheritance; internal/cli/options_test.go:TestInventoryEnvironmentMasking |
| `spec/cli/parse_spec.rb` | 4 | internal/cli/parse.go | internal/cli/runner_test.go:TestParseOfflineAndNumericIntent; internal/cli/cancellation_review_test.go:TestReviewParseCancellationDuringBorrowedReadDiscardsFacts |
| `spec/cli/report_spec.rb` | 12 | report/report.go | report/report_test.go:TestFinalizePhaseFailuresNeverLeaveCompleteSummary; report/report_test.go:TestPrivateRootAndUTCCollisionSuffix; report/report_test.go:TestCancellationAfterFinalRenameSurvivesFailedRecovery |
| `spec/cli/review_spec.rb` | 8 | internal/cli/review.go; report/recommendations.go | internal/cli/review_test.go:TestDefaultOfflineReviewAndNoOutputOnInvalidInput; report/recommendations_test.go:TestReviewRubyFullConfiguration; report/report_test.go:TestFinalizeReviewUsesSameRecoveryAndConfinesPath |
| `spec/cli_spec.rb` | 9 | internal/cli/runner.go; cmd/wireless/main.go | internal/cli/subprocess_test.go:TestSubprocessCLI; internal/cli/options_test.go:TestInformationalAndMissingConfigurationDoNotConsumeInput; internal/cli/options_test.go:TestInitializePrivateNoOverwriteAndInheritance |
| `spec/collector_spec.rb` | 28 | collect/collect.go; collect/neighbors.go; capture.go | collector_test.go:TestCollectorSharedQueriesFirstFactsAndNoSession; collector_test.go:TestCollectorLateAndConcurrentCallbacksNeverReplay; collector_test.go:TestCollectorAPLazyDependenciesSelectionAndBinding; collector_test.go:TestCollectorAPFailuresDoNotLoseFactsOrOtherAPs; collector_test.go:TestCollectorAPInvalidCapturesSetupAndMissingNames |
| `spec/compound_settings_spec.rb` | 3 | vendors/h3c/configuration_settings.go | core_test.go:TestRubyParserCorpus |
| `spec/config_spec.rb` | 4 | parser/registry.go; vendors/h3c/configuration.go | core_test.go:TestRubyParserCorpus; parser_api_test.go:TestConfigViewsAndNeighborAPI |
| `spec/configuration_integrity_spec.rb` | 12 | vendors/h3c/configuration.go; vendors/h3c/configuration_settings.go | core_test.go:TestRubyParserCorpus; domain_boundaries_test.go:TestConfigurationSourceCapturesAreOwnedAndRedacted |
| `spec/coverage_spec.rb` | 6 | coverage.go; projection.go | parser_api_test.go:TestCoverageAllReasonBranches; core_test.go:TestCoverageFreshness; parser_api_test.go:TestCoverageFalseTimestampFallsBackLikeRuby |
| `spec/evidence_contract_spec.rb` | 5 | parser/runtime.go; vendors/huawei/access_points.go; vendors/h3c/neighbors.go | core_test.go:TestRubyParserCorpus; parser_api_test.go:TestProfileQueriesAliasesAndCopies |
| `spec/fields_spec.rb` | 4 | parser/fields.go; parser/fields_integer.go | fields_test.go:TestRubyFieldCorpus; fields_test.go:TestIndependentFieldBoundaries; exact_numbers_test.go:TestExactControllerIntegers |
| `spec/h3c/configuration_and_lldp_spec.rb` | 7 | vendors/h3c/configuration.go; vendors/h3c/neighbors.go | core_test.go:TestRubyParserCorpus; parser_api_test.go:TestConfigViewsAndNeighborAPI |
| `spec/h3c/configuration_source_spec.rb` | 18 | vendors/h3c/configuration_source.go; vendors/h3c/configuration.go | core_test.go:TestRubyParserCorpus; parser_api_test.go:TestSourceLineAndObservationStates; domain_boundaries_test.go:TestConfigurationSourceCapturesAreOwnedAndRedacted |
| `spec/h3c/conflicts_and_models_spec.rb` | 8 | vendors/h3c/client_ip_conflicts.go; vendors/h3c/models.go | core_test.go:TestRubyParserCorpus |
| `spec/h3c/live_evidence_consistency_spec.rb` | 2 | vendors/h3c/client_ip_conflicts.go; vendors/h3c/fields.go | core_test.go:TestRubyParserCorpus |
| `spec/immutable_boundaries_spec.rb` | 9 | observation.go; snapshot.go; projection.go; coverage.go | core_test.go:TestObservationOwnershipAndSafeFormatting; domain_boundaries_test.go:TestSnapshotProjectionAndCoverageOwnership; core_review_test.go:TestReviewCoverageCompositeStatusesAreOwned |
| `spec/live_formats_spec.rb` | 6 | vendors/h3c/access_points.go; vendors/h3c/fields.go; vendors/h3c/configuration.go | core_test.go:TestRubyParserCorpus |
| `spec/loading_spec.rb` | 2 | go.mod; parser/runtime.go; parser/registry.go | parser_api_test.go:TestConfigViewsAndNeighborAPI; parser_api_test.go:TestParserConcurrentReuse |
| `spec/neighbor_parser_spec.rb` | 7 | parser/neighbors.go; vendors/h3c/neighbors.go | core_test.go:TestRubyParserCorpus; parser_api_test.go:TestConfigViewsAndNeighborAPI |
| `spec/parser_integrity_spec.rb` | 17 | vendors/h3c/record.go; vendors/h3c/configuration.go; vendors/h3c/neighbors.go; vendors/huawei/clients.go | core_test.go:TestRubyParserCorpus; domain_boundaries_test.go:TestIPBracketFamily; exact_numbers_test.go:TestExactControllerIntegers |
| `spec/platform_contract_spec.rb` | 5 | parser/registry.go; parser/runtime.go; collect/collect.go | parser_api_test.go:TestProfileQueriesAliasesAndCopies; core_test.go:TestRuntimeInvalidRequestsAndIndependentViews; collector_test.go:TestCollectorOrderingDependenciesUnsupportedAndParserCompleteness |
| `spec/recommendations_spec.rb` | 7 | review/recommendations.go; fingerprint.go; document.go; change/h3c.go | recommendations_test.go:TestRubyRecommendations; change_plan_test.go:TestTypedSecurityChange; report/recommendations_test.go:TestHistoricalExponentNameRemainsAdvisoryOnly |
| `spec/record_separator_spec.rb` | 2 | parser/terminal.go; internal/parsetext/values.go; vendors/h3c/configuration_source.go | fields_test.go:TestAllTextNewlinePolicies; core_test.go:TestRubyParserCorpus |
| `spec/replay_evidence_spec.rb` | 1 | parser/terminal.go; internal/parsetext/values.go; parser/runtime.go | core_test.go:TestRubyParserCorpus |
| `spec/runtime_parser_spec.rb` | 14 | parser/runtime.go; vendors/h3c/access_points.go; vendors/h3c/fields.go; vendors/huawei/access_points.go | core_test.go:TestRubyParserCorpus; core_review_test.go:TestReviewLargePositiveRSSIMatchesRubyFloatRound |
| `spec/terminal_spec.rb` | 2 | parser/terminal.go; internal/parsetext/values.go | core_test.go:TestRubyParserCorpus; fields_test.go:TestAllTextNewlinePolicies |

以上迁移范围未发现被隐藏为“限制”的缺失公共功能。验证范围仍有限：HTTP 是本地 fixture，PostgreSQL 只验证配置/凭据边界；native 是 net.Pipe 字节流，apply 的终端是真实本地 PTY、设备执行为注入实现。它们不能证明特定固件或生产链路行为。

## v0.4.0 依赖升级

共享 `net-connector.yml` 中的 `tftp.h3c_source_file`、`tftp.h3c_wireless_source_file`、`tftp.huawei_source_file` 分别改为 `tftp.source_files.h3c`、`tftp.source_files.h3c_wireless`、`tftp.source_files.huawei`。旧键在设备连接前报错。清单缓存仍使用 `prefer` / `refresh` / `only`。
