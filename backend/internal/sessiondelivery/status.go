package sessiondelivery

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type HostStatus struct {
	Hostname             string  `json:"hostname"`
	CPUCount             int     `json:"cpu_count"`
	CPUUsedPercent       float64 `json:"cpu_used_percent"`
	Load1                float64 `json:"load_1"`
	Load5                float64 `json:"load_5"`
	Load15               float64 `json:"load_15"`
	MemoryTotalBytes     int64   `json:"memory_total_bytes"`
	MemoryUsedBytes      int64   `json:"memory_used_bytes"`
	MemoryAvailableBytes int64   `json:"memory_available_bytes"`
	SwapTotalBytes       int64   `json:"swap_total_bytes"`
	SwapUsedBytes        int64   `json:"swap_used_bytes"`
	DiskTotalBytes       int64   `json:"disk_total_bytes"`
	DiskUsedBytes        int64   `json:"disk_used_bytes"`
	DiskAvailableBytes   int64   `json:"disk_available_bytes"`
	DiskUsedPercent      float64 `json:"disk_used_percent"`
	UptimeSeconds        int64   `json:"uptime_seconds"`
}

type DatabaseStatus struct {
	Status            string `json:"status"`
	SizeBytes         int64  `json:"size_bytes"`
	ConnectionsActive int    `json:"connections_active"`
	ConnectionsTotal  int    `json:"connections_total"`
	ConnectionsMax    int    `json:"connections_max"`
	Partitions        int    `json:"partitions"`
}

type SessionDataStatus struct {
	RecordsInDatabase      int64      `json:"records_in_database"`
	DeliverableInDatabase  int64      `json:"deliverable_in_database"`
	RejectedInDatabase     int64      `json:"rejected_in_database"`
	PayloadBytesInDatabase int64      `json:"payload_bytes_in_database"`
	CurrentHourRecords     int64      `json:"current_hour_records"`
	RecordsLast5Minutes    int64      `json:"records_last_5m"`
	FirstIngestedAt        *time.Time `json:"first_ingested_at,omitempty"`
	LastIngestedAt         *time.Time `json:"last_ingested_at,omitempty"`
}

type DeliveryStatus struct {
	ArchiveFilesVerified int64      `json:"archive_files_verified"`
	ArchiveBytesUploaded int64      `json:"archive_bytes_uploaded"`
	RecordsArchived      int64      `json:"records_archived"`
	DeliveriesArchived   int64      `json:"deliveries_archived"`
	RejectedArchived     int64      `json:"rejected_archived"`
	FailedBatches        int64      `json:"failed_batches"`
	ExportingBatches     int64      `json:"exporting_batches"`
	LastVerifiedAt       *time.Time `json:"last_verified_at,omitempty"`
}

type RecentBatchStatus struct {
	Hour           time.Time  `json:"hour"`
	Status         string     `json:"status"`
	RecordCount    int64      `json:"record_count"`
	DeliveryCount  int64      `json:"delivery_count"`
	RejectedCount  int64      `json:"rejected_count"`
	ArchiveBackend string     `json:"archive_backend,omitempty"`
	ArchiveSize    int64      `json:"archive_size"`
	StartedAt      time.Time  `json:"started_at"`
	ArchivedAt     *time.Time `json:"archived_at,omitempty"`
	VerifiedAt     *time.Time `json:"verified_at,omitempty"`
	PurgedAt       *time.Time `json:"purged_at,omitempty"`
}

type StatusSnapshot struct {
	Status        string              `json:"status"`
	ObservedAt    time.Time           `json:"observed_at"`
	Warnings      []string            `json:"warnings"`
	Host          HostStatus          `json:"host"`
	Database      DatabaseStatus      `json:"database"`
	Sessions      SessionDataStatus   `json:"sessions"`
	Delivery      DeliveryStatus      `json:"delivery"`
	RecentBatches []RecentBatchStatus `json:"recent_batches"`
}

type HostStatusCollector interface {
	Collect(context.Context, string) (HostStatus, error)
}

type LinuxHostStatusCollector struct {
	mu       sync.Mutex
	previous cpuSample
}

type cpuSample struct {
	total uint64
	idle  uint64
}

func (c *LinuxHostStatusCollector) Collect(_ context.Context, diskPath string) (HostStatus, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return HostStatus{}, fmt.Errorf("read hostname: %w", err)
	}
	load, err := readLoadAverage("/proc/loadavg")
	if err != nil {
		return HostStatus{}, err
	}
	memory, err := readMemoryStatus("/proc/meminfo")
	if err != nil {
		return HostStatus{}, err
	}
	uptime, err := readUptime("/proc/uptime")
	if err != nil {
		return HostStatus{}, err
	}
	disk, err := readDiskStatus(diskPath)
	if err != nil {
		return HostStatus{}, err
	}
	cpu, err := readCPUSample("/proc/stat")
	if err != nil {
		return HostStatus{}, err
	}
	c.mu.Lock()
	usedPercent := cpuUsedPercent(c.previous, cpu)
	c.previous = cpu
	c.mu.Unlock()
	return HostStatus{
		Hostname:             hostname,
		CPUCount:             runtime.NumCPU(),
		CPUUsedPercent:       usedPercent,
		Load1:                load[0],
		Load5:                load[1],
		Load15:               load[2],
		MemoryTotalBytes:     memory.total,
		MemoryUsedBytes:      memory.used,
		MemoryAvailableBytes: memory.available,
		SwapTotalBytes:       memory.swapTotal,
		SwapUsedBytes:        memory.swapUsed,
		DiskTotalBytes:       disk.total,
		DiskUsedBytes:        disk.used,
		DiskAvailableBytes:   disk.available,
		DiskUsedPercent:      disk.usedPercent,
		UptimeSeconds:        uptime,
	}, nil
}

func BuildStatusSnapshot(ctx context.Context, store *Store, collector HostStatusCollector, diskPath string, rejectDiskUsage int) (StatusSnapshot, error) {
	if store == nil || store.db == nil {
		return StatusSnapshot{}, errors.New("Session status store is required")
	}
	if collector == nil {
		return StatusSnapshot{}, errors.New("Session host status collector is required")
	}
	host, err := collector.Collect(ctx, diskPath)
	if err != nil {
		return StatusSnapshot{}, err
	}
	database, err := store.Status(ctx)
	if err != nil {
		return StatusSnapshot{}, err
	}
	batches, err := store.RecentExportBatches(ctx, 12)
	if err != nil {
		return StatusSnapshot{}, err
	}
	warnings := make([]string, 0, 4)
	status := "healthy"
	if rejectDiskUsage <= 0 || rejectDiskUsage > 100 {
		rejectDiskUsage = 75
	}
	if host.DiskUsedPercent >= float64(rejectDiskUsage) {
		status = "critical"
		warnings = append(warnings, "disk_guard_reached")
	} else if host.DiskUsedPercent >= float64(rejectDiskUsage-5) {
		status = "degraded"
		warnings = append(warnings, "disk_usage_high")
	}
	if database.FailedBatches > 0 {
		if status == "healthy" {
			status = "degraded"
		}
		warnings = append(warnings, "archive_batches_failed")
	}
	if database.ExportingBatches > 0 {
		if status == "healthy" {
			status = "degraded"
		}
		warnings = append(warnings, "archive_in_progress")
	}
	recent := make([]RecentBatchStatus, 0, len(batches))
	for _, batch := range batches {
		recent = append(recent, RecentBatchStatus{
			Hour:           batch.Hour,
			Status:         batch.Status,
			RecordCount:    batch.RecordCount,
			DeliveryCount:  batch.DeliveryCount,
			RejectedCount:  batch.RejectedCount,
			ArchiveBackend: batch.ArchiveBackend,
			ArchiveSize:    batch.ArchiveSize,
			StartedAt:      batch.StartedAt,
			ArchivedAt:     batch.ArchivedAt,
			VerifiedAt:     batch.VerifiedAt,
			PurgedAt:       batch.PurgedAt,
		})
	}
	return StatusSnapshot{
		Status:     status,
		ObservedAt: time.Now().UTC(),
		Warnings:   warnings,
		Host:       host,
		Database: DatabaseStatus{
			Status:            "healthy",
			SizeBytes:         database.DatabaseSizeBytes,
			ConnectionsActive: database.ConnectionsActive,
			ConnectionsTotal:  database.ConnectionsTotal,
			ConnectionsMax:    database.ConnectionsMax,
			Partitions:        database.Partitions,
		},
		Sessions: SessionDataStatus{
			RecordsInDatabase:      database.RecordsInDatabase,
			DeliverableInDatabase:  database.DeliverableInDatabase,
			RejectedInDatabase:     database.RejectedInDatabase,
			PayloadBytesInDatabase: database.PayloadBytesInDatabase,
			CurrentHourRecords:     database.CurrentHourRecords,
			RecordsLast5Minutes:    database.RecordsLast5Minutes,
			FirstIngestedAt:        database.FirstIngestedAt,
			LastIngestedAt:         database.LastIngestedAt,
		},
		Delivery: DeliveryStatus{
			ArchiveFilesVerified: database.ArchiveFilesVerified,
			ArchiveBytesUploaded: database.ArchiveBytesUploaded,
			RecordsArchived:      database.RecordsArchived,
			DeliveriesArchived:   database.DeliveriesArchived,
			RejectedArchived:     database.RejectedArchived,
			FailedBatches:        database.FailedBatches,
			ExportingBatches:     database.ExportingBatches,
			LastVerifiedAt:       database.LastVerifiedAt,
		},
		RecentBatches: recent,
	}, nil
}

func readCPUSample(path string) (cpuSample, error) {
	file, err := os.Open(path)
	if err != nil {
		return cpuSample{}, fmt.Errorf("read CPU status: %w", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		return cpuSample{}, errors.New("CPU status is empty")
	}
	fields := strings.Fields(scanner.Text())
	if len(fields) < 8 || fields[0] != "cpu" {
		return cpuSample{}, errors.New("CPU status is malformed")
	}
	values := make([]uint64, 0, len(fields)-1)
	for _, field := range fields[1:] {
		value, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return cpuSample{}, fmt.Errorf("parse CPU status: %w", err)
		}
		values = append(values, value)
	}
	var total uint64
	for _, value := range values {
		total += value
	}
	idle := values[3]
	if len(values) > 4 {
		idle += values[4]
	}
	return cpuSample{total: total, idle: idle}, nil
}

func cpuUsedPercent(previous, current cpuSample) float64 {
	if previous.total == 0 || current.total <= previous.total || current.idle < previous.idle {
		return 0
	}
	totalDelta := current.total - previous.total
	idleDelta := current.idle - previous.idle
	if idleDelta >= totalDelta {
		return 0
	}
	return float64(totalDelta-idleDelta) * 100 / float64(totalDelta)
}

func readLoadAverage(path string) ([3]float64, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return [3]float64{}, fmt.Errorf("read load average: %w", err)
	}
	fields := strings.Fields(string(content))
	if len(fields) < 3 {
		return [3]float64{}, errors.New("load average is malformed")
	}
	var values [3]float64
	for index := range values {
		values[index], err = strconv.ParseFloat(fields[index], 64)
		if err != nil {
			return [3]float64{}, fmt.Errorf("parse load average: %w", err)
		}
	}
	return values, nil
}

type memoryStatus struct {
	total     int64
	available int64
	used      int64
	swapTotal int64
	swapUsed  int64
}

func readMemoryStatus(path string) (memoryStatus, error) {
	file, err := os.Open(path)
	if err != nil {
		return memoryStatus{}, fmt.Errorf("read memory status: %w", err)
	}
	defer file.Close()
	values := make(map[string]int64)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		value, parseErr := strconv.ParseInt(fields[1], 10, 64)
		if parseErr != nil {
			continue
		}
		values[strings.TrimSuffix(fields[0], ":")] = value * 1024
	}
	if err := scanner.Err(); err != nil {
		return memoryStatus{}, fmt.Errorf("scan memory status: %w", err)
	}
	total := values["MemTotal"]
	available := values["MemAvailable"]
	if total <= 0 || available < 0 || available > total {
		return memoryStatus{}, errors.New("memory status is malformed")
	}
	swapTotal := values["SwapTotal"]
	swapFree := values["SwapFree"]
	return memoryStatus{
		total:     total,
		available: available,
		used:      total - available,
		swapTotal: swapTotal,
		swapUsed:  maxInt64(0, swapTotal-swapFree),
	}, nil
}

func readUptime(path string) (int64, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read uptime: %w", err)
	}
	fields := strings.Fields(string(content))
	if len(fields) == 0 {
		return 0, errors.New("uptime is malformed")
	}
	seconds, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || seconds < 0 {
		return 0, errors.New("uptime is malformed")
	}
	return int64(seconds), nil
}

type diskStatus struct {
	total       int64
	used        int64
	available   int64
	usedPercent float64
}

func readDiskStatus(path string) (diskStatus, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(strings.TrimSpace(path), &stat); err != nil {
		return diskStatus{}, fmt.Errorf("read Session disk status: %w", err)
	}
	if stat.Blocks == 0 {
		return diskStatus{}, errors.New("Session disk reports zero blocks")
	}
	total := int64(stat.Blocks) * int64(stat.Bsize)
	available := int64(stat.Bavail) * int64(stat.Bsize)
	used := total - available
	return diskStatus{
		total:       total,
		used:        used,
		available:   available,
		usedPercent: float64(used) * 100 / float64(total),
	}, nil
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}
