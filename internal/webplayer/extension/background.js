// Ryotunes: keeps one WebSocket to the Ryotunes daemon (address and token in
// config.json, written by Ryotunes when it starts the browser) and relays
// between it and the Web Player page (relay.js).
//
// Each WebSocket's handlers touch the shared state only while that socket is
// still the current one: a replaced socket closing must not tear down its
// successor (that broke the relay, leaving Ryotunes waiting for the page).
let config = null;
let socket = null;
let page = null;
let pending = [];
let retry = null;

async function loadConfig() {
  if (!config) config = await (await fetch(chrome.runtime.getURL('config.json'))).json();
  return config;
}

function isOpen() {
  return socket !== null && socket.readyState === WebSocket.OPEN;
}

function send(message) {
  const text = JSON.stringify(message);
  if (isOpen()) socket.send(text);
  else if (message.type !== 'ping') pending = pending.concat(text).slice(-200);
}

function scheduleConnect() {
  if (retry !== null) return;
  retry = setTimeout(() => {
    retry = null;
    connect();
  }, 1000);
}

async function connect() {
  if (socket !== null && socket.readyState <= WebSocket.OPEN) return; // connecting or open
  let url;
  try {
    ({ url } = await loadConfig());
  } catch (e) {
    scheduleConnect();
    return;
  }
  const ws = new WebSocket(url);
  socket = ws;
  ws.onopen = () => {
    if (socket !== ws) return;
    // A new page queued its hello (below): it goes first, then the rest.
    for (const text of pending) ws.send(text);
    pending = [];
  };
  ws.onmessage = event => {
    if (socket === ws && page) page.postMessage(JSON.parse(event.data));
  };
  ws.onclose = () => {
    if (socket !== ws) return; // an older socket: nothing to do
    socket = null;
    scheduleConnect();
  };
  ws.onerror = () => {};
}

chrome.runtime.onConnect.addListener(newPage => {
  if (newPage.name !== 'player') return;
  page = newPage;
  send({ type: 'hello' }); // a new page: Ryotunes waits for its bridge again
  if (!isOpen()) connect();
  newPage.onMessage.addListener(message => send(message));
  newPage.onDisconnect.addListener(() => {
    if (page === newPage) page = null;
  });
});

// The browser opens the Web Player as it starts, and the page can load
// before this extension does: then its scripts were never injected and the
// page never connects. Reload such a page (never the sign-in page, which is
// on another host), a few times at most.
let reloads = 0;
function reloadUnconnectedPlayer() {
  if (page || reloads >= 3) return;
  chrome.tabs.query({ url: 'https://open.spotify.com/*' }, tabs => {
    if (page || chrome.runtime.lastError) return;
    for (const tab of tabs) {
      reloads++;
      chrome.tabs.reload(tab.id);
    }
  });
}
setTimeout(reloadUnconnectedPlayer, 3000);

// An open WebSocket with traffic keeps the service worker alive.
setInterval(() => {
  if (isOpen()) send({ type: 'ping' });
  else connect();
  reloadUnconnectedPlayer();
}, 20000);
connect();
