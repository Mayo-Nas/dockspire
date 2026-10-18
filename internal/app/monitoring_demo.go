package app

import (
	"context"
	"fmt"
	"math"
	"time"
)

func (d demoSource) Details(ctx context.Context, id string) (ContainerDetails, error) {
	data, _ := d.Snapshot(ctx)
	for _, item := range data.Containers {
		if item.ID != id {
			continue
		}
		result := ContainerDetails{ID: id, Name: item.Name, Image: item.Image, ImageID: "demo-image", State: item.State, Health: item.Health, Stack: item.Stack, Service: "web", CreatedAt: time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339), StartedAt: time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339), RestartPolicy: "unless-stopped", LogDriver: "json-file", Ports: item.Ports, Mounts: []ContainerMount{{Type: "volume", Source: "demo_data", Destination: "/data", ReadOnly: false}}}
		if item.State != "running" {
			result.FinishedAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
		}
		if result.Health != "none" {
			exit := 0
			if result.Health == "unhealthy" {
				exit = 1
				result.FailingStreak = 3
				result.RestartCount = 2
			}
			result.LastHealthExit = &exit
			result.LastHealthAt = time.Now().Add(-15 * time.Second).UTC().Format(time.RFC3339)
		}
		return result, nil
	}
	return ContainerDetails{}, monitorError{404, "Demo container not found"}
}
func (d demoSource) Logs(ctx context.Context, id string, options LogOptions) (ContainerLogs, error) {
	detail, err := d.Details(ctx, id)
	if err != nil {
		return ContainerLogs{}, err
	}
	now := time.Now().UTC()
	result := ContainerLogs{Lines: []LogLine{}, CheckedAt: now.Format(time.RFC3339Nano)}
	for i := 0; i < 12; i++ {
		line := LogLine{Time: now.Add(time.Duration(i-12) * 10 * time.Second).Format(time.RFC3339Nano), Stream: "stdout", Text: fmt.Sprintf("[sample] %s request completed: status=200", detail.Name)}
		if detail.Health == "unhealthy" && i%3 == 0 {
			line.Stream = "stderr"
			line.Text = "[sample] Health check failed: upstream connection timed out"
		}
		result.Lines = append(result.Lines, line)
	}
	if len(result.Lines) > options.Tail {
		result.Lines = result.Lines[len(result.Lines)-options.Tail:]
	}
	return result, nil
}
func (d demoSource) Stats(ctx context.Context, id string) (ContainerStats, error) {
	detail, err := d.Details(ctx, id)
	if err != nil {
		return ContainerStats{}, err
	}
	if detail.State != "running" {
		return ContainerStats{Available: false}, nil
	}
	now := time.Now()
	cpu := 4 + math.Sin(float64(now.Unix())/5)*3
	return ContainerStats{Available: true, ReadAt: now.UTC().Format(time.RFC3339Nano), CPUPercent: &cpu, MemoryUsage: uint64(70+cpu) * 1024 * 1024, MemoryLimit: 256 << 20, MemoryPercent: (70 + cpu) / 256 * 100, NetworkRx: uint64(now.Unix()) * 1024, NetworkTx: uint64(now.Unix()) * 512, BlockRead: uint64(now.Unix()) * 4096, BlockWrite: uint64(now.Unix()) * 2048, PIDs: 7}, nil
}
