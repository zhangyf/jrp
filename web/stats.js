// 统计页。数据来自 /api/stats（和 CLI 的 jrp stats 同一套 ComputeStats）。
// 不引任何图表库：纯表格 + CSS 条，离线也能看。

var stats = {
  load: function () {
    var days = el('statsDays').value || '30';
    el('statsBody').innerHTML = '<span class="chip muted">加载中…</span>';

    app.api('/api/stats?days=' + encodeURIComponent(days)).then(function (d) {
      el('statsBody').innerHTML =
        trend(d.snapshots || []) +
        changes(d.changes || {}) +
        detail(d.detail || {});
    }).catch(function (e) {
      el('statsBody').innerHTML = '<span class="chip warn">' + esc(e.message) + '</span>';
    });
  }
};

// 总词量走势：纯 CSS 条，最高的一天占满
function trend(snaps) {
  if (!snaps.length) return '';
  var max = 0;
  snaps.forEach(function (s) { if (s.total > max) max = s.total; });

  var rows = snaps.map(function (s) {
    var pct = max ? Math.round(s.total / max * 100) : 0;
    return '<tr>' +
      '<td>' + esc(s.date) + '</td>' +
      '<td>' + esc(s.version || '') + '</td>' +
      '<td><div style="display:flex;align-items:center;gap:6px">' +
      '<div style="height:10px;width:' + pct + '%;min-width:2px;background:var(--accent);border-radius:3px"></div>' +
      '<b>' + s.total + '</b></div></td>' +
      '<td>' + (s.mastered || 0) + '</td>' +
      '<td>' + (s.basic || 0) + '</td>' +
      '<td>' + (s.needs_consol || 0) + '</td>' +
      '<td>' + (s.untested || 0) + '</td>' +
      '<td>' + (s.errors || 0) + '</td>' +
      '</tr>';
  }).join('');

  var first = snaps[0], last = snaps[snaps.length - 1];
  var delta = last.total - first.total;

  return '<div class="stat-block"><h4>词库总量（' + snaps.length + ' 天，' +
    (delta >= 0 ? '新增 ' + delta : '减少 ' + (-delta)) + '）</h4>' +
    '<table><thead><tr><th>日期</th><th>版本</th><th>总量</th>' +
    '<th>已掌握</th><th>基本掌握</th><th>待巩固</th><th>未测试</th><th>错误数</th>' +
    '</tr></thead><tbody>' + rows + '</tbody></table></div>';
}

// 后端 /api/stats 的 changes 用的是 Go 字段名（total_change 等），直接显示看不懂，
// 这里做一层中文映射。顺序也固定住，别跟着 map 的字母序乱排。
var CHANGE_LABELS = {
  total_change: '总词量',
  mastered_change: '已掌握',
  basic_change: '基本掌握',
  needs_consol_change: '待巩固',
  errors_change: '错误数',
  period: '统计区间'
};
var CHANGE_ORDER = ['total_change', 'mastered_change', 'basic_change', 'needs_consol_change', 'errors_change'];

function changes(c) {
  var keys = Object.keys(c);
  if (!keys.length) return '';

  // period 是区间说明，不是指标，单独放一行
  var period = c.period
    ? '<p class="muted" style="margin:0 0 6px;font-size:12px">统计区间：' + esc(c.period) + '</p>'
    : '';

  var shown = {};
  var chips = CHANGE_ORDER.filter(function (k) {
    if (c[k] === undefined) return false;
    shown[k] = true;
    return true;
  }).map(function (k) {
    return '<span class="chip">' + esc(CHANGE_LABELS[k] || k) + ' <b>' + esc(c[k]) + '</b></span>';
  }).join('');

  // 后端以后新增字段也别漏掉
  keys.forEach(function (k) {
    if (shown[k] || k === 'period') return;
    chips += '<span class="chip">' + esc(CHANGE_LABELS[k] || k) + ' <b>' + esc(c[k]) + '</b></span>';
  });

  return '<div class="stat-block"><h4>变化</h4>' + period +
    '<div class="kv">' + chips + '</div></div>';
}

function detail(d) {
  var out = '';

  var lessons = d.by_lesson || [];
  if (lessons.length) {
    var max = 0;
    lessons.forEach(function (l) { if (l.count > max) max = l.count; });
    out += '<div class="stat-block"><h4>按课分布</h4><div class="kv">' +
      lessons.map(function (l) {
        var pct = max ? Math.round(l.count / max * 100) : 0;
        return '<span class="chip">' + esc(l.lesson) +
          ' <b>' + l.count + '</b>' +
          '<div style="height:4px;width:' + pct + '%;min-width:2px;background:var(--accent);border-radius:2px;margin-top:3px"></div>' +
          '</span>';
      }).join('') + '</div></div>';
  }

  var dist = d.accuracy_distribution || {};
  var dkeys = Object.keys(dist);
  if (dkeys.length) {
    out += '<div class="stat-block"><h4>正确率分布</h4><div class="kv">' +
      dkeys.map(function (k) {
        return '<span class="chip">' + esc(k) + ' <b>' + dist[k] + '</b></span>';
      }).join('') + '</div></div>';
  }

  var hw = d.hard_words || {};
  if (hw.total) {
    out += '<div class="stat-block"><h4>钉子户</h4><div class="kv">' +
      '<span class="chip warn">总计 <b>' + hw.total + '</b></span>' +
      '<span class="chip">严重 <b>' + (hw.severe || 0) + '</b></span>' +
      '<span class="chip">中度 <b>' + (hw.moderate || 0) + '</b></span>' +
      '<span class="chip">轻度 <b>' + (hw.mild || 0) + '</b></span>' +
      '</div></div>';
  }

  var top = d.top_reviewed || [];
  if (top.length) {
    out += '<div class="stat-block"><h4>复习最多的词</h4><table>' +
      '<thead><tr><th>单词</th><th>复习</th><th>错误</th><th>正确率</th></tr></thead><tbody>' +
      top.map(function (t) {
        return '<tr><td>' + esc(t.word) + '</td><td>' + t.reviews + '</td>' +
          '<td>' + t.errors + '</td><td>' + Math.round(t.accuracy * 100) + '%</td></tr>';
      }).join('') + '</tbody></table></div>';
  }

  return out;
}

document.addEventListener('DOMContentLoaded', function () {
  el('statsDays').addEventListener('change', function () { stats.load(); });
});
