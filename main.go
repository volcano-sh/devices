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

package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/fsnotify/fsnotify"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"

	"volcano.sh/k8s-device-plugin/pkg/filewatcher"
	"volcano.sh/k8s-device-plugin/pkg/plugin"
	"volcano.sh/k8s-device-plugin/pkg/plugin/nvidia"
	"github.com/NVIDIA/go-gpuallocator/gpuallocator"
)

func getAllPlugins() []plugin.DevicePlugin {
	return []plugin.DevicePlugin{
		nvidia.NewNvidiaDevicePlugin(
			nvidia.VolcanoGPUResource,
			nvidia.NewGpuDeviceManager(false),
			nvidia.VisibleDevice,
			gpuallocator.Policy(nil),
			pluginapi.DevicePluginPath + "volcano.sock"),
	}
}

func main() {
	flag.Parse()

	log.Println("Starting file watcher.")
	watcher, err := filewatcher.NewFileWatcher(pluginapi.DevicePluginPath)
	if err != nil {
		log.Printf("Failed to created file watcher: %v", err)
		os.Exit(1)
	}

	log.Println("Retrieving plugins.")
	plugins := getAllPlugins()

	log.Println("Starting OS signal watcher.")
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		select {
		case s := <-sigCh:
			log.Printf("Received signal \"%v\", shutting down.", s)
			for _, p := range plugins {
				p.Stop()
			}
		}
		os.Exit(-1)
	}()

restart:
	// Loop through all plugins, idempotently stopping them, and then starting
	// them if they have any devices to serve. If even one plugin fails to
	// start properly, try starting them all again.
	for _, p := range plugins {
		p.Stop()

		// Just continue if there are no devices to serve for plugin p.
		if p.DevicesNum() == 0 {
			continue
		}

		// Start the gRPC server for plugin p and connect it with the kubelet.
		if err := p.Start(); err != nil {
			log.Printf("Plugin %s failed to start: %v", p.Name(), err)
			log.Printf("You can check the prerequisites at: https://github.com/volcano-sh/k8s-device-plugin#prerequisites")
			log.Printf("You can learn how to set the runtime at: https://github.com/volcano-sh/k8s-device-plugin#quick-start")
			// If there was an error starting any plugins, restart them all.
			goto restart
		}
	}

	// Start an infinite loop, waiting for several indicators to either log
	// some messages, trigger a restart of the plugins, or exit the program.
	for {
		select {
		// Detect a kubelet restart by watching for a newly created
		// 'pluginapi.KubeletSocket' file. When this occurs, restart this loop,
		// restarting all of the plugins in the process.
		case event := <-watcher.Events:
			if event.Name == pluginapi.KubeletSocket && event.Op&fsnotify.Create == fsnotify.Create {
				log.Printf("inotify: %s created, restarting.", pluginapi.KubeletSocket)
				goto restart
			}

		// Watch for any other fs errors and log them.
		case err := <-watcher.Errors:
			log.Printf("inotify: %s", err)
		}
	}
}
