const messagesEl = document.getElementById('messages');
const emptyState = document.getElementById('empty-state');
const form = document.getElementById('chat-form');
const input = document.getElementById('prompt-input');
const sendBtn = document.getElementById('send-btn');
const modelSelect = document.getElementById('model-select');
const newChatBtn = document.getElementById('new-chat');

let history = [];
let busy = false;

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
  bubble.textContent = content;

  row.appendChild(avatar);
  row.appendChild(bubble);
  messagesEl.appendChild(row);
  scrollToBottom();
  return bubble;
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

  const assistantBubble = addMessage('assistant', '');
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
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      assistantText += decoder.decode(value, { stream: true });
      assistantBubble.textContent = assistantText;
      assistantBubble.appendChild(cursor);
      scrollToBottom();
    }
  } catch (err) {
    assistantText += `\n[error: ${err.message}]`;
    assistantBubble.textContent = assistantText;
  } finally {
    cursor.remove();
    history.push({ role: 'assistant', content: assistantText });
    setBusy(false);
    input.focus();
  }
});

loadModels();
