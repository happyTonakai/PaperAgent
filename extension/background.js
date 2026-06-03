// PaperAgent - Background Service Worker
// Detects local PaperAgent server, supports user-configured custom ports.

const DEFAULT_PORT_START = 8686;
const DEFAULT_PORT_END = 8785;

// ─── Storage Helpers ──────────────────────────────────────────────────────

const STORAGE_KEY = 'paperagent_config';

async function loadConfig() {
  const result = await chrome.storage.sync.get(STORAGE_KEY);
  return result[STORAGE_KEY] || {};
}

function saveConfig(config) {
  return chrome.storage.sync.set({ [STORAGE_KEY]: config });
}

// ─── Message Handler ─────────────────────────────────────────────────────

chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
  if (message.type === 'OPEN_IN_PAPERAGENT') {
    openInPaperAgent(message.url, sender.tab?.id)
      .then((result) => sendResponse(result))
      .catch((err) => sendResponse({ ok: false, error: err.message }));
    return true;
  }

  if (message.type === 'GET_STATUS') {
    getStatus()
      .then((result) => sendResponse(result))
      .catch((err) => sendResponse({ ok: false, error: err.message }));
    return true;
  }

  if (message.type === 'SET_PORT') {
    saveConfig({ port: message.port })
      .then(() => sendResponse({ ok: true }))
      .catch((err) => sendResponse({ ok: false, error: err.message }));
    return true;
  }
});

// ─── Open in PaperAgent ──────────────────────────────────────────────────

async function openInPaperAgent(arxivUrl, sourceTabId) {
  const port = await findPaperAgentPort();
  if (!port) {
    throw new Error(
      'PaperAgent 未运行。请先启动 PaperAgent，或右键扩展图标 → 选项，配置自定义端口。'
    );
  }

  const encodedUrl = encodeURIComponent(arxivUrl);
  const tabUrl = `http://localhost:${port}/?url=${encodedUrl}`;

  // 查所有标签页，手动匹配 localhost:<port>，避免 URL match pattern 歧义（如无斜杠路径）
  const allTabs = await chrome.tabs.query({});
  const reusable = allTabs.find(
    (t) => t.id !== sourceTabId && t.url?.startsWith(`http://localhost:${port}`)
  );

  if (reusable) {
    // 已有 PaperAgent 标签页 → 更新 URL 并激活
    await chrome.tabs.update(reusable.id, {
      url: tabUrl,
      active: true,
    });
    await chrome.windows.update(reusable.windowId, { focused: true });
  } else {
    // 没有 → 新建标签页（在源标签页右侧）
    await chrome.tabs.create({
      url: tabUrl,
      index: sourceTabId != null ? sourceTabId + 1 : undefined,
    });
  }

  return { ok: true, port };
}

// ─── Port Detection ─────────────────────────────────────────────────────

async function findPaperAgentPort() {
  const config = await loadConfig();

  // 1. If user configured a specific port, try it first
  if (config.port) {
    const port = parseInt(config.port, 10);
    if (!isNaN(port) && port > 0 && port < 65536) {
      const ok = await tryPort(port);
      if (ok) return port;
    }
  }

  // 2. Fall back to probing the default range
  const controller = new AbortController();

  for (let batchStart = DEFAULT_PORT_START; batchStart <= DEFAULT_PORT_END; batchStart += 20) {
    const batchEnd = Math.min(batchStart + 19, DEFAULT_PORT_END);
    const promises = [];

    for (let port = batchStart; port <= batchEnd; port++) {
      promises.push(
        fetch(`http://localhost:${port}/api/config`, {
          method: 'GET',
          signal: controller.signal,
        })
          .then((res) => (res.ok ? port : Promise.reject()))
          .catch(() => Promise.reject())
      );
    }

    try {
      const port = await Promise.any(promises);
      controller.abort();
      return port;
    } catch {
      continue;
    }
  }

  return null;
}

async function tryPort(port) {
  try {
    const res = await fetch(`http://localhost:${port}/api/config`, {
      method: 'GET',
      signal: AbortSignal.timeout(2000),
    });
    return res.ok;
  } catch {
    return false;
  }
}

// ─── Status (for popup/options) ──────────────────────────────────────────

async function getStatus() {
  const config = await loadConfig();
  const port = await findPaperAgentPort();
  return {
    running: port != null,
    port: port,
    configuredPort: config.port || null,
  };
}
