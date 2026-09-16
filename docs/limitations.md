# Known Limitations & Future Work

This document outlines the known architectural limitations of the current DRA SDR Driver implementation, particularly concerning the deployment of IP-based SDRs in a shared network topology (e.g., connected to a central switch instead of directly to a node).

## 1. Static IPAM & IP Collisions on Shared Networks
**Problem**: The driver implements an internal, static IP Address Management (IPAM) system to assign IPs to the Pod's `macvlan` interface (e.g., generating `192.168.10.3`, `.4`, etc., based on the SDR's subnet). This assumes a **Point-to-Point (Direct Attach)** topology where the subnet is entirely isolated and empty. 
If the SDR is connected to a shared corporate/lab switch (e.g., a `10.100.0.0/16` network), the driver might assign an IP to the Pod that is already in use by another device (like a laptop or another server), causing an **IP collision**.
**Future Solution**:
- **Multus + DHCP**: Offload network injection to Multus CNI and rely on a DHCP plugin to lease a guaranteed-available IP from the lab's main router.
- **NAT via CNI**: Abandon the `macvlan` approach and rely on standard Kubernetes CNI routing (e.g., Cilium) to reach the SDR, letting the host NAT the traffic. However, this introduces CPU overhead and latency, which can be detrimental to high-throughput UHD UDP streaming.

## 2. Duplicate Discovery (The Clone Problem)
**Problem**: The current discovery mechanism runs independently on every node (`DaemonSet`). If an IP SDR is connected to a central switch, *every* node on that subnet will successfully run `uhd_find_devices` and find the same radio. Consequently, every node will publish its own `ResourceSlice` claiming ownership of the device. Kubernetes will perceive these as multiple independent SDRs, leading to over-provisioning and crashes when multiple Pods attempt to connect to the single physical radio simultaneously.
**Future Solution**: 
Implement a *Leader Election* mechanism or a centralized controller. The controller would aggregate discoveries from all nodes, deduplicate them based on the SDR's serial number, and publish a single, authoritative `ResourceSlice` for the entire cluster.

## 3. Node-Bound Scheduling for Network Devices
**Problem**: Currently, the driver publishes devices using the `nodeName` field in the `ResourceSlice`. This instructs the Kubernetes scheduler that the hardware is strictly bound to the node that discovered it. While this is correct for USB SDRs, it is a severe limitation for IP SDRs on a switch, as it prevents Kubernetes from load-balancing the Pod to other available nodes in the cluster that can also reach the switch.
**Future Solution**:
Transition to Kubernetes DRA's "Network-Attached" resource paradigms, dropping the `nodeName` constraint so the scheduler can place the Pod on any node with network reachability.
