# 贡献指南

- 使用 Go 1.27.2；Windows GUI 为 Win32 / Walk。默认构建不需要 .NET SDK。
- 在 `core/` 运行 `gofmt`、`go test -race -count=1 -timeout 120s ./...` 和 `go vet ./...`。
- Windows GUI / platform 修改还应执行 Windows 编译和实机验证；Linux 测试通过不代表 Win10 运行通过。
- 不要把私有 Redis 配置、生成的 `builtin_private.go`、令牌、用户配置、日志或带凭据的 EXE 提交到仓库。
- 不要把业务数据放进 Redis，也不要添加默认启用的 TURN / 中转回退。
- 改动网络逻辑时，分别记录建连耗时、成功率、传输性能及失败模型，不能把单一实验的成功率泛化到互联网。
- NAT 实验脚本需要 Linux 网络命名空间及管理权限；只应在可控开发环境运行，并执行 cleanup。

提交 Issue 时，请提供版本、Windows 位数、两端网络类型及**已检查/脱敏**的双方日志；不要提供 Redis 密码或 GitHub 令牌。
