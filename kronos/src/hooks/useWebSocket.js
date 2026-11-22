

import { useEffect, useState, useCallback } from 'react';
import WebSocketManager from '../services/WebSocketManager';
import { WS_URL } from '../config';

// The server can push ~60 frames per second; React only needs a few. Updates are
// throttled and copied (the manager mutates one state object in place).
const THROTTLE_MS = 250;

export function useWebSocket(autoConnect = true, wsUrl = WS_URL) {
  const [state, setState] = useState(null);
  const [changes, setChanges] = useState(null);
  const [isConnected, setIsConnected] = useState(false);
  const [error, setError] = useState(null);

  useEffect(() => {
    const manager = WebSocketManager.getInstance();

    
    if (autoConnect && !manager.isConnectedToServer()) {
      try {
        manager.connect(wsUrl);
      } catch (err) {
        setError(err);
      }
    }

    
    setState(manager.getState());
    setIsConnected(manager.isConnectedToServer());

    
    let last = 0;
    let timer = null;
    const flush = () => {
      timer = null;
      last = Date.now();
      const s = manager.getState();
      setState({ ...s });
      setChanges(s._changes);
      setIsConnected(manager.isConnectedToServer());
      setError(null);
    };
    const unsubscribe = manager.subscribe(() => {
      if (timer) return;
      const wait = Math.max(0, THROTTLE_MS - (Date.now() - last));
      timer = setTimeout(flush, wait);
    });

    return () => {
      if (timer) clearTimeout(timer);
      unsubscribe();
    };
  }, [autoConnect, wsUrl]);

  const disconnect = useCallback(() => {
    WebSocketManager.getInstance().disconnect();
  }, []);

  const reconnect = useCallback(() => {
    WebSocketManager.getInstance().reconnect();
  }, []);

  return {
    state,
    changes,
    isConnected,
    error,
    disconnect,
    reconnect
  };
}

export default useWebSocket;
