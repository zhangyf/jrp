// 统计页。数据来自 /api/stats（和 CLI 的 jrp stats 同一套 ComputeStats）。
// 图表用 chart.js 画纯 SVG —— 不引外部库，内网环境也能看。
//
// 版面从上到下：
//   KPI 卡片 → 掌握度构成条 → 词库走势折线 → 正确率分布(柱) + 钉子户(环)
//   → 复习最多的词 → 每日明细（折叠）
// 按课分布原本在「明细」里，已按老师要求去掉（课号在词汇总表里能筛，作用重复）。

var stats = {
  load: function () {
    var days = el('statsDays').value || '30';
    el('statsBody').innerHTML = '<div class="card"><span class="chip muted">加载中…</span></div>';

    app.api('/api/stats?days=' + encodeURIComponent(days)).then(function (d) {
      el('statsBody').innerHTML =
        kpi(d.snapshots || []) +
        share(d.snapshots || []) +
        trend(d.snapshots || []) +
        '<div class="stat-grid">' +
        card('正确率分布', '只看复习过的词', accuracy(d.detail || {})) +
        card('钉子户分布', '正确率低且复习次数够多的词', hard(d.detail || {})) +
        '</div>' +
        topReviewed(d.detail || {}) +
        detail(d.snapshots || []);
    }).catch(function (e) {
      el('statsBody').innerHTML = '<div class="card"><span class="chip warn">' + esc(e.message) + '</span></div>';
    });
  }
};

// 一张卡片。sub 是标题右侧的灰色小字说明
function card(title, sub, body) {
  return '<div class="card">' +
    '<div class="card-head"><h4>' + esc(title) + '</h4>' +
    (sub ? '<span class="muted">' + esc(sub) + '</span>' : '') + '</div>' +
    body + '</div>';
}

function cardWide(title, sub, body) {
  return '<div class="card span-all">' +
    '<div class="card-head"><h4>' + esc(title) + '</h4>' +
    (sub ? '<span class="muted">' + esc(sub) + '</span>' : '') + '</div>' +
    body + '</div>';
}

// 区间增量。goodUp=false 表示「涨是坏事」（比如累计错误）。
// 具体「从多少到多少」挂在 title 上，鼠标悬浮才看，不占版面。
function deltaBadge(delta, goodUp, title) {
  var t = title ? ' title="' + esc(title) + '"' : '';
  if (delta === null) return '<span class="delta flat"' + t + '>无对比</span>';
  if (delta === 0) return '<span class="delta flat"' + t + '>持平</span>';
  var good = goodUp ? delta > 0 : delta < 0;
  return '<span class="delta ' + (good ? 'up' : 'down') + '"' + t + '>' +
    (delta > 0 ? '+' : '') + delta + '</span>';
}

// 顶部 KPI 卡片：取最新快照为当前值，和区间第一天比增量
function kpi(snaps) {
  if (!snaps.length) return '';
  var first = snaps[0], last = snaps[snaps.length - 1];
  var hasCmp = snaps.length >= 2;
  var d = function (k) { return hasCmp ? last[k] - first[k] : null; };

  var items = [
    { key: 'total', label: '总词量', value: last.total, delta: d('total'), up: true, sub: last.version || '' },
    { key: 'mastered', label: '已掌握', value: last.mastered, delta: d('mastered'), up: true, sub: pct(last.mastered, last.total) },
    { key: 'basic', label: '基本掌握', value: last.basic, delta: d('basic'), up: true, sub: pct(last.basic, last.total) },
    { key: 'needs_consol', label: '待巩固', value: last.needs_consol, delta: d('needs_consol'), up: false, sub: pct(last.needs_consol, last.total) },
    { key: 'errors', label: '累计错误', value: last.errors, delta: d('errors'), up: false, sub: '次' }
  ];

  var period = hasCmp
    ? '<div class="stat-period">对比区间 ' + esc(first.date) + ' → ' + esc(last.date) +
      '（' + snaps.length + ' 个存档点）</div>'
    : '<div class="stat-period">只有 ' + esc(last.date) + ' 一个存档点，暂无对比</div>';

  return period + '<div class="kpi-grid">' + items.map(function (it) {
    return '<div class="kpi">' +
      '<div class="kpi-label">' + esc(it.label) + '</div>' +
      '<div class="kpi-value">' + it.value + '</div>' +
      '<div class="kpi-foot">' + deltaBadge(it.delta, it.up,
        hasCmp ? first[it.key] + ' → ' + last[it.key] : '') +
      (it.sub ? '<span class="muted">' + esc(it.sub) + '</span>' : '') +
      '</div></div>';
  }).join('') + '</div>';
}

function pct(a, b) { return b ? Math.round(a / b * 100) + '%' : '—'; }

// 掌握度构成：一条横条，按 已掌握/基本掌握/待巩固/未测试 的比例切分
function share(snaps) {
  if (!snaps.length) return '';
  var last = snaps[snaps.length - 1];
  var segs = [
    { label: '已掌握', value: last.mastered, color: chart.PALETTE.accent },
    { label: '基本掌握', value: last.basic, color: chart.PALETTE.light },
    { label: '待巩固', value: last.needs_consol, color: chart.PALETTE.warn },
    { label: '未测试', value: last.untested, color: chart.PALETTE.line }
  ];
  var total = 0;
  segs.forEach(function (s) { total += s.value; });
  if (!total) return '';

  var bar = '<div class="share-bar">' + segs.map(function (s) {
    if (!s.value) return '';
    var w = s.value / total * 100;
    return '<div class="share-seg" style="width:' + w.toFixed(2) + '%;background:' + s.color + '" ' +
      'title="' + esc(s.label + ' ' + s.value + '（' + Math.round(w) + '%）') + '"></div>';
  }).join('') + '</div>';

  var legend = '<div class="legend">' + segs.map(function (s) {
    return '<span class="legend-item"><i style="background:' + s.color + '"></i>' +
      esc(s.label) + ' <b>' + s.value + '</b>' +
      '<span class="muted">' + (s.value ? Math.round(s.value / total * 100) + '%' : '0%') + '</span></span>';
  }).join('') + '</div>';

  return cardWide('掌握度构成', '共 ' + total + ' 词', bar + legend);
}

// 词库走势：总词量 + 已掌握两条线
function trend(snaps) {
  if (snaps.length < 2) {
    return cardWide('词库走势', '至少两个存档点才能画走势',
      chart.empty('当前区间只有 ' + snaps.length + ' 个存档点'));
  }

  var toPts = function (key) {
    return snaps.map(function (s) { return { label: s.date, value: s[key] }; });
  };

  var svg = chart.line([
    { name: '总词量', color: chart.PALETTE.accent, data: toPts('total') },
    { name: '已掌握', color: chart.PALETTE.light, data: toPts('mastered') }
  ], { height: 220 });

  return cardWide('词库走势', '每天取当天最后一个版本', svg);
}

// 正确率分布：5 个桶，颜色从红到绿
function accuracy(detail) {
  var dist = detail.accuracy_distribution || {};
  var ORDER = ['0-30%', '30-60%', '60-80%', '80-90%', '90-100%'];
  var COLOR = {
    '0-30%': chart.PALETTE.bad,
    '30-60%': chart.PALETTE.orange,
    '60-80%': chart.PALETTE.warn,
    '80-90%': chart.PALETTE.light,
    '90-100%': chart.PALETTE.accent
  };

  var keys = ORDER.filter(function (k) { return dist[k] !== undefined; });
  var data = keys.map(function (k) {
    return { label: k, value: dist[k] || 0, color: COLOR[k] };
  });

  var total = 0;
  data.forEach(function (d) { total += d.value; });
  if (!total) return chart.empty('还没有复习过的词');

  return chart.bars(data, { emptyText: '还没有复习过的词' }) +
    '<div class="chart-note">共 ' + total + ' 个复习过的词。柱子越高，落在这一档的词越多。</div>';
}

// 钉子户分布：严重 / 中度 / 轻度 环形图
function hard(detail) {
  var hw = detail.hard_words || {};
  if (!hw.total) {
    return chart.empty('没有钉子户 —— 目前没有正确率低且复习次数够多的词');
  }

  var data = [
    { label: '严重（<30%）', value: hw.severe || 0, color: chart.PALETTE.bad },
    { label: '中度（30-45%）', value: hw.moderate || 0, color: chart.PALETTE.warn },
    { label: '轻度（≥45%）', value: hw.mild || 0, color: chart.PALETTE.light }
  ];

  return chart.donut(data, { centerLabel: '个钉子户' });
}

function topReviewed(detail) {
  var top = detail.top_reviewed || [];
  if (!top.length) return '';

  var rows = top.map(function (t) {
    var acc = Math.round(t.accuracy * 100);
    var color = acc < 30 ? chart.PALETTE.bad
      : (acc < 60 ? chart.PALETTE.orange
        : (acc < 80 ? chart.PALETTE.warn : chart.PALETTE.accent));
    return '<tr><td>' + esc(t.word) + '</td>' +
      '<td class="num">' + t.reviews + '</td>' +
      '<td class="num">' + t.errors + '</td>' +
      '<td><div class="acc-cell"><div class="acc-bar"><i style="width:' + acc +
        '%;background:' + color + '"></i></div><b>' + acc + '%</b></div></td></tr>';
  }).join('');

  return cardWide('复习最多的词', 'Top ' + top.length,
    '<table><thead><tr><th>单词</th><th>复习</th><th>错误</th><th>正确率</th></tr></thead>' +
    '<tbody>' + rows + '</tbody></table>');
}

// 每日明细：默认折叠，走势图已经表达了主要信息
function detail(snaps) {
  if (!snaps.length) return '';

  var rows = snaps.slice().reverse().map(function (s) {
    return '<tr><td>' + esc(s.date) + '</td><td>' + esc(s.version || '') + '</td>' +
      '<td class="num">' + s.total + '</td>' +
      '<td class="num">' + (s.mastered || 0) + '</td>' +
      '<td class="num">' + (s.basic || 0) + '</td>' +
      '<td class="num">' + (s.needs_consol || 0) + '</td>' +
      '<td class="num">' + (s.untested || 0) + '</td>' +
      '<td class="num">' + (s.errors || 0) + '</td></tr>';
  }).join('');

  return '<div class="card span-all"><details><summary>每日明细（' + snaps.length + ' 天）</summary>' +
    '<table><thead><tr><th>日期</th><th>版本</th><th>总量</th><th>已掌握</th>' +
    '<th>基本掌握</th><th>待巩固</th><th>未测试</th><th>错误数</th></tr></thead>' +
    '<tbody>' + rows + '</tbody></table></details></div>';
}

document.addEventListener('DOMContentLoaded', function () {
  el('statsDays').addEventListener('change', function () { stats.load(); });
});
