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
    expect(screen.getByText(/Slot 1 - Click to Assign/i)).toBeInTheDocument();
    expect(screen.getByText(/Slot 4 - Click to Assign/i)).toBeInTheDocument();
  });

  it('switches between 1, 4, and 9 grid modes upon click', async () => {
    const user = userEvent.setup();
    renderDashboard();

    const btn1 = screen.getByRole('button', { name: '1' });
    await user.click(btn1);

    // In mode 1, only Slot 1 is rendered
    expect(screen.getByText(/Slot 1 - Click to Assign/i)).toBeInTheDocument();
    expect(screen.queryByText(/Slot 2 - Click to Assign/i)).not.toBeInTheDocument();

    const btn9 = screen.getByRole('button', { name: '9' });
    await user.click(btn9);

    // In mode 9, Slot 9 is rendered
    expect(screen.getByText(/Slot 9 - Click to Assign/i)).toBeInTheDocument();
  });
});
