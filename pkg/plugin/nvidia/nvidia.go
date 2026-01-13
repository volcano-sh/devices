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
	"bytes"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/NVIDIA/go-nvml/pkg/nvml"

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

func NewGpuDeviceManager() *GpuDeviceManager {
	return &GpuDeviceManager{}
}

func (g *GpuDeviceManager) Devices() []*Device {
	count, ret := nvmllib.DeviceGetCount()
	if ret != nvml.SUCCESS {
		klog.Fatalf("Failed to get device count: %s", nvmllib.ErrorString(ret))
	}

	var devs []*Device
	for i := 0; i < count; i++ {
		device, ret := nvmllib.DeviceGetHandleByIndex(i)
		if ret != nvml.SUCCESS {
			klog.Fatalf("Failed to get device handle by index %d: %s", i, nvmllib.ErrorString(ret))
		}

		devs = append(devs, buildDevice(device, i))
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

func buildDevice(d nvml.Device, devIndex int) *Device {
	uuid, ret := nvmllib.DeviceGetUUID(d)
	if ret != nvml.SUCCESS {
		klog.Fatalf("Failed to get UUID of device: %s", nvmllib.ErrorString(ret))
	}

	minor, ret := nvmllib.DeviceGetMinorNumber(d)
	if ret != nvml.SUCCESS {
		klog.Fatalf("Failed to get minor number of device: %s", nvmllib.ErrorString(ret))
	}
	path := fmt.Sprintf("/dev/nvidia%d", minor)

	dev := Device{}
	dev.ID = uuid
	dev.Health = pluginapi.Healthy
	dev.Path = path
	dev.Index = uint(devIndex)
	hasNuma, numa, err := getNumaNode(d)
	if err != nil {
		klog.Fatalf("Failed to get device NUMA node: %v", err)
	}

	if hasNuma {
		dev.Topology = &pluginapi.TopologyInfo{
			Nodes: []*pluginapi.NUMANode{
				{
					ID: int64(numa),
				},
			},
		}
	}

	return &dev
}

func getNumaNode(d nvml.Device) (bool, int, error) {
	pciInfo, ret := d.GetPciInfo()
	if ret != nvml.SUCCESS {
		return false, 0, fmt.Errorf("error getting PCI Bus Info of device: %v", ret)
	}

	// Discard leading zeros.
	busID := strings.ToLower(strings.TrimPrefix(int8Slice(pciInfo.BusId[:]).String(), "0000"))

	b, err := os.ReadFile(fmt.Sprintf("/sys/bus/pci/devices/%s/numa_node", busID))
	if err != nil {
		return false, 0, nil
	}

	node, err := strconv.Atoi(string(bytes.TrimSpace(b)))
	if err != nil {
		return false, 0, fmt.Errorf("eror parsing value for NUMA node: %v", err)
	}

	if node < 0 {
		return false, 0, nil
	}

	return true, node, nil
}

func checkHealth(stop <-chan struct{}, devices []*Device, unhealthy chan<- *Device) {
	disableHealthChecks := strings.ToLower(os.Getenv(envDisableHealthChecks))
	if disableHealthChecks == "all" {
		disableHealthChecks = allHealthChecks
	}
	if strings.Contains(disableHealthChecks, "xids") {
		return
	}

	// FIXME: formalize the full list and document it.
	// http://docs.nvidia.com/deploy/xid-errors/index.html#topic_4
	// Application errors: the GPU should still be healthy
	applicationErrorXids := []uint64{
		13, // Graphics Engine Exception
		31, // GPU memory page fault
		43, // GPU stopped processing
		45, // Preemptive cleanup, due to previous errors
		68, // Video processor exception
	}

	skippedXids := make(map[uint64]bool)
	for _, id := range applicationErrorXids {
		skippedXids[id] = true
	}

	for _, additionalXid := range getAdditionalXids(disableHealthChecks) {
		skippedXids[additionalXid] = true
	}

	eventSet, ret := nvmllib.EventSetCreate()
	if ret != nvml.SUCCESS {
		klog.Warningf("could not create event set: %v", ret)
		return
	}
	defer eventSet.Free()

	eventMask := uint64(nvml.EventTypeXidCriticalError | nvml.EventTypeDoubleBitEccError | nvml.EventTypeSingleBitEccError)
	parentToDeviceMap := make(map[string]*Device)
	for _, d := range devices {
		parentToDeviceMap[d.ID] = d

		gpu, ret := nvmllib.DeviceGetHandleByUUID(d.ID)
		if ret != nvml.SUCCESS {
			klog.Infof("unable to get device handle from UUID: %v; marking it as unhealthy", ret)
			unhealthy <- d
			continue
		}

		supportedEvents, ret := gpu.GetSupportedEventTypes()
		if ret != nvml.SUCCESS {
			klog.Infof("Unable to determine the supported events for %v: %v; marking it as unhealthy", d.ID, ret)
			unhealthy <- d
			continue
		}

		ret = gpu.RegisterEvents(eventMask&supportedEvents, eventSet)
		if ret == nvml.ERROR_NOT_SUPPORTED {
			klog.Warningf("Device %v is too old to support healthchecking.", d.ID)
		}
		if ret != nvml.SUCCESS {
			klog.Infof("Marking device %v as unhealthy: %v", d.ID, ret)
			unhealthy <- d
		}
	}

	for {
		select {
		case <-stop:
			return
		default:
		}

		e, ret := eventSet.Wait(5000)
		if ret == nvml.ERROR_TIMEOUT {
			continue
		}
		if ret != nvml.SUCCESS {
			klog.Infof("Error waiting for event: %v; Marking all devices as unhealthy", ret)
			for _, d := range devices {
				unhealthy <- d
			}
			continue
		}

		if e.EventType != nvml.EventTypeXidCriticalError {
			klog.Infof("Skipping non-nvmlEventTypeXidCriticalError event: %+v", e)
			continue
		}

		if skippedXids[e.EventData] {
			klog.Infof("Skipping event %+v", e)
			continue
		}

		klog.Infof("Processing event %+v", e)
		eventUUID, ret := e.Device.GetUUID()
		if ret != nvml.SUCCESS {
			// If we cannot reliably determine the device UUID, we mark all devices as unhealthy.
			klog.Infof("Failed to determine uuid for event %v: %v; Marking all devices as unhealthy.", e, ret)
			for _, d := range devices {
				unhealthy <- d
			}
			continue
		}

		d, exists := parentToDeviceMap[eventUUID]
		if !exists {
			klog.Infof("Ignoring event for unexpected device: %v", eventUUID)
			continue
		}

		klog.Infof("XidCriticalError: Xid=%d on Device=%s; marking device as unhealthy.", e.EventData, d.ID)
		unhealthy <- d
	}
}

// getAdditionalXids returns a list of additional Xids to skip from the specified string.
// The input is treaded as a comma-separated string and all valid uint64 values are considered as Xid values. Invalid values
// are ignored.
func getAdditionalXids(input string) []uint64 {
	if input == "" {
		return nil
	}

	var additionalXids []uint64
	for _, additionalXid := range strings.Split(input, ",") {
		trimmed := strings.TrimSpace(additionalXid)
		if trimmed == "" {
			continue
		}
		xid, err := strconv.ParseUint(trimmed, 10, 64)
		if err != nil {
			log.Printf("Ignoring malformed Xid value %v: %v", trimmed, err)
			continue
		}
		additionalXids = append(additionalXids, xid)
	}

	return additionalXids
}
