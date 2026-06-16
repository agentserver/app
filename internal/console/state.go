package console

import (
	"context"
	"errors"
	"math"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/agentserver/agentserver-pkg/internal/agentserver"
	"github.com/agentserver/agentserver-pkg/internal/modelserver"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/slave"
	"github.com/agentserver/agentserver-pkg/internal/state"
	"github.com/agentserver/agentserver-pkg/internal/tokenrefresh"
	"github.com/agentserver/agentserver-pkg/internal/updater"
)

type Deps struct {
	State                    secretsStateStore
	Secrets                  secrets.Store
	MS                       *modelserver.Client
	MSProxy                  *modelserver.Client
	AS                       *agentserver.Client
	Slaves                   *slave.Manager
	Updates                  *updater.Service
	PendingSlaveRestartsPath string
	ModelserverWebBaseURL    string
	RefreshModelserverToken  func(context.Context) error
	OpenFrontend             func(context.Context) error
	OpenURL                  func(string) error
	SelectFolder             func(context.Context) (string, error)
	Quit                     func()
	Now                      func() time.Time
}

type secretsStateStore interface {
	Load() (*state.State, error)
	Update(func(*state.State) error) error
}

type State struct {
	FrontendMode     string          `json:"frontend_mode"`
	FrontendName     string          `json:"frontend_name"`
	OnboardingStatus string          `json:"onboarding_status"`
	Modelserver      ModelserverView `json:"modelserver"`
	Agentserver      AgentserverView `json:"agentserver"`
	Quotas           []QuotaWindow   `json:"quotas"`
	QuotaError       string          `json:"quota_error,omitempty"`
	SubscriptionURL  string          `json:"subscription_url,omitempty"`
	LastRefreshedAt  string          `json:"last_refreshed_at"`
}

type ModelserverView struct {
	ProjectID         string `json:"project_id,omitempty"`
	ProjectName       string `json:"project_name,omitempty"`
	ReconnectRequired bool   `json:"reconnect_required,omitempty"`
	AuthMessage       string `json:"auth_message,omitempty"`
}

type AgentserverView struct {
	WorkspaceID       string `json:"workspace_id,omitempty"`
	WorkspaceName     string `json:"workspace_name,omitempty"`
	ReconnectRequired bool   `json:"reconnect_required,omitempty"`
	AuthMessage       string `json:"auth_message,omitempty"`
}

type SlaveRemoteOpenResult struct {
	State string `json:"state"`
	URL   string `json:"url,omitempty"`
}

type QuotaWindow struct {
	Window              string  `json:"window"`
	Percentage          float64 `json:"percentage"`
	RemainingPercentage float64 `json:"remaining_percentage"`
	ResetsAt            string  `json:"resets_at,omitempty"`
}

type Controller struct {
	d               Deps
	updateInstallMu sync.Mutex
	refreshMu       sync.Mutex
}

func NewController(d Deps) *Controller {
	return &Controller{d: d}
}

func (c *Controller) State(ctx context.Context) (State, error) {
	if c.d.State == nil {
		return State{}, errors.New("console: state store required")
	}
	st, err := c.d.State.Load()
	if err != nil {
		return State{}, err
	}

	mode := state.NormalizeFrontendMode(st.FrontendMode)
	out := State{
		FrontendMode:     string(mode),
		FrontendName:     frontendName(mode),
		OnboardingStatus: string(st.Onboarding.Status),
		Modelserver:      ModelserverView{ProjectID: st.Modelserver.ProjectID},
		Agentserver: AgentserverView{
			WorkspaceID:   st.Agentserver.WorkspaceID,
			WorkspaceName: st.Agentserver.WorkspaceName,
		},
		LastRefreshedAt: time.Now().UTC().Format(time.RFC3339),
	}
	out.SubscriptionURL = modelserverSubscriptionURL(c.d.ModelserverWebBaseURL, out.Modelserver.ProjectID)

	msToken := c.secret(tokenrefresh.AccessTokenKey)
	preRefreshErr := c.refreshExpiredModelserverToken(ctx, st, msToken)
	msToken = c.secret(tokenrefresh.AccessTokenKey)
	asToken := c.secret("agentserver_ws_api_key")
	c.applyModelserverAuthState(st, msToken, &out)
	c.applyAgentserverAuthState(st, asToken, &out)
	if tokenrefresh.ReauthRequired(preRefreshErr) {
		markModelserverReconnect(&out)
	}
	if (c.d.MS != nil || c.d.MSProxy != nil) && msToken != "" {
		if c.d.MS != nil && out.Modelserver.ProjectID != "" {
			projects, err := c.d.MS.ListProjects(ctx, msToken)
			if err == nil {
				for _, p := range projects {
					if p.ID == out.Modelserver.ProjectID {
						out.Modelserver.ProjectName = p.Name
						break
					}
				}
			}
		}
		usageClient := c.d.MSProxy
		if usageClient == nil {
			usageClient = c.d.MS
		}
		usage, err := c.modelserverUsage(ctx, usageClient, msToken, out.Modelserver.ProjectID)
		if err != nil && isModelserverAuthError(err) && c.canRefreshModelserverToken(st) {
			if refreshErr := c.refreshModelserverToken(ctx, msToken); refreshErr != nil {
				if tokenrefresh.ReauthRequired(refreshErr) {
					markModelserverReconnect(&out)
				}
			} else if refreshedToken := c.secret(tokenrefresh.AccessTokenKey); refreshedToken != "" {
				msToken = refreshedToken
				usage, err = c.modelserverUsage(ctx, usageClient, msToken, out.Modelserver.ProjectID)
			}
		}
		if err != nil {
			out.QuotaError = err.Error()
			if isModelserverAuthError(err) && !c.canRefreshModelserverToken(st) {
				markModelserverReconnect(&out)
			}
		} else {
			out.Quotas = quotaWindows(usage)
		}
	}
	if c.d.AS != nil && asToken != "" {
		identity, err := c.d.AS.Whoami(ctx, asToken)
		if err == nil {
			if identity.Workspace.ID != "" {
				out.Agentserver.WorkspaceID = identity.Workspace.ID
			}
			out.Agentserver.WorkspaceName = identity.Workspace.Name
		} else if isAgentserverAuthError(err) {
			markAgentserverReconnect(&out)
		}
	}

	return out, nil
}

func (c *Controller) Refresh(ctx context.Context) (State, error) {
	return c.State(ctx)
}

func (c *Controller) Slaves(ctx context.Context) (slave.Machine, []slave.Slave, error) {
	if c.d.Slaves == nil {
		return slave.Machine{}, nil, errors.New("console: slave manager unavailable")
	}
	return c.d.Slaves.List(ctx)
}

func (c *Controller) CreateSlave(ctx context.Context, in slave.CreateInput) (slave.Slave, error) {
	if c.d.Slaves == nil {
		return slave.Slave{}, errors.New("console: slave manager unavailable")
	}
	return c.d.Slaves.CreateAndStart(ctx, in)
}

func (c *Controller) SelectFolder(ctx context.Context) (string, error) {
	if c.d.SelectFolder == nil {
		return "", errors.New("console: folder picker unavailable")
	}
	return c.d.SelectFolder(ctx)
}

func (c *Controller) RestartSlave(ctx context.Context, id string) (slave.Slave, error) {
	if c.d.Slaves == nil {
		return slave.Slave{}, errors.New("console: slave manager unavailable")
	}
	return c.d.Slaves.Restart(ctx, id)
}

func (c *Controller) PauseSlave(ctx context.Context, id string) (slave.Slave, error) {
	if c.d.Slaves == nil {
		return slave.Slave{}, errors.New("console: slave manager unavailable")
	}
	return c.d.Slaves.Pause(ctx, id)
}

func (c *Controller) DeleteSlave(ctx context.Context, id string) error {
	if c.d.Slaves == nil {
		return errors.New("console: slave manager unavailable")
	}
	return c.d.Slaves.Delete(ctx, id)
}

func (c *Controller) OpenSlaveRemote(ctx context.Context, id string) (SlaveRemoteOpenResult, error) {
	if c.d.Slaves == nil {
		return SlaveRemoteOpenResult{}, errors.New("console: slave manager unavailable")
	}
	identity, err := c.d.Slaves.RemoteIdentity(ctx, id)
	if errors.Is(err, slave.ErrRemoteIdentityUnavailable) {
		return SlaveRemoteOpenResult{State: "unavailable"}, nil
	}
	if err != nil {
		return SlaveRemoteOpenResult{}, err
	}
	workspaceID := strings.TrimSpace(identity.WorkspaceID)
	if workspaceID == "" {
		workspaceID = c.agentserverWorkspaceID()
	}
	if workspaceID == "" || strings.TrimSpace(identity.SandboxID) == "" {
		return SlaveRemoteOpenResult{State: "unavailable"}, nil
	}
	remoteURL := slaveRemoteURL(identity.ServerURL, workspaceID, identity.SandboxID)
	if c.d.OpenURL != nil {
		if err := c.d.OpenURL(remoteURL); err != nil {
			return SlaveRemoteOpenResult{}, err
		}
	}
	return SlaveRemoteOpenResult{State: "opened", URL: remoteURL}, nil
}

func (c *Controller) Healthy(context.Context) bool {
	return true
}

func (c *Controller) OpenFrontend(ctx context.Context) error {
	if c.d.OpenFrontend == nil {
		return nil
	}
	return c.d.OpenFrontend(ctx)
}

func (c *Controller) OpenSubscription(ctx context.Context) error {
	st, err := c.State(ctx)
	if err != nil {
		return err
	}
	if st.SubscriptionURL == "" {
		return errors.New("console: subscription URL unavailable")
	}
	if c.d.OpenURL == nil {
		return nil
	}
	return c.d.OpenURL(st.SubscriptionURL)
}

func (c *Controller) LogoutModelserver(context.Context) error {
	if c.d.Secrets != nil {
		for _, key := range []string{
			tokenrefresh.AccessTokenKey,
			tokenrefresh.RefreshTokenKey,
			tokenrefresh.AccessTokenExpiresAtKey,
			tokenrefresh.ReauthRequiredKey,
			tokenrefresh.RefreshErrorKey,
			tokenrefresh.RefreshErrorAtKey,
		} {
			if err := c.d.Secrets.Delete(key); err != nil {
				return err
			}
		}
	}
	if c.d.State != nil {
		return c.d.State.Update(func(s *state.State) error {
			s.Modelserver.ProjectID = ""
			s.Modelserver.APIKeySuffix = ""
			return nil
		})
	}
	return nil
}

func (c *Controller) Quit(context.Context) error {
	if c.d.Quit != nil {
		c.d.Quit()
	}
	return nil
}

func (c *Controller) secret(key string) string {
	if c.d.Secrets == nil {
		return ""
	}
	v, err := c.d.Secrets.Get(key)
	if err != nil {
		return ""
	}
	return v
}

func (c *Controller) modelserverUsage(ctx context.Context, usageClient *modelserver.Client, token, projectID string) ([]modelserver.SubscriptionUsageWindow, error) {
	usage, err := usageClient.ProxyUsage(ctx, token)
	if err == nil {
		return usage, nil
	}
	if c.d.MS != nil && projectID != "" {
		return c.d.MS.SubscriptionUsage(ctx, token, projectID)
	}
	return nil, err
}

func (c *Controller) refreshExpiredModelserverToken(ctx context.Context, st *state.State, msToken string) error {
	if !c.canRefreshModelserverToken(st) {
		return nil
	}
	if !c.modelserverAccessTokenNeedsRefresh(msToken) {
		return nil
	}
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	if !c.canRefreshModelserverToken(st) {
		return nil
	}
	if !c.modelserverAccessTokenNeedsRefresh(c.secret(tokenrefresh.AccessTokenKey)) {
		return nil
	}
	return c.callRefreshModelserverToken(ctx)
}

func (c *Controller) canRefreshModelserverToken(st *state.State) bool {
	if st == nil || !st.Onboarding.HasCompleted("modelserver_login") {
		return false
	}
	if c.d.RefreshModelserverToken == nil {
		return false
	}
	if c.secret(tokenrefresh.ReauthRequiredKey) == "true" {
		return false
	}
	return c.secret(tokenrefresh.RefreshTokenKey) != ""
}

func (c *Controller) modelserverAccessTokenNeedsRefresh(msToken string) bool {
	if msToken == "" {
		return true
	}
	raw := c.secret(tokenrefresh.AccessTokenExpiresAtKey)
	if raw == "" {
		return true
	}
	expiresAt, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return true
	}
	return !expiresAt.After(c.now().Add(2 * time.Minute))
}

func (c *Controller) refreshModelserverToken(ctx context.Context, staleToken string) error {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	if staleToken != "" {
		current := c.secret(tokenrefresh.AccessTokenKey)
		if current != "" && current != staleToken {
			return nil
		}
	}
	return c.callRefreshModelserverToken(ctx)
}

func (c *Controller) callRefreshModelserverToken(ctx context.Context) error {
	if c.d.RefreshModelserverToken == nil {
		return errors.New("console: modelserver token refresh unavailable")
	}
	return c.d.RefreshModelserverToken(ctx)
}

func (c *Controller) now() time.Time {
	if c.d.Now != nil {
		return c.d.Now()
	}
	return time.Now().UTC()
}

func (c *Controller) applyModelserverAuthState(st *state.State, msToken string, out *State) {
	if st == nil || out == nil || !st.Onboarding.HasCompleted("modelserver_login") {
		return
	}
	if c.secret(tokenrefresh.ReauthRequiredKey) == "true" {
		markModelserverReconnect(out)
		return
	}
	if msToken == "" || c.secret(tokenrefresh.RefreshTokenKey) == "" {
		markModelserverReconnect(out)
	}
}

func markModelserverReconnect(out *State) {
	out.Modelserver.ReconnectRequired = true
	out.Modelserver.AuthMessage = "大模型连接已失效，请重新连接。"
}

func (c *Controller) applyAgentserverAuthState(st *state.State, asToken string, out *State) {
	if st == nil || out == nil || !st.Onboarding.HasCompleted("agentserver_login") {
		return
	}
	if asToken == "" {
		markAgentserverReconnect(out)
	}
}

func markAgentserverReconnect(out *State) {
	out.Agentserver.ReconnectRequired = true
	out.Agentserver.AuthMessage = "星池工作区连接已失效，请重新连接。"
}

func isModelserverAuthError(err error) bool {
	return isAuthError(err)
}

func isAgentserverAuthError(err error) bool {
	return isAuthError(err)
}

func isAuthError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "status 401") ||
		strings.Contains(msg, "unauthorized") ||
		strings.Contains(msg, "invalid or expired token")
}

func quotaWindows(in []modelserver.SubscriptionUsageWindow) []QuotaWindow {
	out := make([]QuotaWindow, 0, len(in))
	for _, w := range in {
		remaining := math.Round(math.Max(0, 100-w.Percentage)*100) / 100
		out = append(out, QuotaWindow{
			Window:              w.Window,
			Percentage:          w.Percentage,
			RemainingPercentage: remaining,
			ResetsAt:            w.ResetsAt,
		})
	}
	return out
}

func frontendName(mode state.FrontendMode) string {
	switch state.NormalizeFrontendMode(mode) {
	case state.FrontendModeMinimalVSCode:
		return "极简界面"
	case state.FrontendModeOpenCodeDesktop:
		return "OpenCode Desktop"
	default:
		return "Codex Desktop"
	}
}

func (c *Controller) agentserverWorkspaceID() string {
	if c.d.State == nil {
		return ""
	}
	st, err := c.d.State.Load()
	if err != nil || st == nil {
		return ""
	}
	return strings.TrimSpace(st.Agentserver.WorkspaceID)
}

func slaveRemoteURL(baseURL, workspaceID, sandboxID string) string {
	base := strings.TrimRight(defaultString(strings.TrimSpace(baseURL), slave.DefaultServerURL), "/")
	return base + "/w/" + url.PathEscape(workspaceID) + "/sandboxes/" + url.PathEscape(sandboxID)
}

func defaultString(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}

func modelserverSubscriptionURL(baseURL, projectID string) string {
	base := strings.TrimRight(defaultString(baseURL, "https://code.cs.ac.cn"), "/")
	if projectID == "" {
		return base + "/projects"
	}
	return base + "/projects/" + projectID + "/subscription"
}
