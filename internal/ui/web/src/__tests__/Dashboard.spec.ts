import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount } from '@vue/test-utils';
import Dashboard from '../components/Dashboard.vue';
import * as api from '../api';

describe('Dashboard', () => {
  beforeEach(() => vi.restoreAllMocks());

  it('renders project, workspace, quota, and subscription action', async () => {
    vi.spyOn(api, 'getConsoleState').mockResolvedValue({
      frontend_mode: 'codex_desktop',
      frontend_name: 'Codex Desktop',
      onboarding_status: 'complete',
      modelserver: { project_id: 'proj-1', project_name: 'Default project' },
      agentserver: { workspace_id: 'ws-1', workspace_name: 'Default workspace' },
      subscription_url: 'https://code.cs.ac.cn/projects/proj-1/subscription',
      quotas: [
        { window: '5h', percentage: 58, remaining_percentage: 42 },
        { window: '7d', percentage: 22, remaining_percentage: 78 },
      ],
      last_refreshed_at: '2026-06-07T12:00:00Z',
    });
    const w = mount(Dashboard);
    await Promise.resolve();
    await Promise.resolve();
    expect(w.text()).toContain('Default project');
    expect(w.text()).toContain('Default workspace');
    expect(w.text()).toContain('5小时');
    expect(w.text()).toContain('剩余约 42%');
  });
});
