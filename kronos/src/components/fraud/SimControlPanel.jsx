import { useState } from 'react';
import fraudApi from '../../services/fraudApi';
import { SCENARIOS, PATTERN_LABELS } from './riskFormat';

// Launch hundreds of bot transfers at once, a few of them fraudulent.
export default function SimControlPanel({ sim, compact = false }) {
  const [scenario, setScenario] = useState('mixed_attack');
  const [n, setN] = useState(100);
  const [duration, setDuration] = useState(120);
  const [fraudPct, setFraudPct] = useState('');
  const [error, setError] = useState('');
  const s = sim || { state: 'idle', bots: 0 };
  const busy = ['seeding', 'warming', 'planning', 'running'].includes(s.state);
  const progress = s.total ? Math.round((s.done / s.total) * 100) : 0;

  const call = async (fn) => {
    setError('');
    try {
      await fn();
    } catch (e) {
      setError(e.message);
    }
  };

  const start = () =>
    call(() =>
      fraudApi.simStart({
        scenario,
        n: Number(n),
        duration_s: Number(duration),
        ...(fraudPct !== '' ? { fraud_rate: Number(fraudPct) / 100 } : {}),
      })
    );

  const patterns = Object.entries(s.by_pattern || {});
  return (
    <div className={`sim-panel ${compact ? 'compact' : ''}`}>
      <div className="panel-title">
        Simulation <span className="muted">{s.bots || 0} bots</span>
        <span className={`sim-state st-${s.state}`}>{s.state}</span>
      </div>
      <div className="sim-controls">
        <select value={scenario} onChange={(e) => setScenario(e.target.value)} disabled={busy}>
          {SCENARIOS.map((sc) => (
            <option key={sc.id} value={sc.id}>
              {sc.label}
            </option>
          ))}
        </select>
        {!compact && (
          <>
            <label>
              transfers
              <input type="number" min="50" max="3000" value={n} onChange={(e) => setN(e.target.value)} disabled={busy} />
            </label>
            <label>
              seconds
              <input type="number" min="10" max="600" value={duration} onChange={(e) => setDuration(e.target.value)} disabled={busy} />
            </label>
            <label>
              fraud %
              <input
                type="number"
                min="0"
                max="30"
                step="0.5"
                placeholder="auto"
                value={fraudPct}
                onChange={(e) => setFraudPct(e.target.value)}
                disabled={busy}
              />
            </label>
          </>
        )}
        {(s.bots || 0) < 20 ? (
          <button className="btn" onClick={() => call(() => fraudApi.simSeed(100))} disabled={busy}>
            Create 100 bots
          </button>
        ) : busy ? (
          <button className="btn block" onClick={() => call(fraudApi.simStop)}>
            Stop
          </button>
        ) : (
          <button className="btn release" onClick={start}>
            Start
          </button>
        )}
      </div>
      {!compact && <div className="muted small">{SCENARIOS.find((x) => x.id === scenario)?.hint}</div>}
      {error && <div className="error-line">{error}</div>}
      {s.message && <div className="sim-message">{s.message}</div>}
      {s.total > 0 && (
        <>
          <div className="progress">
            <div className="progress-fill" style={{ width: `${progress}%` }} />
            <span>
              {s.done} / {s.total}
            </span>
          </div>
          <div className="scorecard">
            <div className="sc caught">
              <b>{s.caught}</b>held
            </div>
            <div className="sc watched">
              <b>{s.watched}</b>watched
            </div>
            <div className="sc missed">
              <b>{s.missed}</b>missed
            </div>
            <div className="sc fa">
              <b>{s.false_alarms}</b>false alarms
            </div>
            <div className="sc total">
              <b>{s.fraud_planned}</b>fraud total
            </div>
          </div>
          {!compact && patterns.length > 0 && (
            <div className="pattern-recall">
              {patterns.map(([k, v]) => (
                <div key={k} className="pr-row">
                  <span>{PATTERN_LABELS[k] || k}</span>
                  <div className="bar-track">
                    <div className="bar-fill low" style={{ width: `${v.total ? (v.caught / v.total) * 100 : 0}%` }} />
                  </div>
                  <span className="mono small">
                    {v.caught}/{v.total}
                  </span>
                </div>
              ))}
            </div>
          )}
        </>
      )}
    </div>
  );
}
