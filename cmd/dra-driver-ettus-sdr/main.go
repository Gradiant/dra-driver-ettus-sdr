/*
 * This Source Code Form is subject to the terms of the Mozilla Public
 * License, v. 2.0. If a copy of the MPL was not distributed with this
 * file, You can obtain one at https://mozilla.org/MPL/2.0/.
 *
 * Portions of this file were modified from the kubernetes-network-driver-basic
 * project, which is licensed under the Apache License, Version 2.0.
 */

package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime/debug"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"golang.org/x/sys/unix"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	nodeutil "k8s.io/component-helpers/node/util"
	"k8s.io/klog/v2"

	"github.com/Gradiant/dra-driver-ettus-sdr/pkg/driver"
)

const (
	driverName = "sdr.gradiant.org"
)

var (
	hostnameOverride string
	kubeconfig       string
	bindAddress      string

	ready atomic.Bool
)

func init() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "absolute path to the kubeconfig file")
	flag.StringVar(&bindAddress, "bind-address", ":9177", "The IP address and port for the metrics and healthz server to serve on")
	flag.StringVar(&hostnameOverride, "hostname-override", "", "If non-empty, will be used as the name of the Node is running on. If unset, the node name is assumed to be the same as the node's hostname.")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n\n", driverName)
		flag.PrintDefaults()
	}
}

func main() {
	klog.InitFlags(nil)
	flag.Parse()

	printVersion()
	flag.VisitAll(func(f *flag.Flag) {
		klog.Infof("FLAG: --%s=%q", f.Name, f.Value)
	})

	mux := http.NewServeMux()
	// Add healthz handler
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if !ready.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusOK)
		}
	})
	// Add metrics handler
	mux.Handle("/metrics", promhttp.Handler())
	go func() {
		_ = http.ListenAndServe(bindAddress, mux)
	}()

	// Copy UHD firmware images to the host so udev can flash WestBridge and the driver can mount it
	hostUHDImagesPath := "/var/lib/kubelet/plugins/dra-driver-sdr/uhd-images/images"
	klog.Infof("Copying UHD firmware to host path %s...", hostUHDImagesPath)
	// Dynamically find the images directory to avoid hardcoded paths that change between OS versions
	cmd := fmt.Sprintf("mkdir -p %s && UHD_DIR=$(find /usr /opt /lib -type f -name 'usrp_b200_fw.hex' | head -n 1 | xargs dirname) && if [ ! -z \"$UHD_DIR\" ]; then cp -an \"$UHD_DIR\"/. %s/; else echo 'Firmware not found in container'; exit 1; fi", hostUHDImagesPath, hostUHDImagesPath)
	err := exec.Command("sh", "-c", cmd).Run()
	if err != nil {
		klog.Warningf("Failed to copy UHD images to host: %v", err)
	}

	var config *rest.Config
	var errConfig error
	if kubeconfig != "" {
		config, errConfig = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		// creates the in-cluster config
		config, errConfig = rest.InClusterConfig()
	}
	if errConfig != nil {
		klog.Fatalf("can not create client-go configuration: %v", errConfig)
	}

	// use protobuf for better performance at scale
	// https://kubernetes.io/docs/reference/using-api/api-concepts/#alternate-representations-of-resources
	config.AcceptContentTypes = "application/vnd.kubernetes.protobuf,application/json"
	config.ContentType = "application/vnd.kubernetes.protobuf"

	// creates the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Fatalf("can not create client-go client: %v", err)
	}

	nodeName, err := nodeutil.GetHostname(hostnameOverride)
	if err != nil {
		klog.Fatalf("can not obtain the node name, use the hostname-override flag if you want to set it to a specific value: %v", err)
	}

	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)

	// Enable signal handler
	signalCh := make(chan os.Signal, 2)
	defer func() {
		close(signalCh)
		cancel()
	}()
	signal.Notify(signalCh, os.Interrupt, unix.SIGINT)

	opts := []driver.Option{}

	knd, err := driver.Start(ctx, driverName, clientset, nodeName, opts...)
	if err != nil {
		klog.Fatalf("driver failed to start: %v", err)
	}
	defer knd.Stop()
	ready.Store(true)
	klog.Info("driver started")

	select {
	case <-signalCh:
		klog.Infof("Exiting: received signal")
		cancel()
	case <-ctx.Done():
		klog.Infof("Exiting: context cancelled")
	}
}

func printVersion() {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	var vcsRevision, vcsTime string
	for _, f := range info.Settings {
		switch f.Key {
		case "vcs.revision":
			vcsRevision = f.Value
		case "vcs.time":
			vcsTime = f.Value
		}
	}
	klog.Infof("%s go %s build: %s time: %s", driverName, info.GoVersion, vcsRevision, vcsTime)
}
