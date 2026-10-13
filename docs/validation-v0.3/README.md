# v0.3 验证记录

- `nat-v02-standard.json`：旧版三轮受控双 NAT 结果，均连通，建连 6615–6626 ms。
- `nat-v03-standard.json`：新版十轮相同拓扑结果，均连通，186–306 ms，中位 205 ms；每轮验证 768 KiB 二进制回显。
- 这里的 NAT 端口保持，入站采用 UDP 端口受限过滤；两端使用相同私网 IP 的独立命名空间，必须经过两个 NAT。STUN 两节点应答延迟 80 ms，另有一个失效节点。
- `nat-strict.json` / `nat-strict.log`：双端随机目的相关映射未连通。应用明确报告映射变化、应答为零，无中转回退。该失败是能力边界，不是测成了成功。
- 实验早期未丢弃路由器未匹配的 UDP INPUT，产生首包碰撞/端口重映射，也未连通。受控模型的 10/10 不能泛化到其他 NAT。
- `core-tests.txt`：Go race detector 核心测试通过；测试包含三客户端并发、TCP 半关闭、双方重启、Redis 离线后原有直连延续、配置隔离。
- `gui-provider.json` / `gui-client.json`：最终 x64 Windows EXE 在 Wine 10 下的窗口、四页签、配置保存、托盘注册、隐藏恢复检查通过。GUI 保存的 Redis 主机/密码仍为空。

真实 Win10 上新版防火墙/UAC操作、用户 WAN/NAT和 RDP 尚未在本工作区验证。x86 仅编译验证。公共 STUN 的开发环境探测不代表用户所在地可用性。没有把受控测试说成真实用户网络的成功保证。
