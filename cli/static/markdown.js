// markdown.js - minimal, dependency-free Markdown renderer for chat replies.
//
// Security model: every piece of untrusted text passes through escapeHtml()
// before any markup is generated, generated tags come from a fixed whitelist,
// and link/image URLs are scheme-checked (http:, https:, mailto:, / and #).
// The result is a DOM element built with createElement/textContent wherever
// practical, so injected HTML can never execute.

(function () {
  'use strict';

  function escapeHtml(s) {
    return String(s)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;');
  }

  // Only http(s), mailto, protocol-relative and in-page targets are allowed.
  function safeUrl(url) {
    const trimmed = String(url).trim();
    if (/^(https?:|mailto:|\/|#)/i.test(trimmed)) return trimmed;
    return '';
  }

  // Builds an anchor from ALREADY-escaped label/url fragments (captures come
  // out of the escaped working string, so they must not be escaped twice).
  function anchorHtml(labelEscaped, urlEscaped) {
    const clean = safeUrl(urlEscaped.replace(/&amp;/g, '&'));
    if (!clean) return labelEscaped + ' (' + urlEscaped + ')';
    return '<a class="md-link" href="' + escapeHtml(clean) +
      '" target="_blank" rel="noopener noreferrer">' + labelEscaped + '</a>';
  }

  // Inline-level rendering: code spans, links, autolinks, bold, italics,
  // strikethrough. Finished fragments are stashed behind placeholder tokens
  // so later replacements cannot corrupt their contents.
  function renderInline(raw) {
    const parts = String(raw).split(/(`[^`\n]+`)/);
    const out = [];
    for (const part of parts) {
      if (part.length > 2 && part.startsWith('`') && part.endsWith('`')) {
        out.push('<code class="md-code-inline">' + escapeHtml(part.slice(1, -1)) + '</code>');
        continue;
      }
      out.push(renderInlineNoCode(part));
    }
    return out.join('');
  }

  function renderInlineNoCode(raw) {
    const stash = [];
    const keep = (html) => {
      stash.push(html);
      return '\u0000' + (stash.length - 1) + '\u0000';
    };

    let s = escapeHtml(raw);

    // Links [label](url) and bare autolinks become stashed anchors.
    s = s.replace(/(!?)\[([^\]\n]*)\]\(([^)\s]+)(?:\s+&quot;[^&]*&quot;)?\)/g, (m, bang, label, url) => {
      if (bang) return keep(anchorHtml(label || url, url)); // images degrade to links
      return keep(anchorHtml(label, url));
    });
    s = s.replace(/\bhttps?:\/\/[^\s<>&quot;'）)]+[^\s<>&quot;'（）.,;:!?"')]/g, (m) =>
      keep(anchorHtml(m, m)));

    // Emphasis on the remaining plain text.
    s = s.replace(/\*\*\*([^*\n]+)\*\*\*/g, '<strong><em>$1</em></strong>');
    s = s.replace(/\*\*([^*\n]+)\*\*/g, '<strong>$1</strong>');
    s = s.replace(/(^|[^\w*])\*([^*\n]+)\*(?![*\w])/g, '$1<em>$2</em>');
    s = s.replace(/(^|[^\w_])_([^_\n]+)_(?![\w_])/g, '$1<em>$2</em>');
    s = s.replace(/~~([^~\n]+)~~/g, '<del>$1</del>');

    // Restore stashed fragments.
    s = s.replace(/\u0000(\d+)\u0000/g, (m, idx) => stash[Number(idx)]);
    return s;
  }

  // ---------- code blocks ----------

  function buildCodeBlock(lang, codeText) {
    const box = document.createElement('div');
    box.className = 'md-code';

    const bar = document.createElement('div');
    bar.className = 'md-code-bar';
    const chip = document.createElement('span');
    chip.className = 'md-lang';
    chip.textContent = lang || '';
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'md-copy-btn';
    btn.textContent = 'Copy';
    const pre = document.createElement('pre');
    const code = document.createElement('code');
    code.textContent = codeText;
    pre.appendChild(code);
    btn.addEventListener('click', () => {
      const finish = () => {
        btn.textContent = 'Copied';
        setTimeout(() => { btn.textContent = 'Copy'; }, 1200);
      };
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(code.textContent).then(finish, finish);
      } else {
        finish();
      }
    });
    bar.append(chip, btn);

    box.append(bar, pre);
    return box;
  }

  // ---------- tables ----------

  function splitTableRow(line) {
    return line.trim()
      .replace(/\\\|/g, '\u0001')
      .replace(/^\|/, '')
      .replace(/\|$/, '')
      .split('|')
      .map((cell) => cell.replace(/\u0001/g, '|').trim());
  }

  function isTableSeparator(line) {
    if (!line.includes('-') || !line.includes('|')) return false;
    return /^\s*\|?\s*:?-{2,}:?\s*(\|\s*:?-{1,}:?\s*)*\|?\s*$/.test(line);
  }

  function buildTable(headerCells, aligns, bodyRows) {
    const table = document.createElement('table');
    table.className = 'md-table';
    const thead = document.createElement('thead');
    const headRow = document.createElement('tr');
    headerCells.forEach((cell, idx) => {
      const th = document.createElement('th');
      if (aligns[idx]) th.style.textAlign = aligns[idx];
      th.innerHTML = renderInline(cell);
      headRow.appendChild(th);
    });
    thead.appendChild(headRow);
    table.appendChild(thead);

    const tbody = document.createElement('tbody');
    for (const row of bodyRows) {
      const tr = document.createElement('tr');
      headerCells.forEach((_, idx) => {
        const td = document.createElement('td');
        if (aligns[idx]) td.style.textAlign = aligns[idx];
        td.innerHTML = renderInline(row[idx] || '');
        tr.appendChild(td);
      });
      tbody.appendChild(tr);
    }
    table.appendChild(tbody);
    return table;
  }

  // ---------- block parser ----------

  const FENCE_RE = /^```(.*)$/;
  const HEADING_RE = /^(#{1,6})\s+(.+?)\s*#*\s*$/;
  const HR_RE = /^ {0,3}((?:\*[ \t]*){3,}|(?:-[ \t]*){3,}|(?:_[ \t]*){3,})$/;
  const QUOTE_RE = /^ {0,3}> ?/;
  const LIST_ITEM_RE = /^(\s*)([-*+]|\d{1,9}[.)])\s+(.*)$/;
  const INDENT_OF = (line) => {
    const m = line.match(/^ */);
    return m ? m[0].length : 0;
  };

  function renderMarkdown(text) {
    const root = document.createElement('div');
    root.className = 'md';
    const lines = String(text ?? '').replace(/\r\n?/g, '\n').split('\n');

    let i = 0;
    while (i < lines.length) {
      const line = lines[i];

      // blank
      if (/^\s*$/.test(line)) { i++; continue; }

      // fenced code block (unterminated fences run to the end of input)
      let m = line.match(FENCE_RE);
      if (m) {
        const lang = m[1].trim().split(/\s+/)[0];
        const codeLines = [];
        i++;
        while (i < lines.length && !/^```\s*$/.test(lines[i])) {
          codeLines.push(lines[i]);
          i++;
        }
        if (i < lines.length) i++; // consume closing fence
        root.appendChild(buildCodeBlock(lang, codeLines.join('\n')));
        continue;
      }

      // heading
      m = line.match(HEADING_RE);
      if (m) {
        const h = document.createElement('h' + m[1].length);
        h.innerHTML = renderInline(m[2]);
        root.appendChild(h);
        i++;
        continue;
      }

      // horizontal rule
      if (HR_RE.test(line)) {
        root.appendChild(document.createElement('hr'));
        i++;
        continue;
      }

      // blockquote (content rendered recursively)
      if (QUOTE_RE.test(line)) {
        const inner = [];
        while (i < lines.length && QUOTE_RE.test(lines[i])) {
          inner.push(lines[i].replace(QUOTE_RE, ''));
          i++;
        }
        const quote = document.createElement('blockquote');
        quote.className = 'md-quote';
        quote.appendChild(renderMarkdown(inner.join('\n')));
        root.appendChild(quote);
        continue;
      }

      // table: current row followed by a separator row
      if (line.includes('|') && i + 1 < lines.length &&
          isTableSeparator(lines[i + 1]) && !LIST_ITEM_RE.test(line)) {
        const header = splitTableRow(line);
        const aligns = splitTableRow(lines[i + 1]).map((c) => {
          const left = c.startsWith(':');
          const right = c.endsWith(':');
          if (left && right) return 'center';
          if (right) return 'right';
          return left ? 'left' : '';
        });
        i += 2;
        const rows = [];
        while (i < lines.length && lines[i].includes('|') && !/^\s*$/.test(lines[i]) &&
               !FENCE_RE.test(lines[i]) && !HEADING_RE.test(lines[i])) {
          rows.push(splitTableRow(lines[i]));
          i++;
        }
        root.appendChild(buildTable(header, aligns, rows));
        continue;
      }

      // list (nesting via indentation, [ ]/[x] task items)
      m = line.match(LIST_ITEM_RE);
      if (m) {
        i = parseListBlock(lines, i, root);
        continue;
      }

      // paragraph: gather until a blank line or the start of another block
      const paraLines = [line];
      i++;
      while (i < lines.length && !/^\s*$/.test(lines[i]) &&
             !FENCE_RE.test(lines[i]) && !HEADING_RE.test(lines[i]) &&
             !HR_RE.test(lines[i]) && !QUOTE_RE.test(lines[i]) &&
             !LIST_ITEM_RE.test(lines[i])) {
        paraLines.push(lines[i]);
        i++;
      }
      const p = document.createElement('p');
      p.innerHTML = renderInline(paraLines.join('\n')).replace(/\n/g, '<br>');
      root.appendChild(p);
    }

    return root;
  }

  function parseListBlock(lines, start, root) {
    let i = start;
    const firstIndent = INDENT_OF(lines[start]);
    const items = [];
    while (i < lines.length) {
      const l = lines[i];
      if (/^\s*$/.test(l)) {
        // A blank line only continues the list when another item follows.
        if (i + 1 < lines.length && LIST_ITEM_RE.test(lines[i + 1]) &&
            INDENT_OF(lines[i + 1]) >= firstIndent) {
          i++;
          continue;
        }
        break;
      }
      const im = l.match(LIST_ITEM_RE);
      if (im) {
        items.push({ indent: im[1].length, marker: im[2], text: im[3] });
        i++;
        continue;
      }
      // Deeper-indented plain text continues the previous item.
      if (items.length && INDENT_OF(l) > firstIndent + 1) {
        items[items.length - 1].text += '\n' + l.trim();
        i++;
        continue;
      }
      break;
    }

    const makeListEl = (ordered) => {
      const el = document.createElement(ordered ? 'ol' : 'ul');
      el.className = 'md-list';
      return el;
    };
    const levels = [{ el: makeListEl(/^\d/.test(items[0].marker)), lastLi: null }];
    root.appendChild(levels[0].el);

    for (const item of items) {
      const level = Math.min(3, Math.max(0,
        Math.floor((item.indent - firstIndent) / 2)));
      while (levels.length - 1 > level) levels.pop();
      // Fill intermediate nesting levels when indentation jumps more than
      // one step (e.g. straight to a 4-space-deep bullet).
      while (levels.length - 1 < level) {
        const parent = levels[levels.length - 1];
        const nested = makeListEl(/^\d/.test(item.marker));
        parent.lastLi.appendChild(nested);
        levels.push({ el: nested, lastLi: null });
      }
      const li = document.createElement('li');
      const task = item.text.match(/^\[([ xX])\]\s+(.*)$/);
      const html = renderInline(task ? task[2] : item.text).replace(/\n/g, '<br>');
      if (task) {
        const box = document.createElement('input');
        box.type = 'checkbox';
        box.disabled = true;
        box.checked = task[1].toLowerCase() === 'x';
        li.appendChild(box);
        li.className = 'md-task';
      }
      li.insertAdjacentHTML('beforeend', html);
      levels[levels.length - 1].lastLi = li;
      levels[levels.length - 1].el.appendChild(li);
    }
    return i;
  }

  window.renderMarkdown = renderMarkdown;
})();
