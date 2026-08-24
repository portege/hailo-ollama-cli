const messagesEl = document.getElementById('messages');
const emptyState = document.getElementById('empty-state');
const form = document.getElementById('chat-form');
const input = document.getElementById('prompt-input');
const sendBtn = document.getElementById('send-btn');
const modelSelect = document.getElementById('model-select');
const instructionSelect = document.getElementById('instruction-select');
const instructionDialog = document.getElementById('instruction-dialog');
const instructionName = document.getElementById('instruction-name');
const instructionText = document.getElementById('instruction-text');
const instructionSaveBtn = document.getElementById('instruction-save');
const addModelDialog = document.getElementById('model-add-dialog');
const removeModelDialog = document.getElementById('model-remove-dialog');
const chatListEl = document.getElementById('chat-list');
const sidebarEl = document.getElementById('sidebar');
const chatNewBtn = document.getElementById('chat-new');
const thinkToggle = document.getElementById('think-toggle');
const newChatBtn = document.getElementById('new-chat');

let history = [];
let busy = false;

// The backend appends these markers followed by payloads to the end of a
// completed chat stream. Everything at or after the earliest marker is not
// model output and must be stripped before display.
const METRICS_MARKER = '@@HAILO-METRICS:';
const REASONING_MARKER = '@@HAILO-REASONING:';

// Installed model names, refreshed by loadModels(). Used by the remove dialog
// and to grey out entries inside the add dialog.
let installedModels = [];
// The real selected model ('' = none). Sentinel menu entries never land here.
let selectedModel = '';

const MODEL_ADD = '__add_model__';
const MODEL_REMOVE = '__remove_model__';

function appendOption(value, text) {
  const opt = document.createElement('option');
  opt.value = value;
  opt.textContent = text;
  modelSelect.appendChild(opt);
}

// The Manage group sits at the bottom of the dropdown with the add/remove
// actions that open the model management dialogs.
function appendModelManageGroup() {
  const group = document.createElement('optgroup');
  group.label = 'Manage';
  const add = document.createElement('option');
  add.value = MODEL_ADD;
  add.textContent = '＋ Add model…';
  group.appendChild(add);
  const remove = document.createElement('option');
  remove.value = MODEL_REMOVE;
  remove.textContent = '－ Remove model…';
  group.appendChild(remove);
  modelSelect.appendChild(group);
}

// Human-readable byte size ("1.9 GB"); empty string for unknown/zero sizes.
function formatBytes(n) {
  if (!Number.isFinite(n) || n <= 0) return '';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
  const num = i === 0 || v >= 100 ? Math.round(v) : Number(v.toFixed(1));
  return `${num} ${units[i]}`;
}

// /api/models entries may be plain names or {name, size} objects depending on
// what the inference server reports; normalize both into {name, size}.
function normalizeModelList(list) {
  if (!Array.isArray(list)) return [];
  return list
    .map((m) => (typeof m === 'string'
      ? { name: m, size: 0 }
      : { name: String(m?.name ?? ''), size: Number(m?.size) || 0 }))
    .filter((m) => m.name);
}

async function loadModels() {
  const prev = selectedModel;
  let models = null;
  try {
    const res = await fetch('/api/models');
    models = normalizeModelList(await res.json());
  } catch (_) {
    models = null;
  }
  modelSelect.innerHTML = '';
  if (models === null) {
    appendOption('', 'Failed to load models');
  } else if (!models.length) {
    appendOption('', 'No models installed');
  } else {
    for (const m of models) {
      const size = formatBytes(m.size);
      appendOption(m.name, size ? `${m.name} (${size})` : m.name);
    }
  }
  appendModelManageGroup();
  installedModels = models === null ? [] : models;
  // Restore the previous selection when it still exists, else fall back to
  // the first entry.
  if (prev && [...modelSelect.options].some((o) => o.value === prev)) {
    modelSelect.value = prev;
    selectedModel = prev;
  } else {
    modelSelect.selectedIndex = 0;
    selectedModel = modelSelect.value;
  }
}

/* ================= Saved system instructions ================= */
// Instructions are stored per-browser in localStorage as [{ name, text }].
// The dropdown shows them next to the model picker; "Add instruction…" opens
// a dialog window to save a new one.
const INSTRUCTIONS_KEY = 'hailo-system-instructions';
const INSTR_ADD = '__add_instruction__';
const INSTR_DEL = '__delete_instruction__';

// Currently active instruction text ('' = none). Kept separate from the select
// value so the Add/Delete pseudo-options never leak into /api/chat requests.
let activeInstruction = '';

function loadInstructions() {
  try {
    const raw = JSON.parse(localStorage.getItem(INSTRUCTIONS_KEY));
    return Array.isArray(raw) ? raw.filter((it) => it && it.name && it.text) : [];
  } catch (_) {
    return [];
  }
}

function saveInstructions(list) {
  localStorage.setItem(INSTRUCTIONS_KEY, JSON.stringify(list));
}

// Rebuilds the dropdown options: None, every saved instruction, then a Manage
// group with the add/delete actions. `selected` is the instruction text that
// should appear selected afterwards.
function renderInstructionSelect(selected) {
  const items = loadInstructions();
  instructionSelect.innerHTML = '';

  const none = document.createElement('option');
  none.value = '';
  none.textContent = 'None';
  instructionSelect.appendChild(none);

  for (const item of items) {
    const opt = document.createElement('option');
    opt.value = item.text;
    opt.textContent = item.name;
    instructionSelect.appendChild(opt);
  }

  const manage = document.createElement('optgroup');
  manage.label = 'Manage';
  const add = document.createElement('option');
  add.value = INSTR_ADD;
  add.textContent = '＋ Add instruction…';
  manage.appendChild(add);
  if (selected) {
    const current = items.find((it) => it.text === selected);
    const del = document.createElement('option');
    del.value = INSTR_DEL;
    del.textContent = current ? `Delete "${current.name}"` : 'Delete instruction';
    manage.appendChild(del);
  }
  instructionSelect.appendChild(manage);

  instructionSelect.value = selected;
}

function openInstructionDialog() {
  instructionName.value = '';
  instructionText.value = '';
  instructionDialog.showModal();
  instructionName.focus();
}

instructionSelect.addEventListener('change', () => {
  const v = instructionSelect.value;
  if (v === INSTR_ADD) {
    openInstructionDialog();
    return; // selection is restored when the dialog closes without saving
  }
  if (v === INSTR_DEL) {
    const items = loadInstructions();
    const idx = items.findIndex((it) => it.text === activeInstruction);
    const name = idx >= 0 ? items[idx].name : 'this instruction';
    if (!confirm(`Delete instruction "${name}"?`)) {
      renderInstructionSelect(activeInstruction);
      return;
    }
    if (idx >= 0) items.splice(idx, 1);
    saveInstructions(items);
    activeInstruction = '';
    renderInstructionSelect('');
    return;
  }
  activeInstruction = v;
});

instructionSaveBtn.addEventListener('click', () => {
  const name = instructionName.value.trim();
  const text = instructionText.value.trim();
  if (!name || !text) {
    // Require both a name and the instruction text itself.
    (name ? instructionText : instructionName).focus();
    return;
  }
  const items = loadInstructions();
  items.push({ name, text });
  saveInstructions(items);
  activeInstruction = text;
  instructionDialog.close(); // the close handler re-renders the dropdown
});

document.getElementById('instruction-cancel').addEventListener('click', () =>
  instructionDialog.close());

// Enter in the name field saves; Enter inside the textarea stays a newline.
instructionName.addEventListener('keydown', (e) => {
  if (e.key === 'Enter') {
    e.preventDefault();
    instructionSaveBtn.click();
  }
});

// Whenever the dialog closes (save, cancel or Esc) rebuild the dropdown so it
// reflects storage; on cancel this restores the previous selection.
instructionDialog.addEventListener('close', () => {
  renderInstructionSelect(activeInstruction);
});

renderInstructionSelect('');

/* ================= Model management ================= */
// "＋ Add model…" lists downloadable models from the Hailo Model Zoo
// (/api/models/remote) with live pull progress; "－ Remove model…" deletes an
// installed model. Both snap the dropdown back to the previous selection while
// their dialog is open.

modelSelect.addEventListener('change', () => {
  const v = modelSelect.value;
  if (v !== MODEL_ADD && v !== MODEL_REMOVE) {
    selectedModel = v;
    return;
  }
  modelSelect.value = selectedModel;
  if (v === MODEL_ADD) openAddModelDialog();
  else openRemoveModelDialog();
});

document.getElementById('model-add-close').addEventListener('click', () =>
  addModelDialog.close());
document.getElementById('model-remove-close').addEventListener('click', () =>
  removeModelDialog.close());

function remoteRowMessage(text) {
  const div = document.createElement('div');
  div.className = 'model-remote-empty';
  div.textContent = text;
  return div;
}

let remoteModelNames = [];

function openAddModelDialog() {
  const listEl = document.getElementById('model-remote-list');
  listEl.innerHTML = '';
  listEl.appendChild(remoteRowMessage('Loading available models…'));
  addModelDialog.showModal();
  fetch('/api/models/remote')
    .then(async (res) => {
      if (!res.ok) throw new Error(await res.text());
      remoteModelNames = normalizeModelList(await res.json());
      renderRemoteModels(listEl);
    })
    .catch((err) => {
      listEl.innerHTML = '';
      listEl.appendChild(remoteRowMessage(`Failed to load models: ${err.message}`));
    });
}

// Renders one row per downloadable model. Rows already installed get a
// disabled marker; the others get an Install button that streams progress.
function renderRemoteModels(listEl) {
  listEl.innerHTML = '';
  if (!Array.isArray(remoteModelNames) || remoteModelNames.length === 0) {
    listEl.appendChild(remoteRowMessage('No downloadable models reported by the server.'));
    return;
  }
  const installed = new Set(installedModels.map((m) => m.name));
  for (const m of [...remoteModelNames].sort((a, b) => a.name.localeCompare(b.name))) {
    const row = document.createElement('div');
    row.className = 'model-row';

    const label = document.createElement('span');
    label.className = 'model-row-name';
    label.textContent = m.name;
    row.appendChild(label);

    const status = document.createElement('span');
    status.className = 'model-row-status';
    // Pre-fill the status column with the download size when known; it is
    // replaced by live progress once the install starts.
    if (m.size > 0) status.textContent = formatBytes(m.size);
    row.appendChild(status);

    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'mini-btn';
    if (installed.has(m.name)) {
      btn.textContent = 'Installed';
      btn.disabled = true;
    } else {
      btn.textContent = 'Install';
      btn.addEventListener('click', () => installModel(m.name, btn, status));
    }
    row.appendChild(btn);
    listEl.appendChild(row);
  }
}

// Streams /api/models/pull NDJSON progress into the row while downloading.
async function installModel(name, btn, status) {
  btn.disabled = true;
  btn.textContent = 'Installing…';
  const setProgress = (chunk) => {
    if (chunk.total > 0) {
      const pct = Math.min(100, Math.round(((chunk.completed || 0) / chunk.total) * 100));
      status.textContent = `${formatBytes(chunk.completed)} / ${formatBytes(chunk.total)} (${pct}%)`;
    } else if (chunk.status) {
      status.textContent = chunk.status;
    }
  };
  try {
    const res = await fetch('/api/models/pull', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name }),
    });
    if (!res.ok || !res.body) throw new Error(await res.text());

    const reader = res.body.getReader();
    const decoder = new TextDecoder();
    let buf = '';
    let done = false;
    while (!done) {
      const { value, done: streamDone } = await reader.read();
      if (streamDone) break;
      buf += decoder.decode(value, { stream: true });
      let nl;
      while ((nl = buf.indexOf('\n')) >= 0) {
        const line = buf.slice(0, nl).trim();
        buf = buf.slice(nl + 1);
        if (!line) continue;
        const chunk = JSON.parse(line);
        if (chunk.error) throw new Error(chunk.error);
        if (chunk.done) { done = true; break; }
        setProgress(chunk);
      }
    }
    if (!done) throw new Error('download stream ended unexpectedly');

    installedModels = [...new Set([...installedModels, name])];
    status.textContent = '';
    btn.textContent = 'Installed';
    await loadModels();
  } catch (err) {
    status.textContent = err.message;
    btn.disabled = false;
    btn.textContent = 'Retry';
  }
}

function openRemoveModelDialog() {
  const listEl = document.getElementById('model-installed-list');
  listEl.innerHTML = '';
  if (!installedModels.length) {
    listEl.appendChild(remoteRowMessage('No models are installed.'));
  }
  for (const item of installedModels) {
    const row = document.createElement('div');
    row.className = 'model-row';

    const label = document.createElement('span');
    label.className = 'model-row-name';
    label.textContent = item.name;
    row.appendChild(label);

    const meta = document.createElement('span');
    meta.className = 'model-row-status';
    const size = formatBytes(item.size);
    if (size) meta.textContent = size;
    row.appendChild(meta);

    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'mini-btn danger';
    btn.textContent = 'Remove';
    btn.addEventListener('click', async () => {
      if (!confirm(`Remove model "${item.name}" from the server?`)) return;
      btn.disabled = true;
      try {
        const res = await fetch('/api/models/remove', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ name: item.name }),
        });
        if (!res.ok) throw new Error(await res.text());
        row.remove();
        if (!listEl.children.length) listEl.appendChild(remoteRowMessage('No models are installed.'));
        await loadModels();
      } catch (err) {
        alert(`Failed to remove "${item.name}": ${err.message}`);
        btn.disabled = false;
      }
    });
    row.appendChild(btn);
    listEl.appendChild(row);
  }
  removeModelDialog.showModal();
}

// Renders text into el. Assistant replies go through the Markdown renderer
// (markdown.js); it degrades to plain text when the renderer is unavailable.
function setMarkdownContent(el, text, isMarkdown) {
  if (isMarkdown && typeof window.renderMarkdown === 'function') {
    el.replaceChildren(window.renderMarkdown(text));
  } else {
    el.textContent = text;
  }
}

function scrollToBottom() {
  messagesEl.scrollTop = messagesEl.scrollHeight;
}

// Builds a message row and returns the bubble element so its text can be updated as tokens stream in.
function addMessage(role, content) {
  emptyState.style.display = 'none';
  const row = document.createElement('div');
  row.className = `message-row ${role}`;

  const avatar = document.createElement('div');
  avatar.className = 'avatar';
  avatar.textContent = role === 'user' ? 'U' : 'H';

  const bubble = document.createElement('div');
  bubble.className = 'bubble';
  // Assistant replies are Markdown and need a block-level container.
  const body = document.createElement(role === 'assistant' ? 'div' : 'span');
  body.className = 'bubble-text';
  setMarkdownContent(body, content, role === 'assistant');
  bubble.appendChild(body);

  row.appendChild(avatar);
  row.appendChild(bubble);
  messagesEl.appendChild(row);
  scrollToBottom();
  return { row, bubble, body };
}

function setBusy(v) {
  busy = v;
  sendBtn.disabled = v;
  input.disabled = v;
}

input.addEventListener('input', () => {
  input.style.height = 'auto';
  input.style.height = Math.min(input.scrollHeight, 200) + 'px';
});

input.addEventListener('keydown', (e) => {
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault();
    form.requestSubmit();
  }
});

function startNewChat() {
  history = [];
  currentChatId = null;
  messagesEl.innerHTML = '';
  messagesEl.appendChild(emptyState);
  emptyState.style.display = 'block';
  renderChatList();
}

newChatBtn.addEventListener('click', startNewChat);
chatNewBtn.addEventListener('click', startNewChat);

form.addEventListener('submit', (e) => {
  e.preventDefault();
  if (busy) return;

  const prompt = input.value.trim();
  if (!prompt) return;

  input.value = '';
  input.style.height = 'auto';
  chat(prompt);
});

// Sends a prompt through the chat stream, rendering the user message and the
// streamed assistant reply along with their action buttons (copy / retry).
async function chat(prompt) {
  if (busy) return;

  const model = selectedModel;
  if (!model) {
    alert('Select a model first.');
    return;
  }
  const system = activeInstruction.trim();
  const think = thinkToggle.checked ? true : undefined;

  const userRow = addMessage('user', prompt);
  const hi = history.length;
  userRow.row.dataset.hi = hi;
  history.push({ role: 'user', content: prompt });
  addActions(userRow.bubble, {
    body: userRow.body,
    copy: true,
    onRetry: () => retry(hi, prompt),
  });

  const { bubble: assistantBubble, body: assistantBody } = addMessage('assistant', '');
  const cursor = document.createElement('span');
  cursor.className = 'cursor';
  assistantBubble.appendChild(cursor);

  setBusy(true);
  let assistantText = '';

  try {
    const res = await fetch('/api/chat', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ model, messages: history, system, think }),
    });
    if (!res.ok || !res.body) {
      throw new Error(await res.text());
    }

    const reader = res.body.getReader();
    const decoder = new TextDecoder();
    let rawText = '';
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      rawText += decoder.decode(value, { stream: true });
      assistantText = stripMetrics(rawText);
      setMarkdownContent(assistantBody, assistantText, true);
      assistantBubble.appendChild(cursor);
      scrollToBottom();
    }

    const reasoning = parseReasoning(rawText);
    if (reasoning) {
      addReasoningPanel(assistantBubble, reasoning);
      scrollToBottom();
    }

    const metrics = parseMetrics(rawText);
    if (metrics) {
      addStatsPanel(assistantBubble, metrics, model);
      scrollToBottom();
    }
  } catch (err) {
    assistantText += `\n[error: ${err.message}]`;
    setMarkdownContent(assistantBody, assistantText, true);
  } finally {
    cursor.remove();
    history.push({ role: 'assistant', content: assistantText });
    saveCurrentChat();
    addActions(assistantBubble, {
      body: assistantBody,
      copy: true,
    });
    setBusy(false);
    input.focus();
  }
}

// Removes the given user message and every later message from both the history
// and the rendered rows, then resubmits the same prompt for a fresh answer.
function retry(hi, prompt) {
  if (busy) return;

  history = history.slice(0, hi);
  saveCurrentChat();

  const rows = Array.from(messagesEl.children);
  const start = rows.findIndex((el) => Number(el.dataset.hi) === hi);
  if (start !== -1) rows.slice(start).forEach((el) => el.remove());

  chat(prompt);
}

// Appends copy (and optionally retry) action buttons to a message bubble.
function addActions(bubble, { body, copy, onRetry }) {
  if (!copy && !onRetry) return;

  const actions = document.createElement('div');
  actions.className = 'msg-actions';

  if (copy) {
    const copyBtn = document.createElement('button');
    copyBtn.className = 'action-btn';
    copyBtn.textContent = 'Copy';
    copyBtn.addEventListener('click', async () => {
      await copyToClipboard(body.textContent);
      copyBtn.textContent = 'Copied';
      setTimeout(() => (copyBtn.textContent = 'Copy'), 1500);
    });
    actions.appendChild(copyBtn);
  }

  if (onRetry) {
    const retryBtn = document.createElement('button');
    retryBtn.className = 'action-btn';
    retryBtn.textContent = 'Retry';
    retryBtn.addEventListener('click', onRetry);
    actions.appendChild(retryBtn);
  }

  bubble.appendChild(actions);
}

// Copies text to the clipboard, falling back to a hidden-textarea select+copy
// for environments where the navigator clipboard API is not available.
function copyToClipboard(text) {
  if (navigator.clipboard && navigator.clipboard.writeText) {
    return navigator.clipboard.writeText(text);
  }
  const ta = document.createElement('textarea');
  ta.value = text;
  ta.style.position = 'fixed';
  ta.style.opacity = '0';
  document.body.appendChild(ta);
  ta.select();
  try { document.execCommand('copy'); } catch (e) { /* ignored */ }
  document.body.removeChild(ta);
  return Promise.resolve();
}

loadModels();

// Removes the trailing backend payloads (reasoning and/or metrics) from the
// raw stream text so only the model's answer remains for display.
function stripMetrics(raw) {
  const idx = firstMarkerIndex(raw);
  return idx === -1 ? raw : raw.slice(0, idx);
}

// Index of the earliest instrument marker in the raw stream text, or -1.
function firstMarkerIndex(raw) {
  const idxs = [raw.indexOf(METRICS_MARKER), raw.indexOf(REASONING_MARKER)].filter((i) => i !== -1);
  return idxs.length ? Math.min(...idxs) : -1;
}

// Extracts and parses the JSON metrics payload appended by the backend.
function parseMetrics(raw) {
  const idx = raw.indexOf(METRICS_MARKER);
  if (idx === -1) return null;
  try {
    const parsed = JSON.parse(raw.slice(idx + METRICS_MARKER.length));
    return parsed && typeof parsed === 'object' ? parsed : null;
  } catch {
    return null;
  }
}

// Extracts the JSON-encoded reasoning/thinking payload (if any) emitted by the
// backend for thinking-capable models.
function parseReasoning(raw) {
  const idx = raw.indexOf(REASONING_MARKER);
  if (idx === -1) return null;
  let rest = raw.slice(idx + REASONING_MARKER.length);
  const m = rest.indexOf(METRICS_MARKER);
  if (m !== -1) rest = rest.slice(0, m);
  rest = rest.trim();
  try {
    const parsed = JSON.parse(rest);
    return typeof parsed === 'string' && parsed ? parsed : null;
  } catch {
    return null;
  }
}

function formatDuration(msValue) {
  if (msValue == null || Number.isNaN(msValue)) return '—';
  if (msValue >= 1000) return (msValue / 1000).toFixed(2) + ' s';
  return Math.round(msValue) + ' ms';
}

// Appends a collapsed-by-default <details> panel showing the model's reasoning
// when the backend reported a separate thinking stream.
function addReasoningPanel(bubble, text) {
  if (!text) return;

  const details = document.createElement('details');
  details.className = 'reasoning';

  const summary = document.createElement('summary');
  summary.textContent = 'Reasoning';

  const body = document.createElement('div');
  body.className = 'reasoning-text';
  body.textContent = text;

  details.appendChild(summary);
  details.appendChild(body);
  bubble.appendChild(details);
}

// Appends a collapsed-by-default <details> panel with response statistics.
// `model` is the model id that produced this response.
function addStatsPanel(bubble, m, model) {
  const rows = [];
  if (model) rows.push(['Model', model]);
  if (m.first_token_ms != null) rows.push(['Time to first token', formatDuration(m.first_token_ms)]);
  if (m.total_ms != null) rows.push(['Total time', formatDuration(m.total_ms)]);
  if (m.output_tokens) rows.push(['Generated tokens', String(m.output_tokens)]);
  if (m.tokens_per_second) rows.push(['Generation speed', m.tokens_per_second.toFixed(1) + ' tok/s']);
  if (m.prompt_tokens) rows.push(['Prompt tokens', String(m.prompt_tokens)]);
  if (m.prompt_eval_ms != null) rows.push(['Prompt evaluation', formatDuration(m.prompt_eval_ms)]);
  if (m.eval_ms != null) rows.push(['Generation time', formatDuration(m.eval_ms)]);
  if (m.load_ms != null) rows.push(['Model load time', formatDuration(m.load_ms)]);
  if (m.response_chars) rows.push(['Response size', `${m.response_chars} chars`]);
  if (rows.length === 0) return;

  const details = document.createElement('details');
  details.className = 'stats';

  const summary = document.createElement('summary');
  summary.textContent = 'Response statistics';

  const grid = document.createElement('div');
  grid.className = 'stats-grid';
  for (const [label, value] of rows) {
    const labelEl = document.createElement('span');
    labelEl.className = 'stats-label';
    labelEl.textContent = label;
    const valueEl = document.createElement('span');
    valueEl.className = 'stats-value';
    valueEl.textContent = value;
    grid.appendChild(labelEl);
    grid.appendChild(valueEl);
  }

  details.appendChild(summary);
  details.appendChild(grid);
  bubble.appendChild(details);
}

/* ================= NPU status (hailo-monitor) ================= */
const npuChip = document.getElementById('npu-chip');
const npuDrawer = document.getElementById('npu-drawer');
const npuBody = document.getElementById('npu-body');

let npuLastSeq = null;

function npuFmt(v, d = 1) {
  return v === null || v === undefined ? '—' : Number(v).toFixed(d);
}
function npuPctColor(p) {
  if (p >= 90) return '#e06c75';
  if (p >= 60) return '#d19a66';
  return 'var(--accent)';
}
function esc(s) {
  const d = document.createElement('div');
  d.textContent = s;
  return d.innerHTML;
}

function renderNpuChip(s, stale) {
  if (!s) { npuChip.textContent = 'NPU no data'; npuChip.classList.add('stale'); return; }
  const usage = s.nnc_utilization == null ? null : s.nnc_utilization;
  const temp = s.ts0_c == null ? '?' : Number(s.ts0_c).toFixed(0);
  npuChip.textContent = `NPU ${usage === null ? '—' : usage.toFixed(0) + '%'} · ${temp}°C`;
  npuChip.classList.toggle('stale', !!stale);
}

function npuCard(label, valueHTML, subHTML) {
  return `<div class="npu-card"><div class="label">${label}</div>` +
         `<div class="value">${valueHTML}</div>${subHTML ? `<div class="sub">${subHTML}</div>` : ''}</div>`;
}

function renderNpuDrawer(j) {
  const s = j.snapshot;
  if (!s) {
    npuBody.innerHTML = '<div class="npu-empty">No NPU data yet.</div>' +
      '<div class="npu-empty" style="margin-top:8px">Start the producer:<br>' +
      '<code>hailo-monitor --interval 1000 --json &gt;&gt; ~/.cache/hailo-ollama/npu-metrics.jsonl</code><br>' +
      'or pass a file: <code>webui :8080 /path/metrics.jsonl</code></div>';
    return;
  }
  const stale = j.age_ms > 5000;
  const serverModels = Array.isArray(j.running_models) ? j.running_models : [];
  const serverTag = serverModels.length
    ? `server: ${esc(serverModels.join(', '))}`
    : '';
  const usage = s.nnc_utilization;
  const cpu = s.cpu_utilization;
  const ramPct = s.ram_total_kib > 0 ? (s.ram_used_kib / s.ram_total_kib) * 100 : null;

  let html = '';

  // Headline utilization card
  html += npuCard('NPU core utilization',
    `<span style="color:${npuPctColor(usage ?? 0)}">${npuFmt(usage)}%</span>`,
    `cpu ${npuFmt(cpu)}%` +
    (s.workload_active ? ` · local ${esc(s.model)} @ ${Number(s.fps).toFixed(1)} fps` :
     serverTag ? ` · ${serverTag}` :
     (usage > 1 ? ' · busy with another process' : '')) +
        `<div class="bar"><span style="width:${Math.min(usage ?? 0, 100)}%;background:${npuPctColor(usage ?? 0)}"></span></div>`);

  // Temperatures + voltage
  html += '<div class="npu-grid">';
  html += npuCard('Temperature', `${npuFmt(s.ts0_c)}°C`, `TS1 ${npuFmt(s.ts1_c)}°C · die ${npuFmt(s.on_die_c)}°C`);
  html += npuCard('Voltage', `${npuFmt(s.on_die_voltage_mv, 0)} mV`,
    s.bist_failure_mask > 0 ? '<span class="badge danger">BIST FAIL</span>' : '<span class="badge ok">BIST ok</span>');
  html += '</div>';

  // RAM
  if (s.ram_total_kib > 0) {
    html += npuCard('Device RAM',
      `${(s.ram_used_kib/1024).toFixed(0)} / ${(s.ram_total_kib/1024).toFixed(0)} MiB`,
      ramPct !== null ? `<div class="bar"><span style="width:${ramPct}%"></span></div>` : '');
  }

  // Protection badges
  const badges = [];
  badges.push(`<span class="badge ${s.temp_throttling_active ? 'danger' : 'ok'}">temp-throttle</span>`);
  badges.push(`<span class="badge ${s.overcurrent_throttling_active ? 'danger' : 'ok'}">oc-throttle</span>`);
  badges.push(`<span class="badge ${s.overcurrent_protect_active ? 'danger' : 'ok'}">oc-protect</span>`);
  html += npuCard('Protection', badges.join(' '),
    `orange ${s.orange_temp_c ?? '—'}°C · red ${s.red_temp_c ?? '—'}°C`);

  // Platform / link kv pairs
  html += npuCard('Platform', '', `
    <div class="npu-kv"><span>Device</span><span>${esc(s.device_id)}</span></div>
    <div class="npu-kv"><span>Firmware</span><span>${esc(s.fw_version)}${s.fw_is_release ? '' : ' (dev)'}</span></div>
    <div class="npu-kv"><span>Driver</span><span>${esc(s.driver_version)}</span></div>
    <div class="npu-kv"><span>Kernel</span><span>${esc(s.kernel_release)}</span></div>
    <div class="npu-kv"><span>PCIe link</span><span>${esc(s.pcie.link_cur)} x${s.pcie.width_cur} (max x${s.pcie.width_max})</span></div>
    <div class="npu-kv"><span>Boot / LCS</span><span>${esc(s.boot_source)} / 0x${Number(s.lcs).toString(16)}</span></div>
    <div class="npu-kv"><span>NN-core clock</span><span>${s.nn_core_clock_hz ? (s.nn_core_clock_hz/1e6).toFixed(0)+' MHz' : '—'}</span></div>`);

  // Model attribution: local --hef workload vs server-resident chat models
  var modelHTML;
  if (s.workload_active) {
    modelHTML = npuCard('Local model (--hef)', esc(s.model),
      `${npuFmt(s.fps)} fps · ${npuFmt(s.latency_ms,2)} ms/frame · load ${npuFmt(s.load_percent)}%` +
      `<div class="bar"><span style="width:${Math.min(s.load_percent,100)}%"></span></div>`);
  } else if (serverModels.length) {
    modelHTML = npuCard('Server models',
      esc(serverModels.join(', ')),
      'resident in the inference server — this is what is driving NPU usage');
  } else {
    modelHTML = npuCard('Models', '<span class="npu-empty">none resident</span>',
      'load a chat model or run hailo-monitor --hef <m.hef> to populate');
  }
  html += modelHTML;

  // Events
  const ev = (s.events || []).slice(-8).reverse();
  html += npuCard(`Firmware events <span class="sub">(${s.events_total} total)</span>`,
    ev.length ? '' : '<span class="npu-empty">none</span>',
    ev.length ? `<ul class="npu-events">${ev.map(e =>
      `<li><b>${esc(e.time)}</b> ${esc(e.name)}${e.detail ? ' — ' + esc(e.detail) : ''}</li>`).join('')}</ul>` : '');

  if (stale) html += '<div class="npu-empty">⚠ data is stale — producer may have stopped</div>';
  npuBody.innerHTML = html;
}

async function refreshNPU() {
  try {
    const res = await fetch('/api/npu/metrics');
    const j = await res.json();
    renderNpuChip(j.snapshot, j.age_ms > 5000);
    if (!npuDrawer.classList.contains('open')) return;
    // Only repaint the open drawer when there is something new.
    if (j.received_at === npuLastSeq) return;
    npuLastSeq = j.received_at;
    renderNpuDrawer(j);
  } catch (_) { /* backend unreachable */ }
}

npuChip.addEventListener('click', () => {
  npuDrawer.classList.toggle('open');
  npuLastSeq = null; // force redraw on open
  refreshNPU();
});
document.getElementById('npu-close').addEventListener('click', () =>
  npuDrawer.classList.remove('open'));

setInterval(refreshNPU, 2000);
refreshNPU();

/* ================= Saved chats (server-side SQLite) ================= */
// Conversations live in a SQLite database managed by the webui process
// (see /api/chats endpoints), so history survives restarts and is shared
// across every browser that opens this instance.

let chats = [];
let currentChatId = null;

async function refreshChatList() {
  try {
    const res = await fetch('/api/chats');
    if (!res.ok) throw new Error(await res.text());
    chats = await res.json();
    renderChatList();
  } catch (err) {
    console.warn('Failed to load chat list:', err);
  }
}

function renderChatList() {
  chatListEl.innerHTML = '';
  if (!chats.length) {
    const empty = document.createElement('div');
    empty.className = 'chat-list-empty';
    empty.textContent = 'No saved chats yet.';
    chatListEl.appendChild(empty);
    return;
  }
  for (const c of chats) {
    const item = document.createElement('div');
    item.className = 'chat-item' + (c.id === currentChatId ? ' active' : '');

    const title = document.createElement('span');
    title.className = 'chat-item-title';
    title.textContent = c.title || 'Untitled chat';

    const time = document.createElement('span');
    time.className = 'chat-item-time';
    time.textContent = formatChatTime(c.updatedAt);

    const del = document.createElement('button');
    del.type = 'button';
    del.className = 'chat-item-del';
    del.textContent = '✕';
    del.title = 'Delete chat';
    del.addEventListener('click', async (e) => {
      e.stopPropagation();
      if (!confirm(`Delete "${c.title || 'this chat'}"?`)) return;
      try {
        const res = await fetch('/api/chats/' + encodeURIComponent(c.id), {
          method: 'DELETE',
        });
        if (!res.ok) throw new Error(await res.text());
        await refreshChatList();
        if (c.id === currentChatId) startNewChat();
      } catch (err) {
        alert(`Failed to delete chat: ${err.message}`);
      }
    });

    item.append(title, time, del);
    item.addEventListener('click', () => loadChat(c.id));
    chatListEl.appendChild(item);
  }
}

function formatChatTime(ts) {
  if (!ts) return '';
  const d = new Date(ts);
  const now = new Date();
  return d.toDateString() === now.toDateString()
    ? d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
    : d.toLocaleDateString([], { month: 'short', day: 'numeric' });
}

// Upserts the active conversation after each completed exchange.
async function saveCurrentChat() {
  if (!history.length) return;
  const firstUser = history.find((m) => m.role === 'user');
  const payload = {
    id: currentChatId || undefined,
    title: (firstUser?.content || 'New chat').replace(/\s+/g, ' ').trim().slice(0, 60),
    messages: history.map((m) => ({ role: m.role, content: m.content })),
  };
  try {
    const res = await fetch('/api/chats', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });
    if (!res.ok) throw new Error(await res.text());
    const data = await res.json();
    if (data.id) currentChatId = data.id;
    await refreshChatList();
  } catch (err) {
    console.warn('Failed to save chat:', err);
  }
}

// Fetches a saved conversation and replays it into the message area.
// Assistant replies are re-rendered as Markdown by addMessage().
async function loadChat(id) {
  try {
    const res = await fetch('/api/chats/' + encodeURIComponent(id));
    if (!res.ok) throw new Error(await res.text());
    const chat = await res.json();

    currentChatId = chat.id;
    history = (chat.messages || []).map((m) => ({ role: m.role, content: m.content }));

    messagesEl.innerHTML = '';
    emptyState.style.display = 'none';
    for (const msg of history) {
      const { bubble } = addMessage(msg.role, msg.content);
      addActions(bubble, { copy: true });
    }
    messagesEl.scrollTop = messagesEl.scrollHeight;
    renderChatList();
    sidebarEl.classList.remove('open'); // close the drawer on narrow screens
  } catch (err) {
    alert(`Failed to load chat: ${err.message}`);
  }
}

document.getElementById('sidebar-toggle').addEventListener('click', () =>
  sidebarEl.classList.toggle('open'));

refreshChatList();

// One-time migration: pull conversations saved by older builds out of
// localStorage into the server database.
(async function migrateLocalChats() {
  try {
    const raw = localStorage.getItem('hailo-chats');
    if (!raw) return;
    const local = JSON.parse(raw);
    localStorage.removeItem('hailo-chats');
    if (!Array.isArray(local)) return;
    for (const c of local.slice(-50)) {
      if (!c || !Array.isArray(c.messages) || !c.messages.length) continue;
      await fetch('/api/chats', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ title: c.title || 'Imported chat', messages: c.messages }),
      });
    }
    await refreshChatList();
  } catch (_) { /* best effort */ }
})();
