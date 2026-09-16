# Implementing Dynamic Resource Allocation (DRA) for SDR Management in Kubernetes

## What is Dynamic Resource Allocation (DRA)?
Dynamic Resource Allocation (DRA) is the new Kubernetes API designed to natively manage specialized hardware resources. Today, its most well-known use case is the orchestration of graphics cards using [Nvidia's official DRA driver](https://github.com/kubernetes-sigs/dra-driver-nvidia-gpu). However, its architecture is completely agnostic. This means that DRA can be used to orchestrate any type of physical hardware, and in our specific case, we use it to manage Software Defined Radios (SDRs) connected via USB and IP.

In the context of DRA, the state of the cluster is managed transparently. The hardware physically detected by the driver on each node is published in Kubernetes through an object called a `ResourceSlice`. This object tells the orchestrator exactly what resources are available on each server. Later, when users want to use this hardware, Pods make their request through a `ResourceClaimTemplate`. This template automatically generates a unique `ResourceClaim` for that Pod, acting as an exclusive reservation ticket that guarantees the assigned physical resource is not used simultaneously by any other workload.

## Motivation: The Leap from Device Plugins
At Gradiant, the research and validation of communications technologies requires the frequent use of Software Defined Radios (SDRs). To manage this equipment within our Kubernetes cluster, we previously used a [Device Plugin](https://github.com/Gradiant/ettus-device-plugin), which was also developed by the Gradiant team. This development allowed us to integrate SDR antennas into our tests and deployments over the last few years.

However, the Kubernetes ecosystem advances at high speed, and the Device Plugin paradigm carries architectural disadvantages compared to DRA:
1. **Rigidity in allocation**: Device Plugins advertise resources as basic integers (e.g., `sdr: 1`), making it impossible to differentiate between specific antenna models, connection types, or pass configuration parameters during the request.
2. **Lifecycle limitations**: The Device Plugin acts very late in the deployment chain and does not communicate richly with the central scheduler. If a node has no free hardware, the Pod might fail instead of being rescheduled correctly.
3. **Lack of advanced dynamic exclusivity**: Ensuring that multiple containers do not try to collide when accessing the same physical interface is highly complex and rigid to manage at the orchestrator level.
4. **Networked hardware isolation**: Device Plugins cannot securely and dynamically manage IP addresses or inject isolated network interfaces into Pods for IP-based SDRs.

To avoid technological obsolescence in the face of these limitations and to ensure our infrastructure was prepared for future standards, we made the strategic decision to migrate to DRA. Developing this new driver was a natural and necessary step, given the high volume of SDR usage in Gradiant's projects.

## Software Flow and Supported Hardware
To achieve this integration, our system is based on a continuous software flow running on the cluster's servers. The architecture deploys a `DaemonSet` component, meaning there is a control Pod constantly running on each and every physical node. The lifecycle is divided into four main phases:

1. **Hybrid Discovery**: The mission of this `DaemonSet` is to continuously investigate the physical node using two vectors. At the USB level, it loops through the Linux filesystem tree at `/sys/bus/usb/devices`, reading the internal `idVendor` and `idProduct` files of every USB device plugged into the server. At the network level, it executes the `uhd_find_devices` utility directly on the host to accurately discover IP-attached SDRs.
2. **Publishing**: When the agent finds a compatible antenna (either via Vendor ID for USB, or via UHD discovery output for IP), it collects its information and publishes it to the Kubernetes control plane in the form of `ResourceSlices`. 
3. **Scheduling**: When a user deploys an application, the central Kubernetes Scheduler evaluates the available `ResourceSlices`. If it finds a free antenna of the requested class (USB or IP), it pairs the Pod with the corresponding node and logically reserves the resource.
4. **Preparation and Injection (CDI & NRI)**: Finally, once the Pod arrives at the node, the local agent takes action depending on the hardware type:
   * **For USB SDRs**: The driver generates a CDI (Container Device Interface) document. This document instructs the container system to directly inject the physical USB path into the Pod with explicit read/write/create permissions (`mrw`), and mounts the necessary firmware volumes. Thus, the container gains native and secure access to the USB without needing to run in privileged mode.
   * **For IP SDRs**: The driver hooks into the container lifecycle using the Node Resource Interface (NRI). It creates an isolated `macvlan` network interface, dynamically allocates an IP address using an internal IPAM (IP Address Management), and injects it directly into the Pod's network namespace.

The system is programmed to identify a wide family of devices from Ettus Research and National Instruments. Specifically, the driver supports:
* **USB SDRs**: 
  * USRP B200 (Identifier 0020) and USRP B200 Mini (Identifier 0021).
  * USRP B205 Mini (Identifier 0022).
  * USRP B200 Ni (Identifier 7813) and USRP B210 Ni (Identifier 7814) models.
* **IP SDRs**: 
  * USRP X300 and X310 series.

Despite having this wide range of support at the code level, it is important to note that the driver has been exclusively tested and validated in the laboratory using an **Ettus USRP B210** (via USB) and an **Ettus USRP X310** (via IP).

## Workload Deployment
The real advantage of this development lies in how it facilitates the daily work of researchers when deploying their applications. Users simply select the desired `deviceClassName` (`sdr-usb` or `sdr-ip`) in their `ResourceClaimTemplate`.

**1. Resource Claim Template (ResourceClaimTemplate)**
A `ResourceClaimTemplate` allows users to generically define the type of resource the application needs without worrying about low-level topology details.

```yaml
apiVersion: resource.k8s.io/v1
kind: ResourceClaimTemplate
metadata:
  name: single-ip-sdr
spec:
  spec:
    devices:
      requests:
      - name: sdr-device
        exactly:
          deviceClassName: sdr-ip # Change to sdr-usb for USB SDRs
```

**2. Pod Definition**
Next, in the main Pod definition, the developer simply references this template in the `resourceClaims` section.

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: test-sdr-pod
spec:
  containers:
  - name: debian
    image: debian:12-slim
    command: ["bash", "-c"]
    args:
    - |
      apt-get update > /dev/null 2>&1 && DEBIAN_FRONTEND=noninteractive apt-get install -y usbutils uhd-host > /dev/null 2>&1
      echo "=== Discovering SDR Devices ==="
      uhd_find_devices
      sleep infinity
    resources:
      claims:
      - name: sdr
  resourceClaims:
  - name: sdr
    resourceClaimTemplateName: single-ip-sdr
```

Upon instantiating the Pod, Kubernetes reads the `ResourceClaimTemplate` and manufactures a derived `ResourceClaim`. The orchestrator evaluates the available `ResourceSlices` and assigns a free SDR to the Pod. 

If it is an IP SDR, the container starts up with a dedicated Macvlan interface securely connected to the antenna's network. If it's a USB SDR, it starts with direct access to the USB bus. In both cases, the workload can immediately configure its radio frequency parameters and transmit signals without any privileged escalations. 

This entire unified process abstracts the immense complexity of knowing exactly which server the antenna is connected to and what protocol it uses, allowing workloads to be deployed dynamically and efficiently.

## Try it yourself!
To try this out, you can download the code, drivers, and complete examples from the official repository:
