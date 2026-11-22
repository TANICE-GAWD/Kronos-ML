import { useEffect, useMemo, useState } from 'react';
import RiskBadge from './RiskBadge';
import { TruthTag } from './AlertFeed';
import { fmtAmount, timeAgo } from './riskFormat';
import fraudApi, { openCase } from '../../services/fraudApi';

// Held transfers, highest score first. Keyboard: j/k move, r release, b block, enter open.
export default function ReviewQueue({ held = [], totalHeld, onDecision }) {
  const [reason, setReason] = useState('all');
  const [cursor, setCursor] = useState(0);
  const [busy, setBusy] = useState(null);
  const [error, setError] = useState('');

  const reasonCodes = useMemo(() => {
    const set = new Set();
    held.forEach((c) => (c.reasons || []).forEach((r) => set.add(r.code)));
    return ['all', ...Array.from(set).sort()];
  }, [held]);

  const rows = useMemo(
    () => (reason === 'all' ? held : held.filter((c) => (c.reasons || []).some((r) => r.code === reason))),
    [held, reason]
  );

  const decide = async (c, decision) => {
    if (!c) return;
    setBusy(c.tx_id);
    setError('');
    try {
      const res = decision === 'release' ? await fraudApi.release(c.tx_id) : await fraudApi.block(c.tx_id);
      onDecision?.(c, decision, res);
    } catch (e) {
      setError(e.message);
    } finally {
      setBusy(null);
    }
  };

  useEffect(() => {
    const onKey = (e) => {
      if (['INPUT', 'TEXTAREA', 'SELECT'].includes(e.target.tagName)) return;
      const c = rows[cursor];
      if (e.key === 'j') setCursor((i) => Math.min(i + 1, Math.max(rows.length - 1, 0)));
      else if (e.key === 'k') setCursor((i) => Math.max(i - 1, 0));
      else if (e.key === 'r') decide(c, 'release');
      else if (e.key === 'b') decide(c, 'block');
      else if (e.key === 'Enter' && c) openCase(c.tx_id);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  });

  return (
    <div className="review-queue">
      <div className="panel-title">
        Review queue{' '}
        <span className="muted">
          {totalHeld ?? held.length} held{totalHeld > held.length ? `, showing the riskiest ${held.length}` : ''}
        </span>
        <select value={reason} onChange={(e) => setReason(e.target.value)} className="queue-filter">
          {reasonCodes.map((c) => (
            <option key={c} value={c}>
              {c === 'all' ? 'All reasons' : c}
            </option>
          ))}
        </select>
        <span className="kbd-hint">j/k to move, r release, b block, enter to open</span>
      </div>
      {error && <div className="error-line">{error}</div>}
      {rows.length === 0 ? (
        <div className="empty">Nothing to review. High-risk transfers wait here, funds locked, until someone releases or blocks them.</div>
      ) : (
        <table>
          <thead>
            <tr>
              <th>Risk</th>
              <th>Sender → Receiver</th>
              <th className="num">Amount</th>
              <th>Top reasons</th>
              <th>Held</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {rows.map((c, i) => (
              <tr key={c.tx_id} className={i === cursor ? 'cursor' : ''} onClick={() => setCursor(i)}>
                <td>
                  <RiskBadge band={c.band} score={c.score} />
                </td>
                <td className="mono clickable" onClick={() => openCase(c.tx_id)}>
                  @{c.sender} → @{c.receiver}
                  {c.simulated && <TruthTag truth={c.truth} />}
                </td>
                <td className="num">
                  {fmtAmount(c.amount)} <span className="muted">{c.currency}</span>
                </td>
                <td>
                  {(c.reasons || []).map((r) => (
                    <span key={r.code} className="reason-chip" title={r.text}>
                      {r.code}
                    </span>
                  ))}
                </td>
                <td className="muted small">{timeAgo(c.created_at)}</td>
                <td className="actions">
                  <button className="btn release" disabled={busy === c.tx_id} onClick={() => decide(c, 'release')}>
                    Release
                  </button>
                  <button className="btn block" disabled={busy === c.tx_id} onClick={() => decide(c, 'block')}>
                    Block
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
