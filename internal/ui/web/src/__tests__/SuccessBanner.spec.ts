import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import SuccessBanner from '../components/SuccessBanner.vue';

describe('SuccessBanner', () => {
  it('renders success text', () => {
    const w = mount(SuccessBanner, { props: { frontendName: 'Codex Desktop' } });
    expect(w.text()).toContain('全部完成');
  });

  it('emits "launch" when Codex Desktop button clicked', async () => {
    const w = mount(SuccessBanner, { props: { frontendName: 'Codex Desktop' } });
    const btn = w.findAll('button').find(b => b.text().includes('打开 Codex Desktop'));
    expect(btn).toBeDefined();
    await btn!.trigger('click');
    expect(w.emitted('launch')).toBeTruthy();
  });

  it('renders pending message when launching Codex Desktop', async () => {
    const w = mount(SuccessBanner, { props: { launching: true, frontendName: 'Codex Desktop' } });
    expect(w.text()).toContain('Codex Desktop 启动中');
  });
});
