package runtime

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"builder/prompts"
	"builder/server/llm"
)

type metaContextKind uint8

const (
	metaContextKindUnknown metaContextKind = iota
	metaContextKindAgents
	metaContextKindSkills
	metaContextKindEnvironment
	metaContextKindHeadless
	metaContextKindHeadlessExit
)

type metaContextClassification struct {
	kind        metaContextKind
	key         string
	sourcePath  string
	messageType llm.MessageType
}

type metaContextBuildOptions struct {
	ExistingMessages          []llm.Message
	IncludeAgents             bool
	IncludeSkills             bool
	IncludeEnvironment        bool
	IncludeHeadless           bool
	IncludeHeadlessExit       bool
	IncludeSkillWarnings      bool
	PermissiveAgentsReadError bool
}

type metaContextBuildResult struct {
	Agents       []llm.Message
	SkillWarning []llm.Message
	Skills       []llm.Message
	Environment  []llm.Message
	Headless     []llm.Message
	HeadlessExit []llm.Message
}

func (r metaContextBuildResult) OrderedMetaMessages() []llm.Message {
	out := make([]llm.Message, 0, len(r.Agents)+len(r.Skills)+len(r.Environment)+len(r.Headless)+len(r.HeadlessExit))
	out = append(out, r.Agents...)
	out = append(out, r.Skills...)
	out = append(out, r.Environment...)
	out = append(out, r.Headless...)
	out = append(out, r.HeadlessExit...)
	return out
}

func (r metaContextBuildResult) OrderedInjectionMessages() []llm.Message {
	out := make([]llm.Message, 0, len(r.Agents)+len(r.SkillWarning)+len(r.Skills)+len(r.Environment)+len(r.Headless)+len(r.HeadlessExit))
	out = append(out, r.Agents...)
	out = append(out, r.SkillWarning...)
	out = append(out, r.Skills...)
	out = append(out, r.Environment...)
	out = append(out, r.Headless...)
	out = append(out, r.HeadlessExit...)
	return out
}

type metaContextBuilder struct {
	workspaceRoot  string
	model          string
	thinkingLevel  string
	disabledSkills map[string]bool
	now            time.Time
}

func newMetaContextBuilder(workspaceRoot, model, thinkingLevel string, disabledSkills map[string]bool, now time.Time) metaContextBuilder {
	return metaContextBuilder{
		workspaceRoot:  strings.TrimSpace(workspaceRoot),
		model:          strings.TrimSpace(model),
		thinkingLevel:  strings.TrimSpace(thinkingLevel),
		disabledSkills: normalizedDisabledSkills(disabledSkills),
		now:            now,
	}
}

func (b metaContextBuilder) Build(opts metaContextBuildOptions) (metaContextBuildResult, error) {
	ranks, rankErr := b.agentPathRanks()
	if rankErr != nil && opts.IncludeAgents && !opts.PermissiveAgentsReadError {
		return metaContextBuildResult{}, rankErr
	}
	collector := newMetaContextCollector(ranks)
	collector.addMessages(opts.ExistingMessages)

	if opts.IncludeAgents {
		agents, err := b.discoverAgents(opts.PermissiveAgentsReadError)
		if err != nil {
			return metaContextBuildResult{}, err
		}
		collector.addMessages(agents)
	}

	if opts.IncludeSkills {
		skills, issues, err := discoverInjectedSkills(b.workspaceRoot, b.disabledSkills)
		if err != nil {
			return metaContextBuildResult{}, err
		}
		if opts.IncludeSkillWarnings {
			collector.addWarnings(skillDiscoveryWarnings(issues))
		}
		if len(skills) > 0 {
			collector.addMessages([]llm.Message{{
				Role:        llm.RoleDeveloper,
				MessageType: llm.MessageTypeSkills,
				Content:     renderSkillsContext(skills),
			}})
		}
	}

	if opts.IncludeEnvironment {
		environmentMessage, err := environmentContextMessage(b.workspaceRoot, b.model, b.timestamp())
		if err != nil {
			return metaContextBuildResult{}, err
		}
		collector.addMessages([]llm.Message{{
			Role:        llm.RoleDeveloper,
			MessageType: llm.MessageTypeEnvironment,
			Content:     environmentMessage,
		}})
	}

	if opts.IncludeHeadless {
		if message, ok := headlessModeMetaMessage(); ok {
			collector.addMessages([]llm.Message{message})
		}
	}

	if opts.IncludeHeadlessExit {
		if message, ok := headlessModeExitMetaMessage(); ok {
			collector.addMessages([]llm.Message{message})
		}
	}

	return collector.result(), nil
}

func (b metaContextBuilder) timestamp() time.Time {
	if !b.now.IsZero() {
		return b.now
	}
	return time.Now()
}

func (b metaContextBuilder) agentPathRanks() (map[string]int, error) {
	paths, err := agentsInjectionPaths(b.workspaceRoot)
	if err != nil {
		return nil, err
	}
	ranks := make(map[string]int, len(paths))
	for idx, path := range paths {
		ranks[agentSourceKey(path)] = idx
	}
	return ranks, nil
}

func (b metaContextBuilder) discoverAgents(permissive bool) ([]llm.Message, error) {
	paths, err := agentsInjectionPaths(b.workspaceRoot)
	if err != nil {
		if permissive {
			return nil, nil
		}
		return nil, err
	}
	out := make([]llm.Message, 0, len(paths))
	for _, path := range paths {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			if errors.Is(readErr, os.ErrNotExist) || permissive {
				continue
			}
			return nil, fmt.Errorf("read AGENTS.md: %w", readErr)
		}
		out = append(out, llm.Message{
			Role:        llm.RoleDeveloper,
			MessageType: llm.MessageTypeAgentsMD,
			SourcePath:  path,
			Content:     renderAgentsContext(path, string(data)),
		})
	}
	return out, nil
}

func renderAgentsContext(path, contents string) string {
	return fmt.Sprintf("%s\nsource: %s\n\n```%s\n%s\n```", agentsInjectedHeader, path, agentsInjectedFenceLabel, contents)
}

func headlessModeMetaMessage() (llm.Message, bool) {
	content := strings.TrimSpace(prompts.HeadlessModePrompt)
	if content == "" {
		return llm.Message{}, false
	}
	return llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeHeadlessMode, Content: content}, true
}

func headlessModeExitMetaMessage() (llm.Message, bool) {
	content := strings.TrimSpace(prompts.HeadlessModeExitPrompt)
	if content == "" {
		return llm.Message{}, false
	}
	return llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeHeadlessModeExit, Content: content}, true
}

func skillDiscoveryWarnings(issues []skillDiscoveryIssue) []llm.Message {
	if len(issues) == 0 {
		return nil
	}
	out := make([]llm.Message, 0, len(issues))
	for _, issue := range issues {
		out = append(out, llm.Message{
			Role:        llm.RoleDeveloper,
			MessageType: llm.MessageTypeErrorFeedback,
			Content:     formatSkillDiscoveryWarning(issue),
		})
	}
	return out
}

type metaContextAgentMessage struct {
	rank    int
	seq     int
	message llm.Message
}

type metaContextCollector struct {
	agentRanks          map[string]int
	nextAgentSequence   int
	seenAgentKeys       map[string]bool
	seenWarningMessages map[string]bool
	agents              []metaContextAgentMessage
	skills              *llm.Message
	environment         *llm.Message
	headless            *llm.Message
	headlessExit        *llm.Message
	warnings            []llm.Message
}

func newMetaContextCollector(agentRanks map[string]int) *metaContextCollector {
	return &metaContextCollector{
		agentRanks:          agentRanks,
		seenAgentKeys:       make(map[string]bool),
		seenWarningMessages: make(map[string]bool),
	}
}

func (c *metaContextCollector) addMessages(messages []llm.Message) {
	for _, message := range messages {
		c.add(message)
	}
}

func (c *metaContextCollector) addWarnings(messages []llm.Message) {
	for _, message := range messages {
		if message.Role != llm.RoleDeveloper || message.MessageType != llm.MessageTypeErrorFeedback {
			continue
		}
		key := strings.TrimSpace(message.Content)
		if key == "" || c.seenWarningMessages[key] {
			continue
		}
		c.seenWarningMessages[key] = true
		c.warnings = append(c.warnings, message)
	}
}

func (c *metaContextCollector) add(message llm.Message) bool {
	classification, ok := classifyMetaContextMessage(message)
	if !ok {
		return false
	}
	message = canonicalizeMetaContextMessage(message, classification)
	if classification.key == "" {
		return false
	}
	if classification.kind == metaContextKindAgents {
		if c.seenAgentKeys[classification.key] {
			return false
		}
		c.seenAgentKeys[classification.key] = true
		rank := len(c.agentRanks) + c.nextAgentSequence
		if explicitRank, ok := c.agentRanks[classification.key]; ok {
			rank = explicitRank
		}
		c.agents = append(c.agents, metaContextAgentMessage{rank: rank, seq: c.nextAgentSequence, message: message})
		c.nextAgentSequence++
		return true
	}
	slot := c.slot(classification.kind)
	if slot == nil {
		return false
	}
	if *slot != nil {
		return false
	}
	copyMessage := message
	*slot = &copyMessage
	return true
}

func (c *metaContextCollector) slot(kind metaContextKind) **llm.Message {
	switch kind {
	case metaContextKindSkills:
		return &c.skills
	case metaContextKindEnvironment:
		return &c.environment
	case metaContextKindHeadless:
		return &c.headless
	case metaContextKindHeadlessExit:
		return &c.headlessExit
	default:
		return nil
	}
}

func (c *metaContextCollector) result() metaContextBuildResult {
	sort.SliceStable(c.agents, func(i, j int) bool {
		if c.agents[i].rank != c.agents[j].rank {
			return c.agents[i].rank < c.agents[j].rank
		}
		return c.agents[i].seq < c.agents[j].seq
	})
	result := metaContextBuildResult{
		Agents:       make([]llm.Message, 0, len(c.agents)),
		SkillWarning: append([]llm.Message(nil), c.warnings...),
	}
	for _, agent := range c.agents {
		result.Agents = append(result.Agents, agent.message)
	}
	if c.skills != nil {
		result.Skills = []llm.Message{*c.skills}
	}
	if c.environment != nil {
		result.Environment = []llm.Message{*c.environment}
	}
	if c.headless != nil {
		result.Headless = []llm.Message{*c.headless}
	}
	if c.headlessExit != nil {
		result.HeadlessExit = []llm.Message{*c.headlessExit}
	}
	return result
}

func splitMetaContextMessages(messages []llm.Message) ([]llm.Message, []llm.Message) {
	meta := make([]llm.Message, 0, 4)
	transcript := make([]llm.Message, 0, len(messages))
	for _, message := range messages {
		if _, ok := classifyMetaContextMessage(message); ok {
			meta = append(meta, message)
			continue
		}
		transcript = append(transcript, message)
	}
	return meta, transcript
}

func classifyMetaContextMessage(message llm.Message) (metaContextClassification, bool) {
	if message.Role != llm.RoleDeveloper {
		return metaContextClassification{}, false
	}
	switch message.MessageType {
	case llm.MessageTypeAgentsMD:
		sourcePath := agentSourceKey(message.SourcePath)
		if sourcePath == "" {
			return metaContextClassification{}, false
		}
		return metaContextClassification{
			kind:        metaContextKindAgents,
			key:         sourcePath,
			sourcePath:  sourcePath,
			messageType: llm.MessageTypeAgentsMD,
		}, true
	case llm.MessageTypeSkills:
		return metaContextClassification{kind: metaContextKindSkills, key: "skills", messageType: llm.MessageTypeSkills}, true
	case llm.MessageTypeEnvironment:
		return metaContextClassification{kind: metaContextKindEnvironment, key: "environment", messageType: llm.MessageTypeEnvironment}, true
	case llm.MessageTypeHeadlessMode:
		return metaContextClassification{kind: metaContextKindHeadless, key: "headless", messageType: llm.MessageTypeHeadlessMode}, true
	case llm.MessageTypeHeadlessModeExit:
		return metaContextClassification{kind: metaContextKindHeadlessExit, key: "headless_exit", messageType: llm.MessageTypeHeadlessModeExit}, true
	}
	return metaContextClassification{}, false
}

func canonicalizeMetaContextMessage(message llm.Message, classification metaContextClassification) llm.Message {
	message.Role = llm.RoleDeveloper
	message.MessageType = classification.messageType
	return message
}

func agentSourceKey(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	return filepath.Clean(trimmed)
}
