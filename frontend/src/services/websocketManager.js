/**
 * WebSocket Manager Singleton
 * 
 * Manages a single WebSocket connection for the entire application lifetime.
 * Dispatches custom events on window with "ws:" prefix for all messages.
 * Handles automatic reconnection with exponential backoff.
 */

const WS_URL = `${window.location.protocol === 'https:' ? 'wss:' : 'ws:'}//${window.location.host}/ws`;
const MAX_RECONNECT_ATTEMPTS = 10;
const INITIAL_RECONNECT_DELAY = 1000;
const MAX_RECONNECT_DELAY = 30000;
const HEARTBEAT_INTERVAL = 30000;
const HEARTBEAT_TIMEOUT = 10000;

class WebSocketManager {
  constructor() {
    this.ws = null;
    this.connectionState = 'disconnected';
    this.reconnectAttempts = 0;
    this.reconnectTimeout = null;
    this.heartbeatInterval = null;
    this.heartbeatTimeout = null;
    this.messageQueue = [];
    this.isManualClose = false;
  }

  /**
   * Get singleton instance
   */
  static getInstance() {
    if (!WebSocketManager.instance) {
      WebSocketManager.instance = new WebSocketManager();
    }
    return WebSocketManager.instance;
  }

  /**
   * Connect to WebSocket server
   */
  connect() {
    if (this.ws?.readyState === WebSocket.OPEN || this.ws?.readyState === WebSocket.CONNECTING) {
      console.log('[WS] Already connected or connecting');
      return;
    }

    this.setConnectionState('connecting');
    console.log('[WS] Connecting to', WS_URL);

    try {
      this.ws = new WebSocket(WS_URL);

      this.ws.onopen = this.handleOpen.bind(this);
      this.ws.onclose = this.handleClose.bind(this);
      this.ws.onerror = this.handleError.bind(this);
      this.ws.onmessage = this.handleMessage.bind(this);
    } catch (error) {
      console.error('[WS] Connection error:', error);
      this.handleError(error);
    }
  }

  /**
   * Handle WebSocket open event
   */
  handleOpen() {
    console.log('[WS] Connected');
    this.reconnectAttempts = 0;
    this.isManualClose = false;
    this.setConnectionState('connected');
    
    // Send any queued messages
    this.flushMessageQueue();
    
    // Start heartbeat
    this.startHeartbeat();
    
    // Dispatch connection event
    this.dispatchEvent('connection_state', { state: 'connected' });
    
    // Request full sync from server
    this.send({ type: 'request_full_sync' });
  }

  /**
   * Handle WebSocket close event
   */
  handleClose(event) {
    console.log('[WS] Disconnected:', event.code, event.reason);
    this.stopHeartbeat();
    this.setConnectionState('disconnected');
    
    this.dispatchEvent('connection_state', { 
      state: 'disconnected', 
      code: event.code, 
      reason: event.reason 
    });

    // Attempt reconnection if not manually closed
    if (!this.isManualClose) {
      this.scheduleReconnect();
    }
  }

  /**
   * Handle WebSocket error event
   */
  handleError(error) {
    console.error('[WS] Error:', error);
    this.dispatchEvent('connection_state', { 
      state: 'error', 
      error: error?.message || 'Unknown error' 
    });
  }

  /**
   * Handle incoming WebSocket messages
   */
  handleMessage(event) {
    try {
      const data = JSON.parse(event.data);
      
      // Handle pong response
      if (data.type === 'pong') {
        this.handlePong();
        return;
      }
      
      // Dispatch custom event with ws: prefix
      this.dispatchEvent(data.type, data.payload || data);
    } catch (error) {
      console.error('[WS] Failed to parse message:', error, event.data);
    }
  }

  /**
   * Dispatch custom event on window
   */
  dispatchEvent(eventName, payload) {
    const fullEventName = `ws:${eventName}`;
    const customEvent = new CustomEvent(fullEventName, { 
      detail: payload,
      bubbles: true,
      cancelable: true
    });
    window.dispatchEvent(customEvent);
    
    // Also dispatch a generic message event for debugging
    if (eventName !== 'connection_state' && eventName !== 'pong') {
      window.dispatchEvent(new CustomEvent('ws:message', { 
        detail: { type: eventName, payload } 
      }));
    }
  }

  /**
   * Set connection state and dispatch event
   */
  setConnectionState(state) {
    this.connectionState = state;
  }

  /**
   * Get current connection state
   */
  getConnectionState() {
    return this.connectionState;
  }

  /**
   * Schedule reconnection with exponential backoff
   */
  scheduleReconnect() {
    if (this.reconnectAttempts >= MAX_RECONNECT_ATTEMPTS) {
      console.error('[WS] Max reconnection attempts reached');
      this.setConnectionState('failed');
      this.dispatchEvent('connection_state', { 
        state: 'failed', 
        reason: 'Max reconnection attempts reached' 
      });
      return;
    }

    const delay = Math.min(
      INITIAL_RECONNECT_DELAY * Math.pow(2, this.reconnectAttempts),
      MAX_RECONNECT_DELAY
    );
    
    this.reconnectAttempts++;
    this.setConnectionState('reconnecting');
    
    console.log(`[WS] Reconnecting in ${delay}ms (attempt ${this.reconnectAttempts}/${MAX_RECONNECT_ATTEMPTS})`);
    
    this.dispatchEvent('connection_state', { 
      state: 'reconnecting', 
      attempt: this.reconnectAttempts, 
      maxAttempts: MAX_RECONNECT_ATTEMPTS,
      delay
    });

    this.reconnectTimeout = setTimeout(() => {
      this.connect();
    }, delay);
  }

  /**
   * Send message to WebSocket server
   */
  send(message) {
    const messageStr = typeof message === 'string' ? message : JSON.stringify(message);
    
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(messageStr);
    } else {
      // Queue message if not connected
      this.messageQueue.push(messageStr);
      console.log('[WS] Message queued (not connected)');
    }
  }

  /**
   * Flush queued messages
   */
  flushMessageQueue() {
    while (this.messageQueue.length > 0 && this.ws?.readyState === WebSocket.OPEN) {
      const message = this.messageQueue.shift();
      this.ws.send(message);
    }
  }

  /**
   * Start heartbeat/ping-pong
   */
  startHeartbeat() {
    this.stopHeartbeat();
    
    this.heartbeatInterval = setInterval(() => {
      if (this.ws?.readyState === WebSocket.OPEN) {
        this.send({ type: 'ping' });
        
        // Set timeout for pong response
        this.heartbeatTimeout = setTimeout(() => {
          console.warn('[WS] Heartbeat timeout, closing connection');
          this.ws.close();
        }, HEARTBEAT_TIMEOUT);
      }
    }, HEARTBEAT_INTERVAL);
  }

  /**
   * Handle pong response
   */
  handlePong() {
    if (this.heartbeatTimeout) {
      clearTimeout(this.heartbeatTimeout);
      this.heartbeatTimeout = null;
    }
  }

  /**
   * Stop heartbeat
   */
  stopHeartbeat() {
    if (this.heartbeatInterval) {
      clearInterval(this.heartbeatInterval);
      this.heartbeatInterval = null;
    }
    if (this.heartbeatTimeout) {
      clearTimeout(this.heartbeatTimeout);
      this.heartbeatTimeout = null;
    }
  }

  /**
   * Close connection manually
   */
  disconnect() {
    this.isManualClose = true;
    
    if (this.reconnectTimeout) {
      clearTimeout(this.reconnectTimeout);
      this.reconnectTimeout = null;
    }
    
    this.stopHeartbeat();
    
    if (this.ws) {
      this.ws.close();
      this.ws = null;
    }
    
    this.setConnectionState('disconnected');
  }
}

// Create singleton instance
WebSocketManager.instance = null;

// Export singleton getter
export const getWebSocketManager = () => WebSocketManager.getInstance();

// Export default for convenience
export default WebSocketManager;
