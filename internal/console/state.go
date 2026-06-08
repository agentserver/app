package console

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/agentserver/agentserver-pkg/internal/agentserver"
	"github.com/agentserver/agentserver-pkg/internal/modelserver"
	"github.com/agentserver/agentserver-pkg/internal/secrets"
	"github.com/agentserver/agentserver-pkg/internal/state"
	"github.com/agentserver/agentserver-pkg/internal/tokenrefresh"
)

type Deps struct {
	State                 secretsStateStore
	Secrets               secrets.Store
	MS                    *modelserver.Client
	MSProxy               *modelserver.Client
	AS                    *agentserver.Client
	ModelserverWebBaseURL string
	OpenFrontend          func(context.Context) error
	OpenURL               func(string) error
	Quit                  func()
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
	WorkspaceID   string `json:"workspace_id,omitempty"`
	WorkspaceName string `json:"workspace_name,omitempty"`
}

type QuotaWindow struct {
	Window              string  `json:"window"`
	Percentage          float64 `json:"percentage"`
	RemainingPercentage float64 `json:"remaining_percentage"`
	ResetsAt            string  `json:"resets_at,omitempty"`
}

type Controller struct {
	d Deps
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

	msToken := c.secret("modelserver_api_key")
	asToken := c.secret("agentserver_ws_api_key")
	c.applyModelserverAuthState(st, msToken, &out)
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
		usage, err := usageClient.ProxyUsage(ctx, msToken)
		if err != nil {
			if c.d.MS != nil && out.Modelserver.ProjectID != "" {
				usage, err = c.d.MS.SubscriptionUsage(ctx, msToken, out.Modelserver.ProjectID)
			}
		}
		if err != nil {
			out.QuotaError = err.Error()
			if isModelserverAuthError(err) {
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
		}
	}

	return out, nil
}

func (c *Controller) Refresh(ctx context.Context) (State, error) {
	return c.State(ctx)
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
			tokenrefresh.RefreshErrorKey,
			tokenrefresh.RefreshErrorAtKey,
		} {
			if err := c.d.Secrets.Delete(key); err != nil {
				return err
			}
		}
		if err := c.d.Secrets.Set(tokenrefresh.ReauthRequiredKey, "true"); err != nil {
			return err
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

func isModelserverAuthError(err error) bool {
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
	if state.NormalizeFrontendMode(mode) == state.FrontendModeMinimalVSCode {
		return "极简界面"
	}
	return "Codex Desktop"
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
