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
    if (socket !== ws) return;
    const message = JSON.parse(event.data);
    if (browserCommands[message.type]) {
      runBrowserCommand(message);
    } else if (page) {
      page.postMessage(message);
    }
  };
  ws.onclose = () => {
    if (socket !== ws) return; // an older socket: nothing to do
    socket = null;
    scheduleConnect();
  };
  ws.onerror = () => {};
}

// Commands the extension runs itself, for signing in to other services in
// Ryotunes' own browser profile: the listener signs in there, in a window
// Ryotunes opens, and Ryotunes takes that site's session from the browser's
// cookie API. Ryotunes never reads other browsers' cookie files.
const cookieSites = ['youtube.com', 'soundcloud.com'];
const browserCommands = {
  // {domain}: that site's cookies in this profile.
  cookies: async message => {
    const domain = String(message.domain || '');
    if (!cookieSites.some(site => domain === site || domain.endsWith('.' + site))) {
      throw new Error('cookies of ' + domain + ' are not shared');
    }
    const cookies = await chrome.cookies.getAll({ domain });
    return cookies.map(c => ({
      name: c.name, value: c.value, domain: c.domain, path: c.path,
      expires: c.expirationDate || 0, partitioned: !!c.partitionKey,
    }));
  },
  // {url, left, top, width, height}: a normal, focused window for signing in.
  // Centred on the primary monitor, in the browser's own (scaled) units.
  openWindow: async message => {
    let width = message.width || 1000, height = message.height || 760, left, top;
    try {
      const displays = await chrome.system.display.getInfo();
      const area = (displays.find(d => d.isPrimary) || displays[0]).workArea;
      width = Math.min(width, area.width);
      height = Math.min(height, area.height);
      left = area.left + Math.round((area.width - width) / 2);
      top = area.top + Math.round((area.height - height) / 2);
    } catch (e) {
      // no display info: the browser places the window
    }
    const created = await chrome.windows.create({
      url: String(message.url), type: 'normal', focused: true, left, top, width, height,
    });
    try {
      await chrome.windows.update(created.id, { state: 'normal', focused: true, drawAttention: true });
    } catch (e) {
      // Ryotunes restores it from outside too
    }
    return created.id;
  },
  // {windowId}. Its tabs are closed one by one: closing a window of several
  // tabs makes Brave ask first, and the window would stay open.
  closeWindow: async message => closeWindowTabs(message.windowId),
};

async function closeWindowTabs(windowId) {
  let tabs;
  try {
    tabs = await chrome.tabs.query({ windowId });
  } catch (e) {
    return false; // the listener closed it
  }
  if (tabs.length) await chrome.tabs.remove(tabs.map(tab => tab.id));
  return true;
}

// The browser restores the windows of its last session, sign-in windows
// included: close all but the Web Player's when it starts.
chrome.runtime.onStartup.addListener(async () => {
  const windows = await chrome.windows.getAll({ populate: true });
  for (const w of windows) {
    const player = w.tabs.some(tab => String(tab.url || tab.pendingUrl || '').startsWith('https://open.spotify.com/'));
    if (w.type === 'normal' && !player) await closeWindowTabs(w.id);
  }
});

async function runBrowserCommand(message) {
  let reply;
  try {
    reply = { type: 'result', id: message.id, ok: true, value: await browserCommands[message.type](message) };
  } catch (e) {
    reply = { type: 'result', id: message.id, ok: false, error: String(e && e.message || e) };
  }
  send({ type: 'page', json: JSON.stringify(reply) });
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

// An alarm wakes the service worker if the browser stopped it (idle, sleep):
// timers do not survive that.
chrome.alarms.create('ryotunes-keepalive', { periodInMinutes: 0.5 });
chrome.alarms.onAlarm.addListener(() => {
  if (!isOpen()) connect();
});

// An open WebSocket with traffic keeps the service worker alive.
setInterval(() => {
  if (isOpen()) send({ type: 'ping' });
  else connect();
  reloadUnconnectedPlayer();
}, 20000);
connect();
