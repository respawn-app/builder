package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"builder/internal/auth"
	"builder/internal/config"
	"builder/internal/llm"
	tea "github.com/charmbracelet/bubbletea"
)

func ensureOnboardingReady(ctx context.Context, cfg config.App, mgr *auth.Manager, interactor authInteractor, reloadConfig func() (config.App, error)) (config.App, bool, error) {
	if cfg.Source.SettingsFileExists {
		return cfg, false, nil
	}
	if _, ok := interactor.(*interactiveAuthInteractor); !ok {
		path, created, err := config.WriteDefaultSettingsFile()
		if err != nil {
			return cfg, false, err
		}
		reloaded, err := reloadConfig()
		if err != nil {
			return cfg, false, err
		}
		reloaded.Source.CreatedDefaultConfig = created
		reloaded.Source.SettingsPath = path
		reloaded.Source.SettingsFileExists = true
		return reloaded, true, nil
	}
	if mgr == nil {
		return cfg, false, errors.New("auth manager is required for onboarding")
	}
	state, err := mgr.Load(ctx)
	if err != nil {
		return cfg, false, err
	}
	result, err := runOnboardingFlow(cfg, state)
	if err != nil {
		return cfg, false, err
	}
	if !result.Completed {
		return cfg, false, errors.New("first-time setup canceled")
	}
	reloaded, err := reloadConfig()
	if err != nil {
		return cfg, false, err
	}
	reloaded.Source.CreatedDefaultConfig = result.CreatedDefaultConfig
	reloaded.Source.SettingsFileExists = true
	if strings.TrimSpace(result.SettingsPath) != "" {
		reloaded.Source.SettingsPath = result.SettingsPath
	}
	return reloaded, true, nil
}

func runOnboardingFlow(cfg config.App, authState auth.State) (onboardingResult, error) {
	providerCaps, err := onboardingProviderCapabilities(authState, cfg.Settings)
	if err != nil {
		return onboardingResult{}, err
	}
	state := onboardingFlowState{
		settings:             cfg.Settings,
		baselineSettings:     cfg.Settings,
		theme:                cfg.Settings.Theme,
		alternateScreen:      cfg.Settings.TUIAlternateScreen,
		authState:            authState,
		providerCapabilities: providerCaps,
		skillImport:          onboardingImportSelection{Mode: onboardingImportModeMergeCopy},
		commandImport:        onboardingImportSelection{Mode: onboardingImportModeNone},
	}
	model := newOnboardingModel(cfg.PersistenceRoot, state)
	options := []tea.ProgramOption{}
	if shouldUseStartupPickerAltScreen(cfg.Settings.TUIAlternateScreen) {
		options = append(options, tea.WithAltScreen())
	}
	program := tea.NewProgram(model, options...)
	finalModel, err := program.Run()
	if err != nil {
		return onboardingResult{}, err
	}
	finalized, ok := finalModel.(*onboardingModel)
	if !ok {
		return onboardingResult{}, fmt.Errorf("unexpected onboarding model type %T", finalModel)
	}
	if finalized.canceled {
		return onboardingResult{}, errors.New("first-time setup canceled")
	}
	return finalized.result, nil
}

func onboardingProviderCapabilities(authState auth.State, settings config.Settings) (llm.ProviderCapabilities, error) {
	providerID := strings.TrimSpace(settings.ProviderOverride)
	if providerID == "" {
		switch authState.Method.Type {
		case auth.MethodOAuth:
			providerID = "chatgpt-codex"
		case auth.MethodAPIKey:
			if strings.TrimSpace(settings.OpenAIBaseURL) != "" {
				providerID = "openai-compatible"
			} else {
				providerID = "openai"
			}
		default:
			providerID = "openai"
		}
	}
	return llm.InferProviderCapabilities(providerID)
}

func filepathDir(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	return filepath.Dir(path)
}
