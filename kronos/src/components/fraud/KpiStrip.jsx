import { fmtScore } from './riskFormat';

function Stat({ label, value, tone }) {
  return (
    <div className={`stat ${tone || ''}`}>
      <div className="stat-value">{value}</div>
      <div className="stat-label">{label}</div>
    </div>
  );
}

export default function KpiStrip({ stats, sim }) {
  const s = stats || {};
  const bands = s.by_band || {};
  const ran = sim && ['running', 'done', 'stopped'].includes(sim.state) && sim.total > 0;
  return (
    <div className="stat-row">
      <Stat label="Held now" value={s.held_now || 0} tone="tone-high" />
      <Stat label="Watching" value={bands.medium || 0} tone="tone-medium" />
      <Stat label="Blocked" value={s.blocked || 0} />
      <Stat label="Released" value={s.released || 0} />
      <Stat label="Frozen accounts" value={s.frozen || 0} />
      <Stat label="Scored" value={s.scored || 0} />
      <Stat label="Transfers/s" value={(s.tps || 0).toFixed(1)} />
      <Stat label="Avg score" value={fmtScore(s.avg_score || 0)} />
      {ran && <Stat label="Sim: fraud caught" value={`${sim.caught}/${sim.fraud_planned}`} />}
    </div>
  );
}
