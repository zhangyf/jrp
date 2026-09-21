// 词汇总表：把档案里的全部词列出来，看假名 / 汉字 / 中文 / 词性 / 阶段。
//
// 老师要的是「看着复习」的浏览表 —— 不答题、不回写，纯展示。
// 词性来自服务端 word_meta.json（不在档案里），没标到的显示「—」。

var lexicon = {
  items: [],
  view: [],
  sortKey: 'number',
  sortDir: 1,
  loaded: false,

  load: function () {
    var self = this;
    if (this.loaded) { this.render(); return; }
    el('lexBody').innerHTML = '<p class="muted">加载中…（要读整份档案，稍等）</p>';
    app.api('/api/lexicon').then(function (d) {
      self.items = d.items || [];
      self.loaded = true;
      self.fillSelects(d);
      self.bind();
      self.render();
    }).catch(function (e) {
      el('lexBody').innerHTML = '<p class="chip warn">加载失败：' + esc(e.message) + '</p>';
    });
  },

  // 下拉选项：词性 / 阶段 / 课，都从实际数据里抽，不写死
  fillSelects: function (d) {
    var poss = {}, stats = {}, groups = {};
    this.items.forEach(function (it) {
      poss[it.pos || ''] = (poss[it.pos || ''] || 0) + 1;
      stats[it.status || ''] = (stats[it.status || ''] || 0) + 1;
      if (it.group) groups[it.group] = true;
    });
    var posKeys = Object.keys(poss).filter(Boolean).sort();
    var statKeys = Object.keys(stats).filter(Boolean).sort();
    var groupKeys = Object.keys(groups).sort(naturalCmp);

    el('lexPos').innerHTML = '<option value="">全部词性</option>' +
      posKeys.map(function (k) {
        return '<option value="' + esc(k) + '">' + esc(k) + '（' + poss[k] + '）</option>';
      }).join('');
    el('lexStatus').innerHTML = '<option value="">全部阶段</option>' +
      statKeys.map(function (k) {
        return '<option value="' + esc(k) + '">' + esc(k) + '（' + stats[k] + '）</option>';
      }).join('');
    el('lexGroup').innerHTML = '<option value="">全部课</option>' +
      groupKeys.map(function (g) {
        return '<option value="' + esc(g) + '">' + esc(g) + '</option>';
      }).join('');

    renderSummary(el('lexSummary'), [
      { label: '总词数', value: this.items.length },
      { label: '动词', value: (d.pos_summary && d.pos_summary['动词']) || 0 },
      { label: '形容词', value: (d.pos_summary && d.pos_summary['形容词']) || 0 },
      { label: '未标词性', value: (d.pos_summary && d.pos_summary['未标注']) || 0, warn: (d.pos_summary || {})['未标注'] > 0 }
    ]);
  },

  bind: function () {
    var self = this;
    var t = null;
    el('lexSearch').addEventListener('input', function () {
      clearTimeout(t);
      t = setTimeout(function () { self.render(); }, 200);
    });
    ['lexPos', 'lexStatus', 'lexGroup'].forEach(function (id) {
      el(id).addEventListener('change', function () { self.render(); });
    });
    el('lexWrongOnly').addEventListener('change', function () { self.render(); });
  },

  filtered: function () {
    var q = el('lexSearch').value.trim().toLowerCase();
    var pos = el('lexPos').value;
    var st = el('lexStatus').value;
    var grp = el('lexGroup').value;
    var wrongOnly = el('lexWrongOnly').checked;

    return this.items.filter(function (it) {
      if (pos && (it.pos || '') !== pos) return false;
      if (st && (it.status || '') !== st) return false;
      if (grp && it.group !== grp) return false;
      if (wrongOnly && !(it.errors > 0)) return false;
      if (q) {
        var hay = ((it.kana || '') + (it.kanji || '') + (it.def || '')).toLowerCase();
        if (hay.indexOf(q) < 0) return false;
      }
      return true;
    });
  },

  sorted: function (rows) {
    var k = this.sortKey, dir = this.sortDir;
    var out = rows.slice();
    out.sort(function (a, b) {
      var x, y;
      if (k === 'reviews' || k === 'errors') { x = a[k]; y = b[k]; }
      else { x = String(a[k] || ''); y = String(b[k] || ''); }
      if (x < y) return -1 * dir;
      if (x > y) return 1 * dir;
      return a.number - b.number;
    });
    return out;
  },

  render: function () {
    if (!this.loaded) return;
    var rows = this.sorted(this.filtered());
    this.view = rows;
    el('lexCount').textContent = rows.length + ' / ' + this.items.length + ' 词';
    if (!rows.length) {
      el('lexBody').innerHTML = '<p class="muted">没有符合条件的词</p>';
      return;
    }

    var head = [
      ['number', '#'], ['kana', '假名'], ['kanji', '汉字'], ['def', '中文释义'],
      ['pos', '词性'], ['status', '阶段'], ['reviews', '复习'], ['errors', '错'],
      ['group', '课']
    ];
    var self = this;
    var th = head.map(function (h) {
      var arrow = (self.sortKey === h[0]) ? (self.sortDir > 0 ? ' ▲' : ' ▼') : '';
      return '<th class="sortable" data-k="' + h[0] + '">' + h[1] + arrow + '</th>';
    }).join('');

    var tr = rows.map(function (it) {
      var posText = it.pos || '';
      if (it.sub) posText += '·' + it.sub;
      return '<tr>' +
        '<td class="num">' + it.number + '</td>' +
        '<td class="kana">' + esc(it.kana) + '</td>' +
        '<td class="kanji">' + esc(it.kanji || '—') + '</td>' +
        '<td class="def">' + esc(it.def) + (it.note ? ' <span class="muted">（' + esc(it.note) + '）</span>' : '') + '</td>' +
        '<td class="pos' + (it.sub ? ' has-sub' : '') + '">' + esc(posText || '—') + '</td>' +
        '<td class="st">' + esc(it.status || '') + '</td>' +
        '<td class="num">' + it.reviews + '</td>' +
        '<td class="num' + (it.errors > 0 ? ' bad' : '') + '">' + (it.errors || '·') + '</td>' +
        '<td class="grp muted">' + esc(shortGroup(it.group)) + '</td>' +
        '</tr>';
    }).join('');

    el('lexBody').innerHTML = '<table class="lex-table"><thead><tr>' + th +
      '</tr></thead><tbody>' + tr + '</tbody></table>';

    var table = el('lexBody').querySelector('table');
    Array.prototype.forEach.call(el('lexBody').querySelectorAll('th.sortable'), function (h) {
      h.addEventListener('click', function () {
        var k = h.dataset.k;
        if (self.sortKey === k) self.sortDir = -self.sortDir;
        else { self.sortKey = k; self.sortDir = 1; }
        self.render();
      });
    });
    return table;
  }
};

// 课分组标题形如「📖 第7课01 饮食寒暄/综合（7/6–7/8）」—— 表格里只留「第7课01」
function shortGroup(g) {
  if (!g) return '';
  var m = g.match(/第\d+课\d*/);
  return m ? m[0] : g.replace(/^[📖\s]+/, '').slice(0, 14);
}

// 第2课 < 第10课（按数字排，不按字符串）
function naturalCmp(a, b) {
  var na = (String(a).match(/\d+/) || [0])[0] | 0;
  var nb = (String(b).match(/\d+/) || [0])[0] | 0;
  if (na !== nb) return na - nb;
  return String(a) < String(b) ? -1 : 1;
}
