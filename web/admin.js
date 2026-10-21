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
    var out = document.getElementById('logoutLink');
    if (out) {
      out.onclick = function (e) {
        e.preventDefault();
        fetch('/api/auth/logout', { credentials: 'same-origin' })
          .then(function () { localStorage.removeItem('jrp_token'); location.reload(); })
          .catch(function () { location.reload(); });
      };
    }
    jrpAdmin.check();
  },

  // 只有一条路 —— owner 的扫码会话。不是 owner 的一律按「没登录」处理，
  // 直接回扫码页，不单独摆一个「你不是所有者」的页面（那页没意义，又不给下一步）。
  check: function () {
    fetch('/api/auth/me', { credentials: 'same-origin' })
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (j) {
        if (j && j.success && j.is_owner) {
          jrpAdmin.showPanel();
          jrpAdmin.load();
          return;
        }
        jrpAdmin.showLogin(j && j.success
          ? '这个微信还没通过审核，或者不是站点所有者，进不了管理后台。请换站点所有者的微信扫码。'
          : '管理后台要用站点所有者的微信扫码登录。');
      })
      .catch(function (e) {
        jrpAdmin.setMsg('连不上服务器：' + e.message, true);
      });
  },

  showLogin: function (note) {
    document.getElementById('gate').style.display = 'block';
    document.getElementById('panel').style.display = 'none';
    var n = document.getElementById('gateNote');
    if (n) n.textContent = note || '';
    jrpAuth.next = '/admin.html'; // 扫完回管理后台，别跳首页
    jrpAuth.boot();
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
    fetch('/api/admin/users', { credentials: 'same-origin' })
      .then(function (r) { return r.json().then(function (j) { return { ok: r.ok, j: j }; }); })
      .then(function (res) {
        if (!res.ok || !res.j || !res.j.success) {
          jrpAdmin.setMsg((res.j && res.j.error) || '读用户列表失败', true);
          return;
        }
        jrpAdmin.roleText = res.j.roles || {};
        jrpAdmin.render(res.j.users || []);
      })
      .catch(function (e) { jrpAdmin.setMsg('连不上服务器：' + e.message, true); });
  },

  render: function (users) {
    jrpAdmin.summary(users);
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

  // 一眼扫完：共几人、几个待批准、几个已冻结。没人待审时把「下一步」说清楚，
  // 别让人对着一张只有自己的表发愣。
  summary: function (users) {
    var el = document.getElementById('summary');
    if (!el) return;
    var n = { owner: 0, member: 0, pending: 0, disabled: 0 };
    users.forEach(function (u) { if (n[u.role] !== undefined) n[u.role]++; });

    var parts = ['共 <b>' + users.length + '</b> 个账号'];
    if (n.pending) parts.push('<span class="n-pending">待批准 <b>' + n.pending + '</b></span>');
    if (n.member) parts.push('成员 <b>' + n.member + '</b>');
    if (n.disabled) parts.push('<span class="n-disabled">已冻结 <b>' + n.disabled + '</b></span>');

    var tail = '';
    if (!n.pending) {
      tail = users.length <= 1
        ? '　还没有待批准的人 —— 别人用微信扫首页的码，就会出现在表的最上面等你批。'
        : '　没有待批准的人。';
    } else {
      tail = '　待批准的排在第一行，点「批准」他就能用了。';
    }
    el.innerHTML = parts.join('　·　') + '<span class="tip">' + tail + '</span>';
  },

  row: function (u) {
    var tr = document.createElement('tr');
    if (u.role === 'pending') tr.className = 'pending';
    if (u.role === 'disabled') tr.className = 'disabled';

    var who = u.name || (u.is_owner ? '站点所有者' : '（未命名）');
    if (u.is_owner) who += ' · 你';
    tr.appendChild(jrpAdmin.cell(who));
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
      headers: { 'Content-Type': 'application/json' },
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
