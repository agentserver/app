package ui

import (
	"context"
	"fmt"

	"github.com/agentserver/agentserver-pkg/internal/agentserver"
	"github.com/agentserver/agentserver-pkg/internal/modelserver"
	"github.com/agentserver/agentserver-pkg/internal/oauth"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/state"
)

type Deps struct {
	State             *state.Store
	Secrets           secrets.Store
	MS                *modelserver.Client
	AS                *agentserver.Client
	MSOAuth           oauth.Config
	ASOAuth           oauth.Config
	CodexConfigPath   string
	VSCodeUserDataDir string
	VSCodeExtDir      string
	EmbeddedVSIXPath  string
	CodexAbsPath      string

	// Used by Finalize (set by launcher; see P9.3)
	LauncherExePath   string
	OpenFolderExePath string
	IconPath          string
}

type realOrchestrator struct {
	d Deps
	// transient: in-flight device-code challenges per step
	msChallenge oauth.DeviceCodeChallenge
	asChallenge oauth.DeviceCodeChallenge
	msToken     oauth.Token
	asToken     oauth.Token
}

func NewRealOrchestrator(d Deps) Orchestrator {
	return &realOrchestrator{d: d}
}

func (r *realOrchestrator) State(ctx context.Context) (SanitizedState, error) {
	s, err := r.d.State.Load()
	if err != nil {
		return SanitizedState{}, err
	}
	return SanitizedState{
		SchemaVersion:          s.SchemaVersion,
		InstallID:              s.InstallID,
		OnboardingStatus:       string(s.Onboarding.Status),
		CompletedSteps:         append([]string(nil), s.Onboarding.CompletedSteps...),
		LastError:              s.Onboarding.LastError,
		ModelserverProjectID:   s.Modelserver.ProjectID,
		AgentserverWorkspaceID: s.Agentserver.WorkspaceID,
		VSCodePath:             s.VSCode.Path,
		VSCodeVersion:          s.VSCode.Version,
	}, nil
}

func (r *realOrchestrator) LoginModelserver(ctx context.Context) (oauth.DeviceCodeChallenge, error) {
	ch, err := oauth.RequestDeviceCode(ctx, r.d.MSOAuth)
	if err != nil {
		return oauth.DeviceCodeChallenge{}, err
	}
	r.msChallenge = ch
	return ch, nil
}

func (r *realOrchestrator) PollModelserverLogin(ctx context.Context) (modelserver.APIKey, error) {
	if r.msChallenge.DeviceCode == "" {
		return modelserver.APIKey{}, fmt.Errorf("no in-flight modelserver login")
	}
	tok, err := oauth.PollToken(ctx, r.d.MSOAuth, r.msChallenge)
	if err != nil {
		return modelserver.APIKey{}, err
	}
	r.msToken = tok
	proj, err := r.d.MS.PickOrCreateProject(ctx, tok.AccessToken, "default")
	if err != nil {
		return modelserver.APIKey{}, err
	}
	key, err := r.d.MS.CreateAPIKey(ctx, tok.AccessToken, proj.ID, "agentserver-vscode")
	if err != nil {
		return modelserver.APIKey{}, err
	}
	if err := r.d.Secrets.Set("modelserver_api_key", key.Secret); err != nil {
		return modelserver.APIKey{}, err
	}
	if err := r.d.State.Update(func(s *state.State) error {
		s.Modelserver.ProjectID = proj.ID
		s.Modelserver.APIKeySuffix = key.KeySuffix
		s.Onboarding.AddCompleted("modelserver_login")
		return nil
	}); err != nil {
		return modelserver.APIKey{}, err
	}
	return key, nil
}

func (r *realOrchestrator) LoginAgentserver(ctx context.Context) (oauth.DeviceCodeChallenge, error) {
	ch, err := oauth.RequestDeviceCode(ctx, r.d.ASOAuth)
	if err != nil {
		return oauth.DeviceCodeChallenge{}, err
	}
	r.asChallenge = ch
	return ch, nil
}

func (r *realOrchestrator) PollAgentserverLogin(ctx context.Context) (agentserver.WorkspaceAPIKey, error) {
	if r.asChallenge.DeviceCode == "" {
		return agentserver.WorkspaceAPIKey{}, fmt.Errorf("no in-flight agentserver login")
	}
	tok, err := oauth.PollToken(ctx, r.d.ASOAuth, r.asChallenge)
	if err != nil {
		return agentserver.WorkspaceAPIKey{}, err
	}
	r.asToken = tok
	ws, err := r.d.AS.GetOrCreateDefaultWorkspace(ctx, tok.AccessToken, "default")
	if err != nil {
		return agentserver.WorkspaceAPIKey{}, err
	}
	key, err := r.d.AS.CreateWorkspaceAPIKey(ctx, tok.AccessToken, ws.ID, "agentserver-vscode")
	if err != nil {
		return agentserver.WorkspaceAPIKey{}, err
	}
	if err := r.d.Secrets.Set("agentserver_ws_api_key", key.Secret); err != nil {
		return agentserver.WorkspaceAPIKey{}, err
	}
	if err := r.d.State.Update(func(s *state.State) error {
		s.Agentserver.WorkspaceID = ws.ID
		s.Agentserver.WorkspaceAPIKeySuffix = key.KeySuffix
		s.Onboarding.AddCompleted("agentserver_login")
		return nil
	}); err != nil {
		return agentserver.WorkspaceAPIKey{}, err
	}
	return key, nil
}

// EnsureVSCode + ConfigureVSCode bodies are wired in P9.2.
// Finalize is wired in P9.3. For now: stubs.
func (r *realOrchestrator) EnsureVSCode(ctx context.Context, ch chan<- ProgressEvent) error {
	return fmt.Errorf("EnsureVSCode: not wired yet (P9.2)")
}
func (r *realOrchestrator) ConfigureVSCode(ctx context.Context) error {
	return fmt.Errorf("ConfigureVSCode: not wired yet (P9.2)")
}
func (r *realOrchestrator) Finalize(ctx context.Context) error {
	return r.d.State.Update(func(s *state.State) error {
		s.Onboarding.Status = state.StatusComplete
		return nil
	})
}
func (r *realOrchestrator) Abort(ctx context.Context) error { return nil }
