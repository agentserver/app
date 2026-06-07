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
)

type Deps struct {
	State                 secretsStateStore
	Secrets               secrets.Store
	MS                    *modelserver.Client
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
	ProjectID   string `json:"project_id,omitempty"`
	ProjectName string `json:"project_name,omitempty"`
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
		Agentserver:      AgentserverView{WorkspaceID: st.Agentserver.WorkspaceID},
		LastRefreshedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if out.Modelserver.ProjectID != "" {
		out.SubscriptionURL = strings.TrimRight(defaultString(c.d.ModelserverWebBaseURL, "https://code.cs.ac.cn"), "/") +
			"/projects/" + out.Modelserver.ProjectID + "/subscription"
	}

	msToken := c.secret("modelserver_api_key")
	asToken := c.secret("agentserver_ws_api_key")
	if c.d.MS != nil && msToken != "" {
		projects, err := c.d.MS.ListProjects(ctx, msToken)
		if err == nil {
			for _, p := range projects {
				if p.ID == out.Modelserver.ProjectID {
					out.Modelserver.ProjectName = p.Name
					break
				}
			}
		}
		if out.Modelserver.ProjectID != "" {
			usage, err := c.d.MS.SubscriptionUsage(ctx, msToken, out.Modelserver.ProjectID)
			if err != nil {
				out.QuotaError = err.Error()
			} else {
				out.Quotas = quotaWindows(usage)
			}
		}
	}
	if c.d.AS != nil && asToken != "" {
		workspaces, err := c.d.AS.ListWorkspaces(ctx, asToken)
		if err == nil {
			for _, w := range workspaces {
				if w.ID == out.Agentserver.WorkspaceID {
					out.Agentserver.WorkspaceName = w.Name
					break
				}
			}
		}
	}

	return out, nil
}

func (c *Controller) Refresh(ctx context.Context) (State, error) {
	return c.State(ctx)
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
