import { useEffect, useMemo, useState } from 'react';
import { X } from 'lucide-react';
import RiskBadge from './RiskBadge';
import { TruthTag } from './AlertFeed';
import { BAND_COLORS, STATUS_LABELS, fmtAmount } from './riskFormat';
import fraudApi, { OPEN_CASE_EVENT } from '../../services/fraudApi';

function ReasonBars({ reasons = [] }) {
  if (!reasons.length) return <div className="empty">No single signal stands out. The score comes from several small ones together.</div>;
  const max = Math.max(...reasons.map((r) => r.weight || 0), 0.01);
  return (
    <div className="reason-bars">
      {reasons.map((r) => (
        <div key={r.code} className="reason-bar">
          <div className="reason-bar-head">
            <span className="mono">{r.code}</span>
            <span className="muted small">{Math.round((r.weight || 0) * 100)}%</span>
          </div>
          <div className="bar-track">
            <div className="bar-fill" style={{ width: `${((r.weight || 0) / max) * 100}%` }} />
          </div>
          <div className="reason-text">{r.text}</div>
        </div>
      ))}
    </div>
  );
}

function Sparkline({ history = [], highlight }) {
  const pts = [...history].reverse();
  if (pts.length < 2) return <div className="empty">Not enough history.</div>;
  const w = 320;
  const h = 70;
  const max = Math.max(...pts.map((p) => p.amount), 1);
  const x = (i) => (i / (pts.length - 1)) * (w - 10) + 5;
  const y = (v) => h - 6 - (v / max) * (h - 14);
  const d = pts.map((p, i) => `${i ? 'L' : 'M'}${x(i).toFixed(1)},${y(p.amount).toFixed(1)}`).join(' ');
  return (
    <svg width={w} height={h} className="sparkline">
      <path d={d} fill="none" stroke="#7aa7ff" strokeWidth="1.6" />
      {pts.map((p, i) => (
        <circle
          key={p.tx_id}
          cx={x(i)}
          cy={y(p.amount)}
          r={p.tx_id === highlight ? 4.5 : 2.2}
          fill={p.tx_id === highlight ? BAND_COLORS.high : p.status === 'blocked' ? '#888' : '#7aa7ff'}
        >
          <title>
            {fmtAmount(p.amount)} → @{p.receiver} ({p.status})
          </title>
        </circle>
      ))}
    </svg>
  );
}

// Edges on a directed cycle of 3-4 hops (A→B→C→A): the layering signal. Two-hop
// pay-backs between friends are normal and not highlighted.
function cycleEdges(edges) {
  const out = {};
  edges.forEach((e) => {
    (out[e.from] = out[e.from] || []).push(e.to);
  });
  const onCycle = new Set();
  const nodes = Object.keys(out);
  for (const start of nodes) {
    const stack = [[start, [start]]];
    while (stack.length) {
      const [node, path] = stack.pop();
      for (const next of out[node] || []) {
        if (next === start && path.length > 2) {
          path.forEach((n, i) => onCycle.add(`${n}>${path[i + 1] || start}`));
        } else if (!path.includes(next) && path.length < 4) {
          stack.push([next, [...path, next]]);
        }
      }
    }
  }
  return onCycle;
}

function MoneyGraph({ edges = [], sender, receiver }) {
  const { nodes, pos, cyc } = useMemo(() => {
    const names = Array.from(new Set(edges.flatMap((e) => [e.from, e.to])));
    const W = 340;
    const H = 260;
    const cx = W / 2;
    const cy = H / 2;
    const r = Math.min(W, H) / 2 - 28;
    const p = {};
    names.forEach((n, i) => {
      const a = (i / names.length) * Math.PI * 2 - Math.PI / 2;
      p[n] = { x: cx + r * Math.cos(a), y: cy + r * Math.sin(a) };
    });
    return { nodes: names, pos: p, cyc: cycleEdges(edges) };
  }, [edges]);
  if (!edges.length) return <div className="empty">No money flow around this account in the last day.</div>;
  const maxAmt = Math.max(...edges.map((e) => e.amount), 1);
  return (
    <svg width={340} height={260} className="money-graph">
      <defs>
        <marker id="arrow" viewBox="0 0 10 10" refX="16" refY="5" markerWidth="6" markerHeight="6" orient="auto">
          <path d="M0,0 L10,5 L0,10 z" fill="#9aa3b5" />
        </marker>
        <marker id="arrow-red" viewBox="0 0 10 10" refX="16" refY="5" markerWidth="6" markerHeight="6" orient="auto">
          <path d="M0,0 L10,5 L0,10 z" fill={BAND_COLORS.high} />
        </marker>
      </defs>
      {edges.map((e) => {
        const a = pos[e.from];
        const b = pos[e.to];
        const isCyc = cyc.has(`${e.from}>${e.to}`);
        return (
          <line
            key={`${e.from}>${e.to}`}
            x1={a.x}
            y1={a.y}
            x2={b.x}
            y2={b.y}
            stroke={isCyc ? BAND_COLORS.high : '#56607a'}
            strokeWidth={0.8 + 3 * Math.sqrt(e.amount / maxAmt)}
            markerEnd={isCyc ? 'url(#arrow-red)' : 'url(#arrow)'}
            opacity={isCyc ? 1 : 0.7}
          >
            <title>
              @{e.from} → @{e.to}: {fmtAmount(e.amount)} in {e.count} transfer(s)
            </title>
          </line>
        );
      })}
      {nodes.map((n) => {
        const isKey = n === sender || n === receiver;
        return (
          <g key={n}>
            <circle
              cx={pos[n].x}
              cy={pos[n].y}
              r={isKey ? 8 : 5}
              fill={n === sender ? BAND_COLORS.high : n === receiver ? BAND_COLORS.medium : '#7aa7ff'}
            />
            <text x={pos[n].x} y={pos[n].y - 11} textAnchor="middle" className="graph-label">
              {n}
            </text>
          </g>
        );
      })}
    </svg>
  );
}

export default function CaseDrawer() {
  const [txId, setTxId] = useState(null);
  const [data, setData] = useState(null);
  const [error, setError] = useState('');
  const [note, setNote] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    const open = (e) => {
      setTxId(e.detail);
      setNote('');
    };
    window.addEventListener(OPEN_CASE_EVENT, open);
    return () => window.removeEventListener(OPEN_CASE_EVENT, open);
  }, []);

  useEffect(() => {
    if (!txId) return;
    let live = true;
    setData(null);
    setError('');
    const load = () =>
      fraudApi
        .caseDetail(txId)
        .then((d) => live && setData(d))
        .catch((e) => live && setError(e.message));
    load();
    const t = setInterval(load, 3000);
    return () => {
      live = false;
      clearInterval(t);
    };
  }, [txId]);

  if (!txId) return null;
  const c = data?.case;

  const decide = async (decision) => {
    setBusy(true);
    setError('');
    try {
      if (decision === 'release') await fraudApi.release(txId, note);
      else await fraudApi.block(txId, note);
      const d = await fraudApi.caseDetail(txId);
      setData(d);
    } catch (e) {
      setError(e.message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="case-drawer-overlay" onClick={() => setTxId(null)}>
      <aside className="case-drawer" onClick={(e) => e.stopPropagation()}>
        <header>
          <div>
            <div className="muted small">Case</div>
            <div className="mono">{txId.slice(0, 8)}…</div>
          </div>
          {c && <RiskBadge band={c.band} score={c.score} />}
          {c && <span className={`status-pill s-${c.status}`}>{STATUS_LABELS[c.status] || c.status}</span>}
          <button className="icon-btn" onClick={() => setTxId(null)} title="Close">
            <X size={18} />
          </button>
        </header>
        {error && <div className="error-line">{error}</div>}
        {!c && !error && (
          <div className="empty">
            Loading case…
          </div>
        )}
        {c && (
          <>
            <section className="case-facts">
              <div>
                <span className="muted">From</span> <b className="mono">@{c.sender}</b> ({c.origin_planet})
              </div>
              <div>
                <span className="muted">To</span> <b className="mono">@{c.receiver}</b> ({c.destination_planet})
              </div>
              <div>
                <span className="muted">Amount</span> <b>{fmtAmount(c.amount)}</b> {c.currency}
              </div>
              <div>
                <span className="muted">Model</span> <span className="mono small">{c.model_version || 'n/a'}</span>
              </div>
              {c.simulated && (
                <div>
                  <span className="muted">Demo</span> <TruthTag truth={c.truth} />
                </div>
              )}
            </section>
            <section>
              <h4>Why it was flagged</h4>
              <ReasonBars reasons={c.reasons} />
            </section>
            <section>
              <h4>Sender's last 20 transfers</h4>
              <Sparkline history={data.history} highlight={c.tx_id} />
            </section>
            <section>
              <h4>Money flow (last 24h)</h4>
              <MoneyGraph edges={data.graph} sender={c.sender} receiver={c.receiver} />
              <div className="muted small">Red arrows mark money that went around a loop and came back. That is how layering looks.</div>
            </section>
            {c.status === 'held' && (
              <section className="decision">
                <textarea placeholder="Note for the audit log (optional)" value={note} onChange={(e) => setNote(e.target.value)} />
                <div className="decision-buttons">
                  <button className="btn release" disabled={busy} onClick={() => decide('release')}>
                    Release
                  </button>
                  <button className="btn block" disabled={busy} onClick={() => decide('block')}>
                    Block and refund
                  </button>
                </div>
              </section>
            )}
          </>
        )}
      </aside>
    </div>
  );
}
