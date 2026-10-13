#!/usr/bin/env bash
# Isolated Linux namespace lab. Does not change the host's routing/firewall.
set -euo pipefail
prefix=nl-lab
if [[ ${1:-setup} == cleanup ]]; then
 for n in p c rp rc wan; do ip netns del "$prefix-$n" 2>/dev/null || true; done
 exit 0
fi
if [[ ${1:-setup} == symmetric ]]; then
 for r in rp rc; do ip netns exec "$prefix-$r" iptables -t nat -F POSTROUTING; ip netns exec "$prefix-$r" iptables -t nat -A POSTROUTING -s 10.77.0.0/24 -o wan -j MASQUERADE --random-fully; done
 exit 0
fi
for n in wan rp rc p c; do ip netns add "$prefix-$n"; ip -n "$prefix-$n" link set lo up; done
ip -n "$prefix-wan" link add br0 type bridge
ip -n "$prefix-wan" link set br0 up
ip -n "$prefix-wan" addr add 198.18.0.1/24 dev br0
ip -n "$prefix-wan" addr add 198.18.0.2/24 dev br0
for side in p c; do
 router="$prefix-r$side"; end="$prefix-$side"; pub=11; [[ $side == c ]] && pub=21
 ip link add "w-$side" type veth peer name "rw-$side"
 ip link set "w-$side" netns "$prefix-wan"; ip link set "rw-$side" netns "$router"
 ip -n "$prefix-wan" link set "w-$side" master br0; ip -n "$prefix-wan" link set "w-$side" up
 ip -n "$router" link set "rw-$side" name wan; ip -n "$router" addr add "198.18.0.$pub/24" dev wan; ip -n "$router" link set wan up
 ip link add "rl-$side" type veth peer name "e-$side"
 ip link set "rl-$side" netns "$router"; ip link set "e-$side" netns "$end"
 ip -n "$router" link set "rl-$side" name lan; ip -n "$router" addr add 10.77.0.1/24 dev lan; ip -n "$router" link set lan up
 ip -n "$end" link set "e-$side" name eth0; ip -n "$end" addr add 10.77.0.2/24 dev eth0; ip -n "$end" link set eth0 up; ip -n "$end" route add default via 10.77.0.1
 ip netns exec "$router" sysctl -qw net.ipv4.ip_forward=1
 ip netns exec "$router" iptables -A INPUT -i wan -p udp -m conntrack --ctstate NEW -j DROP
 ip netns exec "$router" iptables -P FORWARD DROP
 ip netns exec "$router" iptables -A FORWARD -i lan -o wan -j ACCEPT
 ip netns exec "$router" iptables -A FORWARD -i wan -o lan -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
 ip netns exec "$router" iptables -t nat -A POSTROUTING -s 10.77.0.0/24 -o wan -j MASQUERADE
 done
echo 'NAT_LAB_READY: both endpoints have the SAME private IP in isolated networks; only punched UDP can connect them.'
