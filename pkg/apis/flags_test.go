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
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnmarshalFlags(t *testing.T) {
	testCases := []struct {
		input  string
		output Flags
		err    bool
	}{
		{
			input: ``,
			err:   true,
		},
		{
			input:  `{}`,
			output: Flags{},
		},
		{
			input: `{
				"GPUStrategy": "number"
			}`,
			output: Flags{
				&CommandLineFlags{
					GPUStrategy: "number",
				},
			},
		},
	}

	for i, tc := range testCases {
		t.Run(fmt.Sprintf("test case %d", i), func(t *testing.T) {
			var output Flags
			err := json.Unmarshal([]byte(tc.input), &output)
			if tc.err {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.output, output)
		})
	}
}

func TestMarshalFlags(t *testing.T) {
	testCases := []struct {
		input  Flags
		output string
		err    bool
	}{
		{
			input: Flags{
				&CommandLineFlags{
					GPUStrategy: "number",
				},
			},
			output: `{
				"GPUStrategy": "number"
			}`,
		},
	}

	for i, tc := range testCases {
		t.Run(fmt.Sprintf("test case %d", i), func(t *testing.T) {
			output, err := json.Marshal(tc.input)
			if tc.err {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.JSONEq(t, tc.output, string(output))
		})
	}
}
