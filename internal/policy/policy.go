// Package policy records what one session was launched with, so `srtbox why`
// can explain it from inside the sandbox, where the config is unreadable.
package policy

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"

	"github.com/brettatoms/srtbox/internal/config"
)

// Env names the variable that holds the policy file's path inside a session.
const Env = "SRTBOX_POLICY"

// Sources of a rule, besides the config files.
const SourceSrtbox = "added by srtbox at launch"

// Policy is the merged settings srt received, plus the srtbox meta keys and
// the source of each rule.
type Policy struct {
	Project  string         `json:"project"`
	Root     string         `json:"root"`
	OS       string         `json:"os"`
	Settings map[string]any `json:"settings"`
	Forward  []string       `json:"forward"`
	DenyEnv  []string       `json:"denyEnv"`
	AllowEnv []string       `json:"allowEnv"`
	// Sources maps "section.key" and a rule's value to the file it came from,
	// e.g. "filesystem.denyRead" → "~" → "base.json".
	Sources map[string]map[string]string `json:"sources"`
}

// sections are the settings arrays whose entries Policy traces to a source.
var sections = [][2]string{
	{"filesystem", "denyRead"}, {"filesystem", "allowRead"},
	{"filesystem", "allowWrite"}, {"filesystem", "denyWrite"},
	{"network", "allowedDomains"}, {"network", "deniedDomains"},
}

// New builds the policy for project from its final settings. Rules found in
// neither config file were added by srtbox.
func New(project string, meta config.Meta, settings map[string]any) (*Policy, error) {
	base, overlay, err := config.Layers(project)
	if err != nil {
		return nil, err
	}
	p := &Policy{
		Project: project, Root: meta.Root, OS: runtime.GOOS, Settings: settings,
		Forward: meta.Forward, DenyEnv: meta.DenyEnv, AllowEnv: meta.AllowEnv,
		Sources: map[string]map[string]string{},
	}
	layers := []struct {
		name string
		doc  map[string]any
	}{{"base.json", base}, {project + ".json", overlay}}
	trace := func(key string, values func(map[string]any) []string) {
		src := map[string]string{}
		for _, v := range values(settings) {
			src[v] = SourceSrtbox
		}
		// The project file is checked last so it wins when both list a rule.
		for _, l := range layers {
			if l.doc == nil {
				continue
			}
			doc := config.Expand(l.doc, os.LookupEnv).(map[string]any)
			for _, v := range values(doc) {
				if _, ok := src[v]; ok {
					src[v] = l.name
				}
			}
		}
		p.Sources[key] = src
	}
	for _, sec := range sections {
		trace(sec[0]+"."+sec[1], func(doc map[string]any) []string { return Strings(doc, sec[0], sec[1]) })
	}
	trace("credentials.files", func(doc map[string]any) []string {
		var paths []string
		for _, f := range CredentialFiles(doc) {
			paths = append(paths, f.Path)
		}
		return paths
	})
	return p, nil
}

// CredentialFile is one credentials.files entry. srt makes a "deny" file
// unreadable and unwritable. On Linux it replaces a "mask" file with a
// read-only placeholder that its proxy swaps for the real value in requests
// to InjectHosts (every allowed host when empty); on macOS "mask" acts as
// "deny".
type CredentialFile struct {
	Path, Mode  string
	InjectHosts []string
}

// CredentialFiles returns doc's credentials.files entries.
func CredentialFiles(doc map[string]any) []CredentialFile {
	creds, _ := doc["credentials"].(map[string]any)
	files, _ := creds["files"].([]any)
	var out []CredentialFile
	for _, f := range files {
		e, _ := f.(map[string]any)
		path, ok := e["path"].(string)
		if !ok {
			continue
		}
		mode, _ := e["mode"].(string)
		cf := CredentialFile{Path: path, Mode: mode}
		hosts, _ := e["injectHosts"].([]any)
		for _, h := range hosts {
			cf.InjectHosts = append(cf.InjectHosts, fmt.Sprint(h))
		}
		out = append(out, cf)
	}
	return out
}

// Write saves the policy to path.
func (p *Policy) Write(path string) error {
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// Load reads a policy written by Write.
func Load(path string) (*Policy, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p Policy
	return &p, json.Unmarshal(b, &p)
}

// Source names where a rule came from.
func (p *Policy) Source(section, value string) string {
	if s := p.Sources[section][value]; s != "" {
		return s
	}
	return SourceSrtbox
}

// Strings returns the string array at doc[a][b].
func Strings(doc map[string]any, a, b string) []string {
	m, _ := doc[a].(map[string]any)
	arr, _ := m[b].([]any)
	var out []string
	for _, v := range arr {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
