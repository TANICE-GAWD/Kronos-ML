import { describe, it, expect, beforeEach } from 'vitest';
import WebSocketManager from '../services/WebSocketManager';

function frame(extra = {}) {
  return { data: JSON.stringify({ packets: {}, wallets: {}, transactions: [], users: {}, ...extra }) };
}

describe('WebSocketManager: Fraud Shield fields', () => {
  let m;
  beforeEach(() => {
    WebSocketManager.instance = undefined;
    m = WebSocketManager.getInstance();
  });

  it('stores held, alerts, riskStats and sim from a frame', () => {
    m.handleMessage(
      frame({
        held: [{ tx_id: 'a', band: 'high', score: 0.93 }],
        alerts: [{ tx_id: 'a', band: 'high' }, { tx_id: 'b', band: 'medium' }],
        riskStats: { scored: 10, held_now: 1 },
        sim: { state: 'running', caught: 3 },
      })
    );
    const s = m.getState();
    expect(s.held).toHaveLength(1);
    expect(s.alerts.map((a) => a.tx_id)).toEqual(['a', 'b']);
    expect(s.riskStats.held_now).toBe(1);
    expect(s.sim.caught).toBe(3);
  });

  it('defaults to empty lists when an older backend omits the fields', () => {
    m.handleMessage(frame());
    const s = m.getState();
    expect(s.held).toEqual([]);
    expect(s.alerts).toEqual([]);
    expect(s.riskStats).toBeNull();
  });

  it('keeps risk fields on packets', () => {
    m.handleMessage(frame({ packets: { p1: { id: 'p1', risk_band: 'medium', risk_score: 0.6, current_pos: { x: 0, y: 0, z: 0 } } } }));
    expect(m.getState().packets.p1.risk_band).toBe('medium');
  });
});
