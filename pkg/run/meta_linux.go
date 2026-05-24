//go:build linux

package run

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
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

	// Convert ticks to epoch seconds: ticks / CLK_TCK + boot_time.
	var si syscall.Sysinfo_t
	if syscall.Sysinfo(&si) != nil {
		return 0
	}
	clkTck := getClockTicks()
	bootTime := time.Now().Unix() - int64(si.Uptime)
	return bootTime + ticks/clkTck
}
