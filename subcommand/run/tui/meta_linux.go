//go:build linux

package tui

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

func init() {
	// Override the Linux implementation at init time.
	GetProcessStartTime = getProcessStartTimeLinuxImpl
}

func getProcessStartTimeLinuxImpl(pid int) int64 {
	if pid <= 0 {
		return 0
	}

	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0
	}

	// Fields are space-separated, but field 2 (comm) may contain spaces
	// enclosed in parentheses. Skip past the closing ')' to parse correctly.
	s := string(data)
	closeParen := strings.LastIndex(s, ")")
	if closeParen < 0 || closeParen+2 >= len(s) {
		return 0
	}
	fields := strings.Fields(s[closeParen+2:])
	// After skipping fields 1 (pid) and 2 (comm), field indices shift:
	// Fields: 3=state, 4=ppid, 5=pgrp, ..., 22=starttime
	// So starttime is at index 22-3 = 19.
	const starttimeField = 19
	if len(fields) <= starttimeField {
		return 0
	}

	ticks, err := strconv.ParseInt(fields[starttimeField], 10, 64)
	if err != nil {
		return 0
	}

	// Read boot time (btime) directly from /proc/stat.
	// Previously we computed bootTime as now - Sysinfo.Uptime, but that has
	// a race condition: time.Now().Unix() and Sysinfo.Uptime (both second-
	// precision) can tick over at different moments, causing ±1 second jitter
	// in the result. This made IsRunning() flaky — the same PID could appear
	// alive or dead on different invocations of ai ls.
	//
	// The btime field in /proc/stat is a fixed value set at system boot,
	// eliminating the race entirely.
	btime, err := readProcStatBtime()
	if err != nil {
		return 0
	}

	clkTck := getClockTicks()
	return btime + ticks/clkTck
}

// readProcStatBtime reads the boot time (btime) field from /proc/stat.
func readProcStatBtime() (int64, error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "btime") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				return 0, fmt.Errorf("btime field missing value in /proc/stat")
			}
			btime, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil {
				return 0, fmt.Errorf("parse btime: %w", err)
			}
			return btime, nil
		}
	}
	return 0, fmt.Errorf("btime not found in /proc/stat")
}
