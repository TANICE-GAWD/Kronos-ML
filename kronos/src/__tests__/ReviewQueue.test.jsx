// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, fireEvent, cleanup } from '@testing-library/react';
import ReviewQueue from '../components/fraud/ReviewQueue';
import fraudApi from '../services/fraudApi';

const held = [
  { tx_id: 't1', band: 'high', score: 0.99, sender: 'bot_001', receiver: 'bot_002', amount: 900, currency: 'MARS',
    reasons: [{ code: 'AMOUNT_SPIKE', text: 'x', weight: 1 }], created_at: new Date().toISOString(), simulated: true, truth: 'whale' },
  { tx_id: 't2', band: 'high', score: 0.85, sender: 'bot_003', receiver: 'bot_004', amount: 12, currency: 'EARTH',
    reasons: [{ code: 'VELOCITY_10M', text: 'y', weight: 1 }], created_at: new Date().toISOString(), simulated: true, truth: '' },
];

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe('ReviewQueue', () => {
  it('lists held transfers with reasons and demo truth tags', () => {
    render(<ReviewQueue held={held} />);
    expect(screen.getByText(/@bot_001 → @bot_002/)).toBeTruthy();
    expect(screen.getAllByText('AMOUNT_SPIKE').length).toBeGreaterThan(0);
    expect(screen.getByText(/sim: Whale transfer/)).toBeTruthy();
    expect(screen.getByText(/sim: normal/)).toBeTruthy();
  });

  it('filters by reason code', () => {
    render(<ReviewQueue held={held} />);
    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'VELOCITY_10M' } });
    expect(screen.queryByText(/@bot_001/)).toBeNull();
    expect(screen.getByText(/@bot_003/)).toBeTruthy();
  });

  it('calls the block endpoint and reports the decision', async () => {
    const block = vi.spyOn(fraudApi, 'block').mockResolvedValue({ status: 'blocked' });
    const onDecision = vi.fn();
    render(<ReviewQueue held={held} onDecision={onDecision} />);
    fireEvent.click(screen.getAllByText('Block')[0]);
    await vi.waitFor(() => expect(onDecision).toHaveBeenCalled());
    expect(block).toHaveBeenCalledWith('t1');
    expect(onDecision.mock.calls[0][1]).toBe('block');
  });

  it('shows an empty state', () => {
    render(<ReviewQueue held={[]} />);
    expect(screen.getByText(/Nothing to review/)).toBeTruthy();
  });
});
