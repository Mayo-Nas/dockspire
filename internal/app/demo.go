package app

import (
	"context"
	"time"
)

type demoSource struct{}

func (d demoSource) ProjectContainers(ctx context.Context, project string) ([]Container, error) {
	data, err := d.Snapshot(ctx)
	return filterProjectContainers(data.Containers, project), err
}

func filterProjectContainers(containers []Container, project string) []Container {
	result := []Container{}
	for _, container := range containers {
		if container.Stack == project {
			result = append(result, container)
		}
	}
	return result
}

func (demoSource) Snapshot(context.Context) (Inventory, error) {
	containers := []Container{
		{ID: "demo-1", Name: "notes-web", Image: "example/notes:1.0", State: "running", Status: "Up 2 hours (healthy)", Stack: "notes", Ports: []string{"127.0.0.1:8081 → 8080/tcp"}},
		{ID: "demo-2", Name: "notes-db", Image: "postgres:17", State: "running", Status: "Up 2 hours", Stack: "notes", Ports: []string{"5432/tcp"}},
		{ID: "demo-3", Name: "status", Image: "example/status:1.0", State: "running", Status: "Up 1 day (unhealthy)", Stack: "status", Ports: []string{"127.0.0.1:8082 → 3000/tcp"}},
		{ID: "demo-4", Name: "test-job", Image: "hello-world:latest", State: "exited", Status: "Exited (0)", Ports: []string{}},
	}
	for i := range containers {
		containers[i].Health = containerHealth(containers[i].Status)
	}
	return Inventory{Mode: "demo", UpdatedAt: time.Now().UTC().Format(time.RFC3339), Host: Host{Name: "demo-host", Version: "29.7.2", API: "1.55", OS: "Demo Linux", Architecture: "x86_64", CPUs: 4, Memory: 4 << 30}, Containers: containers, Stacks: groupStacks(containers), Images: []Image{{ID: "demo-image-1", Tags: []string{"example/notes:1.0"}, Size: 145 << 20}, {ID: "demo-image-2", Tags: []string{"postgres:17"}, Size: 430 << 20}, {ID: "demo-image-3", Tags: []string{"example/status:1.0"}, Size: 110 << 20}, {ID: "demo-image-4", Tags: []string{"hello-world:latest"}, Size: 12000}}, Networks: []Network{{ID: "demo-network-1", Name: "bridge", Driver: "bridge", Scope: "local"}, {ID: "demo-network-2", Name: "notes_default", Driver: "bridge", Scope: "local"}}, Volumes: []Volume{{Name: "notes_database", Driver: "local", Scope: "local"}}}, nil
}
