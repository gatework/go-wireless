# 可复现性能基准

本轮测量使用 Go 1.27.3、macOS arm64、Apple M5，固定 `-cpu=1`、
`-count=6`、`-benchtime=300ms`。前后采用相同工具链、输入和基准命令，
其他模块的 Go 编译/测试/基准在正式采样期间暂停。

```sh
GOWORK=off GOFLAGS=-mod=readonly GOTOOLCHAIN=go1.27.3 \
  go test -run '^$' \
  -bench 'Benchmark(ObservationProjection|EvidenceClone|TemplateBindings|TemplateReferences)$' \
  -benchmem -cpu=1 -count=6 -benchtime=300ms . ./review
benchstat before.txt after.txt
```

本版本已使用正式依赖，复测使用 GOWORK=off。原始 before/after 在同一个任务
workspace 中测量；这里只比较业务计算，不代表设备交互吞吐量或生产性能。

固定数据规模为 64、512、4096 个 AP：证据包含精确大整数、浮点测量、radio
子记录、显式 null 和空数组；引用关联包含两个 radio、分组、重复模板引用、禁用
模板和缺失模板。构造输入不计入 `b.Loop`，所有子基准报告分配量。

分配 profile 显示，证据复制约 70.6% 的对象来自反射 map key/value；因此为
`Record` 使用原生遍历和已知容量，命名 map 保留原反射路径，循环检测、数值验证
和字段类型仍共用同一逻辑。引用遍历约 79.5% 的对象在递归收集函数中分配；改为
共用一个有序目标切片，消除每层的中间切片及拷贝。

| 操作，4096 AP | 修改前 | 修改后 | 对象分配变化 |
| --- | ---: | ---: | ---: |
| 证据 CloneRecord | 8.403 ms/op | 5.583 ms/op | 176,158 → 49,163，约 -72.1% |
| Observation.ToMap | 8.747 ms/op | 5.799 ms/op | 176,150 → 49,167，约 -72.1% |
| AP/模板绑定关联 | 4.546 ms/op | 4.324 ms/op | 104,143 → 83,464，约 -19.9% |

这些耗时差异的 benchstat 检验为 `p=0.002, n=6`。证据复制/投影在三个规模的
耗时下降约 32–36%，字节分配下降约 34–35%。纯引用收集在三个规模的对象分配
下降约 25%；512 和 4096 AP 的耗时差异未达到显著性，不据此声称耗时改善。

原始 before/最终 after、benchstat、两个 CPU/内存 profile 及分配摘要保存在仓库外
任务根 `wireless-bench-*.txt`、`wireless-*-{cpu,mem}.pprof`。一次与其他模块短测试
重叠的中间 after 已标记 discarded，并完整重跑最终 after；它不参与表格统计。
不向源码仓库提交原始日志或 profile。正式变更继续由数值、nil/empty、所有权、
Ruby 金样和指纹回归检查约束，而不以基准阈值代替正确性。
