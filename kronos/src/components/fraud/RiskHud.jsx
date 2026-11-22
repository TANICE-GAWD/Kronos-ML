import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { ChevronDown, ChevronUp } from 'lucide-react';
import { useFraudState, useIsAnalyst } from '../../hooks/useFraudState';
import AlertFeed from './AlertFeed';
import SimControlPanel from './SimControlPanel';
import { BAND_COLORS } from './riskFormat';

// Overlay on the solar system: packet colour legend for everyone; live alerts,
// held count and the simulation launcher for analysts.
export default function RiskHud() {
  const isAnalyst = useIsAnalyst();
  const navigate = useNavigate();
  const { held, alerts, stats, sim } = useFraudState();
  const [open, setOpen] = useState(true);

  return (
    <div className="risk-hud">
      <div className="risk-legend">
        <span style={{ color: BAND_COLORS.low }}>low</span>
        <span style={{ color: BAND_COLORS.medium }}>watch</span>
        <span style={{ color: BAND_COLORS.high }}>held</span>
      </div>
      {isAnalyst && (
        <div className="risk-panel">
          <div className="risk-panel-head">
            <button className="btn desk-btn" onClick={() => navigate('/fraud')}>
              Fraud desk
              {(stats?.held_now || held.length) > 0 && <span className="held-count">{stats?.held_now ?? held.length} held</span>}
            </button>
            <span className="muted small">{stats?.scored || 0} scored</span>
            <button className="icon-btn" onClick={() => setOpen((o) => !o)} title={open ? 'Collapse' : 'Expand'}>
              {open ? <ChevronUp size={16} /> : <ChevronDown size={16} />}
            </button>
          </div>
          {open && (
            <>
              <SimControlPanel sim={sim} compact />
              <AlertFeed alerts={alerts} limit={8} compact title="Alerts" />
            </>
          )}
        </div>
      )}
    </div>
  );
}
