> 历史版本记录。当前开源版没有预置 Redis 账号，以仓库根目录 README 为准。

# NorthLink 0.2.0 原生免安装版验证记录

日期：2026-10-13。

## 成品窗口验证

环境：Linux + Wine 10（64 位 Windows 兼容环境），不是物理 Win10。直接运行最终交付的 EXE，没有 .NET、WebView2 或同目录应用 DLL。

提供端与客户端的最终成品均已实际创建窗口、读取配置、切换四个页签、保存配置、注册托盘、隐藏/恢复窗口并正常退出。报告：

### provider

```json
{
  "architecture": "amd64",
  "configCapture": true,
  "configLoaded": true,
  "configSaved": true,
  "hideRestore": true,
  "role": "provider",
  "stunFirst": "stun:stun.hitv.com:3478",
  "tabCount": 4,
  "tabsSwitch": true,
  "trayAvailable": true,
  "version": "0.2.0",
  "windowCreated": true
}
```

### client

```json
{
  "architecture": "amd64",
  "configCapture": true,
  "configLoaded": true,
  "configSaved": true,
  "hideRestore": true,
  "role": "client",
  "stunFirst": "stun:stun.hitv.com:3478",
  "tabCount": 4,
  "tabsSwitch": true,
  "trayAvailable": true,
  "version": "0.2.0",
  "windowCreated": true
}
```

## 核心验证

Linux 上 `go test -race -count=1 -timeout 100s ./...`、`go vet ./...` 通过；Windows GUI 包的 `go vet` 通过。包含三客户端传输、HTTP 二进制一致性、TCP 半关闭、两端重启、Redis 离线后的存量直连及配置草稿不污染运行中状态等检查。

## 未通过 / 尚未验证

另行运行的 Windows 网络集成测试在 Wine 中失败，错误为 `route ip+net: getadaptersaddresses: Invalid data.`，未能取得可用接口用于 ICE。没有将此测试称为通过，也不能据此保证或断言真实 Win10 的网络行为。原始输出保存在 `NATIVE-windows-network-test.txt`。

x86 可执行文件已编译，但未在真实 32 位 Windows 中启动。真实 Win10、跨 NAT、公共 STUN 可用性、RDP 登录及流畅度均待实机验证。未访问用户 Redis、未收集密码。

## 依赖说明

EXE 使用 Go 原生网络核心与 Win32 控件。CGO 关闭，未打包 .NET，也不需外置应用 DLL。会调用 Windows 自带的系统 DLL（user32、comctl32 等），这不等于“完全不使用 DLL”。Win32 清单、图标和许可已嵌入。
