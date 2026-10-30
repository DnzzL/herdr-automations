package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// The edits here change one thing in automations.yaml and leave the rest as
// the user wrote it: comments, order, and entries that do not load. Save
// rewrites the whole file from what loaded, which is right for the wizard and
// wrong for a panel that touches one entry at a time.

// PutResult is what a Put found. Problem is why the entry would not load, in
// the words Load uses; empty means it was written (or would be, on a dry run).
type PutResult struct {
	Problem string      `json:"problem,omitempty"`
	Next    []time.Time `json:"next,omitempty"`
}

// Put writes a, replacing the entry named original, or appending when
// original is empty. Nothing is written when the result would not load.
func Put(original string, a Automation, dryRun bool) (PutResult, error) {
	doc, err := readDoc()
	if err != nil {
		return PutResult{}, err
	}
	seq := automationsOf(doc)
	at := entryIndex(seq, original)
	if original != "" && at < 0 {
		return PutResult{}, fmt.Errorf("no automation named %q", original)
	}
	if a.Name != original && entryIndex(seq, a.Name) >= 0 {
		return PutResult{Problem: fmt.Sprintf("%s: an automation by that name already exists", a.Name)}, nil
	}

	var node yaml.Node
	if err := node.Encode(a); err != nil {
		return PutResult{}, err
	}
	if at >= 0 {
		node.HeadComment = seq.Content[at].HeadComment
		seq.Content[at] = &node
	} else {
		seq.Content = append(seq.Content, &node)
	}

	raw, err := encode(doc)
	if err != nil {
		return PutResult{}, err
	}
	cfg, err := parse(raw)
	if err != nil {
		return PutResult{Problem: err.Error()}, nil
	}
	if d := cfg.Diagnostic(a.Name); d != nil {
		return PutResult{Problem: d.Message}, nil
	}
	res := PutResult{Next: nextRuns(cfg.Find(a.Name).Cron, 3)}
	if dryRun {
		return res, nil
	}
	return res, write(raw)
}

// Remove deletes the entry named name, valid or not.
func Remove(name string) error {
	doc, err := readDoc()
	if err != nil {
		return err
	}
	seq := automationsOf(doc)
	at := entryIndex(seq, name)
	if at < 0 {
		return fmt.Errorf("no automation named %q", name)
	}
	seq.Content = append(seq.Content[:at], seq.Content[at+1:]...)
	raw, err := encode(doc)
	if err != nil {
		return err
	}
	return write(raw)
}

// SetPaused pauses or resumes one automation (its disabled flag), or every
// automation when name is empty (the top-level paused flag).
func SetPaused(name string, paused bool) error {
	doc, err := readDoc()
	if err != nil {
		return err
	}
	target, key := doc.Content[0], "paused"
	if name != "" {
		seq := automationsOf(doc)
		at := entryIndex(seq, name)
		if at < 0 {
			return fmt.Errorf("no automation named %q", name)
		}
		target, key = seq.Content[at], "disabled"
	}
	setBool(target, key, paused)
	raw, err := encode(doc)
	if err != nil {
		return err
	}
	return write(raw)
}

// readDoc parses the file as a node tree; a missing file is an empty mapping.
func readDoc() (*yaml.Node, error) {
	raw, err := os.ReadFile(Path())
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	var doc yaml.Node
	indent = detectIndent(raw)
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return nil, fmt.Errorf("parse %s: %w", Path(), err)
		}
	}
	if len(doc.Content) == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	if doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: expected a mapping at the top", Path())
	}
	return &doc, nil
}

// automationsOf returns the automations sequence, creating it if absent.
func automationsOf(doc *yaml.Node) *yaml.Node {
	root := doc.Content[0]
	if v := valueOf(root, "automations"); v != nil {
		if v.Kind != yaml.SequenceNode {
			// "automations:" with nothing after it is a null scalar.
			*v = yaml.Node{Kind: yaml.SequenceNode}
		}
		return v
	}
	seq := &yaml.Node{Kind: yaml.SequenceNode}
	root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "automations"}, seq)
	return seq
}

func entryIndex(seq *yaml.Node, name string) int {
	if name == "" {
		return -1
	}
	for i, e := range seq.Content {
		if v := valueOf(e, "name"); v != nil && v.Value == name {
			return i
		}
	}
	return -1
}

func valueOf(m *yaml.Node, key string) *yaml.Node {
	if m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// setBool writes key: true, or removes the key for false so a resumed entry
// reads as if it had never been paused.
func setBool(m *yaml.Node, key string, on bool) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			if on {
				m.Content[i+1] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"}
			} else {
				m.Content = append(m.Content[:i], m.Content[i+2:]...)
			}
			return
		}
	}
	if on {
		m.Content = append(m.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: key},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"})
	}
}

// indent is the file's own indentation, so an edit does not reformat entries
// it did not touch. Set by readDoc; every edit reads, then encodes, once.
var indent = 4

// detectIndent reads where the first list dash sits: yaml.v3 puts it at the
// indent width. 4 is what yaml.Marshal (and so Save) writes.
func detectIndent(raw []byte) int {
	for _, line := range bytes.Split(raw, []byte("\n")) {
		trimmed := bytes.TrimLeft(line, " ")
		if n := len(line) - len(trimmed); n > 0 && bytes.HasPrefix(trimmed, []byte("- ")) {
			return n
		}
	}
	return 4
}

func encode(doc *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(indent)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	return buf.Bytes(), enc.Close()
}

// write replaces the file through a rename, so a crash mid-write cannot leave
// half a config for the daemon to read as "no automations".
func write(raw []byte) error {
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(Dir(), ".automations-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(Dir(), "automations.yaml"))
}

// nextRuns lists the next n occurrences of a cron line already validated.
func nextRuns(cron string, n int) []time.Time {
	sched, err := CronParser.Parse(cron)
	if err != nil {
		return nil
	}
	out := make([]time.Time, 0, n)
	t := time.Now()
	for range n {
		t = sched.Next(t)
		out = append(out, t)
	}
	return out
}
