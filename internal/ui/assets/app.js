// Minimal SPA: render 5 steps, drive each via /api/step/*.
const STEPS = [
  { id: 'modelserver_login',  label: '登录 modelserver',  type: 'oauth' },
  { id: 'agentserver_login',  label: '登录 agentserver',  type: 'oauth' },
  { id: 'vscode_install',     label: '安装 VS Code',       type: 'progress' },
  { id: 'vscode_configure',   label: '配置 VS Code 与 codex', type: 'action' },
  { id: 'finalize',           label: '完成配置(快捷方式 + 右键菜单)', type: 'action' },
];

async function fetchJSON(url, opts) {
  const r = await fetch(url, opts);
  if (!r.ok) throw new Error(await r.text());
  return r.json();
}

let state = null;
async function refreshState() {
  state = await fetchJSON('/api/state');
  render();
}

function render() {
  const root = document.getElementById('app');
  root.innerHTML = '';
  const h = document.createElement('h1');
  h.textContent = 'agentserver-vscode 配置向导';
  root.appendChild(h);

  for (const s of STEPS) {
    const div = document.createElement('div');
    div.className = 'step';
    const done = state && state.completed_steps && state.completed_steps.includes(s.id);
    if (done) div.classList.add('done');
    div.innerHTML = `<b>${s.label}</b> ${done ? '✓' : ''}`;
    if (!done) {
      const btn = document.createElement('button');
      btn.textContent = '开始';
      btn.onclick = () => runStep(s);
      div.appendChild(document.createElement('br'));
      div.appendChild(btn);
    }
    root.appendChild(div);
  }
}

async function runStep(s) {
  try {
    if (s.id === 'finalize') {
      await fetchJSON('/api/finalize', { method: 'POST' });
    } else if (s.id === 'vscode_configure') {
      await fetchJSON('/api/step/vscode_configure', { method: 'POST' });
    } else if (s.id === 'vscode_install') {
      const r = await fetchJSON('/api/step/vscode_install', { method: 'POST' });
      await streamProgress(r.stream_id);
    } else if (s.id === 'modelserver_login' || s.id === 'agentserver_login') {
      const ch = await fetchJSON('/api/step/' + s.id, { method: 'POST' });
      alert('请在弹出的浏览器中完成登录。\n用户码: ' + ch.user_code);
      // Poll until success
      while (true) {
        const st = await fetchJSON('/api/step/' + s.id + '/status');
        if (st.state === 'success') break;
        await new Promise(r => setTimeout(r, 3000));
      }
    }
    await refreshState();
  } catch (e) {
    alert('失败: ' + e.message);
  }
}

function streamProgress(id) {
  return new Promise((resolve) => {
    const es = new EventSource('/api/events?stream=' + id);
    es.onmessage = e => {
      const ev = JSON.parse(e.data);
      console.log('progress', ev);
    };
    es.onerror = () => { es.close(); resolve(); };
  });
}

refreshState();
