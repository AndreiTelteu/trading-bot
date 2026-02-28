/**
 * useWebSocket Hook
 * 
 * A React hook for subscribing to WebSocket events dispatched via window.CustomEvent.
 * Events are prefixed with "ws:" and can be subscribed individually.
 * 
 * Usage:
 *   const { connectionState, send } = useWebSocket();
 *   
 *   useEffect(() => {
 *     const handleWalletUpdate = (e) => {
 *       setWallet(e.detail);
 *     };
 *     window.addEventListener('ws:wallet_update', handleWalletUpdate);
 *     return () => window.removeEventListener('ws:wallet_update', handleWalletUpdate);
 *   }, []);
 */

import { useState, useEffect, useCallback, useRef } from 'react';
import { getWebSocketManager } from '../services/websocketManager';

/**
 * Subscribe to a specific WebSocket event type
 * @param {string} eventType - The event type to subscribe to (without ws: prefix)
 * @param {function} callback - Callback function receiving the event detail
 * @returns {function} Unsubscribe function
 */
export function useWebSocketEvent(eventType, callback) {
  const callbackRef = useRef(callback);
  
  // Keep callback ref up to date
  useEffect(() => {
    callbackRef.current = callback;
  }, [callback]);
  
  useEffect(() => {
    const eventName = eventType.startsWith('ws:') ? eventType : `ws:${eventType}`;
    
    const handler = (event) => {
      callbackRef.current?.(event.detail, event);
    };
    
    window.addEventListener(eventName, handler);
    
    return () => {
      window.removeEventListener(eventName, handler);
    };
  }, [eventType]);
}

/**
 * Main WebSocket hook for connection state and sending messages
 * @returns {Object} { connectionState, send, isConnected }
 */
export function useWebSocket() {
  const manager = getWebSocketManager();
  const [connectionState, setConnectionState] = useState(manager.getConnectionState());
  
  useEffect(() => {
    // Connect on first mount if not already connected
    if (manager.getConnectionState() === 'disconnected') {
      manager.connect();
    }
    
    // Listen for connection state changes
    const handleStateChange = (event) => {
      setConnectionState(event.detail.state);
    };
    
    window.addEventListener('ws:connection_state', handleStateChange);
    
    // Also check state periodically (in case events are missed)
    const interval = setInterval(() => {
      setConnectionState(manager.getConnectionState());
    }, 1000);
    
    return () => {
      window.removeEventListener('ws:connection_state', handleStateChange);
      clearInterval(interval);
    };
  }, [manager]);
  
  const send = useCallback((message) => {
    manager.send(message);
  }, [manager]);
  
  const isConnected = connectionState === 'connected';
  
  return {
    connectionState,
    send,
    isConnected,
    isConnecting: connectionState === 'connecting',
    isReconnecting: connectionState === 'reconnecting',
    hasFailed: connectionState === 'failed'
  };
}

/**
 * Hook for listening to multiple WebSocket events
 * @param {Object} handlers - Object mapping event types to handler functions
 * Example: { wallet_update: (data) => setWallet(data), positions_update: (data) => setPositions(data) }
 */
export function useWebSocketEvents(handlers) {
  useEffect(() => {
    const cleanupFunctions = [];
    
    Object.entries(handlers).forEach(([eventType, callback]) => {
      const eventName = eventType.startsWith('ws:') ? eventType : `ws:${eventType}`;
      
      const handler = (event) => {
        callback(event.detail, event);
      };
      
      window.addEventListener(eventName, handler);
      cleanupFunctions.push(() => window.removeEventListener(eventName, handler));
    });
    
    return () => {
      cleanupFunctions.forEach(cleanup => { cleanup(); });
    };
  }, [handlers]);
}

/**
 * Hook specifically for data synchronization
 * Requests full sync on mount and handles reconnection sync
 * @param {function} onSync - Callback when full sync data is received
 */
export function useWebSocketSync(onSync) {
  const { send, isConnected } = useWebSocket();
  const hasSynced = useRef(false);
  
  // Request full sync when connected
  useEffect(() => {
    if (isConnected && !hasSynced.current) {
      send({ type: 'request_full_sync' });
      hasSynced.current = true;
    }
  }, [isConnected, send]);
  
  // Listen for various sync events
  useWebSocketEvents({
    wallet_update: (data) => onSync?.('wallet', data),
    positions_update: (data) => onSync?.('positions', data),
    activity_log_bulk: (data) => onSync?.('activity_logs', data),
    trending_update: (data) => onSync?.('trending', data),
    snapshot_update: (data) => onSync?.('snapshot', data),
    orders_update: (data) => onSync?.('orders', data),
  });
  
  // Reset sync flag on disconnect to re-sync on reconnect
  useEffect(() => {
    if (!isConnected) {
      hasSynced.current = false;
    }
  }, [isConnected]);
}

export default useWebSocket;
