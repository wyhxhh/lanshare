package gui

import (
	"net"
	"sort"
	"strings"
)

// skipIface 排除不可被局域网直连的虚拟/特殊适配器：
// 虚拟化（VMware/VirtualBox/Hyper-V 的 vEthernet）、WSL、Docker NAT、
// 蓝牙 PAN（点对点）、Mesh VPN（Tailscale/ZeroTier，非同一局域网的直连地址）。
// 真实有线/无线网卡（以太网/WLAN/Wi-Fi 等）名称不含这些关键字，不受影响。
func skipIface(name string) bool {
	n := strings.ToLower(name)
	for _, k := range []string{
		"vmware", "virtualbox", "vbox", "vethernet",
		"bluetooth", "tailscale", "zerotier", "docker",
	} {
		if strings.Contains(n, k) {
			return true
		}
	}
	return false
}

// lanIPv4s 返回本机可被局域网访问的 IPv4 地址（排序稳定，去回环与链路本地）。
// 供界面展示 "http://<本机IP>:端口" 访问地址。
func lanIPv4s() []string {
	var out []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, it := range ifaces {
		// 只取 up、非回环、非点对点（蓝牙 PAN）且非虚拟适配器
		if it.Flags&net.FlagUp == 0 ||
			it.Flags&net.FlagLoopback != 0 ||
			it.Flags&net.FlagPointToPoint != 0 ||
			skipIface(it.Name) {
			continue
		}
		addrs, err := it.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipn.IP.To4()
			if ip4 == nil || ip4.IsLoopback() {
				continue
			}
			// 跳过 169.254.* 链路本地地址（无 DHCP 时的占位地址，无法服务）
			if ip4[0] == 169 && ip4[1] == 254 {
				continue
			}
			out = append(out, ip4.String())
		}
	}
	sort.Strings(out)
	return out
}
