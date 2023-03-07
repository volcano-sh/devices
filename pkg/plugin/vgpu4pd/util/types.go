/*
 * Copyright © 2021 peizhaoyou <peizhaoyou@4paradigm.com>
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

package util

const (
	//ResourceName = "nvidia.com/gpu"
	//ResourceName = "4pd.io/vgpu"
	AssignedTimeAnnotations          = "volcano.sh/vgpu-time"
	AssignedIDsAnnotations           = "volcano.sh/vgpu-ids-new"
	AssignedIDsToAllocateAnnotations = "volcano.sh/devices-to-allocate"
	AssignedNodeAnnotations          = "volcano.sh/vgpu-node"
	BindTimeAnnotations              = "volcano.sh/bind-time"
	DeviceBindPhase                  = "volcano.sh/bind-phase"

	GPUInUse = "nvidia.com/use-gputype"
	GPUNoUse = "nvidia.com/nouse-gputype"

	DeviceBindAllocating = "allocating"
	DeviceBindFailed     = "failed"
	DeviceBindSuccess    = "success"

	//Set default mem to 5000m
	//DefaultMem   = 5000
	//DefaultCores = 0

	DeviceLimit = 100
	//TimeLayout = "ANSIC"
	//DefaultTimeout = time.Second * 60

	BestEffort string = "best-effort"
	Restricted string = "restricted"
	Guaranteed string = "guaranteed"

	NvidiaGPUDevice     = "NVIDIA"
	NvidiaGPUCommonWord = "GPU"

	NodeLockTime = "volcano.sh/mutex.lock"
	MaxLockRetry = 5

	NodeHandshake              = "volcano.sh/node-vgpu-handshake"
	NodeNvidiaDeviceRegistered = "volcano.sh/node-vgpu-register"

	// DeviceName used to indicate this device
	VGPUDeviceName = "vgpu4pd"
)

var (
	ResourceName          string
	ResourceMem           string
	ResourceCores         string
	ResourceMemPercentage string
	ResourcePriority      string
	DebugMode             bool

	MLUResourceCount  string
	MLUResourceMemory string

	KnownDevice = map[string]string{
		NodeHandshake: NodeNvidiaDeviceRegistered,
	}
)

//	type ContainerDevices struct {
//	   Devices []string `json:"devices,omitempty"`
//	}
//
//	type PodDevices struct {
//	   Containers []ContainerDevices `json:"containers,omitempty"`
//	}
type ContainerDevice struct {
	UUID      string
	Type      string
	Usedmem   int32
	Usedcores int32
}

type ContainerDeviceRequest struct {
	Nums             int32
	Type             string
	Memreq           int32
	MemPercentagereq int32
	Coresreq         int32
}

type ContainerDevices []ContainerDevice

type PodDevices []ContainerDevices

type DeviceInfo struct {
	Id                   string   `protobuf:"bytes,1,opt,name=id,proto3" json:"id,omitempty"`
	Count                int32    `protobuf:"varint,2,opt,name=count,proto3" json:"count,omitempty"`
	Devmem               int32    `protobuf:"varint,3,opt,name=devmem,proto3" json:"devmem,omitempty"`
	Type                 string   `protobuf:"bytes,4,opt,name=type,proto3" json:"type,omitempty"`
	Health               bool     `protobuf:"varint,5,opt,name=health,proto3" json:"health,omitempty"`
	XXX_NoUnkeyedLiteral struct{} `json:"-"`
	XXX_unrecognized     []byte   `json:"-"`
	XXX_sizecache        int32    `json:"-"`
}
