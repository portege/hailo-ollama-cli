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
const thinkToggle = document.getElementById('think-toggle');
const newChatBtn = document.getElementById('new-chat');

let history = [];
let busy = false;

// The backend appends these markers followed by payloads to the end of a
// completed chat stream. Everything at or after the earliest marker is not
// model output and must be stripped before display.
const METRICS_MARKER = '@@HAILO-METRICS:';
const REASONING_MARKER = '@@HAILO-REASONING:';

async function loadModels() {
  try {
    const res = await fetch('/api/models');
    const models = await res.json();
    modelSelect.innerHTML = '';
    if (!models || models.length === 0) {
      const opt = document.createElement('option');
      opt.textContent = 'No models installed';
      modelSelect.appendChild(opt);
      return;
    }
    for (const name of models) {
      const opt = document.createElement('option');
      opt.value = name;
      opt.textContent = name;
      modelSelect.appendChild(opt);
    }
  } catch (err) {
    modelSelect.innerHTML = '';
    const opt = document.createElement('option');
    opt.textContent = 'Failed to load models';
    modelSelect.appendChild(opt);
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
  const body = document.createElement('span');
  body.className = 'bubble-text';
  body.textContent = content;
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

newChatBtn.addEventListener('click', () => {
  history = [];
  messagesEl.innerHTML = '';
  messagesEl.appendChild(emptyState);
  emptyState.style.display = 'block';
});

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

  const model = modelSelect.value;
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
      assistantBody.textContent = assistantText;
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
    assistantBody.textContent = assistantText;
  } finally {
    cursor.remove();
    history.push({ role: 'assistant', content: assistantText });
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
