import { useEffect, useState } from 'react';
import fraudApi from '../../services/fraudApi';

const NAMES = {
  zscore_baseline: 'Z-score baseline',
  isolation_forest: 'Isolation Forest',
  lof: 'Local Outlier Factor',
  ocsvm: 'One-Class SVM',
  ensemble: 'Ensemble of Isolation Forest and LOF',
};

function pct(v) {
  return v == null ? '-' : `${(v * 100).toFixed(1)}%`;
}

export default function ModelCard() {
  const [card, setCard] = useState(null);
  const [error, setError] = useState('');
  useEffect(() => {
    fraudApi.model().then(setCard).catch((e) => setError(e.message));
  }, []);
  if (error) return <div className="model-card"><div className="error-line">Model card unavailable: {error}</div></div>;
  if (!card) return <div className="model-card"><div className="empty">loading model card…</div></div>;
  const tm = card.test_metrics || {};
  const val = card.validation_pr_auc || {};
  return (
    <div className="model-card">
      <div className="panel-title">
        Model
      </div>
      <div className="mc-head">
        <b>{NAMES[card.model] || card.model}</b>
        <span className="mono small muted">{card.version}</span>
      </div>
      <div className="muted small">
        Trained {new Date(card.trained_at).toLocaleString()} on {card.training_window?.rows?.toLocaleString()} transfers
        from {card.training_window?.start} to {card.training_window?.end}. Fraud labels were never used in training.
      </div>
      <div className="mc-metrics">
        <div><b>{(tm.pr_auc ?? 0).toFixed(3)}</b>PR-AUC, test</div>
        <div><b>{(tm.roc_auc ?? 0).toFixed(3)}</b>ROC-AUC, test</div>
        <div><b>{pct(tm['recall@thr'])}</b>of fraud held</div>
        <div><b>{pct(tm['precision@thr'])}</b>of holds are fraud</div>
        <div><b>{pct(card.thresholds?.high_alert_rate)}</b>of traffic held</div>
        <div><b>{card.features?.length}</b>features</div>
      </div>
      <div className="panel-subtitle">Validation PR-AUC</div>
      <table className="mc-table">
        <tbody>
          {Object.entries(val)
            .sort((a, b) => b[1] - a[1])
            .map(([k, v]) => (
              <tr key={k} className={k === card.model ? 'active' : ''}>
                <td>{NAMES[k] || k}</td>
                <td className="num">{v.toFixed(3)}</td>
                <td>
                  <div className="bar-track"><div className="bar-fill" style={{ width: `${v * 100}%` }} /></div>
                </td>
              </tr>
            ))}
        </tbody>
      </table>
      <div className="muted small">Scores of 0.5 and up are watched. Scores of 0.8 and up are held. The 0.8 cut sits where missed fraud plus review time costs least.</div>
    </div>
  );
}
