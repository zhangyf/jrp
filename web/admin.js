// 管理后台页面逻辑。
//
// ⚠️ 这个文件只跟 style.css + auth.js + admin.js 共存，主页面的 12 个 script
// 不加载到这里，所以 el() / esc() / app.* 一律不可用，全部自己写 getElementById。
//
// ⚠️ 全站只有一个顶层符号：jrpAdmin。web/*.js 是老式全局挂载，顶层 function 会
// 覆盖先加载文件里的同名 var（2026-09-21 出过整站白屏事故），别再新开顶层名字。
var jrpAdmin = {
  roleText: {},

  boot: function () {
    jrpAdmin.check();
  },

  // --auth-token 那条路（CLI / 脚本）也算管理员，有就带上。
  token: function () { return localStorage.getItem('jrp_token') || ''; },

  headers: function () {
    var h = { 'Content-Type': 'application/json' };
    if (jrpAdmin.token()) h['Authorization'] = 'Bearer ' + jrpAdmin.token();
    return h;
  },

  // 先问「我是谁」：没登录就摆扫码框，不是 owner 就挡回去。
  check: function () {
    fetch('/api/auth/me', { credentials: 'same-origin' })
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (j) {
        if (j && j.success) {
          if (!j.is_owner) {
            jrpAdmin.showDenied(j.name || j.uid || '这个账号');
            return;
          }
          jrpAdmin.showPanel();
          jrpAdmin.load();
          return;
        }
        // 没有会话：服务端配了 --auth-token 的话，填 token 也能进。
        if (jrpAdmin.token()) {
          jrpAdmin.showPanel();
          jrpAdmin.load();
          return;
        }
        jrpAdmin.showTokenGate();
        jrpAdmin.showLogin();
      })
      .catch(function (e) {
        jrpAdmin.setMsg('连不上服务器：' + e.message, true);
      });
  },

  showTokenGate: function () {
    var g = document.getElementById('tokenGate');
    if (g) g.style.display = 'block';
    var go = document.getElementById('adminTokenGo');
    if (!go) return;
    go.onclick = function () {
      localStorage.setItem('jrp_token', (document.getElementById('adminToken').value || '').trim());
      jrpAdmin.showPanel();
      jrpAdmin.load();
    };
  },

  showLogin: function () {
    document.getElementById('gate').style.display = 'block';
    document.getElementById('panel').style.display = 'none';
    jrpAdmin.setMsg('管理后台要用站点所有者的账号登录。用微信扫下面的码。');
    jrpAuth.next = '/admin.html'; // 扫完回管理后台，别跳首页
    jrpAuth.boot();
  },

  showDenied: function (who) {
    document.getElementById('gate').style.display = 'none';
    document.getElementById('panel').style.display = 'none';
    var d = document.getElementById('denied');
    d.style.display = 'block';
    d.querySelector('.who').textContent = who;
  },

  showPanel: function () {
    document.getElementById('gate').style.display = 'none';
    document.getElementById('panel').style.display = 'block';
  },

  setMsg: function (text, isErr) {
    var m = document.getElementById('msg');
    m.textContent = text || '';
    m.className = 'msg' + (isErr ? ' err' : '');
  },

  load: function () {
    fetch('/api/admin/users', { credentials: 'same-origin', headers: jrpAdmin.headers() })
      .then(function (r) { return r.json().then(function (j) { return { ok: r.ok, j: j }; }); })
      .then(function (res) {
        if (!res.ok || !res.j || !res.j.success) {
          jrpAdmin.setMsg((res.j && res.j.error) || '读用户列表失败', true);
          jrpAdmin.showTokenGate();
          return;
        }
        jrpAdmin.roleText = res.j.roles || {};
        jrpAdmin.render(res.j.users || []);
      })
      .catch(function (e) { jrpAdmin.setMsg('连不上服务器：' + e.message, true); });
  },

  render: function (users) {
    var box = document.getElementById('rows');
    box.innerHTML = '';
    if (!users.length) {
      var empty = document.createElement('div');
      empty.className = 'empty';
      empty.textContent = '还没有人扫码登录过。';
      box.appendChild(empty);
      return;
    }
    users.forEach(function (u) { box.appendChild(jrpAdmin.row(u)); });
  },

  row: function (u) {
    var tr = document.createElement('tr');
    if (u.role === 'pending') tr.className = 'pending';
    if (u.role === 'disabled') tr.className = 'disabled';

    tr.appendChild(jrpAdmin.cell(u.name || '（未命名）' + (u.is_owner ? ' · 你' : '')));
    tr.appendChild(jrpAdmin.cell(u.openid_tail || '—'));

    var roleCell = document.createElement('td');
    var badge = document.createElement('span');
    badge.className = 'badge r-' + u.role;
    badge.textContent = jrpAdmin.roleText[u.role] || u.role;
    roleCell.appendChild(badge);
    tr.appendChild(roleCell);

    tr.appendChild(jrpAdmin.cell(u.created || '—'));
    tr.appendChild(jrpAdmin.cell(u.last_login || '—'));

    var act = document.createElement('td');
    act.className = 'act';
    if (u.is_owner) {
      act.appendChild(jrpAdmin.span('—'));
      tr.appendChild(act);
      return tr;
    }
    if (u.role === 'pending') {
      act.appendChild(jrpAdmin.btn('批准', 'ok', function () { jrpAdmin.act(u.uid, 'approve'); }));
    } else if (u.role === 'disabled') {
      act.appendChild(jrpAdmin.btn('解冻', 'ok', function () { jrpAdmin.act(u.uid, 'approve'); }));
    } else {
      act.appendChild(jrpAdmin.btn('冻结', 'warn', function () { jrpAdmin.act(u.uid, 'disable'); }));
      act.appendChild(jrpAdmin.btn('改名', '', function () { jrpAdmin.rename(u); }));
    }
    act.appendChild(jrpAdmin.btn('删除', 'bad', function () { jrpAdmin.del(u); }));
    tr.appendChild(act);
    return tr;
  },

  cell: function (text) {
    var td = document.createElement('td');
    td.textContent = text;
    return td;
  },

  span: function (text) {
    var s = document.createElement('span');
    s.textContent = text;
    return s;
  },

  btn: function (label, cls, onclick) {
    var b = document.createElement('button');
    b.className = 'btn' + (cls ? ' ' + cls : '');
    b.textContent = label;
    b.onclick = onclick;
    return b;
  },

  rename: function (u) {
    var name = window.prompt('给这个账号起个名字（方便你认人）：', u.name || '');
    if (name === null) return;
    name = name.trim();
    if (!name) return;
    jrpAdmin.act(u.uid, 'rename', name);
  },

  del: function (u) {
    var who = u.name || u.openid_tail || u.uid;
    var ok = window.confirm(
      '删除账号「' + who + '」？\n\n' +
      '· 只删账号，他存下来的学习记录保留\n' +
      '· 他再用微信扫会重新变成「待批准」，要你再批一次\n' +
      '· 想临时停用就点「冻结」，别删'
    );
    if (!ok) return;
    jrpAdmin.act(u.uid, 'delete');
  },

  act: function (uid, action, name) {
    jrpAdmin.setMsg('处理中…');
    var body = { uid: uid, action: action };
    if (name) body.name = name;
    fetch('/api/admin/user', {
      method: 'POST',
      credentials: 'same-origin',
      headers: jrpAdmin.headers(),
      body: JSON.stringify(body)
    })
      .then(function (r) { return r.json().then(function (j) { return { ok: r.ok, j: j }; }); })
      .then(function (res) {
        if (!res.ok || !res.j || !res.j.success) {
          jrpAdmin.setMsg((res.j && res.j.error) || '操作失败', true);
          return;
        }
        jrpAdmin.setMsg(res.j.note || '好了');
        jrpAdmin.load();
      })
      .catch(function (e) { jrpAdmin.setMsg('连不上服务器：' + e.message, true); });
  }
};
