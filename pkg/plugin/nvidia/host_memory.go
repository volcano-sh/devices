package nvidia

import (
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"k8s.io/klog/v2"
)

// GetHostMemory runs `free -b` and parses total/used/free (in bytes),
// supporting both English and localized (e.g. Chinese) output.
func GetHostMemory() (uint64, error) {
	cmd := exec.Command("free", "-b")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("failed to run 'free -b': %v, output: %s", err, out.String())
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) < 2 {
		return 0, fmt.Errorf("unexpected 'free' output: %s", out.String())
	}

	// find the first line that looks like data (numeric second field)
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		// skip header lines (contain non-digit in second field)
		if _, err := strconv.ParseUint(strings.ReplaceAll(fields[1], ",", ""), 10, 64); err != nil {
			continue
		}

		// found the data line
		total, err1 := strconv.ParseUint(fields[1], 10, 64)
		used, err2 := strconv.ParseUint(fields[2], 10, 64)
		free, err3 := strconv.ParseUint(fields[3], 10, 64)
		if err1 != nil || err2 != nil || err3 != nil {
			return 0, fmt.Errorf("parse error: %v %v %v", err1, err2, err3)
		}

		klog.Infof("get system memory total: %dGB, used: %dGB, free: %dGB", total/1024/1024/1024, used/1024/1024/1024, free/1024/1024/1024)
		return total, nil
	}

	return 0, fmt.Errorf("no memory data line found in 'free' output")
}
