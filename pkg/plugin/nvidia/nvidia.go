/*
 * Copyright (c) 2019, NVIDIA CORPORATION.  All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package nvidia

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/NVIDIA/gpu-monitoring-tools/bindings/go/nvml"

	"k8s.io/klog"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

const (
	envDisableHealthChecks = "DP_DISABLE_HEALTHCHECKS"
	allHealthChecks        = "xids"
)

type Device struct {
	pluginapi.Device
	Path  string
	Index uint
}

type ResourceManager interface {
	GetPluginDevices(devs []*Device) []*pluginapi.Device
	Devices() []*Device
	CheckHealth(stop <-chan struct{}, devices []*Device, unhealthy chan<- *Device)
}

type GpuDeviceManager struct{}

func check(err error) {
	if err != nil {
		log.Panicln("Fatal:", err)
	}
}

func NewGpuDeviceManager() *GpuDeviceManager {
	return &GpuDeviceManager{}
}

func (g *GpuDeviceManager) Devices() []*Device {
	n, err := nvml.GetDeviceCount()
	check(err)

	var devs []*Device
	for i := uint(0); i < n; i++ {
		d, err := nvml.NewDeviceLite(i)
		check(err)
		devs = append(devs, buildDevice(d))
	}

	return devs
}

// GetPluginDevices returns the plugin Devices from all devices in the Devices
func (g *GpuDeviceManager) GetPluginDevices(devs []*Device) []*pluginapi.Device {
	var res []*pluginapi.Device
	for _, d := range devs {
		res = append(res, &d.Device)
	}
	return res
}

func (g *GpuDeviceManager) CheckHealth(stop <-chan struct{}, devices []*Device, unhealthy chan<- *Device) {
	checkHealth(stop, devices, unhealthy)
}

func buildDevice(d *nvml.Device) *Device {
	dev := Device{}
	dev.ID = d.UUID
	dev.Health = pluginapi.Healthy
	dev.Path = d.Path

	_, err := fmt.Sscanf(d.Path, "/dev/nvidia%d", &dev.Index)
	check(err)

	if d.CPUAffinity != nil {
		dev.Topology = &pluginapi.TopologyInfo{
			Nodes: []*pluginapi.NUMANode{
				{
					ID: int64(*(d.CPUAffinity)),
				},
			},
		}
	}
	return &dev
}

func checkHealth(stop <-chan struct{}, devices []*Device, unhealthy chan<- *Device) {
	disableHealthChecks := strings.ToLower(os.Getenv(envDisableHealthChecks))
	if disableHealthChecks == "all" {
		disableHealthChecks = allHealthChecks
	}
	if strings.Contains(disableHealthChecks, "xids") {
		return
	}

	eventSet := nvml.NewEventSet()
	defer nvml.DeleteEventSet(eventSet)

	for _, d := range devices {
		err := nvml.RegisterEventForDevice(eventSet, nvml.XidCriticalError, d.ID)
		if err != nil && strings.HasSuffix(err.Error(), "Not Supported") {
			log.Printf("Warning: %s is too old to support healthchecking: %s. Marking it unhealthy.", d.ID, err)
			unhealthy <- d
			continue
		}
		check(err)
	}

	firstTime := true
	ki, err := NewKubeInteractor()
	if err != nil {
		klog.Fatalf("cannot create kube interactor. %v", err)
	}

	for {
		select {
		case <-stop:
			return
		default:
		}

		e, err := nvml.WaitForEvent(eventSet, 5000)
		if err != nil && e.Etype != nvml.XidCriticalError {
			if firstTime {
				// reset unhealthy gpu list if all devices healthy
				ki.PatchUnhealthyGPUListOnNode(devices)
				firstTime = false
			}
			continue
		}

		// FIXME: formalize the full list and document it.
		// http://docs.nvidia.com/deploy/xid-errors/index.html#topic_4
		// Application errors: the GPU should still be healthy
		if e.Edata == 31 || e.Edata == 43 || e.Edata == 45 {
			if firstTime {
				// reset unhealthy gpu list if all devices healthy
				ki.PatchUnhealthyGPUListOnNode(devices)
				firstTime = false
			}
			continue
		}

		if e.UUID == nil || len(*e.UUID) == 0 {
			// All devices are unhealthy
			log.Printf("XidCriticalError: Xid=%d, All devices will go unhealthy.", e.Edata)
			for _, d := range devices {
				unhealthy <- d
			}
			continue
		}

		for _, d := range devices {
			if d.ID == *e.UUID {
				log.Printf("XidCriticalError: Xid=%d on Device=%s, the device will go unhealthy.", e.Edata, d.ID)
				unhealthy <- d
			}
		}
	}
}
