# 领域词汇

| 名称 | 含义与约束 | 所在 module |
|---|---|---|
| 借用操作（Borrowed operation） | 在已经连接的设备上持有一段顺序租约；使用借入 context，不隐式连接、重连或重放。 | connector；adapter/netconnector |
| 命令尝试（Attempt） | 已尝试发送的命令及收到的原始字节。提示到达、设备接受与持久化是不同事实；失败尝试仍是证据。 | connector；collect.Receipt |
| 响应正文（Body） | 编码和完整性验证后的文本，仅移除已确认提示及第一行匹配回显；失败响应没有可用正文。 | adapter/netconnector；collect |
| 采集（Capture） | 一条查询的事实、时间、原始字节摘要和安全诊断。没有发送尝试时不生成字节证据。 | wireless core |
| 采集范围（CollectionScope） | Profile 一次决定的默认维度、依赖维度、去重查询和 AP 邻居采集范围；读取返回独立值。 | parser |
| 采集批次执行（Collection execution） | 一批只读设备采集的结果、取消、报告最终发布与输出失败恢复；恢复不重放采集。 | internal/workflow/collect |
| 运行配置快照（Runtime options） | 配置优先级及校验完成后独立持有的 typed 值；公开配置视图的后续修改不改变执行。 | internal/bootstrap |
| 已验证计划（ChangePlan） | 一次验证后深度持有的 schema 1 变更值；绑定设备、完整配置指纹及重新渲染的命令。读取不能修改计划。 | change |
| 变更执行（Apply execution） | 一次已确认计划的预检、intent、发送、acknowledgement、读回、清理和最终发布。 | internal/workflow/apply |
| 持久意图（Intent） | 发送前确认写入执行日志的命令索引；一旦提交，后续失败按可能已发送处理。 | internal/workflow/apply；report |
| 不确定结果（Uncertain） | intent 提交后，命令、取消、清理或日志发布任一环节无法确认。禁止自动重试、回滚、保存。 | internal/workflow/apply；report |
| 配置已验证（configuration_verified） | 读回符合预期、会话清理成功且最终日志持久化完成；不代表业务验收或启动配置已保存。 | internal/workflow/apply；report |

interface 使用 consumer 所需的能力表达；Opener 与 Journal 是变更执行的两个 seam。TTY 与格式化属于 internal/cli；凭据发现与依赖装配属于 internal/bootstrap。真实设备 adapter 和测试会话共用同一执行 interface，故障测试直接覆盖状态变化与所有权。
