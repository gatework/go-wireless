# 发布 Go Wireless

发布版本由根目录 VERSION 指定，并与 internal/cli.Version 保持一致。构建统一使用 GOWORK=off、只读模块解析及正式依赖；不得添加本地 replace。

1. 提交并推送 main，等待同一完整 SHA 的 Go workflow 成功。四组 Linux/macOS × Go 1.26/1.27 测试、quality 和 package 必须全部通过。
2. 记录 run ID、attempt、不可变 artifact ID 与 digest。下载该 run 的 `release-FULL_SHA-ATTEMPT`，不得用本地重建文件替换。
3. 从对应提交运行 `python3 scripts/release.py verify --sha FULL_SHA --version VERSION --output DOWNLOAD_DIR`。校验精确源码、归档文件集、模式、版本、平台、编译器、构建元数据和全部校验和。
4. 确认版本 tag/release 不存在，给该 SHA 创建 tag，创建草稿并上传八个已验证文件。重新下载草稿资产并验证后发布；禁止覆盖已有 tag 或附件。
5. 回读公开 tag、main、release、资产及仓库可见性，执行正式版本的独立 Go consumer 和 `go install`，保留每项证据。

八个制品包括四个平台的 wireless 二进制归档、同提交源码归档、REVISION、MANIFEST.json、SHA256SUMS。二进制归档包含 bin/wireless、README、MIT 项目许可、完整第三方许可、VERSION 和 REVISION。`wireless --version` 打印版本与源提交。

打包只读取干净的已提交树。归档采用稳定顺序、固定时间与权限，Go 构建固定平台选项并去除本地路径和 build ID。相同源码与编译器可生成相同字节。版本占位、replace、脏树、不安全符号链接、已有输出和重算校验和后的内容篡改均由发布测试拒绝。

Go workflow 仅有 contents: read 权限，负责验证和产物，不自动发布。GitHub Release 由已获授权的发布者使用 gh 创建草稿、上传、验证和发布。仓库按用户明确选择公开；后续发布保留当前可见性。
