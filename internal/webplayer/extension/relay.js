// Ryotunes: the extension's side of the Web Player page (isolated world).
// Carries the bridge's messages to background.js and its commands back.
//
// The browser stops the extension's background script when it idles or the
// computer sleeps; its port to this page then closes. Connecting again wakes
// the background script, which reconnects to Ryotunes (otherwise Ryotunes
// stayed "not connected" until the page reloaded).
(() => {
  if (window.__ryotunesRelay) return; // added again by connectPlayerPage
  window.__ryotunesRelay = true;
  let port = null;
  let queued = [];

  function connect() {
    try {
      port = chrome.runtime.connect({ name: 'player' });
    } catch (e) {
      port = null; // the extension was reloaded: this page's scripts are orphaned
      return;
    }
    port.onMessage.addListener(message => {
      if (message.type === 'call') {
        window.postMessage({ __ryotunes: 'down', id: message.id, method: message.method, args: message.args || {} }, location.origin);
      } else if (message.type === 'navigate' && typeof message.url === 'string') {
        location.assign(message.url);
      }
    });
    port.onDisconnect.addListener(() => {
      port = null;
      setTimeout(connect, 1000);
    });
    // The bridge's announcements while disconnected (its ready and state).
    const pending = queued;
    queued = [];
    for (const message of pending) port.postMessage(message);
    if (reconnecting) {
      // Ryotunes saw a new connection and waits for the bridge's ready again.
      window.postMessage({ __ryotunes: 'reannounce' }, location.origin);
    }
    reconnecting = true;
  }
  let reconnecting = false;

  window.addEventListener('message', event => {
    const data = event.data;
    if (event.source !== window || !data || data.__ryotunes !== 'up') return;
    const message = { type: 'page', json: data.json };
    if (port) {
      port.postMessage(message);
    } else {
      queued = queued.concat(message).slice(-50);
    }
  });

  connect();
})();
