# Go Wireless

独立的 Go 无线控制器证据采集、离线解析、报告复核与受控变更库/CLI。移植基线为 Wireless Ruby `b328e1f`（2026-10-26）；运行和普通测试不需要 Ruby、Rails 或数据库。证据完整不代表网络健康。

需要 Go **1.26.0+**；真实连接使用主机的 `ssh`/`telnet`。依赖固定为正式模块 go-net-connector **v0.3.0**、go-expect **v0.4.0**。当前版本 **v0.1.0**；Python 3 仅用于源码交付与发布验证。

## 安装

从 [GitHub Releases](https://github.com/gatework/go-wireless/releases/tag/v0.1.0) 下载对应 Linux/macOS、amd64/arm64 归档，并按 SHA256SUMS 校验。解压后运行 `bin/wireless --version`，可查看版本与源提交。macOS 发行二进制要求 macOS 13+。

也可使用 Go 安装 CLI 或在项目中添加库依赖：

```sh
GOWORK=off go install github.com/gatework/go-wireless/cmd/wireless@v0.1.0
go get github.com/gatework/go-wireless@v0.1.0
```

## 构建与安全预览

```sh
export GOWORK=off
go build -o bin/wireless ./cmd/wireless
bin/wireless --help
bin/wireless collect --config examples/wireless.yml --show-config
bin/wireless collect --config examples/wireless.yml --plan --json
bin/wireless parse --file examples/controller.cfg --complete --json
cat examples/controller.cfg | bin/wireless parse --complete --json
bin/wireless review --help
bin/wireless apply --help
```

构建与验证统一设置 `GOWORK=off`，使用 `go.mod/go.sum`，不含 `replace`。历史 `dependencies/` 和重建脚本仅保留为审计材料，不再参与默认构建。已有本地 `go.work` 不会被删除，请沿用上面的环境设置。

`--show-config`、`--plan` 和各级帮助不会连接设备。示例使用文档地址和虚构凭据名称；`examples/controller.cfg` 是合成的离线语法样本。不要把它下发到控制器。

## 配置与采集

发现目录依次为 `/etc/net-connector`、`/usr/local/etc/net-connector`、`$XDG_CONFIG_HOME/net-connector`（回退 `$HOME/.config/net-connector`）、可执行文件目录、启动目录。各目录读取 `.env`、平铺的 `net-connector.yml`、`wireless.yml`；显式 `--env-file`/`--config` 最后叠加。有效优先级为 **CLI > 进程环境 > YAML > .env > 默认值**。配置文件内路径相对该文件，CLI/进程路径相对启动目录。

共享文件采用连接器 schema；无线目标、并发、目录、SSH 端口和超时放在 `wireless.yml`。旧 `common`/`wireless` 容器会返回迁移错误。共享 SSH 仅继承 `host_key_policy`/`known_hosts`，其协议、厂商协议、日志字段不控制无线采集。

`bin/wireless collect --init` 只在用户配置目录创建缺失的私有模板，保留已有文件。未知键、重复地址、无效维度、公开 YAML 中的密码均在会话前拒绝。完整 schema、环境键和输入取消契约见 [CLI API](docs/cli-api.md)。

静态清单见 `examples/wireless.yml`；动态清单见 `examples/netdisco.yml`，支持 Netdisco HTTP 分页、只读 PostgreSQL、筛选规则及校验缓存。静态 devices、内联动态清单、外部 `netdisco_config` 互斥。`devices: []` 是显式零目标；没有目标退出 1。CIDR 只筛选现有地址，不扫描网段。`--plan` 可预览动态清单，但可能读取所配置的库存服务。

凭据放在私有 0600 `.env` 或进程环境；示例见 `examples/credentials.env.example`。控制器优先使用 `NC_H3C_WIRELESS_USERNAME/PASSWORD` 或 `NC_HUAWEI_USERNAME/PASSWORD`，再回退 `NC_DEVICE_USERNAME/PASSWORD`；另支持 `NC_ENABLE_PASSWORD`。用户名必填，密码可省略以使用 SSH 密钥/agent。SSH 默认严格主机校验。

```sh
# 核对清单、凭据和 known_hosts 后才执行；此命令会连接配置的设备。
bin/wireless collect --config /path/to/private/wireless.yml --json
# 离线复核已有批次；--scripts 显式生成注释草案和合格的类型化计划。
bin/wireless review --batch /path/to/batch --scripts --output /path/to/private/reviews
```

默认并行 4（1..500），同一控制器查询顺序执行，共享命令只执行一次。LLDP 默认不采集；显式选择 `lldp` 时才按 AP 清单建立 H3C 嵌套会话，独立、延迟读取 `WIRELESS_AP_USERNAME/PASSWORD`。未选择 AP 或 AP 清单为空不解析该凭据。

H3C 支持 AP、radio、BSS、client、configuration、AP group、service template、802.1X/MAB connection、blocked client、IP conflict、AP model、AP LLDP；Huawei 支持 AP 与 client。未支持的维度返回 `unsupported`，不会伪造空的完整记录。截断、终端损坏、计数冲突、未知语法和不完整端口证据保留 partial/failed 与诊断。

## 报告与受控应用

新批次和设备目录 0700、文件 0600。设备 JSON 为 `device/status/dimensions/duration_ms/snapshot`（失败时 snapshot 可为 null），snapshot 为 `target/dimensions/observed_at`；observation 为 `name/status/records/issues/evidence/started_at/completed_at`。批次 `summary.json` 保留 total/completed/pending/counts/devices/report_saved 与持久化错误；原始输出/凭据不进入显示或报告。事实 JSON 的浮点类型、负零与大整数保持精度；读取通用 map 时使用 `Decoder.UseNumber`。

报告自动带诊断；`--scripts` 仅为合格 H3C 安全候选生成 schema 1 计划，支持移除 TKIP/WPA 且保留已有 CCMP/RSN，保留模板原启停状态。计划绑定原始报告 SHA256 与完整配置指纹。草案全为注释，任何建议都不是自动执行指令。

```sh
# 需要真实终端的 stdin/stdout/stderr；会重新读配置并可能写入设备。
bin/wireless apply --plan /path/to/change.plan.json --directory /path/to/private/executions
```

必须逐字输入 `APPLY <规范IP> <计划文件SHA256前12位>`；无免确认参数，管道输入被拒绝。确认后才加载凭据，主机信任始终 strict。执行前核对完整配置指纹与类型化动作，重新渲染并比对命令，不执行随意编辑的文本。

每条命令先持久化 intent，再发送，随后持久化 acknowledgement；最后重新读取完整配置。只有读回符合预期且最终文件持久化成功，才记录 `configuration_verified`。一旦 intent 已提交，任何后续不确定性都保留 `uncertain`；不会重试、重连、自动回滚或保存配置。失败同时保留 operation/cancellation/cleanup/report 诊断；若失败记录本身发布失败，新诊断只在 stderr 披露，不能当作已持久化证据。最终发布失败保留独立 checkpoint；人工核对当前设备和日志后再制定下一步，不能直接重放旧计划。

退出码：0 为证据完整/预览/帮助成功；1 为 partial/failed、零目标或错误确认；2 为无效输入、会话/持久化失败等；130 为取消。`complete` 与 `configuration_verified` 都不等于业务验收。报告与 apply 全契约见 [变更 API](docs/change-api.md)。

## 作为库使用

```sh
GOWORK=off go run ./examples/library
```

```go
parser, err := wireless.NewConfig("h3c", "")
if err != nil { return err }
observation, err := parser.Parse(text, true)
if err != nil { return err }
body, err := wireless.MarshalJSONIndent(observation, "", "  ")
```

导入路径 `github.com/gatework/go-wireless`，包名 `wireless`。核心包不导入 native connector，适合离线解析；`adapter` 和完整 CLI 使用正式 native 模块。`scripts/check-delivery.py` 用临时文件模块代理验证当前源码的独立 consumer，完整依赖通过正常版本解析，无 workspace 或本地 replace；正式版本另按 [发布流程](docs/releasing.md) 验证 tag、同提交 CI、下载资产及公开模块消费。借用会话、时钟、库存来源和报告写入均可注入，见 [核心 API](docs/core-api.md)、[采集/传输 API](docs/transport-api.md)。

## 验证与边界

```sh
GOWORK=off go test ./...
GOWORK=off go test -race ./...
GOWORK=off go vet ./...
python3 -O scripts/test_check_delivery.py
python3 scripts/check-delivery.py
# CI 同时执行只读模块、格式和完整 race 检查
bash scripts/ci
```

[兼容矩阵](docs/COMPATIBILITY.md) 覆盖 34 个 Ruby spec 文件和全部 lib 文件；[本次验证记录](docs/2026-11-07-modernization.md) 与 [历史验证记录](docs/VALIDATION.md) 分别列出本次迁移和冻结基线的证据。测试只使用合成/已冻结离线证据、注入会话、本地 HTTP、内存字节传输和真实本地 PTY；未验证真实设备、外部 Netdisco/PostgreSQL、生产部署。显式 Ruby oracle 只用于开发对照，普通 Go 测试和运行不调用 Ruby。

许可沿用参考项目 [MIT](LICENSE)，原署名完整保留；冻结依赖各自的 LICENSE 随归档保留，fpconv 的 Boost Software License 在源码中保留。
