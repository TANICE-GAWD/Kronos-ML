import { useEffect, useState } from 'react';
import useWebSocket from './useWebSocket';
import fraudApi from '../services/fraudApi';

// Live fraud-desk state from the WebSocket, plus whether the user is an analyst.
export function useFraudState() {
  const { state, isConnected } = useWebSocket(true);
  return {
    isConnected,
    held: state?.held || [],
    alerts: state?.alerts || [],
    stats: state?.riskStats || null,
    sim: state?.sim || null,
  };
}

let meCache = null;

export function useIsAnalyst() {
  const [isAnalyst, setIsAnalyst] = useState(meCache?.is_analyst ?? false);
  useEffect(() => {
    if (meCache) return;
    if (!localStorage.getItem('authToken')) return;
    fraudApi
      .me()
      .then((me) => {
        meCache = me;
        setIsAnalyst(!!me.is_analyst);
      })
      .catch(() => setIsAnalyst(false));
  }, []);
  return isAnalyst;
}

export function resetMeCache() {
  meCache = null;
}

export default useFraudState;
