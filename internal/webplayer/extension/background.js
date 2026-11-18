// Ryotunes: keeps one WebSocket to the Ryotunes daemon (address and token in
// config.json, written by Ryotunes when it starts the browser) and relays
// between it and the Web Player page (relay.js).
let config = null;
let socket = null;
let page = null;
let pending = [];

async function loadConfig() {
  if (!config) config = await (await fetch(chrome.runtime.getURL('config.json'))).json();
  return config;
}

function send(message) {
  const text = JSON.stringify(message);
  if (socket && socket.readyState === WebSocket.OPEN) socket.send(text);
  else if (message.type !== 'ping') pending = pending.concat(text).slice(-200);
}

async function connect() {
  const { url } = await loadConfig();
  socket = new WebSocket(url);
  socket.onopen = () => {
    if (page) socket.send(JSON.stringify({ type: 'hello' }));
    for (const text of pending) socket.send(text);
    pending = [];
  };
  socket.onmessage = event => {
    if (page) page.postMessage(JSON.parse(event.data));
  };
  socket.onclose = () => {
    socket = null;
    setTimeout(connect, 1000);
  };
  socket.onerror = () => {};
}

chrome.runtime.onConnect.addListener(newPage => {
  if (newPage.name !== 'player') return;
  page = newPage;
  send({ type: 'hello' });
  newPage.onMessage.addListener(message => send(message));
  newPage.onDisconnect.addListener(() => {
    if (page === newPage) page = null;
  });
});

// An open WebSocket with traffic keeps the service worker alive.
setInterval(() => send({ type: 'ping' }), 20000);
connect();
