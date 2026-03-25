package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type SkillInspection struct {
	Name        string
	Description string
	Path        string
	Loaded      bool
	Reason      string
}

func (e *Engine) CompactionCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.compactionCount
}

func InspectSkills(workspaceRoot string) ([]SkillInspection, error) {
	roots, err := skillsInjectionRoots(workspaceRoot)
	if err != nil {
		return nil, err
	}

	inspections := make([]SkillInspection, 0)
	seenLoadedPaths := map[string]bool{}
	for _, root := range roots {
		entries, readErr := readSkillsDir(root)
		if readErr != nil {
			if os.IsNotExist(readErr) {
				continue
			}
			return nil, fmt.Errorf("read skills directory %q: %w", root, readErr)
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			skillDir := filepath.Join(root, entry.Name())
			skillPath := filepath.Join(skillDir, skillFileName)
			inspection := inspectSkillAtPath(entry.Name(), skillPath)
			if inspection.Loaded {
				if seenLoadedPaths[inspection.Path] {
					inspection.Loaded = false
					inspection.Reason = "duplicate resolved SKILL.md path"
				} else {
					seenLoadedPaths[inspection.Path] = true
				}
			}
			inspections = append(inspections, inspection)
		}
	}

	sort.Slice(inspections, func(i, j int) bool {
		if inspections[i].Loaded != inspections[j].Loaded {
			return inspections[i].Loaded && !inspections[j].Loaded
		}
		return inspections[i].Path < inspections[j].Path
	})
	return inspections, nil
}

func inspectSkillAtPath(fallbackName, skillPath string) SkillInspection {
	resolvedPath := filepath.ToSlash(skillPath)
	if canonical, err := filepath.EvalSymlinks(skillPath); err == nil {
		resolvedPath = filepath.ToSlash(canonical)
	}

	contents, err := os.ReadFile(skillPath)
	if err != nil {
		reason := "could not read SKILL.md"
		if os.IsNotExist(err) {
			reason = "missing SKILL.md"
		}
		return SkillInspection{
			Name:   sanitizeSkillSingleLine(fallbackName),
			Path:   resolvedPath,
			Loaded: false,
			Reason: reason,
		}
	}

	frontmatter, ok := extractSkillFrontmatter(string(contents))
	if !ok {
		return SkillInspection{
			Name:   sanitizeSkillSingleLine(fallbackName),
			Path:   resolvedPath,
			Loaded: false,
			Reason: "missing or invalid frontmatter",
		}
	}

	var parsed skillFrontmatter
	if err := yamlUnmarshal([]byte(frontmatter), &parsed); err != nil {
		return SkillInspection{
			Name:   sanitizeSkillSingleLine(fallbackName),
			Path:   resolvedPath,
			Loaded: false,
			Reason: "invalid frontmatter YAML",
		}
	}

	name := sanitizeSkillSingleLine(parsed.Name)
	if name == "" {
		name = sanitizeSkillSingleLine(fallbackName)
	}
	description := sanitizeSkillSingleLine(parsed.Description)
	if name == "" || description == "" {
		return SkillInspection{
			Name:   name,
			Path:   resolvedPath,
			Loaded: false,
			Reason: "missing name or description",
		}
	}

	return SkillInspection{
		Name:        name,
		Description: description,
		Path:        resolvedPath,
		Loaded:      true,
	}
}

func InstalledAgentsPaths(workspaceRoot string) ([]string, error) {
	paths, err := agentsInjectionPaths(workspaceRoot)
	if err != nil {
		return nil, err
	}
	installed := make([]string, 0, len(paths))
	for _, path := range paths {
		if _, statErr := os.Stat(path); statErr != nil {
			if os.IsNotExist(statErr) {
				continue
			}
			return nil, fmt.Errorf("stat AGENTS.md %q: %w", path, statErr)
		}
		resolved := path
		if canonical, evalErr := filepath.EvalSymlinks(path); evalErr == nil {
			resolved = canonical
		}
		installed = append(installed, filepath.ToSlash(strings.TrimSpace(resolved)))
	}
	sort.Strings(installed)
	return installed, nil
}

var yamlUnmarshal = func(data []byte, out any) error {
	return yaml.Unmarshal(data, out)
}
