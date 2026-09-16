# DRA Resource Driver for Ettus SDRs (USB & IP)

This project is a Kubernetes resource driver designed to seamlessly integrate Software Defined Radios (SDRs) into cluster workloads. It leverages the [Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/) API to provide dynamic hardware provisioning.

The driver automatically discovers and allocates **Ettus Research USRP** devices to Kubernetes Pods, bridging the gap between radio frequency hardware and cloud-native deployments. It provides native support for:
* **USB-attached Radios** (e.g., USRP B210)
* **Network-attached IP Radios** (e.g., USRP X310)

When a workload requests an SDR, the driver handles the underlying device attachment and initialization.

## Documentation & Architecture

For a deep dive into how this driver works (including hybrid hardware discovery, firmware volume injection via CDI, and low-level Linux Macvlan networking via NRI), please read the **[Architecture Guide](docs/architecture.md)**.

## Prerequisites

* **Kubernetes v1.31+**: Dynamic Resource Allocation (DRA) is a modern Kubernetes feature. This driver specifically uses the `resource.k8s.io/v1` API group.
* **NRI (Node Resource Interface)**: Must be enabled in the container runtime to allow the driver to intercept container creation and inject Macvlan network interfaces for IP SDRs. The driver will **automatically attempt to enable NRI** if it detects `containerd`. If a different container runtime (e.g., CRI-O) is used, NRI must be enabled manually in the runtime's configuration file.
* **IP SDR Topology Requirement (Direct-Attach)**: To avoid IP Address Management (IPAM) collisions, IP-based SDRs **MUST** be deployed in an isolated, Point-to-Point subnet (e.g., a dedicated NIC on the host or a dedicated VLAN). The driver's internal IPAM is static and not designed for shared DHCP networks.

## Building the Driver Image

First, clone the repository and navigate to its root directory:

```bash
git clone https://github.com/Gradiant/dra-driver-ettus-sdr
cd dra-driver-ettus-sdr
```

Build and push the Docker image to your registry:

```bash
docker build -f deployments/container/Dockerfile -t your-registry/dra-driver-ettus-sdr:v0.1.0 .
docker push your-registry/dra-driver-ettus-sdr:v0.1.0
```

*(Alternatively, you can simply use `make image` and `make push` from the root directory if you want to use the default `Makefile` variables).*

## Installation

### Method 1: Helm Chart (Recommended)

The standard and most robust way to deploy the driver is using the provided Helm chart. 

```bash
# Package the chart
helm package deployments/helm/dra-driver-ettus-sdr -d deployments/helm/

# Install the chart in your cluster
helm install dra-driver-ettus-sdr deployments/helm/dra-driver-ettus-sdr-0.1.0.tgz \
  --namespace kube-system \
  --set config.image.repository=your-registry/dra-driver-ettus-sdr \
  --set config.image.tag=v0.1.0 \
  --set config.image.pullSecrets={your-registry-secret}
```

### Method 2: Static Manifest

If you prefer not to use Helm, you can install the driver using the raw static Kubernetes manifest:

> **Important:** Before applying the manifest, edit `deployments/manifests/install.yaml` to update the `image` field to point to your container registry, and update or remove the `imagePullSecrets` according to your cluster's configuration.

```bash
kubectl apply -f deployments/manifests/install.yaml
```

Verify that the components are running correctly:

```console
$ kubectl get pods -n kube-system -l app=dra-driver-ettus-sdr
NAME                         READY   STATUS    RESTARTS   AGE
dra-driver-ettus-sdr-6mwgt   1/1     Running   0          38s
dra-driver-ettus-sdr-cwjcv   1/1     Running   0          38s
dra-driver-ettus-sdr-f76vb   1/1     Running   0          38s
```

You can also check if your SDR devices have been successfully discovered and announced to the cluster:

```console
$ kubectl get resourceslice
NAME                                      NODE         DRIVER                      POOL         AGE
00000-sdr.gradiant.org-larry-prg99        larry        sdr.gradiant.org            larry        111m
00000-sdr.gradiant.org-mario-h9dvh        mario        sdr.gradiant.org            mario        111m
00000-sdr.gradiant.org-solidsnake-4qbb5   solidsnake   sdr.gradiant.org            solidsnake   111m
```

To see the full details of the discovered SDRs (such as connection type, model, and serial number), you can describe the slices.

**Example: IP SDR (X310):**
```console
$ kubectl describe resourceslices.resource.k8s.io 00000-sdr.gradiant.org-mario-h9dvh 
Name:         00000-sdr.gradiant.org-mario-h9dvh
Namespace:    
Labels:       <none>
Annotations:  <none>
API Version:  resource.k8s.io/v1
Kind:         ResourceSlice
Metadata:
  Creation Timestamp:  2026-08-18T11:01:09Z
  Generate Name:       00000-sdr.gradiant.org-mario-
  Generation:          1
  Owner References:
    API Version:     v1
    Controller:      true
    Kind:            Node
    Name:            mario
    UID:             f445724b-0890-4539-b489-c6fc6107a7b8
  Resource Version:  411944978
  UID:               e5330dc5-61fb-4bcd-866e-11b5478442f4
Spec:
  Devices:
    Attributes:
      sdr.gradiant.org/connection_type:
        String:  ip
      sdr.gradiant.org/ip_address:
        String:  192.168.10.2
      sdr.gradiant.org/product:
        String:  X310
      sdr.gradiant.org/serial:
        String:  344F47C
      sdr.gradiant.org/type:
        String:  x300
    Name:        344f47c
  Driver:        sdr.gradiant.org
  Node Name:     mario
  Pool:
    Generation:            1
    Name:                  mario
    Resource Slice Count:  1
Events:                    <none>
```

**Example: USB SDR (B200):**
```console
$ kubectl describe resourceslices.resource.k8s.io 00000-sdr.gradiant.org-solidsnake-4qbb5 
Name:         00000-sdr.gradiant.org-solidsnake-4qbb5
Namespace:    
Labels:       <none>
Annotations:  <none>
API Version:  resource.k8s.io/v1
Kind:         ResourceSlice
Metadata:
  Creation Timestamp:  2026-08-18T11:01:10Z
  Generate Name:       00000-sdr.gradiant.org-solidsnake-
  Generation:          1
  Owner References:
    API Version:     v1
    Controller:      true
    Kind:            Node
    Name:            solidsnake
    UID:             ead95616-e3d2-4774-9e31-417752c3a567
  Resource Version:  411945002
  UID:               3cc99189-ee97-4451-bc69-67cefc34e8ad
Spec:
  Devices:
    Attributes:
      sdr.gradiant.org/connection_type:
        String:  usb
      sdr.gradiant.org/index:
        Int:  0
      sdr.gradiant.org/product_id:
        String:  0020
      sdr.gradiant.org/product_name:
        String:  B200
      sdr.gradiant.org/serial:
        String:  31D4A76
      sdr.gradiant.org/usb_device_path:
        String:  /dev/bus/usb/002/014
      sdr.gradiant.org/vendor_id:
        String:  2500
    Name:        usb-31d4a76
  Driver:        sdr.gradiant.org
  Node Name:     solidsnake
  Pool:
    Generation:            1
    Name:                  solidsnake
    Resource Slice Count:  1
Events:                    <none>
```

## Usage & Examples

To request an SDR in your workloads, you must define a `ResourceClaimTemplate` and reference it in your `Pod` specification. You can choose whether you want a USB SDR or an IP SDR by selecting the appropriate `deviceClassName` (`sdr-usb` or `sdr-ip`).

> **Note:** The complete YAML manifests for these examples can be found in the [`examples/`](examples) directory.

### Example 1: Requesting a USB SDR (e.g., B210)

```yaml
apiVersion: resource.k8s.io/v1
kind: ResourceClaimTemplate
metadata:
  name: sdr-usb-claim
spec:
  spec:
    devices:
      requests:
      - name: req-1
        exactly:
          deviceClassName: sdr-usb
---
apiVersion: v1
kind: Pod
metadata:
  name: test-sdr-usb
spec:
  containers:
  - name: debian
    image: debian:12-slim
    command: ["bash", "-c"]
    args:
    - |
      apt-get update > /dev/null 2>&1 && DEBIAN_FRONTEND=noninteractive apt-get install -y usbutils uhd-host > /dev/null 2>&1
      echo "=== FIND DEVICES ==="
      uhd_find_devices
      sleep infinity
    resources:
      claims:
      - name: sdr
  resourceClaims:
  - name: sdr
    resourceClaimTemplateName: sdr-usb-claim
```

### Example 2: Requesting an IP SDR (e.g., X310)

```yaml
apiVersion: resource.k8s.io/v1
kind: ResourceClaimTemplate
metadata:
  name: sdr-ip-claim
spec:
  spec:
    devices:
      requests:
      - name: req-1
        exactly:
          deviceClassName: sdr-ip
---
apiVersion: v1
kind: Pod
metadata:
  name: test-sdr-ip
spec:
  containers:
  - name: debian
    image: debian:12-slim
    command: ["bash", "-c"]
    args:
    - |
      apt-get update > /dev/null 2>&1 && DEBIAN_FRONTEND=noninteractive apt-get install -y iproute2 uhd-host > /dev/null 2>&1
      echo "=== FIND DEVICES ==="
      uhd_find_devices
      sleep infinity
    resources:
      claims:
      - name: sdr
  resourceClaims:
  - name: sdr
    resourceClaimTemplateName: sdr-ip-claim
```

When the Pod starts, the DRA driver will allocate the corresponding device. For USB SDRs, it mounts the USB device node and firmware directly into the container. For IP SDRs, it creates an isolated Macvlan network interface and injects the `UHD_SDR_IP` environment variable so the container can communicate natively with the physical antenna.

You can verify that the library successfully finds the SDR by checking the logs of the Pods:

**USB SDR Logs:**
```console
$ kubectl logs test-sdr-usb 
=== FIND DEVICES ===
[INFO] [UHD] linux; GNU C++ version 11.2.0; Boost_107400; UHD_4.1.0.5-3
--------------------------------------------------
-- UHD Device 0
--------------------------------------------------
Device Address:
    serial: 31D4A76
    name: MyB210
    product: B210
    type: b200
```

**IP SDR Logs:**
```console
$ kubectl logs test-sdr-ip
=== FIND DEVICES ===
[INFO] [UHD] linux; GNU C++ version 11.2.0; Boost_107400; UHD_4.1.0.5-3
--------------------------------------------------
-- UHD Device 0
--------------------------------------------------
Device Address:
    serial: 344F47C
    addr: 192.168.10.2
    fpga: HG
    name: 
    product: X310
    type: x300
```

### 5G Deployment Example (srsRAN + Open5GS)

To see a real-world, end-to-end example of how to use this driver to deploy a complete 5G network (using srsRAN as the radio over the SDR and Open5GS as the core network), please check the **[`srsran-example` directory](examples/srsran-example/)**. 

Inside, you will find a complete **[User Guide](examples/srsran-example/USER_GUIDE.md)** detailing the deployment of the Open5GS operator, the core network CRDs, and the gNB (radio).

## Uninstallation

To remove the driver from the cluster, use the command corresponding to the installation method:

**If installed via Helm:**
```bash
helm uninstall dra-driver-ettus-sdr --namespace kube-system
```

**If installed via Static Manifest:**
```bash
kubectl delete -f deployments/manifests/install.yaml
```

## References

This project has been developed based on the following repositories:
* [kubernetes-network-driver-basic](https://github.com/aojea/kubernetes-network-driver-basic/), by Antonio Ojea (for the implementation of the NRI Macvlan network).
* [dra-example-driver](https://github.com/kubernetes-sigs/dra-example-driver), by the Kubernetes SIGs (for the DRA framework architecture).
## License

This project is distributed under the terms of the [Mozilla Public License, v. 2.0 (MPL-2.0)](LICENSE). 

However, portions of the code in this repository are derived from other projects licensed under the Apache License, Version 2.0. For full attribution and details on modified files, please refer to the [NOTICE](NOTICE) file.
