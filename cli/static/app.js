const messagesEl = document.getElementById('messages');
const emptyState = document.getElementById('empty-state');
const form = document.getElementById('chat-form');
const input = document.getElementById('prompt-input');
const sendBtn = document.getElementById('send-btn');
const modelSelect = document.getElementById('model-select');
const systemInput = document.getElementById('system-input');
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
  const system = systemInput.value.trim();
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
