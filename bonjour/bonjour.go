package bonjour

import (
	"fmt"
	"net"
	"strconv"

	"github.com/grandcat/zeroconf"
)

var bonjourServers []*zeroconf.Server

func findInterfaceByAddress(targetIP string) ([]net.Interface, error) {
	if targetIP == "" {
		return nil, nil
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	for _, iface := range interfaces {
		addrs, err := iface.Addrs()
		if err != nil {
			return nil, err
		}

		for _, addr := range addrs {
			switch v := addr.(type) {
			case *net.IPNet:
				if v.IP.String() == targetIP {
					return []net.Interface{iface}, nil
				}
			}
		}
	}

	return nil, fmt.Errorf("no interface found with IP address: %s", targetIP)
}

func getLocalIPForDefaultGateway() (string, error) {
	// Choose a public IP (like Google DNS 8.8.8.8) to determine the appropriate interface.
	// No actual connection or data sending is done.
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "", err
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)

	return localAddr.IP.String(), nil
}

func getNonLoopbackIPAddresses() ([]string, error) {
	var ips []string

	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	for _, iface := range interfaces {
		addrs, err := iface.Addrs()
		if err != nil {
			return nil, err
		}

		for _, addr := range addrs {
			switch v := addr.(type) {
			case *net.IPNet:
				if ipv4 := v.IP.To4(); ipv4 != nil && !ipv4.IsLoopback() {
					ips = append(ips, ipv4.String())
				}
			}
		}
	}

	return ips, nil
}

// https://developer.apple.com/library/archive/releasenotes/NetworkingInternetWeb/Time_Machine_SMB_Spec/index.html#//apple_ref/doc/uid/TP40017496
func Advertise(listenAddr string, hostname string, svcName string, shareName string, tm bool) error {
	host, portStr, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return fmt.Errorf("parse listen address: %w", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return fmt.Errorf("parse listen port: %w", err)
	}

	// An unspecified listen address means all usable interfaces; it is not an
	// address that can be handed to zeroconf or matched to one interface.
	advertiseHost := host
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		advertiseHost = ""
	}

	ifaces, err := findInterfaceByAddress(advertiseHost)
	if err != nil {
		return fmt.Errorf("find interface: %w", err)
	}

	ips := []string{advertiseHost}
	if advertiseHost == "" {
		if ip, err := getLocalIPForDefaultGateway(); err == nil {
			ips = []string{ip}
		} else {
			ips, err = getNonLoopbackIPAddresses()
			if err != nil {
				return fmt.Errorf("list non-loopback addresses: %w", err)
			}
		}
	}

	s, err := zeroconf.RegisterProxy(hostname, "_smb._tcp", ".local", port, svcName, ips, []string{""}, ifaces)
	if err != nil {
		return fmt.Errorf("advertise SMB: %w", err)
	}
	bonjourServers = append(bonjourServers, s)

	s, err = zeroconf.RegisterProxy(hostname, "_adisk._tcp", ".local", 9, svcName, ips, []string{fmt.Sprintf("dk0=adVN=%s,adVF=0x82", shareName), "sys=waMa=0,adVF=0x100"}, ifaces)
	if err != nil {
		Shutdown()
		return fmt.Errorf("advertise Time Machine disk: %w", err)
	}
	bonjourServers = append(bonjourServers, s)

	if tm {
		s, err = zeroconf.RegisterProxy(hostname, "_device-info._tcp", ".local", 9, svcName, ips, []string{"model=TimeCapsule8,119"}, ifaces)
		if err != nil {
			Shutdown()
			return fmt.Errorf("advertise device info: %w", err)
		}
		bonjourServers = append(bonjourServers, s)
	}
	return nil
}

func Shutdown() {
	for _, s := range bonjourServers {
		s.Shutdown()
	}
}
