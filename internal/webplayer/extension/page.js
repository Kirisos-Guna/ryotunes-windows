// Ryotunes: runs in the Web Player page itself, before Myspoti's bridge
// (player-bridge.js). The bridge reports through window.__mspHostPost; this
// hands its messages to relay.js and runs the commands relay.js passes in.
(() => {
  if (window.__ryotunesPage) return;
  window.__ryotunesPage = true;
  window.__mspHostPost = json => window.postMessage({ __ryotunes: 'up', json }, location.origin);
  window.addEventListener('message', event => {
    const data = event.data;
    if (event.source !== window || !data || data.__ryotunes !== 'down') return;
    if (window.Myspoti && typeof window.Myspoti.call === 'function') {
      // The bridge posts the result (or error) itself, with this id.
      window.Myspoti.call(data.id, data.method, data.args).catch(() => {});
    } else {
      window.__mspHostPost(JSON.stringify({ type: 'result', id: data.id, ok: false, error: 'not ready' }));
    }
  });
})();
