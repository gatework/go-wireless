# 2026-11-07 Wireless 模块组织迁移验证

本文记录组织重构阶段的验收，后续发布依赖采纳见文末。该阶段在已授权的四模块隔离工作区完成，原有未提交工作保留。领域代码迁入真实职责包，公开 CLI 迁为内部应用入口；详细接口迁移见 [模块职责](module-deepening.md)。该阶段没有提交、推送或发布。

| 检查 | 结果 |
| --- | --- |
| Go 1.26.0 全包测试、vet | 通过 |
| Go 1.27.3 全包测试、shuffled race | 通过 |
| 配置的 golangci-lint、goimports、git diff --check | 通过，lint 0 issues |
| 架构导入检查 | 23 个包通过；已接入 scripts/ci |
| 模块 tidy -diff、checksum verify | 通过；仍固定 Connector v0.3.0 / Expect v0.4.0 |
| Ruby 源码映射和 oracle fixture 校验 | 通过；仅迁移实现/测试路径，语料和 oracle 哈希未变 |
| 交付脚本普通 / Python -O 控制测试 | 两组各 3 项通过 |
| 发布依赖独立 consumer | clean export、模块 verify/tidy 通过；构建受 Connector 尚未发布的新 API 阻挡 |

重点回归覆盖已注册 ProfileSpec 外部变更不改变解析、注入的零 Profile 在打开会话前拒绝、AP LLDP 使用所选自定义 Profile，以及 opener 使用 detached context 时父取消停止后续写入和读回。已确认命令的 acknowledgement 仍保存；不重放以修复日志。

独立 consumer 保持 GOWORK=off、无 replace、版本化依赖和全新 module cache。当时阻挡为已固定 Connector v0.3.0 不包含 FetchOptions / FetchHTTP / FetchPostgres / InventoryEnvironment / ValidateInventory。脚本导出新领域包，consumer 导入公开 SDK 与 adapter/netconnector，并在检出目录外运行 CLI；不允许外部导入 internal/cli。发布门槛未放宽。

本轮日志位于工作区 sibling `organization-20261107-155459/` 的 `wireless-*.log`。父级统筹验证还覆盖四模块消费关系和重构前后离线 CLI 行为。离线、注入会话、本地 HTTP/PTY 检查不代表真实控制器、外部库存服务或生产验收。

## v0.2.0 发布依赖采纳

用户在上述组织验收后明确授权发布。Connector v0.4.0 和 Expect v0.4.1 已通过各自同提交 CI、GitHub 发布附件核验及全新缓存的正式消费者；本模块已固定这两个版本，先前共享 API 缺失的阻挡已解除。

更新后通过完整 `scripts/ci`：GOWORK=off、无 replace、架构与模块检查、全包 race/shuffle、vet、普通及 Python -O 交付控制测试、源码导出、未变化的 Ruby oracle 哈希和独立本地代理消费者。该消费者的 Connector/Expect 均来自正式发布版本，Wireless 自身仍是源码候选；正式 v0.2.0 发布后另外执行全新缓存的标签消费者。配置的 golangci-lint 为 0 issues，govulncheck 未发现漏洞。

本次版本与迁移说明见 [v0.2.0](releases/v0.2.0.md)。发布须复用本提交成功 CI 的不可变制品，并验证上传、下载附件字节；此准备记录不预先声明自身已发布。完整日志与后续发布账本位于 sibling `release-20261107-164506/`。本次未连接生产控制器、真实清单或执行部署。

首次远程打包在许可完整性门禁拒绝了候选：整理源码时 `fpconv.go` 的既有 Boost 许可和来源注释丢失。已从 v0.1.0 精确恢复完整文件头，确认其余实现字节不变；保留发布校验，不绕过缺失声明。修正使用独立后续提交并重新执行该提交的完整 CI，之前失败的候选不会用于发行。
