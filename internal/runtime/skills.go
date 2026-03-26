package runtime

import (
	"builder/prompts"
	"errors"
	"fmt"
	"io/fs"
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

type skillDiscoveryIssue struct {
	Name   string
	Path   string
	Reason string
}

func skillsContextMessage(workspaceRoot string) (string, bool, error) {
	return skillsContextMessageWithDisabled(workspaceRoot, nil)
}

func skillsContextMessageWithDisabled(workspaceRoot string, disabledSkills map[string]bool) (string, bool, error) {
	skills, _, err := discoverInjectedSkills(workspaceRoot, normalizedDisabledSkills(disabledSkills))
	if err != nil {
		return "", false, err
	}
	if len(skills) == 0 {
		return "", false, nil
	}
	return renderSkillsContext(skills), true, nil
}

func discoverInjectedSkills(workspaceRoot string, disabledSkills map[string]bool) ([]injectedSkill, []skillDiscoveryIssue, error) {
	roots, err := skillsInjectionRoots(workspaceRoot)
	if err != nil {
		return nil, nil, err
	}
	out := make([]injectedSkill, 0)
	issues := make([]skillDiscoveryIssue, 0)
	seenPaths := map[string]bool{}
	for _, root := range roots {
		entries, readErr := readSkillsDir(root)
		if readErr != nil {
			if os.IsNotExist(readErr) {
				continue
			}
			return nil, nil, fmt.Errorf("read skills directory %q: %w", root, readErr)
		}
		for _, entry := range entries {
			resolution := resolveSkillDir(root, entry)
			if resolution.Issue != nil {
				issues = append(issues, *resolution.Issue)
			}
			if !resolution.Discoverable {
				continue
			}
			skillPath := filepath.Join(resolution.SkillDir, skillFileName)
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
	return out, issues, nil
}

type skillDirResolution struct {
	SkillDir     string
	Discoverable bool
	Issue        *skillDiscoveryIssue
}

func resolveSkillDir(root string, entry os.DirEntry) skillDirResolution {
	skillDir := filepath.Join(root, entry.Name())
	info, err := os.Lstat(skillDir)
	if err != nil {
		return skillDirResolution{Issue: &skillDiscoveryIssue{
			Name:   sanitizeSkillSingleLine(entry.Name()),
			Path:   filepath.ToSlash(skillDir),
			Reason: formatSkillDirResolutionFailure(err),
		}}
	}
	if info.IsDir() {
		return skillDirResolution{SkillDir: skillDir, Discoverable: true}
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return skillDirResolution{}
	}
	targetInfo, err := os.Stat(skillDir)
	if err != nil {
		return skillDirResolution{Issue: &skillDiscoveryIssue{
			Name:   sanitizeSkillSingleLine(entry.Name()),
			Path:   filepath.ToSlash(skillDir),
			Reason: formatSkillDirResolutionFailure(err),
		}}
	}
	if targetInfo.IsDir() {
		return skillDirResolution{SkillDir: skillDir, Discoverable: true}
	}
	return skillDirResolution{Issue: &skillDiscoveryIssue{
		Name:   sanitizeSkillSingleLine(entry.Name()),
		Path:   filepath.ToSlash(skillDir),
		Reason: "symlink target is not a directory",
	}}
}

func formatSkillDirResolutionFailure(err error) string {
	if os.IsNotExist(err) {
		return "symlink target does not exist"
	}
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return strings.TrimSpace(pathErr.Err.Error())
	}
	return strings.TrimSpace(err.Error())
}

func formatSkillDiscoveryWarning(issue skillDiscoveryIssue) string {
	name := strings.TrimSpace(issue.Name)
	if name == "" {
		name = filepath.Base(strings.TrimSpace(issue.Path))
	}
	if strings.TrimSpace(issue.Path) == "" {
		return fmt.Sprintf("Skipped skill %q: %s", name, issue.Reason)
	}
	return fmt.Sprintf("Skipped skill %q at %s: %s", name, issue.Path, issue.Reason)
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
