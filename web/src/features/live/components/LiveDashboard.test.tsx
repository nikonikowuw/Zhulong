import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import i18n from '@/shared/i18n';
import { LiveDashboard } from './LiveDashboard';

function renderDashboard() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });

  return render(
    <QueryClientProvider client={queryClient}>
      <LiveDashboard />
    </QueryClientProvider>,
  );
}

describe('LiveDashboard', () => {
  beforeEach(async () => {
    localStorage.clear();
    await i18n.changeLanguage('en');
  });

  afterEach(() => {
    cleanup();
  });

  it('renders default 4-grid view and layout switcher buttons', () => {
    renderDashboard();

    expect(screen.getByText('Live Video Surveillance')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '1' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '4' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '9' })).toBeInTheDocument();

    // In mode 4, 4 slots are rendered
    expect(screen.getByText('Slot 1')).toBeInTheDocument();
    expect(screen.getByText('Slot 4')).toBeInTheDocument();
  });

  it('switches between 1, 4, and 9 grid modes upon click', async () => {
    const user = userEvent.setup();
    renderDashboard();

    const btn1 = screen.getByRole('button', { name: '1' });
    await user.click(btn1);

    // In mode 1, Slot 1 is rendered (since selected slot is 0)
    expect(screen.getByText('Slot 1')).toBeInTheDocument();
    expect(screen.queryByText('Slot 2')).not.toBeInTheDocument();

    const btn9 = screen.getByRole('button', { name: '9' });
    await user.click(btn9);

    // In mode 9, Slot 9 is rendered
    expect(screen.getByText('Slot 9')).toBeInTheDocument();
  });

  it('switches layout mode via keyboard shortcuts 1, 4, 9 when not in inputs', async () => {
    const user = userEvent.setup();
    renderDashboard();

    // Default mode 4 has Slot 4
    expect(screen.getByText('Slot 4')).toBeInTheDocument();

    // Press '1'
    await user.keyboard('1');
    expect(screen.getByText('Slot 1')).toBeInTheDocument();
    expect(screen.queryByText('Slot 2')).not.toBeInTheDocument();

    // Press '9'
    await user.keyboard('9');
    expect(screen.getByText('Slot 9')).toBeInTheDocument();

    // Press '4'
    await user.keyboard('4');
    expect(screen.getByText('Slot 4')).toBeInTheDocument();
    expect(screen.queryByText('Slot 5')).not.toBeInTheDocument();
  });

  it('preserves focused slot number when switching to single-view mode', async () => {
    const user = userEvent.setup();
    renderDashboard();

    // 1. Focus Slot 3 (index 2)
    const slot3 = screen.getByText('Slot 3');
    await user.click(slot3);

    // 2. Switch to single-view mode (1x1)
    const btn1 = screen.getByRole('button', { name: '1' });
    await user.click(btn1);

    // Single-view mode displays Slot 3 (and not Slot 1)
    expect(screen.getByText('Slot 3')).toBeInTheDocument();
    expect(screen.queryByText('Slot 1')).not.toBeInTheDocument();

    // 3. Switch back to 4-view mode (2x2)
    const btn4 = screen.getByRole('button', { name: '4' });
    await user.click(btn4);

    // Displays slots 1, 2, 3, 4 strictly by slot index
    expect(screen.getByText('Slot 1')).toBeInTheDocument();
    expect(screen.getByText('Slot 2')).toBeInTheDocument();
    expect(screen.getByText('Slot 3')).toBeInTheDocument();
    expect(screen.getByText('Slot 4')).toBeInTheDocument();
  });
});
