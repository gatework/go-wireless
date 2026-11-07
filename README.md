# Go Wireless

独立的 Go 无线控制器证据采集、离线解析、报告复核与受控变更库/CLI。移植基线为 Wireless Ruby `b328e1f`（2026-10-26）；运行和普通测试不需要 Ruby、Rails 或数据库。证据完整不代表网络健康。

需要 Go **1.26.0+**、Python 3、`patch`；真实连接使用主机的 `ssh`/`telnet`。当前源码尚未提交或公开发布，两个 Gatework 依赖使用冻结源码及 `v0.0.0` 占位版本，不能宣称可通过公开 `go install` 安装。

## 构建与安全预览

```sh
python3 scripts/prepare-workspace.py
go build -o bin/wireless ./cmd/wireless
bin/wireless --help
bin/wireless collect --config examples/wireless.yml --show-config
bin/wireless collect --config examples/wireless.yml --plan --json
bin/wireless parse --file examples/controller.cfg --complete --json
cat examples/controller.cfg | bin/wireless parse --complete --json
bin/wireless review --help
bin/wireless apply --help
```

Bootstrap 校验归档、原始/补丁后每个文件、补丁 SHA256 与路径白名单，再原子发布相对路径 `go.work`。不读取兄弟项目。冻结源只包含构建所需公共源码、嵌入模板和许可证；第三方模块仍需 Go 模块缓存或正常下载，不能称为完全离线构建。详情见 [依赖来源](dependencies/README.md)。

`--show-config`、`--plan` 和各级帮助不会连接设备。示例使用文档地址和虚构凭据名称；`examples/controller.cfg` 是合成的离线语法样本。不要把它下发到控制器。

## 配置与采集

发现目录依次为 `/etc/net-connector`、`/usr/local/etc/net-connector`、`$XDG_CONFIG_HOME/net-connector`（回退 `$HOME/.config/net-connector`）、可执行文件目录、启动目录。各目录读取 `.env`、`net-connector.yml` 的 common/wireless、`wireless.yml`；显式 `--env-file`/`--config` 最后叠加。有效优先级为 **CLI > 进程环境 > YAML > .env > 默认值**。配置文件内路径相对该文件，CLI/进程路径相对启动目录。

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
go run ./examples/library
```

```go
parser, err := wireless.NewConfig("h3c", "")
if err != nil { return err }
observation, err := parser.Parse(text, true)
if err != nil { return err }
body, err := wireless.MarshalJSONIndent(observation, "", "  ")
```

导入路径 `github.com/gatework/go-wireless`，包名 `wireless`。核心包不导入 native connector，适合离线解析；`adapter` 和完整 CLI 才需要冻结 native 依赖。独立 consumer 需在自己的 workspace 使用本源码和冻结依赖（模板见 `scripts/check-delivery.py`），当前不能把占位版本当作公开模块。借用会话、时钟、库存来源和报告写入均可注入，见 [核心 API](docs/core-api.md)、[采集/传输 API](docs/transport-api.md)。

## 验证与边界

```sh
go test ./...
go test -race ./...
go vet ./...
python3 scripts/test_prepare_workspace.py
python3 scripts/check-delivery.py
```

[兼容矩阵](docs/COMPATIBILITY.md) 覆盖 34 个 Ruby spec 文件和全部 lib 文件；[验证记录](docs/VALIDATION.md) 列出当前树的命令、日志、数据规模与平台结果。测试只使用合成/已冻结离线证据、注入会话、本地 HTTP、内存字节传输和真实本地 PTY；未验证真实设备、外部 Netdisco/PostgreSQL、生产部署。显式 Ruby oracle 只用于开发对照，普通 Go 测试和运行不调用 Ruby。

许可沿用参考项目 [MIT](LICENSE)，原署名完整保留；冻结依赖各自的 LICENSE 随归档保留，fpconv 的 Boost Software License 在源码中保留。
