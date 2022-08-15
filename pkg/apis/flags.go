/*
Copyright 2022 The Volcano Authors.

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

package apis

import (
	cli "github.com/urfave/cli/v2"
)

// Flags holds the full list of flags used to configure the device plugin and GFD.
type Flags struct {
	*CommandLineFlags
}

// CommandLineFlags holds the list of command line flags used to configure the device plugin and GFD.
type CommandLineFlags struct {
	GPUStrategy     string `json:"GPUStrategy"                yaml:"GPUStrategy"`
	GPUMemoryFactor uint   `json:"GPUMemoryFactor"                yaml:"GPUMemoryFactor"`
}

func NewCommandLineFlags(c *cli.Context) *CommandLineFlags {
	return &CommandLineFlags{
		GPUStrategy:     c.String("gpu-strategy"),
		GPUMemoryFactor: c.Uint("gpu-memory-factor"),
	}
}
