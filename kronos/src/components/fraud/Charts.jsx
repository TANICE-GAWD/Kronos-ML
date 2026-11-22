import { BAND_COLORS } from './riskFormat';

// Live score distribution: 10 bins from 0 to 1, coloured by band.
export function ScoreHistogram({ histogram = [] }) {
  const max = Math.max(...histogram, 1);
  const color = (i) => (i >= 8 ? BAND_COLORS.high : i >= 5 ? BAND_COLORS.medium : BAND_COLORS.low);
  return (
    <div className="chart">
      <div className="panel-title">
        Score distribution
      </div>
      <svg viewBox="0 0 300 120" className="chart-svg">
        {histogram.map((v, i) => {
          const h = (v / max) * 92;
          return (
            <g key={i}>
              <rect x={6 + i * 29} y={100 - h} width={25} height={h} fill={color(i)} rx="2">
                <title>
                  {(i / 10).toFixed(1)}–{((i + 1) / 10).toFixed(1)}: {v}
                </title>
              </rect>
              <text x={18 + i * 29} y={114} textAnchor="middle" className="axis">
                {(i / 10).toFixed(1)}
              </text>
            </g>
          );
        })}
        <line x1={6 + 5 * 29 - 2} x2={6 + 5 * 29 - 2} y1="4" y2="102" className="thr" />
        <line x1={6 + 8 * 29 - 2} x2={6 + 8 * 29 - 2} y1="4" y2="102" className="thr" />
      </svg>
      <div className="legend">
        <span style={{ color: BAND_COLORS.low }}>low</span>
        <span style={{ color: BAND_COLORS.medium }}>watch, 0.5+</span>
        <span style={{ color: BAND_COLORS.high }}>held, 0.8+</span>
      </div>
    </div>
  );
}

// Alerts per minute over the last 30 minutes.
export function AlertsPerMinute({ series = [] }) {
  const max = Math.max(...series.map((m) => m.high + m.medium), 1);
  const w = 300;
  const bw = series.length ? Math.min(18, (w - 10) / series.length - 2) : 10;
  return (
    <div className="chart">
      <div className="panel-title">
        Alerts per minute
      </div>
      {series.length === 0 ? (
        <div className="empty">No alerts yet.</div>
      ) : (
        <svg viewBox={`0 0 ${w} 110`} className="chart-svg">
          {series.map((m, i) => {
            const hh = (m.high / max) * 90;
            const hm = (m.medium / max) * 90;
            const x = 6 + i * (bw + 2);
            return (
              <g key={m.minute}>
                <rect x={x} y={100 - hh} width={bw} height={hh} fill={BAND_COLORS.high} />
                <rect x={x} y={100 - hh - hm} width={bw} height={hm} fill={BAND_COLORS.medium} />
                <title>
                  {new Date(m.minute * 60000).toLocaleTimeString()}: {m.high} high, {m.medium} medium
                </title>
              </g>
            );
          })}
        </svg>
      )}
    </div>
  );
}
