const messagesEl = document.getElementById('messages');
const emptyState = document.getElementById('empty-state');
const form = document.getElementById('chat-form');
const input = document.getElementById('prompt-input');
const sendBtn = document.getElementById('send-btn');
const modelSelect = document.getElementById('model-select');
const newChatBtn = document.getElementById('new-chat');

let history = [];
let busy = false;

// The backend appends this marker followed by a JSON metrics payload to the
// end of every completed chat stream. Everything after it is not model output.
const METRICS_MARKER = '@@HAILO-METRICS:';

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
  return { bubble, body };
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

form.addEventListener('submit', async (e) => {
  e.preventDefault();
  if (busy) return;

  const prompt = input.value.trim();
  if (!prompt) return;

  const model = modelSelect.value;
  if (!model) {
    alert('Select a model first.');
    return;
  }

  input.value = '';
  input.style.height = 'auto';
  addMessage('user', prompt);
  history.push({ role: 'user', content: prompt });

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
      body: JSON.stringify({ model, messages: history }),
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

    const metrics = parseMetrics(rawText);
    if (metrics) {
      addStatsPanel(assistantBubble, metrics);
      scrollToBottom();
    }
  } catch (err) {
    assistantText += `\n[error: ${err.message}]`;
    assistantBody.textContent = assistantText;
  } finally {
    cursor.remove();
    history.push({ role: 'assistant', content: assistantText });
    setBusy(false);
    input.focus();
  }
});

loadModels();

// Removes the trailing metrics payload (if any) from the raw stream text.
function stripMetrics(raw) {
  const idx = raw.indexOf(METRICS_MARKER);
  return idx === -1 ? raw : raw.slice(0, idx);
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

function formatDuration(msValue) {
  if (msValue == null || Number.isNaN(msValue)) return '—';
  if (msValue >= 1000) return (msValue / 1000).toFixed(2) + ' s';
  return Math.round(msValue) + ' ms';
}

// Appends a collapsed-by-default <details> panel with response statistics.
function addStatsPanel(bubble, m) {
  const rows = [];
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
