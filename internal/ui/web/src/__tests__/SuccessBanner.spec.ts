import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import SuccessBanner from '../components/SuccessBanner.vue';

describe('SuccessBanner', () => {
  it('renders success text', () => {
    const w = mount(SuccessBanner);
    expect(w.text()).toContain('全部完成');
  });

  it('emits "launch" when "立即打开 VS Code" clicked', async () => {
    const w = mount(SuccessBanner);
    const btn = w.findAll('button').find(b => b.text().includes('立即打开 VS Code'));
    expect(btn).toBeDefined();
    await btn!.trigger('click');
    expect(w.emitted('launch')).toBeTruthy();
  });

  it('renders pending message when launching', async () => {
    const w = mount(SuccessBanner, { props: { launching: true } });
    expect(w.text()).toContain('VS Code 启动中');
  });
});
