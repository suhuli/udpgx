# udpgx · NorthLink

**Windows 原生、免安装的 P2P TCP 端口映射工具。**

`udpgx` 是 NorthLink 原生版的开源仓库。提供端发布本机或可达内网的 TCP 服务，客户端把它映射到本地端口；Redis 只交换信令，业务数据通过 ICE/STUN 建立的直连路径和 QUIC 传输。

```text
提供端 ─────── Redis 信令 ─────── 客户端
   └────── ICE / STUN → QUIC 直连 ──┘
```

**不使用 Redis 转发业务，不提供 TURN 或中转回退。直连失败时会明确失败，不保证所有 NAT 都能穿透。**

> 当前为 0.3 实验版本，面向 Windows 10 x64 / x86。公开源码和默认构建不包含预置 Redis 账号或密码，请配置自己的信令服务。仓库不上传带私有凭据的 EXE。

## 功能

- 提供端 / 客户端两个独立 EXE，中文原生 Win32 界面；无需额外安装 .NET、WebView2 或应用 DLL。
- 配置保存、导入导出、托盘隐藏与恢复、停止退出、可选登录启动。
- 一对设备复用一条 QUIC 连接，每个 TCP 连接使用一个双向流；设计场景为 1–3 个客户端。
- Trickle ICE：候选边收集边交换，连通检查不等待全部 STUN 完成。
- 每轮协商内，同地址族共用 UDP socket；IPv4 STUN 候选和原生 IPv6 地址直连。
- 建连阶段、候选数、检查收发数、映射变化、RTT、流量与错误日志。
- 用户主动授权后放行当前 EXE 的 Windows UDP 防火墙规则；不关闭防火墙。

## 构建

需要 **Go 1.27.2**。清单、图标资源已包含，正常构建不需要 .NET SDK。

```powershell
git clone https://github.com/suhuli/udpgx.git
cd udpgx
.\build.ps1                      # Windows x64
.\build.ps1 -Architecture x86    # Windows x86
```

输出分别位于 `build/native-x64/` 和 `build/native-x86/`：

- `NorthLink-Server-v0.3.exe`：提供端。
- `NorthLink-Client-v0.3.exe`：客户端。

也可以查看 [GitHub Actions](https://github.com/suhuli/udpgx/actions)；工作流成功后会生成不含私有凭据的 Windows 构建工件。正常运行不需要管理员权限，添加防火墙规则时才会请求授权。

## 使用

1. 两端填写相同的 Redis 地址、端口、DB、账号/密码及连接组。主机栏支持 `host:port`，不要填写网页 URL。
2. 两端按需点击“连接配置 → 放行本 EXE 的 UDP”。移动 EXE 后，应检查规则是否仍匹配实际程序路径。
3. 提供端启用并保存要发布的 TCP 服务，确认目标本身已经运行。
4. 两端启动连接；客户端选择在线提供端和服务，添加本地映射并保存。

| 用途 | 提供端目标 | 客户端访问示例 |
|---|---|---|
| 网站 | `127.0.0.1:80` | `http://127.0.0.1:18080` |
| 远程桌面 | `127.0.0.1:3389` | 远程桌面连接 `127.0.0.1:13389` |

Redis 需要 5.0+，并允许程序使用 PING、SET、GET、SCAN、DEL、EXPIRE、XADD、XREAD 等命令及自身的 `northlink:v1:*` 键前缀；按实例配置 AUTH、SELECT、TLS。不支持 Redis Cluster / Sentinel 的专用配置。

默认 STUN 列表可编辑：

```text
stun:stun.hitv.com:3478
stun:stun.miwifi.com:3478
stun:stun.xten.com:3478
stun:stun.voip.aebc.com:3478
```

公共节点的可达性取决于实际网络。两端必须允许对端动态 UDP 流量，仅允许 STUN 的 3478 端口不够。

## 配置与后台运行

配置和日志位于 `%LOCALAPPDATA%\NorthLink\provider` 或 `client`。

- 点 X 隐藏到托盘；托盘图标恢复窗口；“退出”停止网络并释放端口。
- 普通托盘模式不安装 Windows 服务，Windows 注销后不会继续运行。
- 配置及导出文件可能包含**自己填写的 Redis 密码**，不要上传到公开 Issue。
- 修改配置可能重建连接；断开的 TCP 会话不能无感恢复。
- 连接组不是身份认证或授权边界；此项目不提供完善的多用户权限系统。

## 排错与限制

- **Redis TCP 超时**：先检查 TCP 端口可达性，添加 UDP 规则无法解决信令 TCP 不通。
- **信令在线但 ICE 无应答**：检查两端防火墙、UDP 策略和 NAT 映射；导出两端日志。
- **ICE 成功、QUIC 失败**：检查后续握手错误，不要与初始打洞混为一谈。
- **已直连但目标不可用**：检查目标服务、IP、端口、Windows 远程桌面设置及授权。

仅支持 TCP 业务映射；当前不含原生业务 UDP、TCP 打洞、端口预测、UPnP、TURN 或其他中转。不会改变 Windows 的 RDP 版本/会话限制，不改写 HTTP Host、重定向或证书。

## 验证范围

- Go race tests / vet，以及三个客户端并发传输、TCP 半关闭、双方重启、信令离线时既有直连延续等本地检查。
- 受控、独立双 NAT 网络命名空间实验：在一种端口保持/端口受限过滤模型中，0.3 的十轮测试全部完成 768 KiB 二进制回显，建连 186–306 ms。
- 双端随机目的相关端口映射实验**未连通**。上述十轮结果不是互联网成功率，也不是对实际用户网络的保证。
- Windows EXE 曾在 Wine 中完成 GUI 启动/托盘/配置检查；这不等于真实 Win10、UAC、防火墙或 WAN/RDP 验证。

详见 [0.3 验证记录](docs/validation-v0.3/README.md)。历史文档可能描述先前的私有打包版本，当前公开构建行为以本 README 为准。

## 开发

```bash
cd core
go test -race -count=1 -timeout 120s ./...
go vet ./...
```

| 路径 | 内容 |
|---|---|
| `core/internal/nativeui` | 当前原生 Windows 界面 |
| `core/internal/engine` | Redis 信令、会话和 TCP 转发 |
| `core/internal/link` | ICE/STUN、UDP 复用与 QUIC |
| `core/internal/config` | 配置、公开默认值和私有构建入口 |
| `tests/nat-lab.sh` | Linux 隔离 NAT 实验；仅用于开发验证 |
| `desktop/` | 历史 WPF 实现，不是当前默认入口 |

架构见 [ARCHITECTURE.md](docs/ARCHITECTURE.md)，贡献说明见 [CONTRIBUTING.md](CONTRIBUTING.md)。

### 可选私有内置配置

内置 Redis 的能力保留，但仓库没有任何真实账号。需要时在本机创建 `.private/redis-default.json`，然后执行：

```powershell
.\build.ps1 -BuiltinRedis
```

本地 JSON 结构：

```json
{"host":"your-redis-host","port":6379,"username":"","password":"your-password","db":0,"tls":false}
```

`.private/` 及生成的 `builtin_private.go` 均被忽略。**不要公开带私有凭据的构建产物：界面隐藏不代表二进制中的凭据无法被提取。**私有构建中，主机留空用内置；填写主机则使用自定义配置。

## 许可证

[MIT](LICENSE)。第三方许可见源码中的 notices 文件；原生 EXE 也包含“运行日志 → 开源许可”入口。
