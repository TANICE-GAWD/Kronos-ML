import RiskBadge from './RiskBadge';
import { STATUS_LABELS, PATTERN_LABELS, fmtAmount, timeAgo } from './riskFormat';
import { openCase } from '../../services/fraudApi';

export function TruthTag({ truth }) {
  // Demo ground truth from the simulator. Never sent to the scorer.
  return (
    <span className={`truth-tag ${truth ? 'is-fraud' : 'is-normal'}`} title="simulator ground truth (demo only)">
      sim: {truth ? PATTERN_LABELS[truth] || truth : 'normal'}
    </span>
  );
}

// Bank-style live ticker of medium / high risk transfers.
export default function AlertFeed({ alerts = [], limit = 40, compact = false, title = 'Alerts' }) {
  const rows = alerts.slice(0, limit);
  return (
    <div className={`alert-feed ${compact ? 'compact' : ''}`}>
      <div className="panel-title">
        {title} <span className="muted">{alerts.length}</span>
      </div>
      {rows.length === 0 && <div className="empty">No alerts yet. Start a simulation to see transfers being scored.</div>}
      <ul>
        {rows.map((a) => (
          <li key={a.tx_id} className={`alert-row band-${a.band} status-${a.status}`} onClick={() => openCase(a.tx_id)}>
            <RiskBadge band={a.band} score={a.score} />
            <div className="alert-main">
              <div className="alert-line">
                <span className="mono">@{a.sender}</span> → <span className="mono">@{a.receiver}</span>
                <span className="alert-amount">
                  {fmtAmount(a.amount)} {a.currency}
                </span>
              </div>
              <div className="alert-reasons">
                {(a.reasons || []).slice(0, compact ? 2 : 3).map((r) => (
                  <span key={r.code} className="reason-chip" title={r.text}>
                    {r.code}
                  </span>
                ))}
              </div>
            </div>
            <div className="alert-side">
              <span className={`status-pill s-${a.status}`}>{STATUS_LABELS[a.status] || a.status}</span>
              {!compact && a.simulated && <TruthTag truth={a.truth} />}
              <span className="muted small">{timeAgo(a.created_at)}</span>
            </div>
          </li>
        ))}
      </ul>
    </div>
  );
}
