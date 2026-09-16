# DRA Driver Architecture for SDRs (`dra-driver-ettus-sdr`)

## 1. Introduction to the SDR DRA Driver
The **`dra-driver-ettus-sdr`** is a specialized Kubernetes component engineered to dynamically discover, manage, and allocate physical Software-Defined Radios (SDRs) to demanding workloads. It serves as a unified abstraction layer, converging the management of **USB-attached SDRs** and **Network-attached IP SDRs** into a single driver.

Its main mission is to act as a smart intermediary between the physical hardware and the orchestrator (Kubernetes), ensuring SDRs are allocated exclusively and that containers receive the correct permissions, volumes, and network configurations without requiring manual host setup.

To achieve this, it combines the power of **DRA (Dynamic Resource Allocation)** for logical exposure and management, **CDI (Container Device Interface)** for USB permission injection, and **NRI (Node Resource Interface)** for very low-level management and configuration of network resources.

The architecture is highly modularized into specialized packages (`pkg/driver`, `pkg/usb`, `pkg/network`, and `pkg/cdi`), all orchestrated by a central daemon (`cmd/dra-driver-ettus-sdr/main.go`). A deep dive into its internal mechanics is provided below.

---

## 2. SDR Discovery (Hybrid Discovery)

The main engine of the `UnifiedDriver` (in `pkg/driver/driver.go`) orchestrates device discovery through a continuous monitoring loop. In its `PublishResources` function, every 10 seconds, this engine centralizes the search by calling the specialist modules: first, it invokes `network.GetNetworkDevices()` to poll the network, and immediately after, it calls `usb.GetUSBDevices()` to scan local ports.

The results from both operations are combined and sent together to the orchestrator via the DRA plugin as a single node resource *pool* in a `ResourceSlice`. Thanks to this aggregation at the top layer of the driver, Kubernetes does not need to externally distinguish whether an SDR is connected via USB or IP; the driver abstracts and unifies them into a single inventory.

Despite this logical unification for the cluster, the way the specialist sub-modules discover and interact with the hardware at a low level is completely different:

### 2.1 USB SDR Discovery (`GetUSBDevices`)

#### Identification by VendorID and ProductID
For the driver to know which connected USB devices are actually radios, it uses standard identifiers hardcoded into the hardware by the manufacturer. The `pkg/usb` package defines constants for Ettus Research and National Instruments (NI):

```go
const (
	ProfileName = "sdr"

	EttusVendorID     = "2500"
	EttusNiVendorID   = "3923"
	B200ProductID     = "0020"
	B200MiniProductID = "0021"
	B205MiniProductID = "0022"
	B200NiProductID   = "7813"
	B210NiProductID   = "7814"

	SysfsDevices = "/sys/bus/usb/devices"
	VendorFile   = "idVendor"
	ProductFile  = "idProduct"
)
```
The driver scans `/sys/bus/usb/devices` looking for any device whose `idVendor` and `idProduct` fields exactly match this matrix. It then creates a `resourceapi.Device` object, marking it with the attribute `connection_type=usb` so the engine knows it requires CDI processing later.

#### The Firmware Problem and USB Path Changes (B200 Series)
When a B210 series SDR is plugged into the server, the USB controller chip lacks firmware. It identifies itself with Vendor 2500 and Product 0020, but its name (`product` field) is "WestBridge".
If the driver allocated the radio in this state to a Pod, the following would occur:
1. The Pod would receive permissions via DRA/CDI for the current USB path (e.g., `/dev/bus/usb/001/004`).
2. The user would attempt to load the firmware from their container.
3. Upon receiving the firmware, the SDR **restarts internally at the hardware level** (it instantly disconnects from and reconnects to the server).
4. The operating system re-enumerates the port and assigns it a **new physical path** (e.g., `/dev/bus/usb/001/005`).
5. **Fatal result**: The Pod suddenly loses permissions. Since DRA exclusively authorized it to use path `004`, the container lacks permissions to access the new path `005`, leaving the radio inaccessible.

#### Solution: Pre-injection of SDR Firmware
To prevent the path from changing while a Pod is using it, the driver assumes responsibility for injecting the firmware into the SDR *before* publishing it in Kubernetes.

The daemon's Docker image is built by running the `uhd_images_downloader` tool, so it comes pre-loaded with all the binary files. Furthermore, upon startup in `main.go`, the daemon executes a dynamic copy of these files to the host:

```go
cmd := fmt.Sprintf("mkdir -p %s && UHD_DIR=$(find /usr /opt /lib -type f -name 'usrp_b200_fw.hex' | head -n 1 | xargs dirname) && if [ ! -z \"$UHD_DIR\" ]; then cp -an \"$UHD_DIR\"/. %s/; else echo 'Firmware not found in container'; exit 1; fi", hostUHDImagesPath, hostUHDImagesPath)
err := exec.Command("sh", "-c", cmd).Run()
```
This solves two problems:
1. It allows the daemon itself or the host's udev rules to use the UHD tools in the background to flash the "WestBridge" chip. After flashing, the SDR restarts, gets its final path, and identifies itself as "USRP B200". That is when it is safely registered in Kubernetes.
2. For Pods that use SDRs, the generated CDI configuration not only injects the final USB port, but also **mounts this exact host folder as a read-only volume** inside the user container (at `/usr/share/uhd/images`). Thanks to this, any Pod has instant access to the `.hex` files necessary to operate the radio.

### 2.2 IP SDR Discovery (`GetNetworkDevices`)
On the other hand, the discovery of SDR devices over the network is performed in the `pkg/network` package.
The function executes the **`uhd_find_devices`** command on the host, parses the output looking for UHD blocks, and extracts key information using regular expressions: serial number, IP (`addr`), product, and type. It creates a standardized `resourceapi.Device` object (similar to the one returned by USB, but marking `connection_type=ip` and embedding the IP) and returns it to the unified engine for aggregation.

---

## 3. Monitoring Loop (Hotplug and Polling)

To keep the cluster updated against live hardware changes, the `UnifiedDriver` implements a continuous monitoring loop in `PublishResources`:
* **Periodic execution (10 seconds)**: The routine calls `GetNetworkDevices()` and `GetUSBDevices()`.
* **Addition/Removal**: It compares the state and notifies the cluster (`draPlugin.PublishResources`). If an SDR is unplugged, it is removed from the inventory, preventing Kubernetes from assigning Pods to non-existent radios.

---

## 4. Logical Resource Allocation (DRA: `PrepareResourceClaims`)

When a user creates a Pod requesting an SDR, Kubernetes decides which node to send it to and assigns one of the published SDRs. The node's `kubelet` calls the driver's `PrepareResourceClaims` function. In this phase, the driver evaluates the `connection_type` (USB or IP) of the assigned device.

### 4.1 USB Allocation (CDI Generation)
If the SDR is USB, the `pkg/cdi` package kicks in (`CreateClaimSpecFile`).
It dynamically generates a JSON file (CDI Spec) that injects into the container:
1. **Hardware Permissions (Device Nodes):** Grants exclusive Read/Write permissions (`mrw`) over the specific physical character device (e.g., `/dev/bus/usb/001/004`).
2. **Firmware Volume (Mounts):** Independently, it mounts the firmware folder copied on the host into `/usr/share/uhd/images` as a Read-Only (`ro`) volume. This is just a folder with static `.hex` files. We use `ro` because the container only needs to read the firmware to load it, but should not be allowed to modify or delete the host's files.

### 4.2 IP Allocation (Memory Cache)
If the SDR is IP, the driver extracts the antenna's attributes (especially its **IP**) and stores them in an in-memory map (`podDeviceConfig`), associating them with the Pod's UID. This logical operation is done here so the information is instantly available when the container is about to start in the NRI phase.

---

## 5. Low-Level Physical Configuration (NRI Hooks)

This is the most critical part for **network (IP) SDRs**, where the Pod's network is connected to the SDR via NRI (Node Resource Interface), intercepting the *container runtime*.

### 5.1 Environment Variable Injection (`CreateContainer`)
* When the *runtime* is about to create the Pod's container, it calls the NRI `CreateContainer` hook.
* The driver checks its memory (`podDeviceConfig`) to see if that Pod has an assigned IP SDR.
* If it does, it extracts the SDR's IP and injects the environment variable into the container: **`UHD_SDR_IP=<IP_OF_THE_SDR>`**. Thus, the user's application knows exactly which IP to connect to.

### 5.2 Low-Level Network Configuration (`RunPodSandbox` and `ConfigureDeviceForPod`)
Right after the Pod's network "Sandbox" (its *network namespace*) is created, `RunPodSandbox` is called in the `pkg/network` package:
1. The driver figures out via the routing table (`ip route get <IP_OF_THE_SDR>`) which physical interface on the node reaches that SDR.
2. **Creates a Macvlan interface**: It executes `ip link` to create a virtual `macvlan` interface (named `sdr-<uid_start>`) in bridge mode, anchored to the physical one.
3. **Moves the interface to the Pod**: It moves the `macvlan` into the Pod's *network namespace* (`ip link set netns`).
4. **Assigns an IP to the Pod**: It uses the `IPAM` (IP Address Management) to mathematically calculate a free IP within the SDR's same subnet. Using `nsenter`, it enters the Pod's namespace, assigns this IP to the `macvlan` interface, and brings it `up`.
5. *Result*: The Pod has a direct Layer 2 connection to the SDR's network, bypassing NAT or normal Kubernetes routing.

### 5.3 Cleanup and Deletion
When the Pod is shut down or destroyed:
* The driver cleans up logical resources (`UnprepareResourceClaims`), deleting CDI specifications if it was USB, and removing the state from the in-memory map.
* For IP SDRs, it deletes the `macvlan` interface via `nsenter` and frees the IP in the `IPAM` (`ReleaseIP`) so it can be reused by future Pods.

---

## 6. Design Summary
The design of the `dra-driver-ettus-sdr` orchestrates a complex flow by dividing responsibilities:
1. **DRA** handles the logical management and inventory.
2. **CDI** passively injects access to disks and USB peripherals declaratively.
3. **NRI** handles the very low-level network configuration in Linux (creating the Macvlan, moving it into the Pod's network namespace, injecting environment variables).
