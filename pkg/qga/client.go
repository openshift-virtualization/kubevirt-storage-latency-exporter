package qga

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/openshift-virtualization/kubevirt-metrics-exporter/pkg/qmp"
)

// ErrCommandBlacklisted is returned when the QGA command is explicitly disabled.
var ErrCommandBlacklisted = fmt.Errorf("guest-agent command is blacklisted")

type execResponse struct {
	Return struct {
		PID int `json:"pid"`
	} `json:"return"`
}

type ExecResult struct {
	Exited       bool
	ExitCode     int
	Signal       int
	Stdout       []byte
	Stderr       []byte
	OutTruncated bool
	ErrTruncated bool
}

type execStatusResponse struct {
	Return struct {
		Exited       bool   `json:"exited"`
		ExitCode     *int   `json:"exitcode,omitempty"`
		Signal       *int   `json:"signal,omitempty"`
		OutData      string `json:"out-data,omitempty"`
		ErrData      string `json:"err-data,omitempty"`
		OutTruncated bool   `json:"out-truncated,omitempty"`
		ErrTruncated bool   `json:"err-truncated,omitempty"`
	} `json:"return"`
}

// isBlacklistError checks if the error indicates the command is disabled in QGA.
func isBlacklistError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "has been disabled") &&
		strings.Contains(msg, "the command is not allowed")
}

// GuestExec spawns a process inside the guest via QGA and returns the PID.
func GuestExec(ctx context.Context, client *qmp.Client, path string, args []string, timeout int32) (int, error) {
	cmd := map[string]any{
		"execute": "guest-exec",
		"arguments": map[string]any{
			"path":           path,
			"arg":            args,
			"capture-output": true,
		},
	}

	cmdJSON, err := json.Marshal(cmd)
	if err != nil {
		return 0, fmt.Errorf("marshaling guest-exec: %w", err)
	}

	result, err := client.AgentCommand(ctx, string(cmdJSON), timeout)
	if err != nil {
		if isBlacklistError(err) {
			return 0, ErrCommandBlacklisted
		}
		return 0, fmt.Errorf("guest-exec: %w", err)
	}

	var resp execResponse
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		return 0, fmt.Errorf("parsing guest-exec response: %w", err)
	}

	if resp.Return.PID == 0 {
		return 0, fmt.Errorf("guest-exec returned PID 0")
	}

	return resp.Return.PID, nil
}

// GuestExecStatus queries the status of a guest-exec process.
func GuestExecStatus(ctx context.Context, client *qmp.Client, pid int, timeout int32) (*ExecResult, error) {
	cmd := map[string]any{
		"execute": "guest-exec-status",
		"arguments": map[string]any{
			"pid": pid,
		},
	}

	cmdJSON, err := json.Marshal(cmd)
	if err != nil {
		return nil, fmt.Errorf("marshaling guest-exec-status: %w", err)
	}

	result, err := client.AgentCommand(ctx, string(cmdJSON), timeout)
	if err != nil {
		if isBlacklistError(err) {
			return nil, ErrCommandBlacklisted
		}
		return nil, fmt.Errorf("guest-exec-status: %w", err)
	}

	var resp execStatusResponse
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		return nil, fmt.Errorf("parsing guest-exec-status response: %w", err)
	}

	er := &ExecResult{
		Exited:       resp.Return.Exited,
		OutTruncated: resp.Return.OutTruncated,
		ErrTruncated: resp.Return.ErrTruncated,
	}

	if resp.Return.ExitCode != nil {
		er.ExitCode = *resp.Return.ExitCode
	}
	if resp.Return.Signal != nil {
		er.Signal = *resp.Return.Signal
	}

	if resp.Return.OutData != "" {
		decoded, err := base64.StdEncoding.DecodeString(resp.Return.OutData)
		if err != nil {
			return nil, fmt.Errorf("decoding stdout: %w", err)
		}
		er.Stdout = decoded
	}

	if resp.Return.ErrData != "" {
		decoded, err := base64.StdEncoding.DecodeString(resp.Return.ErrData)
		if err != nil {
			return nil, fmt.Errorf("decoding stderr: %w", err)
		}
		er.Stderr = decoded
	}

	return er, nil
}

// GuestExecWait polls guest-exec-status until the process exits or maxWait elapses.
// Returns an error if the process does not exit in time or if the output is truncated.
func GuestExecWait(ctx context.Context, client *qmp.Client, pid int, timeout int32, execWait time.Duration) (*ExecResult, error) {
	time.Sleep(execWait)

	maxAttempts := 3
	for attempt := 0; attempt < maxAttempts; attempt++ {
		result, err := GuestExecStatus(ctx, client, pid, timeout)
		if err != nil {
			return nil, err
		}

		if result.Exited {
			if result.OutTruncated {
				return result, fmt.Errorf("guest-exec output truncated (pid %d)", pid)
			}
			return result, nil
		}

		if attempt < maxAttempts-1 {
			time.Sleep(execWait)
		}
	}

	return nil, fmt.Errorf("guest-exec pid %d did not exit after %d attempts", pid, maxAttempts)
}

// GuestDisk represents a disk discovered via the guest-get-disks QGA command.
type GuestDisk struct {
	Name       string      // e.g. "\\\\.\\PhysicalDrive0"
	DriveIndex int         // parsed from Name (e.g. 0)
	PCIAddr    qmp.PCIAddr // PCI controller address from guest
}

type guestGetDisksResponse struct {
	Return []guestDiskEntry `json:"return"`
}

type guestDiskEntry struct {
	Name    string            `json:"name"`
	Address *guestDiskAddress `json:"address,omitempty"`
}

type guestDiskAddress struct {
	BusType       string              `json:"bus-type"`
	PCIController *guestPCIController `json:"pci-controller,omitempty"`
}

type guestPCIController struct {
	Domain   int `json:"domain"`
	Bus      int `json:"bus"`
	Slot     int `json:"slot"`
	Function int `json:"function"`
}

// GuestGetDisks calls the built-in guest-get-disks QGA command and returns
// disk entries with their PCI addresses. Only entries with a parseable
// PhysicalDrive name and valid PCI address are returned.
func GuestGetDisks(ctx context.Context, client *qmp.Client, timeout int32) ([]GuestDisk, error) {
	cmd := `{"execute":"guest-get-disks"}`
	result, err := client.AgentCommand(ctx, cmd, timeout)
	if err != nil {
		if isBlacklistError(err) {
			return nil, ErrCommandBlacklisted
		}
		return nil, fmt.Errorf("guest-get-disks: %w", err)
	}
	return parseGuestGetDisks([]byte(result))
}

func parseGuestGetDisks(data []byte) ([]GuestDisk, error) {
	var resp guestGetDisksResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parsing guest-get-disks response: %w", err)
	}

	var disks []GuestDisk
	for _, entry := range resp.Return {
		if entry.Address == nil || entry.Address.PCIController == nil {
			continue
		}
		idx, ok := parsePhysicalDriveIndex(entry.Name)
		if !ok {
			continue
		}
		pci := entry.Address.PCIController
		disks = append(disks, GuestDisk{
			Name:       entry.Name,
			DriveIndex: idx,
			PCIAddr: qmp.PCIAddr{
				Domain:   pci.Domain,
				Bus:      pci.Bus,
				Slot:     pci.Slot,
				Function: pci.Function,
			},
		})
	}
	return disks, nil
}

// parsePhysicalDriveIndex extracts the numeric index from a Windows
// PhysicalDrive name like "\\\\.\\PhysicalDrive1" -> 1.
func parsePhysicalDriveIndex(name string) (int, bool) {
	const prefix = "PhysicalDrive"
	idx := strings.LastIndex(name, prefix)
	if idx < 0 {
		return 0, false
	}
	numStr := name[idx+len(prefix):]
	n, err := strconv.Atoi(numStr)
	if err != nil {
		return 0, false
	}
	return n, true
}

// wmicCommand is the wmic command and arguments for collecting disk perf counters.
var wmicCommand = struct {
	Path string
	Args []string
}{
	Path: "cmd.exe",
	Args: []string{
		"/c", "wmic", "path", "Win32_PerfRawData_PerfDisk_PhysicalDisk",
		"get", "Name,AvgDiskReadQueueLength,AvgDiskWriteQueueLength,DiskReadsPerSec,DiskWritesPerSec,Timestamp_Sys100NS",
		"/format:csv",
	},
}

// CollectDiskCounters executes the wmic command via QGA and parses the output.
func CollectDiskCounters(ctx context.Context, client *qmp.Client, timeout int32, execWait time.Duration, log *slog.Logger) ([]DiskCounters, error) {
	pid, err := GuestExec(ctx, client, wmicCommand.Path, wmicCommand.Args, timeout)
	if err != nil {
		return nil, err
	}
	log.Debug("qga: guest-exec started", "pid", pid)

	result, err := GuestExecWait(ctx, client, pid, timeout, execWait)
	if err != nil {
		return nil, err
	}
	log.Debug("qga: guest-exec completed",
		"pid", pid, "exitcode", result.ExitCode,
		"stdout_bytes", len(result.Stdout), "stderr_bytes", len(result.Stderr),
		"out_truncated", result.OutTruncated)

	if result.ExitCode != 0 {
		return nil, fmt.Errorf("wmic exited with code %d: %s", result.ExitCode, string(result.Stderr))
	}

	if len(result.Stdout) == 0 {
		return nil, fmt.Errorf("wmic produced no output")
	}

	log.Debug("qga: wmic raw output", "csv", string(result.Stdout))

	return ParseWMICSV(result.Stdout)
}
