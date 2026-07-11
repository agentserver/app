import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import SuccessBanner from '../components/SuccessBanner.vue';

describe('SuccessBanner', () => {
  it('renders success text', () => {
    const w = mount(SuccessBanner, { props: { frontendName: 'ChatGPT / Codex' } });
    expect(w.text()).toContain('全部完成');
  });

  it('emits "launch" when ChatGPT / Codex button clicked', async () => {
    const w = mount(SuccessBanner, { props: { frontendName: 'ChatGPT / Codex' } });
    const btn = w.findAll('button').find(b => b.text().includes('打开 ChatGPT / Codex'));
    expect(btn).toBeDefined();
    await btn!.trigger('click');
    expect(w.emitted('launch')).toBeTruthy();
  });

  it('renders pending message when launching ChatGPT / Codex', async () => {
    const w = mount(SuccessBanner, { props: { launching: true, frontendName: 'ChatGPT / Codex' } });
    expect(w.text()).toContain('ChatGPT / Codex 启动中');
  });
});
