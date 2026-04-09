package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"builder/server/auth"
	"builder/server/llm"
	serveronboarding "builder/server/onboarding"
	"builder/shared/config"
	tea "github.com/charmbracelet/bubbletea"
)

type interactiveOnboardingRunner struct{}

func (interactiveOnboardingRunner) RunInteractiveOnboarding(ctx context.Context, cfg config.App, authState auth.State) (serveronboarding.Result, error) {
	result, err := runOnboardingFlow(cfg, authState)
	if err != nil {
		return serveronboarding.Result{}, err
	}
	return serveronboarding.Result{
		Completed:            result.Completed,
		CreatedDefaultConfig: result.CreatedDefaultConfig,
		SettingsPath:         result.SettingsPath,
	}, nil
}

func ensureOnboardingReady(ctx context.Context, cfg config.App, mgr *auth.Manager, interactor authInteractor, reloadConfig func() (config.App, error)) (config.App, bool, error) {
	return serveronboarding.EnsureReady(ctx, cfg, mgr, interactor != nil && interactor.Interactive(), reloadConfig, interactiveOnboardingRunner{})
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
		skillImport:          onboardingImportSelection{Mode: onboardingImportModeNone},
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
