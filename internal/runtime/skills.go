package runtime

import (
	"builder/prompts"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	skillsDirName             = "skills"
	skillFileName             = "SKILL.md"
	skillsInjectedHeader      = "## Skills"
	skillsInjectedDescription = "A skill is a set of local instructions to follow that is stored in a `SKILL.md` file. Below is the list of skills that can be used. Each entry includes a name, description, and file path so you can open the source for full instructions when using a specific skill."
	skillsAvailableHeader     = "### Available skills"
	skillsHowToUseHeader      = "### How to use skills"
)

var skillsHowToUseRules = strings.TrimSpace(prompts.SkillsHowToUseRulesPrompt)
var readSkillsDir = os.ReadDir

type injectedSkill struct {
	Name        string
	Description string
	Path        string
}

type SkillMetadata struct {
	Name        string
	Description string
	Path        string
}

type skillFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

func skillsContextMessage(workspaceRoot string) (string, bool, error) {
	return skillsContextMessageWithDisabled(workspaceRoot, nil)
}

func skillsContextMessageWithDisabled(workspaceRoot string, disabledSkills map[string]bool) (string, bool, error) {
	skills, err := discoverInjectedSkills(workspaceRoot, normalizedDisabledSkills(disabledSkills))
	if err != nil {
		return "", false, err
	}
	if len(skills) == 0 {
		return "", false, nil
	}
	return renderSkillsContext(skills), true, nil
}

func discoverInjectedSkills(workspaceRoot string, disabledSkills map[string]bool) ([]injectedSkill, error) {
	roots, err := skillsInjectionRoots(workspaceRoot)
	if err != nil {
		return nil, err
	}
	out := make([]injectedSkill, 0)
	seenPaths := map[string]bool{}
	for _, root := range roots {
		entries, readErr := readSkillsDir(root)
		if readErr != nil {
			if os.IsNotExist(readErr) {
				continue
			}
			return nil, fmt.Errorf("read skills directory %q: %w", root, readErr)
		}
		for _, entry := range entries {
			skillDir, ok, err := resolveSkillDir(root, entry.Name())
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			skillPath := filepath.Join(skillDir, skillFileName)
			skill, ok := parseInjectedSkill(skillPath)
			if !ok {
				continue
			}
			if disabledSkills[normalizeSkillToggleName(skill.Name)] {
				continue
			}
			if seenPaths[skill.Path] {
				continue
			}
			seenPaths[skill.Path] = true
			out = append(out, skill)
		}
	}
	return out, nil
}

func resolveSkillDir(root string, entryName string) (string, bool, error) {
	skillDir := filepath.Join(root, entryName)
	info, err := os.Stat(skillDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("stat skill path %q: %w", skillDir, err)
	}
	if !info.IsDir() {
		return "", false, nil
	}
	return skillDir, true, nil
}

func skillsInjectionRoots(workspaceRoot string) ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home dir: %w", err)
	}

	paths := make([]string, 0, 2)
	seen := map[string]bool{}
	addPath := func(path string) {
		cleaned := filepath.Clean(path)
		if cleaned == "" || seen[cleaned] {
			return
		}
		seen[cleaned] = true
		paths = append(paths, cleaned)
	}

	addPath(filepath.Join(home, agentsGlobalDirName, skillsDirName))
	if strings.TrimSpace(workspaceRoot) != "" {
		addPath(filepath.Join(workspaceRoot, agentsGlobalDirName, skillsDirName))
	}
	return paths, nil
}

func parseInjectedSkill(path string) (injectedSkill, bool) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return injectedSkill{}, false
	}
	frontmatter, ok := extractSkillFrontmatter(string(contents))
	if !ok {
		return injectedSkill{}, false
	}
	var parsed skillFrontmatter
	if err := yaml.Unmarshal([]byte(frontmatter), &parsed); err != nil {
		return injectedSkill{}, false
	}
	name := sanitizeSkillSingleLine(parsed.Name)
	if name == "" {
		name = sanitizeSkillSingleLine(filepath.Base(filepath.Dir(path)))
	}
	description := sanitizeSkillSingleLine(parsed.Description)
	if name == "" || description == "" {
		return injectedSkill{}, false
	}
	resolvedPath := path
	if canonical, err := filepath.EvalSymlinks(path); err == nil {
		resolvedPath = canonical
	}
	return injectedSkill{
		Name:        name,
		Description: description,
		Path:        filepath.ToSlash(resolvedPath),
	}, true
}

func ParseSkillMetadata(path string) (SkillMetadata, bool) {
	skill, ok := parseInjectedSkill(path)
	if !ok {
		return SkillMetadata{}, false
	}
	return SkillMetadata{Name: skill.Name, Description: skill.Description, Path: skill.Path}, true
}

func extractSkillFrontmatter(contents string) (string, bool) {
	lines := strings.Split(contents, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", false
	}

	frontmatterLines := make([]string, 0)
	foundClosing := false
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			foundClosing = true
			break
		}
		frontmatterLines = append(frontmatterLines, line)
	}
	if len(frontmatterLines) == 0 || !foundClosing {
		return "", false
	}
	return strings.Join(frontmatterLines, "\n"), true
}

func sanitizeSkillSingleLine(raw string) string {
	parts := strings.Fields(raw)
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}

func normalizeSkillToggleName(raw string) string {
	return strings.ToLower(sanitizeSkillSingleLine(raw))
}

func normalizedDisabledSkills(disabledSkills map[string]bool) map[string]bool {
	if len(disabledSkills) == 0 {
		return nil
	}
	normalized := make(map[string]bool, len(disabledSkills))
	for name, disabled := range disabledSkills {
		if !disabled {
			continue
		}
		key := normalizeSkillToggleName(name)
		if key == "" {
			continue
		}
		normalized[key] = true
	}
	return normalized
}

func renderSkillsContext(skills []injectedSkill) string {
	lines := make([]string, 0, len(skills)+5)
	lines = append(lines, skillsInjectedHeader)
	lines = append(lines, skillsInjectedDescription)
	lines = append(lines, skillsAvailableHeader)
	for _, skill := range skills {
		lines = append(lines, fmt.Sprintf("- %s: %s (file: %s)", skill.Name, skill.Description, skill.Path))
	}
	lines = append(lines, skillsHowToUseHeader)
	lines = append(lines, skillsHowToUseRules)
	return strings.Join(lines, "\n")
}
