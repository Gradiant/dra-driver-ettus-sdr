/*
 * This Source Code Form is subject to the terms of the Mozilla Public
 * License, v. 2.0. If a copy of the MPL was not distributed with this
 * file, You can obtain one at https://mozilla.org/MPL/2.0/.
 *
 * Portions of this file were modified from the dra-example-driver
 * project, which is licensed under the Apache License, Version 2.0.
 */

package cdi

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	cdiapi "tags.cncf.io/container-device-interface/pkg/cdi"
	cdiparser "tags.cncf.io/container-device-interface/pkg/parser"
	cdispec "tags.cncf.io/container-device-interface/specs-go"
)

const cdiCommonDeviceName = "common"

var nonWord = regexp.MustCompile(`[^a-zA-Z0-9]+`)

// CDIHandler handles the generation of Container Device Interface (CDI) specifications.
// CDI is a standard for injecting complex devices (like USBs with specific permissions and volumes)
// into containers without requiring privileged mode.
type CDIHandler struct {
	cache      *cdiapi.Cache
	driverName string
	class      string
}

// NewCDIHandler initializes a new CDI cache and handler to write JSON specs to the host.
func NewCDIHandler(root string, driverName, class string) (*CDIHandler, error) {
	cache, err := cdiapi.NewCache(
		cdiapi.WithSpecDirs(root),
	)
	if err != nil {
		return nil, fmt.Errorf("unable to create a new CDI cache: %w", err)
	}
	handler := &CDIHandler{
		cache:      cache,
		driverName: driverName,
		class:      class,
	}

	return handler, nil
}

// CreateCommonSpecFile creates a base CDI spec containing environment variables common to all devices.
func (cdi *CDIHandler) CreateCommonSpecFile() error {
	spec := &cdispec.Spec{
		Kind: cdi.kind(),
		Devices: []cdispec.Device{
			{
				Name: cdiCommonDeviceName,
				ContainerEdits: cdispec.ContainerEdits{
					Env: []string{
						fmt.Sprintf("KUBERNETES_NODE_NAME=%s", os.Getenv("NODE_NAME")),
						fmt.Sprintf("DRA_RESOURCE_DRIVER_NAME=%s", cdi.driverName),
					},
				},
			},
		},
	}

	minVersion, err := cdiapi.MinimumRequiredVersion(spec)
	if err != nil {
		return fmt.Errorf("failed to get minimum required CDI spec version: %v", err)
	}
	spec.Version = minVersion

	specName, err := cdiapi.GenerateNameForTransientSpec(spec, cdiCommonDeviceName)
	if err != nil {
		return fmt.Errorf("failed to generate Spec name: %w", err)
	}

	return cdi.cache.WriteSpec(spec, specName)
}

// CreateClaimSpecFile dynamically generates a transient CDI JSON file for a specific Pod claim.
// It injects read/write (mrw) permissions for the physical USB node and mounts the host's firmware folder.
func (cdi *CDIHandler) CreateClaimSpecFile(claimUID string, deviceName string, usbDevicePath string, attrs map[string]string) (string, error) {
	specName := cdiapi.GenerateTransientSpecName(cdi.vendor(), cdi.class, claimUID)

	deviceEnvKey := strings.ToUpper(nonWord.ReplaceAllString(deviceName, "_"))
	
	envVars := []string{
		fmt.Sprintf("%s_DEVICE_%s_RESOURCE_CLAIM=%s", strings.ToUpper(cdi.class), deviceEnvKey, claimUID),
		fmt.Sprintf("UHD_SDR_USB=%s", usbDevicePath),
	}
	if serial, ok := attrs["serial"]; ok {
		envVars = append(envVars, fmt.Sprintf("SDR_DEVICE_SERIAL=%s", serial))
	}
	if model, ok := attrs["product_name"]; ok {
		envVars = append(envVars, fmt.Sprintf("SDR_DEVICE_MODEL=%s", model))
	} else if prod, ok := attrs["product"]; ok {
		envVars = append(envVars, fmt.Sprintf("SDR_DEVICE_MODEL=%s", prod))
	}

	cdiDevice := cdispec.Device{
		Name: fmt.Sprintf("%s-%s", claimUID, deviceName),
		ContainerEdits: cdispec.ContainerEdits{
			Env: envVars,
			DeviceNodes: []*cdispec.DeviceNode{
				{
					Path:        usbDevicePath,
					Type:        "c",
					Permissions: "mrw",
				},
			},
			Mounts: []*cdispec.Mount{
				{
					HostPath:      "/var/lib/kubelet/plugins/dra-driver-sdr/uhd-images/images",
					ContainerPath: "/usr/share/uhd/images",
					Options:       []string{"ro", "bind"},
				},
			},
		},
	}

	spec := &cdispec.Spec{
		Kind:    cdi.kind(),
		Devices: []cdispec.Device{cdiDevice},
	}

	minVersion, err := cdiapi.MinimumRequiredVersion(spec)
	if err != nil {
		return "", fmt.Errorf("failed to get minimum required CDI spec version: %v", err)
	}
	spec.Version = minVersion

	cdiDeviceID := fmt.Sprintf("%s/%s=%s", cdi.vendor(), cdi.class, cdiDevice.Name)
	return cdiDeviceID, cdi.cache.WriteSpec(spec, specName)
}

// DeleteClaimSpecFile removes the transient CDI specification file from the host
// when the Pod is destroyed, keeping the filesystem clean.
func (cdi *CDIHandler) DeleteClaimSpecFile(claimUID string) error {
	specName := cdiapi.GenerateTransientSpecName(cdi.vendor(), cdi.class, claimUID)
	return cdi.cache.RemoveSpec(specName)
}

func (cdi *CDIHandler) GetClaimDevices(claimUID string, devices []string) []string {
	cdiDevices := []string{
		cdiparser.QualifiedName(cdi.vendor(), cdi.class, cdiCommonDeviceName),
	}

	for _, device := range devices {
		cdiDevice := cdiparser.QualifiedName(cdi.vendor(), cdi.class, fmt.Sprintf("%s-%s", claimUID, device))
		cdiDevices = append(cdiDevices, cdiDevice)
	}

	return cdiDevices
}

func (cdi *CDIHandler) kind() string {
	return cdi.vendor() + "/" + cdi.class
}

func (cdi *CDIHandler) vendor() string {
	return "k8s." + cdi.driverName
}
