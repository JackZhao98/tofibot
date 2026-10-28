package mcprunner

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// procDir and killPID are replaced by tests.
var (
	procDir = "/proc"
	killPID = func(pid int) error { return syscall.Kill(pid, syscall.SIGKILL) }
)

// processesOf lists the pids whose real, effective, saved or filesystem uid is
// uid. It reads /proc/<pid>/status, which (unlike the owner of /proc/<pid>) does
// not depend on the process being dumpable. Without a /proc (not Linux) it lists
// nothing.
func processesOf(uid int) []int {
	entries, err := os.ReadDir(procDir)
	if err != nil {
		return nil
	}
	var pids []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 1 {
			continue
		}
		if hasUID(filepath.Join(procDir, e.Name(), "status"), uid) {
			pids = append(pids, pid)
		}
	}
	return pids
}

func hasUID(statusFile string, uid int) bool {
	f, err := os.Open(statusFile)
	if err != nil {
		return false
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		rest, ok := strings.CutPrefix(scanner.Text(), "Uid:")
		if !ok {
			continue
		}
		for _, field := range strings.Fields(rest) {
			if n, err := strconv.Atoi(field); err == nil && n == uid {
				return true
			}
		}
		return false
	}
	return false
}

// killAllOf kills every process owned by uid and returns how many it killed.
// It repeats until a scan finds none, because a process can fork between the
// scan and the kill. uid 0 and the Runner's own uid are never touched.
func killAllOf(uid int) int {
	if uid <= 0 || uid == os.Getuid() || uid == os.Geteuid() {
		return 0
	}
	killed := 0
	for round := 0; round < 20; round++ {
		pids := processesOf(uid)
		if len(pids) == 0 {
			return killed
		}
		for _, pid := range pids {
			if killPID(pid) == nil {
				killed++
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return killed
}
