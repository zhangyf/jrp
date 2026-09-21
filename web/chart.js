// 极简 SVG 图表。刻意不引 Chart.js 之类的库：
// 老师的环境在腾讯内网，外部 CDN 拉不到会直接白屏；而这里要的只是
// 折线 / 柱状 / 环形三种图，自己画不到 200 行，还能跟着站点配色走。
//
// 全部返回 SVG 字符串，由调用方塞进 innerHTML。
// 坐标系统一：viewBox + width:100%，让 SVG 自己按比例缩放，
// 不写死像素宽，窄屏也不会溢出。

var chart = {
  PALETTE: {
    accent: '#2f6f4f',
    bad: '#b4392f',
    warn: '#d9a13b',
    orange: '#cf7a34',
    light: '#7ba05b',
    muted: '#8a8a80',
    line: '#e2e2dd'
  },

  empty: function (text) {
    return '<div class="chart-empty">' + esc(text || '暂无数据') + '</div>';
  },

  // 折线 + 面积图。
  // series: [{ name, color, data: [{label, value}] }]
  // 多条系列共用一套 Y 轴；X 轴按索引等距（快照是按天排的，等距更好读）。
  line: function (series, opt) {
    opt = opt || {};
    var W = opt.width || 640, H = opt.height || 210;
    var pl = 40, pr = 12, pt = 14, pb = 26;
    var iw = W - pl - pr, ih = H - pt - pb;

    var n = 0, max = 0, min = Infinity;
    series.forEach(function (s) {
      n = Math.max(n, s.data.length);
      s.data.forEach(function (p) {
        if (p.value > max) max = p.value;
        if (p.value < min) min = p.value;
      });
    });
    if (!n || min === Infinity) return chart.empty('没有足够的数据点');

    // 上下留白，别让线贴着边缘
    var span = max - min;
    if (span === 0) span = Math.max(1, Math.abs(max) * 0.1);
    var lo = Math.max(0, min - span * 0.18);
    var hi = max + span * 0.15;
    if (hi === lo) hi = lo + 1;

    var X = function (i) { return n === 1 ? pl + iw / 2 : pl + iw * i / (n - 1); };
    var Y = function (v) { return pt + ih - ih * (v - lo) / (hi - lo); };

    // Y 轴刻度：只画三条，够读就行
    var ticks = [];
    for (var t = 0; t < 3; t++) {
      var v = lo + (hi - lo) * t / 2;
      ticks.push(Math.round(v));
    }
    ticks = ticks.filter(function (v, i, a) { return a.indexOf(v) === i; });

    var g = '';
    ticks.forEach(function (v, ti) {
      var y = Y(v);
      g += '<line x1="' + pl + '" y1="' + y.toFixed(1) + '" x2="' + (W - pr) +
        '" y2="' + y.toFixed(1) + '" stroke="var(--line)" stroke-width="1"' +
        (ti === 0 ? '' : ' stroke-dasharray="3 3"') + '/>';
      g += '<text x="' + (pl - 6) + '" y="' + (y + 3.5).toFixed(1) +
        '" text-anchor="end" font-size="10" fill="var(--muted)">' + v + '</text>';
    });

    // X 轴标签：首、尾必显示，中间按密度抽稀，最多 6 个
    var step = Math.max(1, Math.ceil((n - 1) / 5));
    var xs = '';
    for (var i = 0; i < n; i++) {
      if (i !== 0 && i !== n - 1 && i % step !== 0) continue;
      var lb = (series[0].data[i] || {}).label || '';
      var anchor = i === 0 ? 'start' : (i === n - 1 ? 'end' : 'middle');
      xs += '<text x="' + X(i).toFixed(1) + '" y="' + (H - 8) +
        '" text-anchor="' + anchor + '" font-size="10" fill="var(--muted)">' +
        esc(lb) + '</text>';
    }

    var body = '';
    series.forEach(function (s) {
      var pts = s.data.map(function (p, i) {
        return X(i).toFixed(1) + ',' + Y(p.value).toFixed(1);
      });

      if (s.area !== false) {
        var areaPts = pts.slice();
        areaPts.push((n === 1 ? X(0) : pl + iw).toFixed(1) + ',' + (pt + ih));
        areaPts.push(X(0).toFixed(1) + ',' + (pt + ih));
        body += '<polygon points="' + areaPts.join(' ') + '" fill="' + s.color +
          '" opacity="0.10"/>';
      }

      body += '<polyline points="' + pts.join(' ') + '" fill="none" stroke="' +
        s.color + '" stroke-width="2" stroke-linejoin="round" stroke-linecap="round"/>';

      // 点太多就不画圆点（会糊成一团），只在每个点挂 <title> 做悬浮提示
      if (n <= 40) {
        s.data.forEach(function (p, i) {
          body += '<circle cx="' + X(i).toFixed(1) + '" cy="' + Y(p.value).toFixed(1) +
            '" r="2.8" fill="var(--panel)" stroke="' + s.color + '" stroke-width="1.8">' +
            '<title>' + esc(p.label + '  ' + s.name + ' ' + p.value) + '</title></circle>';
        });
      }
    });

    var legend = series.length > 1
      ? '<div class="legend">' + series.map(function (s) {
          return '<span class="legend-item"><i style="background:' + s.color + '"></i>' +
            esc(s.name) + '</span>';
        }).join('') + '</div>'
      : '';

    return legend +
      '<svg class="chart-svg" viewBox="0 0 ' + W + ' ' + H + '" preserveAspectRatio="xMidYMid meet" role="img">' +
      g + xs + body + '</svg>';
  },

  // 垂直柱状图。data: [{label, value, color}]
  bars: function (data, opt) {
    opt = opt || {};
    var W = opt.width || 340, H = opt.height || 210;
    var pl = 30, pr = 8, pt = 16, pb = 34;
    var iw = W - pl - pr, ih = H - pt - pb;

    var max = 0, total = 0;
    data.forEach(function (d) {
      if (d.value > max) max = d.value;
      total += d.value;
    });
    if (!total) return chart.empty(opt.emptyText || '还没有足够的数据');
    if (max === 0) max = 1;

    var X = function (i) { return pl + iw * (i + 0.5) / data.length; };
    var Y = function (v) { return pt + ih - ih * v / max; };

    var g = '';
    [0, max / 2, max].forEach(function (v, i) {
      var y = Y(v);
      g += '<line x1="' + pl + '" y1="' + y.toFixed(1) + '" x2="' + (W - pr) +
        '" y2="' + y.toFixed(1) + '" stroke="var(--line)" stroke-width="1"' +
        (i === 0 ? '' : ' stroke-dasharray="3 3"') + '/>';
      g += '<text x="' + (pl - 6) + '" y="' + (y + 3.5).toFixed(1) +
        '" text-anchor="end" font-size="10" fill="var(--muted)">' +
        Math.round(v) + '</text>';
    });

    var bw = Math.min(38, iw / data.length * 0.6);
    var body = '';
    data.forEach(function (d, i) {
      var y = Y(d.value), h = pt + ih - y;
      var pct = total ? Math.round(d.value / total * 100) : 0;
      var bar = '<rect x="' + (X(i) - bw / 2).toFixed(1) + '" y="' + y.toFixed(1) +
        '" width="' + bw.toFixed(1) + '" height="' + Math.max(h, 1).toFixed(1) +
        '" rx="4" fill="' + d.color + '">' +
        '<title>' + esc(d.label + '  ' + d.value + ' 词（' + pct + '%）') + '</title></rect>';
      var val = '<text x="' + X(i).toFixed(1) + '" y="' + (y - 5).toFixed(1) +
        '" text-anchor="middle" font-size="11" font-weight="600" fill="var(--ink)">' +
        d.value + '</text>';
      var lb = '<text x="' + X(i).toFixed(1) + '" y="' + (H - 16) +
        '" text-anchor="middle" font-size="10" fill="var(--muted)">' +
        esc(d.label) + '</text>';
      body += bar + val + lb;
    });

    return '<svg class="chart-svg" viewBox="0 0 ' + W + ' ' + H + '" preserveAspectRatio="xMidYMid meet" role="img">' +
      g + body + '</svg>';
  },

  // 环形图。data: [{label, value, color}]；中心大字显示 total 与 centerLabel。
  // 用 stroke-dasharray 拼扇形，比手算 path 的圆弧简单也稳。
  donut: function (data, opt) {
    opt = opt || {};
    var size = opt.size || 168;
    var cx = size / 2, cy = size / 2;
    var r = size / 2 - 14, sw = opt.strokeWidth || 22;
    var C = 2 * Math.PI * r;

    var total = 0;
    data.forEach(function (d) { total += d.value; });
    if (!total) return chart.empty(opt.emptyText || '一个都没有');

    var off = 0, seg = '';
    data.forEach(function (d) {
      if (!d.value) return;
      var len = C * d.value / total;
      seg += '<circle cx="' + cx + '" cy="' + cy + '" r="' + r + '" fill="none" stroke="' +
        d.color + '" stroke-width="' + sw + '" stroke-dasharray="' + len.toFixed(2) +
        ' ' + (C - len).toFixed(2) + '" stroke-dashoffset="' + (-off).toFixed(2) +
        '" transform="rotate(-90 ' + cx + ' ' + cy + ')">' +
        '<title>' + esc(d.label + '  ' + d.value + '（' + Math.round(d.value / total * 100) + '%）') +
        '</title></circle>';
      off += len;
    });

    var svg = '<svg class="chart-svg chart-donut" viewBox="0 0 ' + size + ' ' + size +
      '" preserveAspectRatio="xMidYMid meet" role="img">' +
      '<circle cx="' + cx + '" cy="' + cy + '" r="' + r + '" fill="none" stroke="var(--line)" stroke-width="' +
      sw + '" opacity="0.5"/>' + seg +
      '<text x="' + cx + '" y="' + (cy - 2) + '" text-anchor="middle" font-size="26" font-weight="700" fill="var(--ink)">' +
      total + '</text>' +
      '<text x="' + cx + '" y="' + (cy + 16) + '" text-anchor="middle" font-size="11" fill="var(--muted)">' +
      esc(opt.centerLabel || '') + '</text></svg>';

    var legend = '<div class="legend legend-col">' + data.map(function (d) {
      return '<div class="legend-item"><i style="background:' + d.color + '"></i>' +
        '<span class="legend-name">' + esc(d.label) + '</span>' +
        '<b>' + d.value + '</b>' +
        '<span class="muted">' + (total ? Math.round(d.value / total * 100) : 0) + '%</span></div>';
    }).join('') + '</div>';

    return '<div class="donut-wrap">' + svg + legend + '</div>';
  }
};
