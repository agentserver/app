import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import StepCard from '../components/StepCard.vue';

describe('StepCard', () => {
  const baseStep = {
    id: 'modelserver_login',
    label: '登录 modelserver',
    kind: 'oauth' as const,
    autoStart: false,
    runtime: { status: 'pending' as const },
  };

  it('renders label', () => {
    const w = mount(StepCard, { props: { step: baseStep } });
    expect(w.text()).toContain('登录 modelserver');
  });

  it('applies "active" class when status=active', () => {
    const w = mount(StepCard, { props: { step: { ...baseStep, runtime: { status: 'active' } } } });
    expect(w.classes()).toContain('step--active');
  });

  it('applies "done" class and shows ✓ when status=success', () => {
    const w = mount(StepCard, { props: { step: { ...baseStep, runtime: { status: 'success' } } } });
    expect(w.classes()).toContain('step--done');
    expect(w.text()).toContain('✓');
  });

  it('applies "error" class when status=error', () => {
    const w = mount(StepCard, { props: { step: { ...baseStep, runtime: { status: 'error', errorMessage: 'boom' } } } });
    expect(w.classes()).toContain('step--error');
  });

  it('renders action slot', () => {
    const w = mount(StepCard, {
      props: { step: baseStep },
      slots: { action: '<button class="x">开始</button>' },
    });
    expect(w.find('.x').exists()).toBe(true);
  });
});
