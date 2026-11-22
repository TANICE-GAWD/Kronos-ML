import { BAND_COLORS, fmtScore } from './riskFormat';

export default function RiskBadge({ band = 'unscored', score, compact = false }) {
  const color = BAND_COLORS[band] || BAND_COLORS.unscored;
  return (
    <span className={`risk-badge risk-${band}`} style={{ borderColor: color, color }}>
      {band}
      {!compact && score != null && <b>{fmtScore(score)}</b>}
    </span>
  );
}
