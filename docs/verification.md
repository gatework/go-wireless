# 验证说明

## v0.3.0 依赖与验证边界

本版本固定正式 Connector v0.5.0、Expect v0.4.2，使用 `Result.SingleAccepted`、
`StepResult.Body`、`config.ReadFile` 和 `config.LoadContext`。源码的完整检查使用
`GOWORK=off` 与只读模块解析，无本地替换。

此前任务 workspace 和冻结本地 module proxy 的结果属于候选验证。更新正式依赖后
须重新执行完整门禁、独立消费者，并在发布时复用同提交成功 CI 的原制品，读回
发行附件与正式标签消费者；不能把候选测试算作已发布证明。

已增加 native controller/AP 自定义查询与声明所有权回归、配置 context/预算边界、
apply 日志投影所有权与提交能力回归，以及架构门禁的禁止/允许边界负例。
性能方法与可复现结论见 [benchmarks](benchmarks.md)，原始日志放在仓库外任务目录。

## 当前检查入口

使用 Go 1.26 或更新版本，依赖由 go.mod/go.sum 固定。完整本地门禁与 GitHub Actions 共用：

```sh
GOWORK=off bash scripts/ci
```

该入口检查格式、架构导入方向、正式依赖与 checksum、tidy 差异、全包 race/shuffle、vet、Python 普通及 -O 架构/交付控制测试、源码导出和独立消费者。`scripts/check-delivery.py` 使用临时文件模块代理提供当前 Wireless 源码，消费者禁用 workspace、没有 replace、使用全新 module cache；Connector 与 Expect 从正式模块版本取得。该检查证明候选源码可独立消费，正式标签仍按 [发布流程](releasing.md) 验证。

`go test ./...`、`go vet ./...` 和 `python3 scripts/check-architecture.py` 可单独执行。CI 的 quality 作业另外运行配置中的 golangci-lint、govulncheck、actionlint 与 `scripts/test_release.py`。普通构建不需要恢复历史 workspace。

## 保留的回归依据

- `testdata/` 提供解析、来源、字段、精确数值、建议及变更计划的合成/冻结金样，供当前 Go 测试读取。
- `docs/ruby-baseline.json`、`ruby-spec-matrix.json`、`ruby-lib-matrix.json` 保留参考 Ruby 源码身份、34 个 spec 与全部 lib 的实现/测试映射。`change-oracle.json` 固定变更 fixture 的校验和。
- `scripts/export-ruby-*.rb` 与 `check-ruby-change-interop.rb` 是显式开发对照工具，普通 Go 测试和运行不调用 Ruby。原始金样及来源哈希不因目录清理而重生。
- `scripts/test_check_delivery.py` 验证证据篡改、依赖替换、占位版本、秘密文件和符号链接拒绝。源码导出仅包含当前源码、文档、测试与工具，不携带依赖归档或恢复脚本。
- 项目 MIT LICENSE 和发行包第三方许可保留。

兼容范围、非逐字等价项与测试映射见 [兼容矩阵](compatibility.md)。旧依赖快照、实施草案和过程日志由 Git 历史或仓库外的验证记录保存，不参与当前源码交付。

## v0.2.0 已发布版本

[v0.2.0](https://github.com/gatework/go-wireless/releases/tag/v0.2.0) 对应提交 `a0e06f5a74f9779008903ec78cabb1fa8822d5f0`，[同提交 CI](https://github.com/gatework/go-wireless/actions/runs/42433909582) 完成 Linux/macOS × Go 1.26/1.27 测试、quality 和 package。

发布复用该 CI 制品，八个下载附件与原制品一致；全新缓存、GOWORK=off、无 replace 的正式模块消费者及 `go install` 通过。实际下载的 macOS arm64 发行程序完成版本、帮助和离线解析检查。首次候选缺少 fpconv 许可头，被打包门禁拒绝；已完整恢复后以该修正提交通过完整 CI，失败候选未用于发行。

这些结果只对应已发布的提交。后续工作区清理需要重新执行本节列出的本地检查；不修改既有标签或附件。

## 实测边界

本地 HTTP、注入会话、net.Pipe 与真实本地 PTY 用于边界测试。没有以这些结果宣称真实控制器、外部 Netdisco/PostgreSQL、生产配置写入或生产部署验收。设备命令和 apply 行为仍需在明确授权的真实环境中验证。
