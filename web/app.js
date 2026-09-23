// 公共：token、fetch 封装、页面切换、小工具。

var app = {
  token: '',
  page: 'practice',

  boot: function () {
    this.token = localStorage.getItem('jrp_token') || '';
    document.getElementById('today').textContent = todayStr();
    document.getElementById('exportDate').value = todayStr();

    var self = this;
    Array.prototype.forEach.call(document.querySelectorAll('.tab'), function (b) {
      b.addEventListener('click', function () { self.go(b.dataset.page); });
    });

    document.getElementById('gradeMode').addEventListener('change', function () {
      localStorage.setItem('jrp_grade', this.value);
      self.regradeActive();
    });
    var saved = localStorage.getItem('jrp_grade');
    if (saved) document.getElementById('gradeMode').value = saved;

    // 视图：列表（一屏全列）/ 卡片（一次一张）
    var viewSel = document.getElementById('viewMode');
    var savedView = localStorage.getItem('jrp_view_mode');
    if (savedView) viewSel.value = savedView;
    viewSel.addEventListener('change', function () {
      localStorage.setItem('jrp_view_mode', this.value);
      self.go(self.page);
    });

    practice.boot();
    hard.boot();

    document.getElementById('tokenSave').addEventListener('click', function () {
      self.token = document.getElementById('tokenInput').value.trim();
      localStorage.setItem('jrp_token', self.token);
      document.getElementById('tokenBar').classList.add('hidden');
      self.go(self.page);
    });

    this.go('practice');
  },

  go: function (page) {
    this.page = page;
    Array.prototype.forEach.call(document.querySelectorAll('.tab'), function (b) {
      b.classList.toggle('active', b.dataset.page === page);
    });
    Array.prototype.forEach.call(document.querySelectorAll('.page'), function (s) {
      s.classList.toggle('hidden', s.id !== 'page-' + page);
    });
    this.load(page);
  },

  load: function (page) {
    if (page === 'practice') practice.load();
    else if (page === 'sentence') sentence.load();
    else if (page === 'hard') hard.load();
    else if (page === 'stats') stats.load();
    else if (page === 'lexicon') lexicon.load();
  },

  // api 统一走这里：带上 token，401 时弹 token 输入条。
  api: function (path, opts) {
    opts = opts || {};
    var headers = opts.headers || {};
    if (this.token) headers['Authorization'] = 'Bearer ' + this.token;
    var full = path + (path.indexOf('?') < 0 ? '?' : '&') + '_=' + Date.now();
    return fetch(full, {
      method: opts.method || 'GET',
      headers: headers,
      body: opts.body
    }).then(function (r) {
      if (r.status === 401) {
        document.getElementById('tokenBar').classList.remove('hidden');
        throw new Error('需要 token');
      }
      return r.json();
    }).then(function (d) {
      if (d && d.success === false) throw new Error(d.error || '请求失败');
      // 服务端开了 --dry-run：顶上挂红条，老师一眼知道这次不算数
      if (d && d.dry_run !== undefined) {
        document.getElementById('dryRunBanner')
          .classList.toggle('hidden', d.dry_run !== true);
      }
      return d;
    });
  },

  mode: function () { return document.getElementById('gradeMode').value; },

  view: function () { return document.getElementById('viewMode').value; },

  // 切判分档位后，把当前页面上「还没提交」的判定重算一遍。
  // 离线回填和练习列表都是「看过答案还没落库」的状态，必须跟着档位变。
  regradeActive: function () {
    if (this.page === 'offline' && window.offline) offline.regrade();
    else if (this.page === 'practice' && practice.list) practice.list.regrade();
    else if (this.page === 'hard' && hard.list) hard.list.regrade();
  },

  toast: function (msg) {
    var t = document.getElementById('toast');
    t.textContent = msg;
    t.classList.remove('hidden');
    clearTimeout(this._t);
    this._t = setTimeout(function () { t.classList.add('hidden'); }, 2600);
  }
};

function todayStr() {
  var d = new Date();
  return d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate());
}
// 'YYYY-MM-DD HH:MM:SS' —— 回写时间，跟服务端存快照的 saved_at 一个格式
function nowStamp() {
  var d = new Date();
  return todayStr() + ' ' + pad(d.getHours()) + ':' + pad(d.getMinutes()) + ':' + pad(d.getSeconds());
}
function pad(n) { return n < 10 ? '0' + n : '' + n; }

function el(id) { return document.getElementById(id); }
function esc(s) {
  return String(s == null ? '' : s)
    .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}

// 状态标签：去掉 emoji 前缀只留文字
var STATUS_TEXT = {
  '☠️钉子户': '钉子户',
  '🔴待巩固': '待巩固',
  '🔄待测试': '待测试',
  '🟡基本掌握': '基本掌握',
  '🟢抽查': '抽查'
};
function statusText(s) { return STATUS_TEXT[s] || s || ''; }

// 分区统计渲染成一行 chip，钉子户高亮
function renderSummary(node, items) {
  if (!items || !items.length) { node.innerHTML = ''; return; }
  node.innerHTML = items.map(function (it) {
    var cls = 'chip' + (it.warn ? ' warn' : '');
    return '<span class="' + cls + '">' + esc(it.label) + ' <b>' + it.value + '</b></span>';
  }).join('');
}

// 「今天这轮已回写」的回看卡 —— 列表模式和卡片模式共用这一套版式。
// 以前两种模式各画各的：卡片模式有卡（统计 + 错题行 + 按钮），列表模式只在
// 底部塞一个 ghost 按钮，老师看着像两个产品。现在统一成同一张卡。
//   items     /api/review 的当天快照（number/word/definition/answer/correct/unknown/blank）
//   opts      { saved_at, btnId, onRequeue }
function renderReviewCard(box, items, opts) {
  if (!box) return;
  opts = opts || {};
  items = items || [];
  var wrongs = items.filter(function (i) { return !i.blank && !i.correct; });
  var unk = wrongs.filter(function (i) { return i.unknown; }).length;

  var rows = wrongs.map(function (x) {
    var mine = x.answer
      ? '<span class="no">' + esc(x.answer) + '</span>'
      : '<span class="unk-tag">不会</span>';
    return '<div class="wrong-row">' +
      '<span class="num">#' + x.number + '</span>' +
      '<span class="def">' + esc(x.definition || '') + '</span>' +
      '<span class="answer">' + esc(x.word) + '</span>' +
      '<span class="muted">你写的：' + mine + '</span>' +
      '</div>';
  }).join('');

  box.innerHTML =
    '<div class="card"><h3>今天这轮已回写' +
    (opts.saved_at ? '（' + esc(opts.saved_at) + '）' : '') + '</h3>' +
    '<div class="kv"><span class="chip">共 ' + items.length + ' 词</span>' +
    '<span class="chip' + (wrongs.length ? ' warn' : '') + '">错 ' + wrongs.length + ' 个' +
    (unk ? '（其中「不会」' + unk + '）' : '') + '</span></div>' +
    (wrongs.length
      ? '<div class="wrong-list">' + rows + '</div>' +
        '<button id="' + opts.btnId + '" class="primary">再练这 ' + wrongs.length + ' 个错词</button>' +
        '<p class="muted">再练一遍只是当场巩固，不计入档案 —— 今天的成绩已经回写过一次了。</p>'
      : '<p class="muted">今天没有错题，全对。</p>') +
    '</div>';
  box.classList.remove('hidden');

  if (wrongs.length && opts.btnId && opts.onRequeue) {
    var btn = el(opts.btnId);
    if (btn) btn.addEventListener('click', opts.onRequeue);
  }
}
