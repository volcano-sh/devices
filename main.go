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
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/fsnotify/fsnotify"
	cli "github.com/urfave/cli/v2"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"

	apis "volcano.sh/k8s-device-plugin/pkg/apis"
	"volcano.sh/k8s-device-plugin/pkg/filewatcher"
	"volcano.sh/k8s-device-plugin/pkg/plugin"
	"volcano.sh/k8s-device-plugin/pkg/plugin/nvidia"
)

func loadConfig(c *cli.Context, flags []cli.Flag) (*apis.Config, error) {
	config, err := apis.NewConfig(c, flags)
	if err != nil {
		return nil, fmt.Errorf("unable to finalize config: %v", err)
	}
	return config, nil
}

func getAllPlugins(c *cli.Context, flags []cli.Flag) ([]plugin.DevicePlugin, error) {
	// Load the configuration file
	log.Println("Loading configuration.")
	config, err := loadConfig(c, flags)
	if err != nil {
		return nil, fmt.Errorf("unable to load config: %v", err)
	}

	// Print the config to the output.
	configJSON, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal config to JSON: %v", err)
	}
	log.Printf("\nRunning with config:\n%v", string(configJSON))

	return []plugin.DevicePlugin{
		nvidia.NewNvidiaDevicePlugin(config),
	}, nil
}

var version string

func main() {
	var configFile string

	c := cli.NewApp()
	c.Version = version
	c.Action = func(ctx *cli.Context) error {
		return start(ctx, c.Flags)
	}

	c.Flags = []cli.Flag{
		&cli.StringFlag{
			Name:    "gpu-strategy",
			Value:   "share",
			Usage:   "the default strategy is using shared GPU devices while using 'number' meaning using GPUs individually. [number| share]",
			EnvVars: []string{"GPU_STRATEGY"},
		},
		&cli.UintFlag{
			Name:    "gpu-memory-factor",
			Value:   1,
			Usage:   "the default gpu memory block size is 1MB",
			EnvVars: []string{"GPU_MEMORY_FACTOR"},
		},
		&cli.StringFlag{
			Name:        "config-file",
			Usage:       "the path to a config file as an alternative to command line options or environment variables",
			Destination: &configFile,
			EnvVars:     []string{"CONFIG_FILE"},
		},
	}

	err := c.Run(os.Args)
	if err != nil {
		log.SetOutput(os.Stderr)
		log.Printf("Error: %v", err)
		os.Exit(1)
	}
}

func start(c *cli.Context, flags []cli.Flag) error {
	watcher, err := filewatcher.NewFileWatcher(pluginapi.DevicePluginPath)
	if err != nil {
		log.Printf("Failed to created file watcher: %v", err)
		os.Exit(1)
	}

	log.Println("Retrieving plugins.")
	plugins, err := getAllPlugins(c, flags)
	if err != nil {
		log.Printf("Failed to retrieving plugins: %v", err)
		os.Exit(1)
	}

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
