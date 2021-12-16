/*
Copyright 2020 The Volcano Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package nvidia

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"path"
	"sort"
	"time"

	"github.com/NVIDIA/gpu-monitoring-tools/bindings/go/nvml"
	"github.com/NVIDIA/go-gpuallocator/gpuallocator"
	v1 "k8s.io/api/core/v1"
	"k8s.io/klog"

	"google.golang.org/grpc"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

// NvidiaDevicePlugin implements the Kubernetes device plugin API
type NvidiaDevicePlugin struct {
	ResourceManager
	resourceName string
	socket       string

	server *grpc.Server
	deviceListEnvvar string
	allocatePolicy   gpuallocator.Policy
	// Physical gpu card
	physicalDevices []*Device
	health          chan *Device
	stop            chan interface{}

	// Virtual devices
	virtualDevices []*pluginapi.Device
	devicesByIndex map[uint]string

	kubeInteractor *KubeInteractor
}

// NewNvidiaDevicePlugin returns an initialized NvidiaDevicePlugin
func NewNvidiaDevicePlugin(resourceName string, resourceManager ResourceManager, deviceListEnvvar string, allocatePolicy gpuallocator.Policy, socket string) *NvidiaDevicePlugin {
	log.Println("Loading NVML")
	if err := nvml.Init(); err != nil {
		log.Printf("Failed to initialize NVML: %s.", err)
		log.Printf("If this is a GPU node, did you set the docker default runtime to `nvidia`?")
		log.Printf("You can check the prerequisites at: https://github.com/volcano-sh/k8s-device-plugin#prerequisites")
		log.Fatalf("You can learn how to set the runtime at: https://github.com/volcano-sh/k8s-device-plugin#quick-start")
	}

	ki, err := NewKubeInteractor()
	if err != nil {
		klog.Fatalf("cannot create kube interactor. %v", err)
	}

	return &NvidiaDevicePlugin{
		ResourceManager:  resourceManager,
		deviceListEnvvar: deviceListEnvvar,
		resourceName:     resourceName,
		socket:           socket,
		allocatePolicy:   allocatePolicy,
		kubeInteractor:  ki,

		// These will be reinitialized every
		// time the plugin server is restarted.
		physicalDevices: nil,
		server:          nil,
		health:          nil,
		stop:            nil,
		virtualDevices:  nil,
		devicesByIndex:  nil,
	}
}

func (m *NvidiaDevicePlugin) initialize() {
	m.physicalDevices = m.Devices()
	m.server = grpc.NewServer([]grpc.ServerOption{}...)
	m.health = make(chan *Device)
	m.stop = make(chan interface{})

	m.virtualDevices, m.devicesByIndex = GetDevices()
}

func (m *NvidiaDevicePlugin) cleanup() {
	close(m.stop)
	m.stop = nil
	m.physicalDevices = nil
	m.server = nil
	m.health = nil
	m.virtualDevices = nil
	m.devicesByIndex = nil
}

func (m *NvidiaDevicePlugin) GetDeviceNameByIndex(index uint) (name string, found bool) {
	if m.devicesByIndex != nil {
		name, ok := m.devicesByIndex[index]
		return name, ok
	}
	return "", false
}

// Name returns the name of the plugin
func (m *NvidiaDevicePlugin) Name() string {
	return "Volcano-GPU-Sharing"
}

// Start starts the gRPC server, registers the device plugin with the Kubelet,
// and starts the device healthchecks.
func (m *NvidiaDevicePlugin) Start() error {
	m.initialize()
	// must be called after initialize
	if err := m.kubeInteractor.PatchGPUResourceOnNode(len(m.devicesByIndex)); err != nil {
		log.Printf("failed to patch gpu resource: %v", err)
		m.cleanup()
		return fmt.Errorf("failed to patch gpu resource: %v", err)
	}

	err := m.Serve()
	if err != nil {
		log.Printf("Could not start device plugin for '%s': %s", m.resourceName, err)
		m.cleanup()
		return err
	}
	log.Printf("Starting to serve '%s' on %s", m.resourceName, m.socket)

	err = m.Register()
	if err != nil {
		log.Printf("Could not register device plugin: %s", err)
		m.Stop()
		return err
	}
	log.Printf("Registered device plugin for '%s' with Kubelet", m.resourceName)

	go m.CheckHealth(m.stop, m.physicalDevices, m.health)

	return nil
}

// Stop stops the gRPC server.
func (m *NvidiaDevicePlugin) Stop() error {
	if m == nil || m.server == nil {
		return nil
	}
	log.Printf("Stopping to serve '%s' on %s", m.resourceName, m.socket)
	m.server.Stop()
	if err := os.Remove(m.socket); err != nil && !os.IsNotExist(err) {
		return err
	}
	m.cleanup()
	return nil
}

func (m *NvidiaDevicePlugin) DevicesNum() int {
	return len(m.Devices())
}

// Serve starts the gRPC server of the device plugin.
func (m *NvidiaDevicePlugin) Serve() error {
	sock, err := net.Listen("unix", m.socket)
	if err != nil {
		return err
	}

	pluginapi.RegisterDevicePluginServer(m.server, m)

	go func() {
		lastCrashTime := time.Now()
		restartCount := 0
		for {
			log.Printf("Starting GRPC server for '%s'", m.resourceName)
			err := m.server.Serve(sock)
			if err == nil {
				break
			}

			log.Printf("GRPC server for '%s' crashed with error: %v", m.resourceName, err)

			// restart if it has not been too often
			// i.e. if server has crashed more than 5 times and it didn't last more than one hour each time
			if restartCount > 5 {
				// quit
				log.Fatal("GRPC server for '%s' has repeatedly crashed recently. Quitting", m.resourceName)
			}
			timeSinceLastCrash := time.Since(lastCrashTime).Seconds()
			lastCrashTime = time.Now()
			if timeSinceLastCrash > 3600 {
				// it has been one hour since the last crash.. reset the count
				// to reflect on the frequency
				restartCount = 1
			} else {
				restartCount += 1
			}
		}
	}()

	// Wait for server to start by launching a blocking connection
	conn, err := m.dial(m.socket, 5*time.Second)
	if err != nil {
		return err
	}
	conn.Close()

	return nil
}

// Register registers the device plugin for the given resourceName with Kubelet.
func (m *NvidiaDevicePlugin) Register() error {
	conn, err := m.dial(pluginapi.KubeletSocket, 5*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()

	client := pluginapi.NewRegistrationClient(conn)
	req := &pluginapi.RegisterRequest{
		Version:      pluginapi.Version,
		Endpoint:     path.Base(m.socket),
		ResourceName: m.resourceName,
	}

	_, err = client.Register(context.Background(), req)
	if err != nil {
		return err
	}
	return nil
}

func (m *NvidiaDevicePlugin) GetDevicePluginOptions(context.Context, *pluginapi.Empty) (*pluginapi.DevicePluginOptions, error) {
	return &pluginapi.DevicePluginOptions{}, nil
}

// ListAndWatch lists devices and update that list according to the health status
func (m *NvidiaDevicePlugin) ListAndWatch(e *pluginapi.Empty, s pluginapi.DevicePlugin_ListAndWatchServer) error {
	err := s.Send(&pluginapi.ListAndWatchResponse{Devices: m.virtualDevices})
	if err != nil {
		log.Fatalf("failed sending devices %d: %v", len(m.virtualDevices), err)
	}

	for {
		select {
		case <-m.stop:
			return nil
		case d := <-m.health:
			// FIXME: there is no way to recover from the Unhealthy state.
			d.Health = pluginapi.Unhealthy
			log.Printf("'%s' device marked unhealthy: %s", m.resourceName, d.ID)
			s.Send(&pluginapi.ListAndWatchResponse{Devices: m.virtualDevices})
		}
	}
}

// TODO(@hzxuzhonghu): This is called per container by kubelet, we do not handle multi containers pod case correctly.
// Allocate which return list of devices.
func (m *NvidiaDevicePlugin) Allocate(ctx context.Context, reqs *pluginapi.AllocateRequest) (*pluginapi.AllocateResponse, error) {
	var reqCount uint
	for _, req := range reqs.ContainerRequests {
		reqCount += uint(len(req.DevicesIDs))
	}

	responses := pluginapi.AllocateResponse{}

	firstContainerReq := reqs.ContainerRequests[0]
	firstContainerReqDeviceCount := uint(len(firstContainerReq.DevicesIDs))

	availablePods := podSlice{}
	pendingPods, err := m.kubeInteractor.GetPendingPodsOnNode()
	if err != nil {
		return nil, err
	}
	for _, pod := range pendingPods {
		current := pod
		if IsGPURequiredPod(&current) && !IsGPUAssignedPod(&current) && !IsShouldDeletePod(&current) {
			availablePods = append(availablePods, &current)
		}
	}

	sort.Sort(availablePods)

	var candidatePod *v1.Pod
	for _, pod := range availablePods {
		for i, c := range pod.Spec.Containers {
			if !IsGPURequiredContainer(&c) {
				continue
			}

			if GetGPUResourceOfContainer(&pod.Spec.Containers[i]) == firstContainerReqDeviceCount {
				klog.Infof("Got candidate Pod %s(%s), the device count is: %d", pod.UID, c.Name, firstContainerReqDeviceCount)
				candidatePod = pod
				goto Allocate
			}
		}
	}

	if candidatePod == nil {
		return nil, fmt.Errorf("failed to find candidate pod")
	}

Allocate:
	id := GetGPUIDFromPodAnnotation(candidatePod)
	if id < 0 {
		klog.Warningf("Failed to get the gpu id for pod %s/%s", candidatePod.Namespace, candidatePod.Name)
		return nil, fmt.Errorf("failed to find gpu id")
	}
	deviceName, exist := m.GetDeviceNameByIndex(uint(id))
	if !exist {
		klog.Warningf("Failed to find the dev for pod %s/%s because it's not able to find dev with index %d",
			candidatePod.Namespace, candidatePod.Name, id)
		return nil, fmt.Errorf("failed to find gpu device")
	}

	for _, req := range reqs.ContainerRequests {
		reqGPU := len(req.DevicesIDs)
		response := pluginapi.ContainerAllocateResponse{
			Envs: map[string]string{
				VisibleDevice:        fmt.Sprintf("%s", deviceName),
				AllocatedGPUResource: fmt.Sprintf("%d", reqGPU),
				TotalGPUResource:     fmt.Sprintf("%d", gpuMemory),
			},
		}
		responses.ContainerResponses = append(responses.ContainerResponses, &response)
	}

	err = UpdatePodAnnotations(m.kubeInteractor.clientset, candidatePod)
	if err != nil {
		return nil, fmt.Errorf("failed to update pod annotation %v", err)
	}

	return &responses, nil

}

func (m *NvidiaDevicePlugin) PreStartContainer(context.Context, *pluginapi.PreStartContainerRequest) (*pluginapi.PreStartContainerResponse, error) {
	return &pluginapi.PreStartContainerResponse{}, nil
}

// dial establishes the gRPC communication with the registered device plugin.
func (m *NvidiaDevicePlugin) dial(unixSocketPath string, timeout time.Duration) (*grpc.ClientConn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	c, err := grpc.DialContext(ctx, unixSocketPath, grpc.WithInsecure(), grpc.WithBlock(),
		grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			return net.DialTimeout("unix", addr, timeout)
		}),
	)

	if err != nil {
		return nil, err
	}

	return c, nil
}

// GetPreferredAllocation returns the preferred allocation from the set of devices specified in the request
func (m *NvidiaDevicePlugin) GetPreferredAllocation(ctx context.Context, r *pluginapi.PreferredAllocationRequest) (*pluginapi.PreferredAllocationResponse, error) {
	response := &pluginapi.PreferredAllocationResponse{}
	for _, req := range r.ContainerRequests {
		available, err := gpuallocator.NewDevicesFrom(req.AvailableDeviceIDs)
		if err != nil {
			return nil, fmt.Errorf("Unable to retrieve list of available devices: %v", err)
		}

		required, err := gpuallocator.NewDevicesFrom(req.MustIncludeDeviceIDs)
		if err != nil {
			return nil, fmt.Errorf("Unable to retrieve list of required devices: %v", err)
		}

		allocated := m.allocatePolicy.Allocate(available, required, int(req.AllocationSize))

		var deviceIds []string
		for _, device := range allocated {
			deviceIds = append(deviceIds, device.UUID)
		}

		resp := &pluginapi.ContainerPreferredAllocationResponse{
			DeviceIDs: deviceIds,
		}

		response.ContainerResponses = append(response.ContainerResponses, resp)
	}
	return response, nil
}
