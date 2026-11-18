// Ryotunes: the extension's side of the Web Player page (isolated world).
// Carries the bridge's messages to background.js and its commands back.
(() => {
  const port = chrome.runtime.connect({ name: 'player' });
  window.addEventListener('message', event => {
    const data = event.data;
    if (event.source !== window || !data || data.__ryotunes !== 'up') return;
    port.postMessage({ type: 'page', json: data.json });
  });
  port.onMessage.addListener(message => {
    if (message.type === 'call') {
      window.postMessage({ __ryotunes: 'down', id: message.id, method: message.method, args: message.args || {} }, location.origin);
    } else if (message.type === 'navigate' && typeof message.url === 'string') {
      location.assign(message.url);
    }
  });
})();
