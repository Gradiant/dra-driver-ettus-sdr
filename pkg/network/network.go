/*
 * This Source Code Form is subject to the terms of the Mozilla Public
 * License, v. 2.0. If a copy of the MPL was not distributed with this
 * file, You can obtain one at https://mozilla.org/MPL/2.0/.
 */

package network

import (
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"strings"
	"sync"

	"github.com/containerd/nri/pkg/api"
	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/klog/v2"
)

// IPAM (IP Address Management) handles local IP allocation for Pods.
// Since the SDR operates on a specific IP subnet (e.g., 192.168.10.x), the Pod also needs an IP in that same subnet.
type IPAM struct {
	mu          sync.Mutex
	assignedIPs map[string]string // Maps Pod UID -> IP Address
	usedIPs     map[string]bool   // Maps IP Address -> bool
}

// NewIPAM creates a new IPAM instance.
func NewIPAM() *IPAM {
	return &IPAM{
		assignedIPs: make(map[string]string),
		usedIPs:     make(map[string]bool),
	}
}

// incIP is a helper to increment an IP address mathematically
func incIP(ip net.IP) {
	for j := len(ip) - 1; j >= 0; j-- {
		ip[j]++
		if ip[j] > 0 {
			break
		}
	}
}

// AllocateDynamicIP dynamically finds a free IP address based on the physical host's real subnet.
// It mathematically calculates the network range from the host's CIDR and picks an available IP.
func (i *IPAM) AllocateDynamicIP(podUID string, sdrIP string, hostCIDR string) (string, int, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	// If already allocated for this pod, just extract the mask and return the existing IP
	if ip, exists := i.assignedIPs[podUID]; exists {
		_, ipnet, _ := net.ParseCIDR(hostCIDR)
		maskSize, _ := ipnet.Mask.Size()
		return ip, maskSize, nil
	}

	hostIP, hostNet, err := net.ParseCIDR(hostCIDR)
	if err != nil {
		return "", 0, fmt.Errorf("failed to parse host CIDR: %w", err)
	}
	maskSize, _ := hostNet.Mask.Size()

	// Calculate broadcast IP to avoid assigning it to the Pod
	broadcastIP := make(net.IP, len(hostNet.IP))
	for j := 0; j < len(hostNet.IP); j++ {
		broadcastIP[j] = hostNet.IP[j] | ^hostNet.Mask[j]
	}

	// Iterate mathematically through all possible IPs in this specific subnet
	for ip := hostNet.IP.Mask(hostNet.Mask); hostNet.Contains(ip); incIP(ip) {
		ipStr := ip.String()

		// Skip network address, broadcast address, the Host's IP, and the SDR's IP
		if ipStr == hostNet.IP.String() || ipStr == broadcastIP.String() || ipStr == hostIP.String() || ipStr == sdrIP {
			continue
		}

		// Skip IPs already allocated to other Pods by this driver
		if !i.usedIPs[ipStr] {
			i.usedIPs[ipStr] = true
			i.assignedIPs[podUID] = ipStr
			return ipStr, maskSize, nil
		}
	}

	return "", 0, fmt.Errorf("no available IPs left in the subnet %s", hostCIDR)
}

// ReleaseIP frees the assigned IP address when a Pod is terminated.
func (i *IPAM) ReleaseIP(podUID string) {
	i.mu.Lock()
	defer i.mu.Unlock()

	if ip, exists := i.assignedIPs[podUID]; exists {
		delete(i.usedIPs, ip)
		delete(i.assignedIPs, podUID)
	}
}

// getNetworkDevices discovers UHD SDR devices on the network by executing the uhd_find_devices tool.
// It parses the command output to extract the IP address, serial number, and product type.
// It returns a slice of resourceapi.Device, making these IP SDRs visible to the Kubernetes API.
func GetNetworkDevices() ([]resourceapi.Device, error) {
	// Execute uhd_find_devices to broadcast and find SDRs on the local network
	cmd := exec.Command("uhd_find_devices")
	out, err := cmd.Output()
	if err != nil {
		// If uhd_find_devices fails (e.g., no devices found, it returns exit code 1 sometimes)
		// we just log it and return empty rather than failing the whole plugin loop.
		klog.V(4).Infof("uhd_find_devices returned error or no devices found: %v", err)
	}

	output := string(out)
	var devices []resourceapi.Device

	// Split the output by device block
	blocks := strings.Split(output, "-- UHD Device")
	for i, block := range blocks {
		if i == 0 {
			continue // skip header
		}

		// Extract properties using regex
		serialMatch := regexp.MustCompile(`serial:\s*([^\s]+)`).FindStringSubmatch(block)
		addrMatch := regexp.MustCompile(`addr:\s*([^\s]+)`).FindStringSubmatch(block)
		productMatch := regexp.MustCompile(`product:\s*([^\s]+)`).FindStringSubmatch(block)
		typeMatch := regexp.MustCompile(`type:\s*([^\s]+)`).FindStringSubmatch(block)

		if len(serialMatch) < 2 || len(addrMatch) < 2 {
			continue // Skip if we couldn't parse the basic requirements (serial and IP)
		}

		serial := serialMatch[1]
		ipAddr := addrMatch[1]
		product := ""
		if len(productMatch) >= 2 {
			product = productMatch[1]
		}
		devType := ""
		if len(typeMatch) >= 2 {
			devType = typeMatch[1]
		}

		// Create a standard K8s Device object representing the SDR
		device := resourceapi.Device{
			Name: strings.ToLower(serial), // We use the SDR's serial number as the unique device Name
			Attributes: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
				"sdr.gradiant.org/connection_type": {
					StringValue: func(s string) *string { return &s }("ip"),
				},
				"sdr.gradiant.org/ip_address": {
					StringValue: &ipAddr,
				},
				"sdr.gradiant.org/serial": {
					StringValue: &serial,
				},
				"sdr.gradiant.org/product": {
					StringValue: &product,
				},
				"sdr.gradiant.org/type": {
					StringValue: &devType,
				},
			},
		}

		devices = append(devices, device)
		klog.V(4).Infof("Discovered UHD SDR device: %s (IP: %s, Product: %s)", serial, ipAddr, product)
	}

	return devices, nil
}

// getInterfaceForIP uses the host's Linux routing table (`ip route get`) to figure out
// exactly which physical network interface card (e.g. eth0, enx...) is connected to the SDR.
func getInterfaceForIP(ip string) (string, error) {
	cmd := exec.Command("ip", "-o", "route", "get", ip)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	parts := strings.Fields(string(out))
	for i, part := range parts {
		if part == "dev" && i+1 < len(parts) {
			return parts[i+1], nil
		}
	}
	return "", fmt.Errorf("interface not found in route output")
}

// getHostInterfaceCIDR retrieves the exact IP and subnet mask (CIDR) of a physical interface on the host.
func getHostInterfaceCIDR(ifaceName string) (string, error) {
	cmd := exec.Command("ip", "-o", "-f", "inet", "addr", "show", "dev", ifaceName)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get addr for interface %s: %w", ifaceName, err)
	}

	fields := strings.Fields(string(out))
	for i, field := range fields {
		if field == "inet" && i+1 < len(fields) {
			return fields[i+1], nil
		}
	}

	return "", fmt.Errorf("CIDR not found in ip addr output for %s", ifaceName)
}

// configureDeviceForPod performs the low-level Linux networking configuration to bridge
// the Pod directly to the SDR's network using Macvlan interfaces.
func ConfigureDeviceForPod(ipam *IPAM, deviceName string, ipAddr string, networkNamespace string, podSandbox *api.PodSandbox) error {
	klog.V(2).Infof("Configuring Macvlan for device %s (IP: %s) for pod %s/%s in netns %s",
		deviceName, ipAddr, podSandbox.Namespace, podSandbox.Name, networkNamespace)

	// 1. Find the physical interface on the host that connects to the SDR
	hostIface, err := getInterfaceForIP(ipAddr)
	if err != nil {
		return fmt.Errorf("could not find physical interface for SDR IP %s: %w", ipAddr, err)
	}

	// 2. Generate a unique name for the virtual macvlan interface
	macvlanName := fmt.Sprintf("sdr-%s", podSandbox.Uid[:8])

	// 3. Create the Macvlan interface on the host, acting as a bridge to the physical cable
	cmd := exec.Command("ip", "link", "add", "link", hostIface, "name", macvlanName, "type", "macvlan", "mode", "bridge")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create macvlan %s on %s: %w", macvlanName, hostIface, err)
	}

	// 4. Move the Macvlan interface deep into the Pod's isolated network namespace
	cmd = exec.Command("ip", "link", "set", macvlanName, "netns", networkNamespace)
	if err := cmd.Run(); err != nil {
		// Cleanup if move fails
		exec.Command("ip", "link", "delete", macvlanName).Run()
		return fmt.Errorf("failed to move macvlan to pod netns %s: %w", networkNamespace, err)
	}

	// 5. Configure the Macvlan interface INSIDE the pod using nsenter
	// Read the exact CIDR (IP and mask) from the host's physical interface
	hostCIDR, err := getHostInterfaceCIDR(hostIface)
	if err != nil {
		return fmt.Errorf("failed to get CIDR for host interface %s: %v", hostIface, err)
	}

	// Allocate a collision-free IP using real subnet mathematics and the correct mask
	allocatedIp, maskSize, err := ipam.AllocateDynamicIP(podSandbox.Uid, ipAddr, hostCIDR)
	if err != nil {
		return fmt.Errorf("failed to allocate IP dynamically: %v", err)
	}
	podIp := fmt.Sprintf("%s/%d", allocatedIp, maskSize)

	// Assign the generated IP to the interface inside the pod
	cmd = exec.Command("nsenter", "--net="+networkNamespace, "ip", "addr", "add", podIp, "dev", macvlanName)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to assign IP %s to macvlan inside pod: %w", podIp, err)
	}

	// Bring the interface UP inside the pod so it can start transmitting to the SDR
	cmd = exec.Command("nsenter", "--net="+networkNamespace, "ip", "link", "set", macvlanName, "up")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to set macvlan UP inside pod: %w", err)
	}

	klog.V(2).Infof("Successfully configured Macvlan %s (IP: %s) for pod %s/%s", macvlanName, podIp, podSandbox.Namespace, podSandbox.Name)
	return nil
}

// cleanupDeviceForPod forcefully deletes the virtual Macvlan interface from the Linux kernel.
func CleanupDeviceForPod(networkNamespace string, podSandbox *api.PodSandbox) error {
	macvlanName := fmt.Sprintf("sdr-%s", podSandbox.Uid[:8])

	klog.V(2).Infof("Cleaning up Macvlan device %s for pod %s/%s",
		macvlanName, podSandbox.Namespace, podSandbox.Name)

	if networkNamespace != "" {
		cmd := exec.Command("nsenter", "--net="+networkNamespace, "ip", "link", "delete", macvlanName)
		if err := cmd.Run(); err != nil {
			klog.V(4).Infof("cleanup: failed to delete macvlan inside pod netns (might already be deleted): %v", err)
		}
	} else {
		// Just in case it was left in the host namespace due to an error during configuration
		exec.Command("ip", "link", "delete", macvlanName).Run()
	}

	return nil
}

// getNetworkNamespace extracts the low-level Linux network namespace path of the Pod.
func GetNetworkNamespace(pod *api.PodSandbox) string {
	// get the pod network namespace
	for _, namespace := range pod.Linux.GetNamespaces() {
		if namespace.Type == "network" {
			return namespace.Path
		}
	}
	return ""
}
