package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Takeover metadata contains resource identities, never environment values.
// The original import remains immutable; Revision identifies the private,
// image-pinned snapshot used for the first handoff and interrupted recovery.
type TakeoverState struct {
	SourceFile        string              `json:"sourceFile"`
	State             string              `json:"state"`
	Revision          string              `json:"revision"`
	SourceFingerprint string              `json:"sourceFingerprint"`
	Containers        []TakeoverContainer `json:"containers"`
}

type TakeoverMount struct {
	Type     string `json:"type"`
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"readOnly"`
}

type TakeoverContainer struct {
	Hostname   string            `json:"-"`
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Service    string            `json:"service"`
	Image      string            `json:"image"`
	ConfigHash string            `json:"configHash"`
	Mounts     []TakeoverMount   `json:"mounts"`
	Networks   map[string]string `json:"networks"`
	Ports      string            `json:"ports"`
}

type takeoverInspector interface {
	TakeoverContainer(context.Context, string) (TakeoverContainer, error)
}

type publicStackRecord struct {
	StackRecord
	Takeover       *TakeoverState `json:"takeover,omitempty"`
	Managed        bool           `json:"managed"`
	TakeoverStatus string         `json:"takeoverStatus,omitempty"`
}

func stackRecordView(record StackRecord) publicStackRecord {
	status := ""
	if record.Takeover != nil {
		status = record.Takeover.State
	}
	return publicStackRecord{StackRecord: record, Managed: record.managed(), TakeoverStatus: status}
}

func publicStackValue(value any) any {
	switch v := value.(type) {
	case StackRecord:
		return stackRecordView(v)
	case []StackRecord:
		out := make([]publicStackRecord, 0, len(v))
		for _, r := range v {
			out = append(out, stackRecordView(r))
		}
		return out
	case StackDefinition:
		return struct {
			publicStackRecord
			Compose          string `json:"compose"`
			RecoveryRequired bool   `json:"recoveryRequired,omitempty"`
		}{stackRecordView(v.StackRecord), v.Compose, v.RecoveryRequired}
	}
	return value
}

func (d *dockerSource) TakeoverContainer(ctx context.Context, id string) (TakeoverContainer, error) {
	if !revisionName.MatchString(id) {
		return TakeoverContainer{}, errors.New("Invalid container identity")
	}
	prefix, err := d.monitorPrefix(ctx)
	if err != nil {
		return TakeoverContainer{}, err
	}
	var raw struct {
		ID, Name, Image string
		Config          struct {
			Hostname string
			Labels   map[string]string
		}
		Mounts []struct {
			Type, Name, Source, Destination, Mode, Propagation string
			RW                                                 bool
		}
		NetworkSettings struct {
			Networks map[string]struct {
				NetworkID  string
				Aliases    []string
				Links      []string
				DriverOpts map[string]string
				IPAMConfig *struct {
					IPv4Address, IPv6Address string
					LinkLocalIPs             []string
				}
			}
			Ports map[string][]struct{ HostIP, HostPort string }
		}
	}
	if err = d.monitorJSON(ctx, prefix+"/containers/"+id+"/json", &raw); err != nil {
		return TakeoverContainer{}, err
	}
	result := TakeoverContainer{ID: raw.ID, Name: strings.TrimPrefix(raw.Name, "/"), Image: raw.Image, Service: raw.Config.Labels["com.docker.compose.service"], ConfigHash: raw.Config.Labels["com.docker.compose.config-hash"], Mounts: []TakeoverMount{}, Networks: map[string]string{}}
	result.Hostname = raw.Config.Hostname
	for _, mount := range raw.Mounts {
		if (mount.Propagation != "" && mount.Propagation != "rprivate") || (mount.Mode != "" && mount.Mode != "rw" && mount.Mode != "ro") {
			return result, errors.New("Takeover does not support custom mount propagation or mount modes")
		}
		source := mount.Source
		if mount.Type == "volume" {
			source = mount.Name
		}
		result.Mounts = append(result.Mounts, TakeoverMount{mount.Type, source, mount.Destination, !mount.RW})
	}
	sort.Slice(result.Mounts, func(i, j int) bool { return result.Mounts[i].Target < result.Mounts[j].Target })
	for name, network := range raw.NetworkSettings.Networks {
		if len(network.Links) != 0 || len(network.DriverOpts) != 0 || (network.IPAMConfig != nil && (network.IPAMConfig.IPv4Address != "" || network.IPAMConfig.IPv6Address != "" || len(network.IPAMConfig.LinkLocalIPs) != 0)) {
			return result, errors.New("Takeover does not support custom live network addresses, links or driver options")
		}
		for _, alias := range network.Aliases {
			if alias != result.Name && alias != result.Service && alias != result.ID && !(len(result.ID) >= 12 && alias == result.ID[:12]) {
				return result, errors.New("Takeover does not support custom live network aliases")
			}
		}
		result.Networks[name] = network.NetworkID
	}
	for port, bindings := range raw.NetworkSettings.Ports {
		sort.Slice(bindings, func(i, j int) bool {
			return bindings[i].HostIP+":"+bindings[i].HostPort < bindings[j].HostIP+":"+bindings[j].HostPort
		})
		raw.NetworkSettings.Ports[port] = bindings
	}
	ports, _ := json.Marshal(raw.NetworkSettings.Ports)
	result.Ports = string(ports)
	if result.ID != id || !revisionName.MatchString(strings.TrimPrefix(result.Image, "sha256:")) || !strings.HasPrefix(result.Image, "sha256:") {
		return result, errors.New("Container image identity is unavailable")
	}
	return result, nil
}

func (r StackRecord) managed() bool {
	return r.Origin == "new" || (r.Origin == "portainer" && r.Takeover != nil && r.Takeover.State == "managed")
}

func validTakeover(t *TakeoverState) bool {
	if t == nil {
		return true
	}
	if !portainerFile.MatchString(t.SourceFile) {
		return false
	}
	if (t.State != "pending" && t.State != "managed") || !revisionName.MatchString(t.Revision) || !revisionName.MatchString(t.SourceFingerprint) || len(t.Containers) == 0 || len(t.Containers) > 100 {
		return false
	}
	services, names, ids := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, c := range t.Containers {
		if !revisionName.MatchString(c.ID) || !projectName.MatchString(c.Service) || c.Name == "" || strings.ContainsAny(c.Name, "/\\\x00") || !strings.HasPrefix(c.Image, "sha256:") || !revisionName.MatchString(strings.TrimPrefix(c.Image, "sha256:")) || !revisionName.MatchString(c.ConfigHash) || services[c.Service] || names[c.Name] || ids[c.ID] {
			return false
		}
		services[c.Service], names[c.Name], ids[c.ID] = true, true, true
		for _, m := range c.Mounts {
			if (m.Type != "bind" && m.Type != "volume") || m.Source == "" || !path.IsAbs(m.Target) {
				return false
			}
		}
		for name, id := range c.Networks {
			if name == "" || !revisionName.MatchString(id) {
				return false
			}
		}
		if !json.Valid([]byte(c.Ports)) {
			return false
		}
	}
	return true
}

func takeoverResourceStamp(c TakeoverContainer) string {
	c.ID, c.ConfigHash = "", ""
	raw, _ := json.Marshal(c)
	return fingerprint(string(raw))
}

// Copy resolved configuration with dollars escaped for its next Compose parse.
func escapeComposeDollars(v any) any {
	switch x := v.(type) {
	case string:
		return strings.ReplaceAll(x, "$", "$$")
	case map[string]any:
		for key, value := range x {
			x[key] = escapeComposeDollars(value)
		}
	case []any:
		for i, value := range x {
			x[i] = escapeComposeDollars(value)
		}
	}
	return v
}

func takeoverShape(raw string) error {
	if _, _, _, err := inspectCompose(raw, true); err != nil {
		return err
	}
	model, err := comparisonModel(raw)
	if err != nil {
		return errors.New("Cannot read the imported Compose definition")
	}
	for key := range model {
		if key != "name" && key != "version" && key != "services" && key != "volumes" && key != "networks" && !strings.HasPrefix(key, "x-") {
			return errors.New("Takeover supports only name, version, services, volumes, networks and x- metadata at the top level")
		}
	}
	for key, value := range model {
		if key != "services" && takeoverInterpolation(value) {
			return errors.New("Takeover requires explicit values outside service environment; resolve interpolated paths, metadata and project settings first")
		}
	}
	for _, value := range model["services"].(map[string]any) {
		s := value.(map[string]any)
		for key, value := range s {
			if key != "environment" && takeoverInterpolation(value) {
				return errors.New("Takeover requires explicit values outside service environment; resolved secrets must not enter configuration previews")
			}
		}
		for _, key := range []string{"build", "extends", "env_file", "label_file", "credential_spec", "configs", "secrets", "develop", "profiles", "scale", "deploy", "network_mode", "pid", "ipc", "volumes_from", "tmpfs", "use_api_socket", "mac_address", "links", "external_links", "depends_on", "models", "provider", "pre_start", "post_start", "pre_stop", "uts"} {
			if _, ok := s[key]; ok {
				return fmt.Errorf("Takeover does not support %s; make dependencies explicit in a separate reviewed configuration", key)
			}
		}
		if env, ok := s["environment"].(map[string]any); ok {
			for _, value := range env {
				if value == nil {
					return errors.New("Takeover requires explicit environment values, not inherited host variables")
				}
			}
		}
		if env, ok := s["environment"].([]any); ok {
			for _, value := range env {
				text, ok := value.(string)
				if !ok || !strings.Contains(text, "=") {
					return errors.New("Takeover requires explicit environment values")
				}
			}
		}
	}
	return nil
}

func takeoverSnapshot(canonical []byte, live []TakeoverContainer) (map[string]string, error) {
	// Compose's config CLI doubles every dollar in its rendered JSON, even
	// mapping keys. Undo that rendering once, then escape only YAML values.
	canonical = bytes.ReplaceAll(canonical, []byte("$$"), []byte("$"))
	var model map[string]any
	if json.Unmarshal(canonical, &model) != nil {
		return nil, errors.New("Compose did not return a usable resolved configuration")
	}
	services, ok := model["services"].(map[string]any)
	if !ok || len(services) != len(live) {
		return nil, errors.New("Takeover requires exactly one existing container for every service")
	}
	resources := func(key string) map[string]any {
		v, _ := model[key].(map[string]any)
		if v == nil {
			v = map[string]any{}
			model[key] = v
		}
		return v
	}
	volumes, networks := resources("volumes"), resources("networks")
	usedVolumes, usedNetworks := map[string]bool{}, map[string]bool{}
	for _, c := range live {
		s, ok := services[c.Service].(map[string]any)
		if !ok {
			return nil, errors.New("The live service set differs from the imported definition")
		}
		s["image"], s["container_name"] = c.Image, c.Name
		if configured, ok := s["ports"].([]any); ok {
			for _, v := range configured {
				p, ok := v.(map[string]any)
				if !ok || p["published"] == nil || fmt.Sprint(p["published"]) == "0" || strings.Contains(fmt.Sprint(p["published"]), "-") {
					return nil, errors.New("Takeover requires fixed published ports")
				}
			}
		}
		expected := []TakeoverMount{}
		if configured, ok := s["volumes"].([]any); ok {
			for _, v := range configured {
				mount, ok := v.(map[string]any)
				if !ok {
					return nil, errors.New("Unsupported resolved mount")
				}
				for k := range mount {
					if k != "type" && k != "source" && k != "target" && k != "read_only" && k != "bind" && k != "volume" {
						return nil, errors.New("Takeover does not support advanced mount options")
					}
				}
				if options, present := mount["volume"]; present && options != nil {
					if m, ok := options.(map[string]any); !ok || len(m) != 0 {
						return nil, errors.New("Takeover does not support advanced volume options")
					}
				}
				if bind, ok := mount["bind"].(map[string]any); ok {
					for k := range bind {
						if k != "create_host_path" {
							return nil, errors.New("Takeover does not support advanced bind options")
						}
					}
				}
				typ, _ := mount["type"].(string)
				source, _ := mount["source"].(string)
				target, _ := mount["target"].(string)
				ro, _ := mount["read_only"].(bool)
				if typ == "volume" {
					resource, ok := volumes[source].(map[string]any)
					if !ok {
						return nil, errors.New("Anonymous volumes cannot be taken over safely")
					}
					actual, _ := resource["name"].(string)
					if actual == "" {
						return nil, errors.New("Volume identity is unresolved")
					}
					usedVolumes[source] = true
					volumes[source] = map[string]any{"name": actual, "external": true}
					source = actual
				} else if typ != "bind" || !path.IsAbs(source) {
					return nil, errors.New("Takeover requires named volumes or absolute host bind paths")
				}
				if typ == "bind" {
					mount["bind"] = map[string]any{"create_host_path": false}
				}
				expected = append(expected, TakeoverMount{typ, source, target, ro})
			}
		}
		sort.Slice(expected, func(i, j int) bool { return expected[i].Target < expected[j].Target })
		a, _ := json.Marshal(expected)
		b, _ := json.Marshal(c.Mounts)
		if string(a) != string(b) {
			return nil, errors.New("Live mounts differ from the saved definition, or the image declares an anonymous volume; takeover was blocked")
		}
		configured, ok := s["networks"].(map[string]any)
		if !ok || len(configured) != len(c.Networks) {
			return nil, errors.New("Live networks differ from the resolved definition")
		}
		for key, options := range configured {
			if options != nil {
				opts, ok := options.(map[string]any)
				if !ok || len(opts) != 0 {
					return nil, errors.New("Takeover does not support custom network aliases, addresses or options")
				}
			}
			n, ok := networks[key].(map[string]any)
			if !ok {
				return nil, errors.New("Network identity is unresolved")
			}
			name, _ := n["name"].(string)
			if c.Networks[name] == "" {
				return nil, errors.New("Live network identity differs from the saved definition")
			}
			usedNetworks[key] = true
			networks[key] = map[string]any{"name": name, "external": true}
		}
	}
	if len(usedVolumes) != len(volumes) || len(usedNetworks) != len(networks) {
		return nil, errors.New("Takeover does not support unused volume or network declarations; review those resources separately")
	}
	raw, err := yaml.Marshal(escapeComposeDollars(model))
	if err != nil || len(raw) > maxDefinition {
		return nil, errors.New("The takeover snapshot exceeds the supported definition limit")
	}
	return map[string]string{"compose.yaml": string(raw), ".env": "", "stack.env": ""}, nil
}

func takeoverInterpolation(value any) bool {
	switch v := value.(type) {
	case string:
		for i := 0; i < len(v); i++ {
			if v[i] != '$' || i+1 >= len(v) {
				continue
			}
			next := v[i+1]
			if next == '$' {
				i++
				continue
			}
			if next == '{' || next == '_' || (next >= 'a' && next <= 'z') || (next >= 'A' && next <= 'Z') {
				return true
			}
		}
	case map[string]any:
		for _, item := range v {
			if takeoverInterpolation(item) {
				return true
			}
		}
	case []any:
		for _, item := range v {
			if takeoverInterpolation(item) {
				return true
			}
		}
	}
	return false
}
