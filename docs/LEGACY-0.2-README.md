> 历史版本记录。当前开源版没有预置 Redis 账号，以仓库根目录 README 为准。

# NorthLink 0.2.0 · 原生单 EXE 免安装版

为解决上一版“点击没反应”和文件依赖问题，0.2 改用原生 Windows 控件，并把界面与网络核心合并到同一个可执行文件。不是把旧程序简单改后缀或套一个安装器。

## 运行

- `NorthLink-Server.exe`：提供端，放在网站 / 远程桌面所在电脑，直接双击。
- `NorthLink-Client.exe`：客户端，放在访问电脑，直接双击。
- 默认下载为 Windows 10 x64；32 位 Win10 使用兼容包中的 x86 版本。
- 每个 EXE 独立，不需要放在同一目录，不需要 DLL、.NET、WebView2、Go 或安装程序。
- 正常启动不要求管理员权限。只有显式点击“放行本 EXE 的 UDP”时会请求管理员授权。
- 未做代码签名；本程序不会规避系统或安全软件的拦截。被拦截时请保留提示信息以便排查。

**先从旧版托盘选择“停止并退出”。**若安装过旧版 Windows 服务，先停止或移除旧服务。新旧版本不要同时使用同一个设备 ID。新版读取既有本机配置，不会因首次运行覆盖原配置。

## 使用

1. 两端填写相同的 Redis 地址、端口、DB、用户名（可留空）、密码及连接组，保存。
2. 提供端“发布服务”中确认网站 `127.0.0.1:80` / RDP `127.0.0.1:3389`，可新增其他 TCP 目标，勾选启用。
3. 两端启动连接。客户端“端口映射”里选择在线提供端和服务，添加映射，保存并应用。
4. 默认网站访问 `http://127.0.0.1:18080`；远程桌面连接 `127.0.0.1:13389`。

目标服务要本来就在运行。Redis 需要 5.0+，允许 PING、AUTH/HELLO（按实例）、SELECT（非零 DB）、SET、GET、SCAN、DEL、EXPIRE、XADD、XREAD 及程序自身键前缀 `northlink:v1:*`。不支持 Redis Cluster / Sentinel 的专用配置。

Redis 只交换信令；ICE + QUIC 直接传输业务。不使用 TURN、中转或 Redis 数据转发。TCP 服务都可以映射，RDP 先走 TCP。多个客户端不等于 Win10 获得多人独立远程桌面会话能力。

## STUN

```
stun:stun.hitv.com:3478
stun:stun.miwifi.com:3478
stun:stun.xten.com:3478
stun:stun.voip.aebc.com:3478
```

这是用户指定的候选列表。端口可在界面改；没有验证这些公共节点当前的可用性。不能保证所有 NAT 都能打洞成功，失败不会自动中转。

## 托盘与配置

- 点 X：隐藏到托盘，继续运行；若托盘不可用，则执行正常退出，避免产生找不到的窗口。
- 点击托盘图标恢复；右键可配置、启动、停止、退出。
- 点击“退出”：停止网络、释放端口并退出。
- 支持按当前用户“登录 Windows 时启动”，需用户主动勾选并保存。
- **本版为普通托盘程序，不安装系统服务。**注销后不会继续运行；尚未登录的无人值守场景需要另行配置服务版，不在此免安装版中隐式安装。
- 修改配置会重建连接，不保证正在使用的会话无感续接。

配置和日志保存在：

```
%LOCALAPPDATA%\NorthLink\provider\config.json
%LOCALAPPDATA%\NorthLink\provider\native.log
%LOCALAPPDATA%\NorthLink\client\config.json
%LOCALAPPDATA%\NorthLink\client\native.log
```

配置含 Redis 密码，导出配置也包含密码。日志可在界面导出。GUI 初始化错误会弹窗并写日志；若程序被操作系统/安全软件在运行前阻止，则可能尚未产生日志。

## 0.2 验证情况

- 两个 **Windows x64 成品 EXE** 已在 Wine 10 兼容环境启动，创建窗口、四个页签、配置读取/保存、托盘注册和隐藏恢复均通过检查，正常退出。
- 修正了开发中发现的控件初始化范围错误；窗口启动不依赖 Redis 连接成功。
- Linux 网络核心测试（含 race detector）通过：三客户端并发转发、TCP 半关闭、重连、Redis 掉线后的既有直连延续。
- Windows 成品的窗口启动验证不等于真实 Win10 / NAT / RDP 验证。另一次 Windows 网络集成测试在 Wine 中因 `GetAdaptersAddresses: Invalid data` 未通过，尚不能用该兼容环境确认 Windows 网络路径。真实 Win10 网络仍需实机验证。
- x86 版本已编译，未在 32 位 Windows 中运行验证。

详细记录见 `docs/NATIVE-VALIDATION.md`。没有收集用户密码，没有连接用户 Redis。

## 构建

需要 Go 1.27.2。资源文件（清单、图标）已嵌入并包含在源码中，无需 .NET SDK。

```powershell
.\build.ps1                   # Windows x64
.\build.ps1 -Architecture x86 # Windows x86
```

输出在 `build/native-x64` 或 `build/native-x86`。`desktop/`、`build-legacy-wpf.ps1` 和 `docs/LEGACY-0.1-README.md` 保留旧版实现，不是当前单 EXE 入口。

核心协议说明见 `docs/ARCHITECTURE.md`（其中旧 WPF IPC / 服务描述属于 0.1；0.2 GUI 在进程内调用网络核心）。开源许可已嵌入 EXE，可在“运行日志 → 开源许可”打开。
