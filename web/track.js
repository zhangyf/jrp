// 用户行为埋点 SDK（web 端，2026-11-11）。
//
// 老师要三类数据：页面停留时间、页面浏览顺序、功能用得多少。全部由前端
// 上报到 /api/event（优先 sendBeacon，页面卸载也不丢），服务端按 uid 归组、
// 落 COS，报表页（admin）调 /api/analytics 聚合展示。个人学习数据不出域。
//
// 事件三类（与后端 api_analytics.go 对齐）：
//   view_enter / view_leave —— 板块切换 + 停留时长（leave 的 dur 是毫秒）
//   action —— 核心动作（判分 / 回写 / 播放 / 搜索…）
//
// ⚠️ 全局只挂一个符号 __jrpTrack：web/*.js 是老式多 script 全局挂载，
// 顶层 function 会覆盖先加载文件的同名 var（2026-09-21 出过整站白屏事故）。
// 本文件用 IIFE 包起来，所有内部逻辑不外泄。

var __jrpTrack = (function () {
  var ENDPOINT = '/api/event';
  var MAX_BATCH = 20;             // 与后端 analyticsEventMaxBatch 对齐
  var FLUSH_MS = 3000;            // 攒 3 秒或满 20 条就刷一次
  var MAX_DUR = 24 * 60 * 60 * 1000;

  var queue = [];
  var timer = null;
  var curPage = null;             // 当前停留的板块
  var curEnterAt = 0;             // 进入当前板块的时间戳

  // 会话 id：同一个 tab（含刷新）保持同一个值，关 tab 重开才换。
  // 后端按 (uid, sid) 归组 view_enter 才能排出「这一次访问」的浏览顺序。
  var sid = '';
  try { sid = sessionStorage.getItem('jrp_track_sid') || ''; } catch (e) { sid = ''; }
  if (!sid) {
    sid = 's' + Date.now().toString(36) + Math.random().toString(36).slice(2, 8);
    try { sessionStorage.setItem('jrp_track_sid', sid); } catch (e) {}
  }

  function now() { return Date.now(); }

  function enqueue(ev) {
    queue.push(ev);
    if (queue.length >= MAX_BATCH) { flush(); return; }
    if (!timer) timer = setTimeout(flush, FLUSH_MS);
  }

  function flush() {
    if (!queue.length) return;
    var batch = queue.splice(0, MAX_BATCH);
    if (timer) { clearTimeout(timer); timer = null; }
    var body = JSON.stringify({ events: batch });
    // sendBeacon：异步、不阻塞、页面卸载也能发。Blob 指定 application/json。
    if (navigator.sendBeacon) {
      try {
        if (navigator.sendBeacon(ENDPOINT, new Blob([body], { type: 'application/json' }))) return;
      } catch (e) { /* 回退 fetch */ }
    }
    // 回退：fetch keepalive。失败就丢 —— 埋点尽力而为，别为它重试打扰用户。
    if (window.fetch) {
      try {
        window.fetch(ENDPOINT, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: body,
          keepalive: true,
          credentials: 'same-origin'
        }).catch(function () {});
      } catch (e) {}
    }
  }

  // 进入板块：先结算上一个板块的停留，再记新板块进入。
  function enter(page) {
    if (!page) return;
    if (curPage === page) return;   // 重进同一板块不算（切视图会 go 回当前页）
    leave();
    curPage = page;
    curEnterAt = now();
    enqueue({ v: 1, sid: sid, e: 'view_enter', page: page, t: now() });
  }

  // 离开当前板块：记停留时长。
  function leave() {
    if (!curPage) return;
    var dur = Math.min(Math.max(now() - curEnterAt, 0), MAX_DUR);
    enqueue({ v: 1, sid: sid, e: 'view_leave', page: curPage, dur: dur, t: now() });
    curPage = null;
    curEnterAt = 0;
  }

  // 核心动作。act 是动作名；page 不传就用当前板块。
  function action(act, page) {
    if (!act) return;
    enqueue({ v: 1, sid: sid, e: 'action', act: act, page: page || curPage || '', t: now() });
  }

  // 关页面 / 刷新：把最后一次停留也交上去。
  window.addEventListener('pagehide', function () {
    leave();
    flush();
  });

  return { enter: enter, leave: leave, action: action };
})();
