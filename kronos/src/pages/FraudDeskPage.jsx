import { useNavigate } from 'react-router-dom';
import { useFraudState, useIsAnalyst } from '../hooks/useFraudState';
import KpiStrip from '../components/fraud/KpiStrip';
import AlertFeed from '../components/fraud/AlertFeed';
import ReviewQueue from '../components/fraud/ReviewQueue';
import CaseDrawer from '../components/fraud/CaseDrawer';
import SimControlPanel from '../components/fraud/SimControlPanel';
import ModelCard from '../components/fraud/ModelCard';
import { ScoreHistogram, AlertsPerMinute } from '../components/fraud/Charts';
import '../styles/FraudDesk.css';

// The bank fraud desk: live KPIs, alert ticker, review queue, charts, model card
// and the control panel for the mass-transaction simulation.
export default function FraudDeskPage() {
  const navigate = useNavigate();
  const isAnalyst = useIsAnalyst();
  const { held, alerts, stats, sim, isConnected } = useFraudState();

  if (!isAnalyst) {
    return (
      <div className="fraud-desk">
        <div className="desk-header">
          <button className="btn" onClick={() => navigate('/')}>
            Back to the solar system
          </button>
        </div>
        <div className="empty big">Only analysts can open the fraud desk. Log in as analyst to review held transfers.</div>
      </div>
    );
  }

  return (
    <div className="fraud-desk">
      <div className="desk-header">
        <button className="btn" onClick={() => navigate('/')}>
          ← Solar system
        </button>
        <h1>Fraud desk</h1>
        <span className={`conn ${isConnected ? 'on' : 'off'}`}>{isConnected ? 'Live' : 'Reconnecting…'}</span>
      </div>
      <KpiStrip stats={stats} sim={sim} />
      <div className="desk-grid">
        <div className="col-main">
          <ReviewQueue held={held} totalHeld={stats?.held_now} />
          <div className="chart-row">
            <ScoreHistogram histogram={stats?.histogram || []} />
            <AlertsPerMinute series={stats?.alerts_by_minute || []} />
          </div>
          <AlertFeed alerts={alerts} />
        </div>
        <div className="col-side">
          <SimControlPanel sim={sim} />
          <ModelCard />
        </div>
      </div>
      <CaseDrawer />
    </div>
  );
}
