package app

import (
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

type sourcePatch struct {
	start, end int
	text       string
}

// YAML node positions let ordinary scalar substitutions retain all surrounding
// source bytes. Semantic verification below prevents an ambiguous token boundary
// from silently changing configuration. Complex values use the comment-bearing
// syntax tree rather than a map serializer.
func composeSource(raw string, desired map[string]any) (string, error) {
	var document yaml.Node
	if yaml.Unmarshal([]byte(raw), &document) != nil || len(document.Content) != 1 {
		return "", errors.New("Cannot read Compose source")
	}
	var patches []sourcePatch
	precise := true
	var change func(*yaml.Node, any)
	change = func(n *yaml.Node, value any) {
		var before any
		if n.Decode(&before) != nil {
			precise = false
			return
		}
		if reflect.DeepEqual(before, value) {
			return
		}
		if n.Kind == yaml.MappingNode {
			if mapping, ok := value.(map[string]any); ok {
				for i := 0; i < len(n.Content); i += 2 {
					if n.Content[i].Tag == "!!merge" {
						continue
					}
					change(n.Content[i+1], mapping[n.Content[i].Value])
				}
				return
			}
		}
		if n.Kind == yaml.SequenceNode {
			if items, ok := value.([]any); ok && len(items) == len(n.Content) {
				for i := range items {
					change(n.Content[i], items[i])
				}
				return
			}
		}
		start, end, ok := scalarSpan(raw, n)
		encoded, err := json.Marshal(value)
		if !ok || err != nil {
			precise = false
		} else {
			patches = append(patches, sourcePatch{start, end, string(encoded)})
		}
		head, line, foot, anchor := n.HeadComment, n.LineComment, n.FootComment, n.Anchor
		if n.Encode(value) != nil {
			precise = false
		}
		n.HeadComment, n.LineComment, n.FootComment, n.Anchor = head, line, foot, anchor
	}
	change(document.Content[0], desired)
	verify := func(text string) bool {
		var decoded map[string]any
		return yaml.Unmarshal([]byte(text), &decoded) == nil && reflect.DeepEqual(decoded, desired)
	}
	if precise {
		sort.Slice(patches, func(i, j int) bool { return patches[i].start > patches[j].start })
		result, boundary := raw, len(raw)
		for _, patch := range patches {
			if patch.end > boundary {
				precise = false
				break
			}
			result = result[:patch.start] + patch.text + result[patch.end:]
			boundary = patch.start
		}
		if precise && verify(result) {
			return result, nil
		}
	}
	encoded, err := yaml.Marshal(&document)
	if err != nil || !verify(string(encoded)) {
		return "", errors.New("Cannot safely preserve masked Compose values; simplify aliases or complex environment values")
	}
	return string(encoded), nil
}

func scalarSpan(raw string, n *yaml.Node) (int, int, bool) {
	if n.Kind != yaml.ScalarNode && n.Kind != yaml.AliasNode {
		return 0, 0, false
	}
	start, line := 0, 1
	for line < n.Line && start < len(raw) {
		i := strings.IndexByte(raw[start:], '\n')
		if i < 0 {
			return 0, 0, false
		}
		start += i + 1
		line++
	}
	for column := 1; column < n.Column && start < len(raw); column++ {
		_, size := utf8.DecodeRuneInString(raw[start:])
		start += size
	}
	// Tags and anchor declarations are retained, rather than replaced with values.
	for start < len(raw) && (raw[start] == '&' || raw[start] == '!') {
		for start < len(raw) && !strings.ContainsRune(" \t\r\n", rune(raw[start])) {
			start++
		}
		for start < len(raw) && (raw[start] == ' ' || raw[start] == '\t') {
			start++
		}
	}
	if start >= len(raw) || n.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
		return 0, 0, false
	}
	if raw[start] == '\'' || raw[start] == '"' {
		quote := raw[start]
		for end := start + 1; end < len(raw); end++ {
			if quote == '"' && raw[end] == '\\' {
				end++
				continue
			}
			if raw[end] != quote {
				continue
			}
			if quote == '\'' && end+1 < len(raw) && raw[end+1] == '\'' {
				end++
				continue
			}
			return start, end + 1, true
		}
		return 0, 0, false
	}
	end := start
	for end < len(raw) && raw[end] != '\n' && raw[end] != '\r' {
		if strings.ContainsRune(",]}", rune(raw[end])) {
			break
		}
		if raw[end] == '#' && (end == start || raw[end-1] == ' ' || raw[end-1] == '\t') {
			break
		}
		end++
	}
	for end > start && (raw[end-1] == ' ' || raw[end-1] == '\t') {
		end--
	}
	return start, end, end >= start
}
