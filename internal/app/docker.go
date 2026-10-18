package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Inventory struct {
	Mode       string      `json:"mode"`
	UpdatedAt  string      `json:"updatedAt"`
	Host       Host        `json:"host"`
	Containers []Container `json:"containers"`
	Stacks     []Stack     `json:"stacks"`
	Images     []Image     `json:"images"`
	Networks   []Network   `json:"networks"`
	Volumes    []Volume    `json:"volumes"`
	Warnings   []string    `json:"warnings,omitempty"`
}
type Host struct {
	Name         string `json:"name"`
	Version      string `json:"version"`
	API          string `json:"api"`
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	CPUs         int    `json:"cpus"`
	Memory       int64  `json:"memory"`
}
type Container struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Image            string   `json:"image"`
	ImageID          string   `json:"-"`
	State            string   `json:"state"`
	Status           string   `json:"status"`
	Health           string   `json:"health"`
	Stack            string   `json:"stack"`
	Service          string   `json:"service,omitempty"`
	Ports            []string `json:"ports"`
	ComposeFiles     string   `json:"-"`
	ComposeDirectory string   `json:"-"`
	ComposeOneOff    string   `json:"-"`
}
type Stack struct {
	Name       string `json:"name"`
	Containers int    `json:"containers"`
	Running    int    `json:"running"`
	State      string `json:"state"`
	Source     string `json:"source"`
	Definition string `json:"definition"`
	Origin     string `json:"origin,omitempty"`
	ImportID   string `json:"importId,omitempty"`
}
type Image struct {
	ID   string   `json:"id"`
	Tags []string `json:"tags"`
	Size int64    `json:"size"`
}
type Network struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Driver   string `json:"driver"`
	Scope    string `json:"scope"`
	Internal bool   `json:"internal"`
}
type Volume struct {
	Name   string `json:"name"`
	Driver string `json:"driver"`
	Scope  string `json:"scope"`
}

type dockerSource struct {
	client *http.Client
	base   string
}

func newDockerSource(socket string) *dockerSource {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socket)
	}}
	return &dockerSource{client: &http.Client{Transport: transport, Timeout: 10 * time.Second}, base: "http://docker"}
}

func (d *dockerSource) get(ctx context.Context, path string, out any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, d.base+path, nil)
	if err != nil {
		return fmt.Errorf("Cannot prepare Docker request")
	}
	response, err := d.client.Do(request)
	if err != nil {
		return fmt.Errorf("Docker is unavailable. Check the local socket and permissions, or start with --demo")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Docker inventory request failed (HTTP %d)", response.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(out); err != nil {
		return fmt.Errorf("Docker returned an invalid inventory response")
	}
	return nil
}

func parseAPI(value string) (int, error) {
	major, minor, ok := strings.Cut(value, ".")
	n, err := strconv.Atoi(minor)
	if !ok || major != "1" || err != nil || n < 0 || n > 999 {
		return 0, fmt.Errorf("Docker reported an unsupported API version")
	}
	return n, nil
}

func negotiateAPI(maximum, minimum string) (string, error) {
	const clientMin, clientMax = 44, 55
	max, err := parseAPI(maximum)
	if err != nil {
		return "", err
	}
	min := 0
	if minimum != "" {
		min, err = parseAPI(minimum)
		if err != nil {
			return "", err
		}
	}
	if min > max || max < clientMin || min > clientMax {
		return "", fmt.Errorf("Docker API compatibility check failed; this preview supports API 1.44 through 1.55")
	}
	if max > clientMax {
		max = clientMax
	}
	return fmt.Sprintf("1.%d", max), nil
}

func (d *dockerSource) Snapshot(ctx context.Context) (Inventory, error) {
	result := Inventory{Mode: "live", UpdatedAt: time.Now().UTC().Format(time.RFC3339), Containers: []Container{}, Stacks: []Stack{}, Images: []Image{}, Networks: []Network{}, Volumes: []Volume{}}
	var version struct{ Version, ApiVersion, MinAPIVersion string }
	if err := d.get(ctx, "/version", &version); err != nil {
		return result, err
	}
	api, err := negotiateAPI(version.ApiVersion, version.MinAPIVersion)
	if err != nil {
		return result, err
	}
	prefix := "/v" + api
	var info struct {
		Name, OperatingSystem, Architecture string
		NCPU                                int
		MemTotal                            int64
	}
	if err = d.get(ctx, prefix+"/info", &info); err != nil {
		return result, err
	}
	result.Host = Host{Name: info.Name, Version: version.Version, API: api, OS: info.OperatingSystem, Architecture: info.Architecture, CPUs: info.NCPU, Memory: info.MemTotal}
	result.Containers, err = d.readContainers(ctx, prefix, "")
	if err != nil {
		return result, err
	}
	result.Stacks = groupStacks(result.Containers)
	var images []struct {
		Id       string
		RepoTags []string
		Size     int64
	}
	if err = d.get(ctx, prefix+"/images/json", &images); err != nil {
		return result, err
	}
	for _, image := range images {
		tags := image.RepoTags
		if len(tags) == 0 {
			tags = []string{"<untagged>"}
		}
		result.Images = append(result.Images, Image{ID: image.Id, Tags: tags, Size: image.Size})
	}
	sort.Slice(result.Images, func(i, j int) bool { return result.Images[i].Tags[0] < result.Images[j].Tags[0] })
	var networks []struct {
		Id, Name, Driver, Scope string
		Internal                bool
	}
	if err = d.get(ctx, prefix+"/networks", &networks); err != nil {
		return result, err
	}
	for _, network := range networks {
		result.Networks = append(result.Networks, Network{ID: network.Id, Name: network.Name, Driver: network.Driver, Scope: network.Scope, Internal: network.Internal})
	}
	sort.Slice(result.Networks, func(i, j int) bool { return result.Networks[i].Name < result.Networks[j].Name })
	var volumes struct {
		Volumes []struct{ Name, Driver, Scope string }
	}
	if err = d.get(ctx, prefix+"/volumes", &volumes); err != nil {
		return result, err
	}
	for _, volume := range volumes.Volumes {
		result.Volumes = append(result.Volumes, Volume{Name: volume.Name, Driver: volume.Driver, Scope: volume.Scope})
	}
	sort.Slice(result.Volumes, func(i, j int) bool { return result.Volumes[i].Name < result.Volumes[j].Name })
	return result, nil
}

// Safety decisions always read fresh containers, including stopped and one-off
// containers. Unrelated inventory endpoints are not dependencies of this read.
func (d *dockerSource) ProjectContainers(ctx context.Context, project string) ([]Container, error) {
	if !projectName.MatchString(project) {
		return nil, fmt.Errorf("Invalid Compose project name")
	}
	prefix, err := d.monitorPrefix(ctx)
	if err != nil {
		return nil, err
	}
	return d.readContainers(ctx, prefix, project)
}

func (d *dockerSource) readContainers(ctx context.Context, prefix, project string) ([]Container, error) {
	query := "all=true"
	if project != "" {
		filters, _ := json.Marshal(map[string][]string{"label": {"com.docker.compose.project=" + project}})
		query += "&filters=" + url.QueryEscape(string(filters))
	}
	var containers []struct {
		Id                            string
		Names                         []string
		Image, ImageID, State, Status string
		Labels                        map[string]string
		Ports                         []struct {
			IP                      string
			PrivatePort, PublicPort int
			Type                    string
		}
	}
	if err := d.get(ctx, prefix+"/containers/json?"+query, &containers); err != nil {
		return nil, err
	}
	result := []Container{}
	for _, raw := range containers {
		if project != "" && raw.Labels["com.docker.compose.project"] != project {
			continue
		}
		name := raw.Id
		if len(raw.Names) > 0 {
			name = strings.TrimPrefix(raw.Names[0], "/")
		}
		container := Container{ID: raw.Id, Name: name, Image: raw.Image, ImageID: raw.ImageID, State: raw.State, Status: raw.Status, Stack: raw.Labels["com.docker.compose.project"], Ports: []string{}}
		container.Health = containerHealth(raw.Status)
		container.ComposeFiles = raw.Labels["com.docker.compose.project.config_files"]
		container.ComposeDirectory = raw.Labels["com.docker.compose.project.working_dir"]
		container.Service = raw.Labels["com.docker.compose.service"]
		container.ComposeOneOff = raw.Labels["com.docker.compose.oneoff"]
		for _, port := range raw.Ports {
			binding := fmt.Sprintf("%d/%s", port.PrivatePort, port.Type)
			if port.PublicPort > 0 {
				binding = fmt.Sprintf("%s → %s", net.JoinHostPort(port.IP, strconv.Itoa(port.PublicPort)), binding)
			}
			container.Ports = append(container.Ports, binding)
		}
		result = append(result, container)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func groupStacks(containers []Container) []Stack {
	groups := map[string]*Stack{}
	for _, container := range containers {
		if container.Stack == "" {
			continue
		}
		if groups[container.Stack] == nil {
			groups[container.Stack] = &Stack{Name: container.Stack, Source: "Discovered · definition not imported", Definition: "Not imported"}
		}
		stack := groups[container.Stack]
		if match := portainerFile.FindStringSubmatch(container.ComposeFiles); len(match) == 2 {
			stack.ImportID = match[1]
		}
		stack.Containers++
		if container.State == "running" {
			stack.Running++
		}
	}
	result := []Stack{}
	for _, stack := range groups {
		stack.State = "stopped"
		if stack.Running == stack.Containers {
			stack.State = "running"
		} else if stack.Running > 0 {
			stack.State = "partial"
		}
		result = append(result, *stack)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}
