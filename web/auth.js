// 登录页逻辑。
//
// ⚠️ 这个文件和主页面的 12 个 script 不共存：login.html 只加载 style.css + auth.js。
// 所以 el() / esc() / app.* 这些一律不可用，全部自己写 document.getElementById。
//
// ⚠️ 全站只有一个顶层符号：jrpAuth。web/*.js 是老式全局挂载，顶层 function 会
// 覆盖先加载文件里的同名 var（2026-09-21 出过整站白屏事故），别再新开顶层名字。
var jrpAuth = {
  ticket: '',
  timer: 0,
  tries: 0,

  boot: function () {
    var retry = document.getElementById('retry');
    if (retry) {
      retry.onclick = function (e) { e.preventDefault(); jrpAuth.start(); };
    }
    // 微信内置浏览器：扫不了码，直接引导去小程序。
    if (/micromessenger/i.test(navigator.userAgent || '')) {
      jrpAuth.isWeChat = true;
    }
    jrpAuth.start();
  },

  setStatus: function (text, cls) {
    var s = document.getElementById('status');
    if (!s) return;
    s.textContent = text;
    s.className = 'status' + (cls ? ' ' + cls : '');
  },

  start: function () {
    if (jrpAuth.timer) { clearInterval(jrpAuth.timer); jrpAuth.timer = 0; }
    jrpAuth.tries = 0;
    jrpAuth.setStatus('正在生成二维码…', '');
    fetch('/api/auth/ticket', { method: 'POST', credentials: 'same-origin' })
      .then(function (r) { return r.json().then(function (j) { return { ok: r.ok, j: j }; }); })
      .then(function (res) {
        if (!res.ok || !res.j || !res.j.success) {
          var msg = (res.j && res.j.error) || '生成二维码失败';
          jrpAuth.setStatus(msg, 'err');
          return;
        }
        jrpAuth.ticket = res.j.ticket;
        var box = document.getElementById('qr');
        box.innerHTML = '';
        var img = document.createElement('img');
        img.alt = '登录小程序码';
        img.src = '/api/auth/qr?ticket=' + encodeURIComponent(jrpAuth.ticket);
        img.onerror = function () {
          box.innerHTML = '<span>二维码加载失败，点上方「点这里换一张」重试</span>';
        };
        box.appendChild(img);
        jrpAuth.setStatus(jrpAuth.isWeChat ? '在微信里请长按二维码识别，或打开 jrp 小程序' : '请用微信扫一扫', '');
        jrpAuth.timer = setInterval(jrpAuth.poll, 1500);
      })
      .catch(function (e) {
        jrpAuth.setStatus('连不上服务器：' + e.message, 'err');
      });
  },

  poll: function () {
    jrpAuth.tries++;
    // 5 分钟 = 200 次（1.5 秒一次），留点余量。
    if (jrpAuth.tries > 210) {
      clearInterval(jrpAuth.timer);
      jrpAuth.timer = 0;
      jrpAuth.setStatus('二维码已过期，点「点这里换一张」', 'err');
      return;
    }
    fetch('/api/auth/status?ticket=' + encodeURIComponent(jrpAuth.ticket), { credentials: 'same-origin' })
      .then(function (r) { return r.json(); })
      .then(function (j) {
        if (!j || !j.success) return;
        if (j.status === 'confirmed') {
          clearInterval(jrpAuth.timer);
          jrpAuth.timer = 0;
          jrpAuth.setStatus('登录成功，正在进入…', 'ok');
          // 登录后去哪：管理后台会把 jrpAuth.next 设成自己（默认回首页）。
          var next = jrpAuth.next || '/index.html';
          var m = /[?&]next=([^&]+)/.exec(location.search);
          if (m) { try { next = decodeURIComponent(m[1]); } catch (e) { /* 保持默认 */ } }
          location.href = next;
        } else if (j.status === 'expired') {
          clearInterval(jrpAuth.timer);
          jrpAuth.timer = 0;
          jrpAuth.setStatus('二维码已过期，点「点这里换一张」', 'err');
        }
      })
      .catch(function () { /* 网络抖一下而已，下一轮再来 */ });
  }
};
