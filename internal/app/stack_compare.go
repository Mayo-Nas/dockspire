package app

import (
	"errors"
	"io"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// This is a comparison of stored inputs, not resolved Compose configuration or
// live container state. Raw YAML, comments, environment values and arbitrary
// settings must never be included in a comparison response.
type RevisionComparison struct {
	Status       string                `json:"status"` // available, initial, unavailable
	BaseRevision string                `json:"baseRevision,omitempty"`
	Revision     string                `json:"revision"`
	Warning      string                `json:"warning,omitempty"`
	SourceOnly   bool                  `json:"sourceOnly,omitempty"`
	Truncated    bool                  `json:"truncated,omitempty"`
	Changes      []ConfigurationChange `json:"changes"`
}

type ConfigurationChange struct {
	Scope  string `json:"scope"`
	Field  string `json:"field"`
	Kind   string `json:"kind"` // added, removed, changed
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

const maxComparisonChanges = 200

var comparisonEnvName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var comparisonImage = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/:@-]*$`)
var comparisonDigest = regexp.MustCompile(`^sha256:[a-fA-F0-9]{64}$`)
var comparisonPort = regexp.MustCompile(`^[0-9a-fA-F.\[\]:/-]+(/(tcp|udp|sctp))?$`)

func (c *RevisionComparison) add(scope, field string, old, next any, before, after string) {
	if reflect.DeepEqual(old, next) {
		return
	}
	if len(c.Changes) == maxComparisonChanges {
		c.Truncated = true
		return
	}
	kind := "changed"
	if old == nil {
		kind = "added"
	} else if next == nil {
		kind = "removed"
	}
	c.Changes = append(c.Changes, ConfigurationChange{scope, field, kind, comparisonClip(before), comparisonClip(after)})
}

func comparisonClip(value string) string {
	runes := []rune(value)
	if len(runes) > 512 {
		return string(runes[:512]) + "… [shortened]"
	}
	return value
}

func comparisonKeys(a, b map[string]any) []string {
	seen := map[string]bool{}
	for key := range a {
		seen[key] = true
	}
	for key := range b {
		seen[key] = true
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func comparisonModel(raw string) (map[string]any, error) {
	var model map[string]any
	d := yaml.NewDecoder(strings.NewReader(raw))
	if err := d.Decode(&model); err != nil {
		return nil, err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, errors.New("Invalid configuration")
	}
	services, ok := model["services"].(map[string]any)
	if !ok || len(services) == 0 {
		return nil, errors.New("Invalid services")
	}
	for name, service := range services {
		if _, ok := service.(map[string]any); !ok || !projectName.MatchString(name) {
			return nil, errors.New("Invalid service")
		}
	}
	return model, nil
}

// Resolve a baseline by its content hash, including an initial bundle that has
// no revisions/<hash> directory. Do not scan every historical revision.
func (l *stackLibrary) comparisonBaseline(record StackRecord, current map[string]string) (map[string]string, error) {
	if filesFingerprint(current) == record.AppliedRevision {
		return current, nil
	}
	if !revisionName.MatchString(record.AppliedRevision) {
		return nil, errors.New("No baseline")
	}
	files, _, err := l.verifiedRevision(record.Name, record.AppliedRevision)
	return files, err
}

func (l *stackLibrary) comparisonForPlan(plan UpdatePlan) (*RevisionComparison, error) {
	record, err := l.load(plan.Name)
	if err != nil {
		return nil, err
	}
	if recordStamp(record) != plan.recordVersion {
		return nil, errors.New("This stack changed; review it again")
	}
	current, err := l.content(record)
	if err != nil {
		return nil, err
	}
	if filesFingerprint(current) != plan.Revision {
		return nil, errors.New("The saved configuration changed; review it again")
	}
	c := &RevisionComparison{Status: "available", BaseRevision: record.AppliedRevision, Revision: plan.Revision, Changes: []ConfigurationChange{}}
	unavailable := func(message string) (*RevisionComparison, error) {
		c.Status, c.Warning, c.Changes = "unavailable", message, []ConfigurationChange{}
		return c, nil
	}
	var previous map[string]string
	if record.AppliedRevision == "" {
		if record.DeployedAt != "" {
			return unavailable("The last successful deployment has no recorded configuration revision. Comparison is unavailable.")
		}
		c.Status = "initial"
	} else {
		previous, err = l.comparisonBaseline(record, current)
		if err != nil {
			return unavailable("The last applied configuration is missing, unreadable, or damaged. Comparison is unavailable; inspect the saved configuration before continuing.")
		}
	}
	return compareConfiguration(c, previous, current), nil
}

func compareConfiguration(c *RevisionComparison, previous, current map[string]string) *RevisionComparison {
	unavailable := func(message string) *RevisionComparison {
		c.Status, c.Warning, c.Changes = "unavailable", message, []ConfigurationChange{}
		return c
	}
	next, err := comparisonModel(current["compose.yaml"])
	if err != nil {
		return unavailable("The saved configuration could not be compared. Inspect it before continuing.")
	}
	old := map[string]any{}
	if previous != nil {
		old, err = comparisonModel(previous["compose.yaml"])
		if err != nil {
			return unavailable("The last applied configuration could not be compared. Inspect the saved configuration before continuing.")
		}
	}
	c.compareModels(old, next)
	for _, file := range []string{".env", "stack.env"} {
		a, aOK := comparisonEnvironmentFile(previous[file])
		b, bOK := comparisonEnvironmentFile(current[file])
		if aOK && bOK {
			c.compareEnvironment(file, a, b)
		} else {
			c.add(file, "Environment entries (values hidden)", previous[file], current[file], "", "")
		}
	}
	// Presence of an empty associated file can itself affect Compose resolution.
	for _, file := range []string{".env", "stack.env"} {
		_, was := previous[file]
		_, now := current[file]
		if was != now {
			c.add(file, "File", presence(was), presence(now), "", "")
		}
	}
	c.SourceOnly = c.Status == "available" && c.BaseRevision != c.Revision && len(c.Changes) == 0
	return c
}

func presence(exists bool) any {
	if exists {
		return true
	}
	return nil
}

func comparisonEnvironmentFile(raw string) (map[string]any, bool) {
	values := map[string]any{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		key = strings.TrimSpace(key)
		if !ok || !comparisonEnvName.MatchString(key) {
			return nil, false
		}
		values[key] = value
	}
	return values, true
}

func comparisonEnvironment(value any) (map[string]any, bool) {
	if value == nil {
		return map[string]any{}, true
	}
	if values, ok := value.(map[string]any); ok {
		for key := range values {
			if !comparisonEnvName.MatchString(key) {
				return nil, false
			}
		}
		return values, true
	}
	values, ok := value.([]any)
	if !ok {
		return nil, false
	}
	result := map[string]any{}
	for _, entry := range values {
		text, ok := entry.(string)
		if !ok {
			return nil, false
		}
		key, value, assigned := strings.Cut(text, "=")
		if !comparisonEnvName.MatchString(key) {
			return nil, false
		}
		if assigned {
			result[key] = value
		} else {
			result[key] = nil
		}
	}
	return result, true
}

func (c *RevisionComparison) compareEnvironment(scope string, old, next map[string]any) {
	for _, key := range comparisonKeys(old, next) {
		a, was := old[key]
		b, now := next[key]
		// A null value means inherit, so presence must be compared separately.
		if was == now && reflect.DeepEqual(a, b) {
			continue
		}
		kind := "changed"
		if !was {
			kind = "added"
		} else if !now {
			kind = "removed"
		}
		if len(c.Changes) == maxComparisonChanges {
			c.Truncated = true
			return
		}
		c.Changes = append(c.Changes, ConfigurationChange{Scope: scope, Field: "Environment · " + comparisonClip(key), Kind: kind})
	}
}

func (c *RevisionComparison) compareModels(old, next map[string]any) {
	a, _ := old["services"].(map[string]any)
	b, _ := next["services"].(map[string]any)
	for _, service := range comparisonKeys(a, b) {
		before, was := a[service].(map[string]any)
		after, now := b[service].(map[string]any)
		scope := "Service " + service
		if was != now {
			c.add(scope, "Service", presence(was), presence(now), "", "")
		}
		for _, field := range []string{"image", "ports", "volumes"} {
			c.add(scope, map[string]string{"image": "Image", "ports": "Ports", "volumes": "Mounts"}[field], before[field], after[field], comparisonValue(field, before[field]), comparisonValue(field, after[field]))
		}
		aEnv, aOK := comparisonEnvironment(before["environment"])
		bEnv, bOK := comparisonEnvironment(after["environment"])
		if aOK && bOK {
			c.compareEnvironment(scope, aEnv, bEnv)
		} else {
			c.add(scope, "Environment (values hidden)", before["environment"], after["environment"], "", "")
		}
		labels := map[string]string{"command": "Command", "entrypoint": "Entrypoint", "labels": "Labels", "healthcheck": "Health check", "depends_on": "Dependencies", "deploy": "Deployment settings", "restart": "Restart policy", "env_file": "Environment files", "networks": "Networks"}
		otherBefore, otherAfter := map[string]any{}, map[string]any{}
		for _, field := range comparisonKeys(before, after) {
			if field == "image" || field == "ports" || field == "volumes" || field == "environment" {
				continue
			}
			if label, ok := labels[field]; ok {
				c.add(scope, label, before[field], after[field], "", "")
			} else {
				if value, ok := before[field]; ok {
					otherBefore[field] = value
				}
				if value, ok := after[field]; ok {
					otherAfter[field] = value
				}
			}
		}
		c.add(scope, "Other service settings", otherBefore, otherAfter, "", "")
	}
	for _, field := range []string{"volumes", "networks"} {
		c.add("Stack", map[string]string{"volumes": "Named volumes", "networks": "Networks"}[field], old[field], next[field], "", "")
	}
	otherBefore, otherAfter := map[string]any{}, map[string]any{}
	for _, key := range comparisonKeys(old, next) {
		if key == "services" || key == "volumes" || key == "networks" {
			continue
		}
		if value, ok := old[key]; ok {
			otherBefore[key] = value
		}
		if value, ok := next[key]; ok {
			otherAfter[key] = value
		}
	}
	c.add("Stack", "Other stack settings", otherBefore, otherAfter, "", "")
}

// Only explicitly useful non-environment fields expose values. Interpolation
// expressions can embed secret defaults; never return them or arbitrary fields.
func comparisonValue(field string, value any) string {
	if value == nil {
		return ""
	}
	if field == "image" {
		text, ok := value.(string)
		if !ok || !comparisonImage.MatchString(text) || strings.Contains(text, "://") {
			return "[details hidden]"
		}
		if _, digest, found := strings.Cut(text, "@"); found && !comparisonDigest.MatchString(digest) {
			return "[details hidden]"
		}
		return text
	}
	items, ok := value.([]any)
	if !ok {
		return "[details hidden]"
	}
	parts := []string{}
	for _, item := range items {
		text, ok := item.(string)
		if number, numeric := item.(int); numeric && field == "ports" {
			text, ok = strconv.Itoa(number), true
		}
		if !ok {
			// Long syntax: emit only typed port or mount properties, never labels,
			// driver options, defaults, or arbitrary extension values.
			if m, mapped := item.(map[string]any); mapped {
				keys := []string{"target", "published", "host_ip", "protocol", "mode"}
				if field == "volumes" {
					keys = []string{"type", "source", "target", "read_only"}
				}
				fields := []string{}
				for _, key := range keys {
					if v, exists := m[key]; exists {
						var s string
						switch v := v.(type) {
						case string:
							s = v
						case int:
							s = strconv.Itoa(v)
						case bool:
							s = strconv.FormatBool(v)
						default:
							s = "[details hidden]"
						}
						if !comparisonSafeText(s) {
							s = "[details hidden]"
						}
						// Port strings contain only numeric addresses/ranges or fixed enums.
						if field == "ports" && !comparisonPort.MatchString(s) && s != "tcp" && s != "udp" && s != "sctp" && s != "host" && s != "ingress" {
							s = "[details hidden]"
						}
						fields = append(fields, key+"="+s)
					}
				}
				text, ok = strings.Join(fields, "; "), true
				if len(m) > len(fields) {
					text += "; [other details hidden]"
				}
			}
		}
		if !ok || !comparisonSafeText(text) || (field == "ports" && !strings.Contains(text, "=") && !comparisonPort.MatchString(text)) {
			text = "[details hidden]"
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, "\n")
}

func comparisonSafeText(text string) bool {
	return !strings.ContainsAny(text, "$\r\n\x00") && !strings.Contains(text, "://")
}
