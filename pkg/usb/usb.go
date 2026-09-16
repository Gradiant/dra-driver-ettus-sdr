/*
 * This Source Code Form is subject to the terms of the Mozilla Public
 * License, v. 2.0. If a copy of the MPL was not distributed with this
 * file, You can obtain one at https://mozilla.org/MPL/2.0/.
 */

package usb

import (
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strings"

	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/klog/v2"
)

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

// GetUSBDevices scans the local host's USB buses to discover compatible SDR devices.
// It matches devices against known VendorIDs/ProductIDs and explicitly ignores uninitialized
// chips (like Cypress WestBridge) to allow background firmware flashing to complete first.
// It returns a list of resourceapi.Device objects ready to be published to Kubernetes.
func GetUSBDevices() ([]resourceapi.Device, error) {
	var devices []resourceapi.Device

	files, err := ioutil.ReadDir(SysfsDevices)
	if err != nil {
		klog.Errorf("Failed to read %s: %v", SysfsDevices, err)
		return nil, err
	}

	for _, file := range files {
		if strings.Contains(file.Name(), ":") {
			continue
		}

		devicePath := filepath.Join(SysfsDevices, file.Name())

		vendorFile := filepath.Join(devicePath, VendorFile)
		vendorData, err := ioutil.ReadFile(vendorFile)
		if err != nil {
			continue // Not a valid USB device or permission denied
		}
		vendorID := strings.TrimSpace(string(vendorData))

		productNameFile := filepath.Join(devicePath, "product")
		productNameData, err := ioutil.ReadFile(productNameFile)
		if err == nil && strings.Contains(string(productNameData), "WestBridge") {
			klog.Info("getUSBDevices: Ignoring WestBridge (unflashed Cypress) chip to let the background flasher handle it")
			continue
		}

		// It's a compatible SDR, get details
		productFile := filepath.Join(devicePath, ProductFile)
		productData, _ := ioutil.ReadFile(productFile)
		productID := strings.TrimSpace(string(productData))

		productName := "Undefined"
		if strings.EqualFold(vendorID, EttusVendorID) {
			switch productID {
			case B200ProductID:
				productName = "B200"
			case B200MiniProductID:
				productName = "B200Mini"
			case B205MiniProductID:
				productName = "B205Mini"
			default:
				continue
			}
		} else if strings.EqualFold(vendorID, EttusNiVendorID) {
			switch productID {
			case B200NiProductID:
				productName = "B200"
			case B210NiProductID:
				productName = "B210"
			default:
				continue
			}
		} else {
			continue
		}

		serialFile := filepath.Join(devicePath, "serial")
		serialData, _ := ioutil.ReadFile(serialFile)
		serial := strings.TrimSpace(string(serialData))
		if serial == "" {
			serial = file.Name()
		}

		busFile := filepath.Join(devicePath, "busnum")
		busData, _ := ioutil.ReadFile(busFile)
		bus := strings.TrimSpace(string(busData))

		devnumFile := filepath.Join(devicePath, "devnum")
		devnumData, _ := ioutil.ReadFile(devnumFile)
		devnum := strings.TrimSpace(string(devnumData))

		if bus == "" || devnum == "" {
			klog.Warningf("Found SDR with missing busnum or devnum: %s", devicePath)
			continue
		}

		// The CDI injection path requires /dev/bus/usb/BUS/DEV
		usbNodePath := fmt.Sprintf("/dev/bus/usb/%03s/%03s", bus, devnum)

		deviceIndex := int64(len(devices))
		device := resourceapi.Device{
			Name: strings.ToLower(fmt.Sprintf("usb-%s", serial)),
			Attributes: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
				"sdr.gradiant.org/index": {
					IntValue: &deviceIndex,
				},
				"sdr.gradiant.org/connection_type": {
					StringValue: func(s string) *string { return &s }("usb"),
				},
				"sdr.gradiant.org/vendor_id": {
					StringValue: &vendorID,
				},
				"sdr.gradiant.org/product_id": {
					StringValue: &productID,
				},
				"sdr.gradiant.org/product_name": {
					StringValue: &productName,
				},
				"sdr.gradiant.org/serial": {
					StringValue: &serial,
				},
				"sdr.gradiant.org/usb_device_path": {
					StringValue: func(s string) *string { return &s }(usbNodePath),
				},
			},
		}

		devices = append(devices, device)
		klog.V(4).Infof("Discovered USB SDR device: %s (Model: %s, Path: %s)", serial, productName, usbNodePath)
	}

	return devices, nil
}
