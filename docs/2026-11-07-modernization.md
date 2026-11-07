# 2026-11-07 正式依赖与 Go 结构改进

本次从未提交的无线源码保存独立基线，在隔离 worktree 实施。原有冻结依赖、Ruby oracle 和历史验证记录保留；默认构建改为 `GOWORK=off`，不使用本地 `replace` 或重建 workspace。Wireless 自身未发布。

## 实现与迁移

- go-net-connector v0.3.0 / go-expect v0.4.0：显式借用操作、首次 cleanup error、失败 Attempt 与 accepted Steps 分离。Device.Close 是终止操作，可在观察回调中取消活动工作。
- `ReceiptOptions.Output` / `Receipt.Output()` 改为 `Raw` / `Raw()`；注入的实际发送结果必须设置 `Attempted: true`。正文使用已验证文本，原始证据对所有 Attempt 字节按接收顺序计算摘要，未尝试不生成证据。
- `NewChangePlan` 取代可变 map 的“先验证再使用”。输入、动作及访问器深拷贝，零值不可执行；schema 采用精确十进制比较，保留 Ruby wire JSON 和完整配置指纹。
- `internal/apply.Run` 持有已确认变更的状态与 journal；CLI 保留真实 TTY、原文件 SHA 确认、确认后凭据加载和输出。ApplyFactory 回调增加 context，工作使用借入 context。所有操作、取消、清理和报告诊断独立保留。
- `Dependencies.Registry` 统一厂商查找与 fleet 匹配。文件描述符预算属于 CLI 私有实现，按实际并发目标计算。
- `net-connector.yml` 使用平铺共享 schema；`wireless.yml` 持有无线目标、目录、并行及 SSH 端口/超时。旧 common/wireless 容器明确拒绝。共享 SSH 只投影 trust 设置，不借用协议或日志策略；默认严格主机校验。
- HTTP 缓存按非秘密账号/namespace 和查询隔离。启用缓存的 API-key 认证必须给出 ND_USERNAME 或 cache_namespace；旧 key-hash 缓存需要首次刷新，后续同账号密钥轮换可复用缓存。
- 在适用位置采用 slices.Contains、errors.AsType、maps.Copy、slices.SortStableFunc；保留 nil/empty、深拷贝与稳定排序约束，最低 Go 版本仍为 1.26.0。

## 交付检查

`scripts/check-delivery.py` 校验历史哈希与 Ruby 映射，导出白名单源码，然后禁用 workspace 构建 CLI。独立 consumer 从临时文件模块代理取得未发布 Wireless，使用全新 module cache 与版本化依赖，导入 core/adapter/cli，不添加 replace。该步骤仅证明独立消费，不宣称 Wireless 已公开发布。

交付断言使用显式异常；`scripts/test_check_delivery.py` 在 Python 普通及 `-O` 模式验证篡改、replace、占位版本、秘密文件与符号链接的拒绝。

`.github/workflows/go.yml` 配置 Linux/macOS × Go 1.26/1.27 的完整 race，以及固定版本 lint、govulncheck、actionlint。该文件存在并不表示无线远端 CI 已运行。

## 验证记录

最终 Go 实现使用正式 v0.3.0 通过以下本机检查（darwin/arm64）：

| 检查 | 结果 |
|---|---|
| Go 1.27.3 `bash scripts/ci` | 通过；格式、只读模块、verify/tidy、完整 uncached shuffled race、vet、交付负例与独立 consumer |
| 同步回原目录后 Go 1.26.0 `go test -mod=readonly -count=1 ./...` | 全包通过 |
| 最终源码 Linux/macOS × amd64/arm64，Go 1.26.0 / CGO=0 | 四种构建通过 |
| golangci-lint v2.15.0 | 0 issues |
| govulncheck v1.9.0 / Go 1.27.3 | No vulnerabilities found |
| actionlint v1.7.12 | 通过两个仓库 workflow |
| Python 普通 / `-O` 交付负例 | 两组各 3 项通过 |
| 字节证据与所有权回归 | GB 解码/原始哈希、无 Attempt、busy 不关闭别人的租约、迟到取消、首次 cleanup、AP 精确退出均通过 |
| 本地真实 PTY / CLI SIGINT | 通过；启动用实际输入读取同步，子进程及清理均有界 |
| Ruby 映射和冻结 oracle | 校验和保持一致；当前全套 Go corpus 测试通过；未重跑外部 Ruby checkout |

完整 CI 本机日志保留于 `.work/modernization-20261107-063717/wireless-final-ci.log`，不进入源码包。独立 consumer 使用全新 module cache 从正常模块源下载 connector v0.3.0；Wireless 临时本地版本仅用于测试，不是公开发行。

连接器已发布 [v0.3.0](https://github.com/gatework/go-net-connector/releases/tag/v0.3.0)。main 与 annotated tag 的目标均为 `37aa1cb66b6231eedd0788cc23daa6cd8a608535`。其 [CI run 42384043185](https://github.com/gatework/go-net-connector/actions/runs/42384043185) 的 Linux/macOS × Go 1.26/1.27、quality、package 全部成功。

发布直接使用该 run attempt 1 的不可变 artifact `13578733352`，原始 ZIP SHA256 为 `6a8f5e3717d8f7f1b485e16ca90a64d36529892ed0057065c60550eabb519c19`，与 GitHub digest 一致。八个资产在发布前及重新下载后均通过 `scripts/release.py verify`；本机运行 Darwin arm64 两个正式二进制并通过 installed-worker race 测试。仓库可见性仍为 public。

正式 Go 模块来源再次核对为同一 tag/SHA，内容摘要为 `h1:zZLdQlk5upuY8z5SYONoYypkY4pqtOXXTiuRH0Yul5s=`。无线仓库仅新增 CI 配置，没有把该配置当作已经运行的无线远端 CI。

离线 oracle、注入会话、本地 HTTP、net.Pipe 和本地 PTY 证据不替代真实控制器、外部 Netdisco/PostgreSQL 或生产部署验收。此轮没有设备写入或 Wireless 发布。
