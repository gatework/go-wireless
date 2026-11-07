# 原生连接器候选迁移边界

本轮仅更新源码适配入口，未修改模块版本、发布依赖或设备配置。

已迁移提示匹配到不可变 `expect.Pattern`，固定身份使用字节 `expect.Line`；协议身份使用 `RawPrompt`，文本通过 `Response.Output/Rendered/Prompt` 的错误返回验证。命令正文由已经渲染的响应去除已确认提示及第一行命令回显，正文内同名命令保留。调用结果使用 `JoinedOutput`，交互回复使用 `Reply`。原负例身份测试改经 expect 会话执行匹配，恢复行为测试未删除。

当前候选连接器不提供既有借用会话契约，因此 adapter 测试编译仍被以下能力阻塞：

- `ExecuteBorrowedCommand` / `Borrowed`：借用期间不得隐式连接或重连。
- `PreserveSessionOnRejection`：AP/控制器收到完整、已同步的设备拒绝后，仍需恢复或退出 AP 会话。候选 `ExecuteCommand` 在任意错误后关闭连接，适配层无法补救。
- `WithOperationCleanup`：业务错误、退出失败、底层关闭失败必须分别保留。
- 作用域内 `Operation.Retire` / `Connected`：失去同步时只关闭、不发送 quit；失效操作不得重新连接；日志观察者重入不得越过顺序门禁。
- 测试依赖旧 `Event` / `OnEvent` 观察接口，需迁移到当前日志观察接口，同时保留重入行为断言。

`CapturePartialOutput` 无需新增能力：当前 `Attempt.Response` 已保留失败原始证据；最终迁移应遍历失败 Attempt，不能将不完整字节当作已验证文本。

最小核心设计候选：显式选择执行策略，仅对“完整响应、已确认目标提示、纯设备拒绝、上下文仍有效”的错误允许保留连接；编码、回调、取消、传输、未知提示错误仍退休。策略必须明确禁止隐式重连。另提供作用域校验的主动退休及可区分的关闭错误证据。正式设计和测试通过前，保留本文件列出的编译阻塞，不用别名或删除恢复测试来掩盖差异。

本机候选源码工作区执行 `go test ./adapter` 失败于上述旧借用 API 和 Event 类型，不能据此声称无线适配器验证通过。
