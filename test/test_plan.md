# Test Plan: Unified DRA SDR Driver (USB & IP)

To certify the robustness and production-readiness of the DRA SDR Driver, it is imperative to execute a series of tests simulating various hardware states, orchestrator (Kubernetes) behaviors, and network/USB interactions. 

The following test plan outlines the validation procedures for both USB-attached and IP-attached Software Defined Radios (SDRs).

---

## Phase 1: Basic Functional Testing (E2E)

### Test 1.1: Basic USB SDR Allocation
- **Action**: Deploy `test-sdr-usb.yaml` requesting a USB SDR (`deviceClassName: sdr-usb`).
- **Validation**:
  - The Pod transitions to `Running`.
  - Execution of `uhd_find_devices` inside the Pod successfully discovers a B200/B210 device.
  - The host firmware directory (`/usr/share/uhd/images/`) and USB character device (`/dev/bus/usb/...`) are correctly mounted via CDI.

### Test 1.2: Basic IP SDR Allocation & Networking
- **Action**: Deploy `test-sdr-ip.yaml` requesting an IP SDR (`deviceClassName: sdr-ip`).
- **Validation**:
  - The Pod transitions to `Running`.
  - The Container runtime successfully invokes the NRI plugin, creating a `macvlan` interface inside the Pod's network namespace.
  - The Pod receives a dynamically allocated IP address in the SDR subnet (e.g., `192.168.10.x`).
  - Execution of `uhd_find_devices` inside the Pod successfully discovers an X300/X310 device over the network.

---

## Phase 2: Hybrid Coexistence and Resource Exhaustion

*(Prerequisite: 1 USB SDR and 1 IP SDR are physically connected to the exact same cluster node)*

### Test 2.1: Hybrid Node Registration (Avoid Double Registration)
- **Action**: Check the published ResourceSlices for the node where both SDRs are connected (`kubectl get resourceslices -l resource.k8s.io/node=<node-name> -o yaml`).
- **Validation**:
  - The node must expose exactly 2 devices in the Slice.
  - One device must have `sdr.gradiant.org/connection_type: usb` (registered via `sysfs`).
  - The other device must have `sdr.gradiant.org/connection_type: ip` with its corresponding `ip_address` (registered via `uhd_find_devices`).
  - **Critical**: The USB SDR must NOT be double-registered as an IP SDR. The driver's internal regex intentionally drops devices without the `addr:` field from the UHD output to avoid collisions.

### Test 2.2: Heterogeneous Resource Exhaustion
- **Action**: Deploy three Pods simultaneously: Pod A (requests USB), Pod B (requests IP), Pod C (requests USB).
- **Validation**:
  - Pod A and Pod B must transition to the `Running` state successfully.
  - Pod C must remain in the `Pending` state.
  - The Pod C events (`kubectl describe pod pod-c`) must display a scheduling error, explicitly stating: *0/1 nodes are available: insufficient sdr.gradiant.org.*

### Test 2.3: Automatic Re-allocation across Types
- **Action**: Terminate Pod A (`kubectl delete pod pod-a`).
- **Validation**:
  - The Driver successfully intercepts the deletion and frees the USB hardware resource.
  - Pod C must instantly transition from `Pending` to `Running` as the Kubernetes Scheduler immediately grants it the newly freed USB SDR.

### Test 2.4: Multi-Device Pod (Simultaneous USB and IP Allocation)
- **Action**: Deploy a single Pod that includes two `resourceClaims` in its specification: one for a USB SDR and another for an IP SDR.
- **Validation**:
  - The Kubernetes Scheduler must successfully find a node that can satisfy both claims simultaneously (the node with the USB SDR plugged in).
  - The Pod must transition to `Running`.
  - Inside the Pod, execution of `uhd_find_devices` must return exactly two distinct radios.
  - The Pod must possess both the injected physical `/dev/bus/usb/...` character device (via CDI) and the dedicated `macvlan` network interface (via NRI) simultaneously.

---

## Phase 3: Hardware Lifecycle, Hotplugging, and Network Disruptions

### Test 3.1: USB Cold Disconnection and Reconnection
- **Action**: 
  1. Physically disconnect the USB SDR from the host.
  2. Deploy a user Pod requesting `sdr-usb` (the Pod must remain `Pending`).
  3. Physically reconnect the SDR via USB.
- **Validation**:
  - The Driver logs correctly indicate the detection of the Cypress "WestBridge" controller, explicitly ignores it, and waits for the firmware flashing to complete.
  - The pending Pod transitions to `Running` approximately 5-10 seconds after the physical reconnection, once the SDR re-enumerates as a USRP device.

### Test 3.2: IP SDR Network Unreachability
- **Action**: 
  1. Deploy a Pod requesting an IP SDR.
  2. Inside the Pod, run `uhd_find_devices` to confirm the radio is reachable.
  3. Physically disconnect the network link to the SDR.
  4. Run `uhd_find_devices` again inside the Pod.
  5. Reconnect the physical link and run `uhd_find_devices` one last time.
- **Validation**:
  - In step 2, the X300/X310 is successfully listed.
  - In step 4, the command returns no devices, proving the isolated `macvlan` reflects the physical network disruption.
  - In step 5, the SDR correctly reappears in the output without needing to restart the Pod, proving network recovery works transparently.
  - **CRITICAL EXCEPTION**: If the disconnection in step 3 involved unplugging a USB-to-Ethernet adapter from the host (causing the entire host interface to vanish rather than just losing the link), the kernel will permanently destroy the Pod's `macvlan`. In this edge case, step 5 will fail and the Pod **MUST** be restarted to regenerate the network interface.

---

## Phase 4: Driver Resilience & State Recovery

### Test 4.1: KubeletPlugin Driver Restart
- **Action**: While user Pods (both USB and IP) are in the `Running` state and utilizing their radios, forcefully terminate the driver pod (`kubectl delete pod -n arodal -l app=dra-driver-ettus-sdr`).
- **Validation**:
  - The DaemonSet controller automatically recreates the Driver pod.
  - **Critical**: The user Pods **MUST NOT** be affected, disrupted, or restarted. Active radio transmissions must continue uninterrupted.
  - The newly spawned Driver pod successfully parses the local *checkpoint* file, recovering its internal state, and correctly recognizes that the specific IPs and USB nodes are still allocated.

---

## Phase 5: Container Isolation and IPAM Verification

### Test 5.1: Macvlan Subnet and IPAM Validation
- **Action**: Deploy two Pods requesting IP SDRs (if multiple IP SDRs exist), or one Pod requesting an IP SDR. Inspect the host and the container network namespaces.
- **Validation**:
  - The host machine's routing table must not be polluted by the Pod's specific IP allocations.
  - The driver's internal IPAM (IP Address Management) must strictly avoid assigning the `192.168.10.1` (gateway/SDR default IP) or `.0`/`.255` (network/broadcast) addresses to the Pods.
  - Two concurrently running IP SDR Pods must have different allocated IP addresses (e.g., `.2` and `.3`).

### Test 5.2: Non-privileged CDI Execution
- **Action**: Verify the security context of the user Pods running the SDRs.
- **Validation**:
  - The user Pods must execute in standard mode (not `privileged: true`).
  - The `CDI` injection must successfully map the `/dev/bus/usb/...` device with `mrw` (mknod, read, write) permissions precisely limited to that specific USB node.
