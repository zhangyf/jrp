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
    this.bootMiniQr();
    this.bootBrandToggle();
    this.bootAdminLink();

    document.getElementById('tokenSave').addEventListener('click', function () {
      self.token = document.getElementById('tokenInput').value.trim();
      localStorage.setItem('jrp_token', self.token);
      document.getElementById('tokenBar').classList.add('hidden');
      self.go(self.page);
    });

    this.go('practice');
  },

  // 左侧常驻小程序码。拉不到就整个不显示 —— 不留一个破图在那儿。
  // 拉不到的两种情况都是正常的：没登录（401）、服务端没配微信凭证（503）。
  bootMiniQr: function () {
    var dock = document.getElementById('miniQrDock');
    var img = document.getElementById('miniQrImg');
    var mpBlock = document.getElementById('mpQrBlock');
    var mpImg = document.getElementById('mpQrImg');
    if (!dock || !img) return;

    // 两个码各自独立：拿到任一个就显示浮动层，拿不到的那块自己藏着，不留破图。
    // 拿不到都是正常情况 —— 小程序码：未登录(401) / 没配微信凭证(503)；
    // 公众号：web/mp_qr.jpg 还没放上去(404)。
    var show = function () { dock.classList.remove('hidden'); };
    img.onload = show;
    img.src = '/api/mini-qr';

    if (mpBlock && mpImg) {
      mpImg.onload = function () { mpBlock.classList.remove('hidden'); show(); };
      mpImg.onerror = function () { mpBlock.classList.add('hidden'); };
      mpImg.src = 'mp_qr.jpg';
    }
  },

  // 页头 logo 当开关：点一下把小程序码 / 公众号展开，再点收回去。
  // 两个码都拿不到时 dock 还是 hidden（bootMiniQr 不会摘掉它），点了没反应，
  // 别给出「点了什么都出不来」的错觉。
  bootBrandToggle: function () {
    var btn = document.getElementById('brandToggle');
    var dock = document.getElementById('miniQrDock');
    if (!btn || !dock) return;

    var setOpen = function (open) {
      dock.classList.toggle('open', open);
      btn.setAttribute('aria-expanded', open ? 'true' : 'false');
    };

    btn.addEventListener('click', function (e) {
      e.stopPropagation();
      if (dock.classList.contains('hidden')) return;
      setOpen(!dock.classList.contains('open'));
    });

    // 点页面别处收起（面板内和 logo 上除外）
    document.addEventListener('click', function (e) {
      if (!dock.classList.contains('open')) return;
      if (dock.contains(e.target) || btn.contains(e.target)) return;
      setOpen(false);
    });

    // ESC 收起
    document.addEventListener('keydown', function (e) {
      if (e.key === 'Escape' || e.key === 'Esc') setOpen(false);
    });
  },

  // 管理后台入口：只有 owner 看得见，其他人连这个链接都不出现。
  // 拿不到身份（没登录 / 服务端没开登录）就整个不显示 —— 不留一个点了报错的链接。
  bootAdminLink: function () {
    var a = document.getElementById('adminLink');
    if (!a) return;
    fetch('/api/auth/me', { credentials: 'same-origin' })
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (j) {
        if (j && j.success && j.is_owner) a.classList.remove('hidden');
      })
      .catch(function () { /* 拿不到身份就不显示 */ });
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
    else if (page === 'conj') conj.load();
    else if (page === 'knowledge') knowledge.load();
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
  },

  // ---------------- 词条附加信息：词性 + 备注 ----------------
  //
  // 老师 2026-09-28 要的：卡片上不能只有中文释义，得带上词性和备注。
  // 备注主要写「跟近义词怎么区分」；词义明确的就空着（空着是正常的）。
  //
  // 这几个方法挂 app 上而不是写成全局函数 —— web/*.js 是老式多 script
  // 全局挂载，顶层 function 会覆盖别的文件里的同名 var（踩过一次整站白屏）。

  // 拆释义：「浴室，浴缸（泡澡用；…）」→ 主部「浴室，浴缸」+ 备注「泡澡用；…」
  // 括号里那截本来就是区分说明，摆进备注比堆在释义后面好看，也跟
  // 「主释义只留一个义项」的录词规矩对上。没括号就整串都是主部。
  defParts: function (def) {
    var s = String(def == null ? '' : def);
    var i = s.search(/[（(]/);
    if (i <= 0) return { main: s, note: '' };
    var main = s.slice(0, i).replace(/[、，,;；/／\s]+$/, '');
    var note = s.slice(i + 1).replace(/[）)]\s*$/, '').trim();
    if (!main) return { main: s, note: '' };
    return { main: main, note: note };
  },

  // hideSub=true 只给大类（「动词」「形容词」），不暴露一类/二类这种小类 ——
  // 活用专项答题前用：小类一写出来，て形怎么变就等于告诉你了（2026-09-30 老师）。
  // 对答案之后才把小类补上，见 conjugation.js 的 grade()。
  // 词性表里混着两套写法（老数据「动/1」，新数据「动词/一类」），显示层统一成中文。
  // 只影响显示，不动数据。挂 app 上，避免污染全局（全局重名踩过整站白屏）。
  POS_ALIAS: { '动': '动词', '形': '形容词', '名': '名词', '副': '副词' },
  VERB_SUB: { '1': '一类', '2': '二类', '3': '三类' },
  ADJ_SUB: { '1': 'い形', '2': 'な形' },

  // 词性符号化（2026-11-03 老师要的：卡片上别写「名词」「动词·一类」这种中文，
  // 用 N / V1 / Aい 这类符号，一眼扫过去更快）。
  //
  // ⚠️ 这是**穷举表**，不是「匹配不上的就用原文兜底」—— 词性表里现在一共
  // 25 种取值（2026-11-03 从 COS 拉的统计），每个都有确定符号：
  //   名/名词 → N      专/专有名词/专有名词·人名/地名 → N.pr
  //   代词 → Pron      疑问词 → Q       副词/副 → Adv     助词 → P
  //   连体词 → Adn     连词 → Conj      感叹词/叹 → Int   寒暄语 → Expr
  //   表达 → Phr       慣用 → Idm       未标注 → —
  //   动词：一类 V1（五段）/ 二类 V2（一段）/ 三类 V3（カ変・サ変）
  //   形容词：い形 Aい / な形 Aな
  // 词性表混着两套写法（老数据「动/1」、新数据「动词/一类」），两边都认。
  //
  // ⚠️ 中文没删，只是挪地方：符号是显示层，中文进 title（悬停）和词汇总表详情行
  // —— 老师选的「悬停+详情」。活页答题前 title 也只给大类（见 hideSub）。
  POS_SYMBOL: {
    '名词': 'N', '名': 'N',
    '专有名词': 'N.pr', '专': 'N.pr',
    '代词': 'Pron', '疑问词': 'Q',
    '副词': 'Adv', '副': 'Adv',
    '助词': 'P', '连体词': 'Adn', '连词': 'Conj',
    '感叹词': 'Int', '叹': 'Int',
    '寒暄语': 'Expr', '表达': 'Phr', '慣用': 'Idm',
    '未标注': '—'
  },
  // 动词/形容词：小类 -> 符号（数字体系，跟标日「一类/二类/三类」对得上）
  VERB_SYMBOL: { '一类': 'V1', '1': 'V1', '二类': 'V2', '2': 'V2', '三类': 'V3', '3': 'V3' },
  ADJ_SYMBOL: { 'い形': 'Aい', '1': 'Aい', 'な形': 'Aな', '2': 'Aな' },

  // 大类归一：老写法（动/形/名/副）跟新写法（动词/形容词...）都归到同一把钥匙
  posKey: function (w) {
    if (!w) return '';
    var p = String(w.pos || '').trim();
    return this.POS_ALIAS[p] || p;
  },

  // 符号。hideSub=true 时动词只给 V、形容词只给 A —— 活用专项答题前不能露小类。
  posSymbol: function (w, hideSub) {
    var k = this.posKey(w);
    if (!k) return '';
    if (k === '动词') {
      if (hideSub) return 'V';
      return this.VERB_SYMBOL[String((w && w.sub) || '').trim()] || 'V';
    }
    if (k === '形容词') {
      if (hideSub) return 'A';
      return this.ADJ_SYMBOL[String((w && w.sub) || '').trim()] || 'A';
    }
    return this.POS_SYMBOL[k] || k; // 表里没有的（新词性）原样显示，别吞掉
  },

  posText: function (w, hideSub) {
    if (!w) return '';
    var t = this.POS_ALIAS[String(w.pos || '')] || String(w.pos || '');
    if (w.sub && !hideSub) t += '·' + this.subText(w);
    return t;
  },

  // 小类的显示名。词性表里混着两套写法（老数据用「动/1」，新数据用「动词/一类」），
  // 显示层统一成中文写法，不然活用页揭开会显示成「动·1」。
  subText: function (w) {
    var s = String((w && w.sub) || '');
    var p = this.POS_ALIAS[String((w && w.pos) || '')] || String((w && w.pos) || '');
    if (p === '动词') return this.VERB_SUB[s] || s;
    if (p === '形容词') return this.ADJ_SUB[s] || s;
    return s;
  },

  // 三圆手册分级（2026-11-02 按《标日初上 三圆词汇速记手册》逐个打标）。
  // 老师 2026-11-03 定的画法：**画圈不写字** —— 三圆 3 个红圈、二圆 2 个、一圆 1 个。
  // 文字只留在 title/aria-label 上（悬停和读屏还能知道是几圆）。
  // 手册没收的词 circle 为空 → 显示占位「—」。
  CIRCLE_DOTS: { '三圆': 3, '二圆': 2, '一圆': 1 },

  circleHtml: function (w) {
    var c = String((w && w.circle) || '').trim();
    var n = this.CIRCLE_DOTS[c] || 0;
    if (!n) return '<span class="f-empty">—</span>';
    var dots = '';
    for (var i = 0; i < n; i++) dots += '<i class="cdot"></i>';
    return '<span class="cdots" title="' + esc(c) + '" aria-label="' + esc(c) + '">' + dots + '</span>';
  },

  // 「圆级 / 词性 / 备注」左右并排三格（2026-09-28 老师：上下放太占地；
  // 圆级是 2026-11-03 加的第四格信息，跟词性同一行）。
  // 卡片模式、列表模式、今日练习、钉子户全都调这一个函数 ——
  // 同一件事不许画成两种样子（2026-09-23 老师定的规矩）。
  //   w         词条目（circle/pos/sub/note）
  //   extraNote 兜底备注：释义括号里拆出来的那截。词表里手写了 note 就以
  //             手写的为准（那是老师亲自校准过的区分点）。
  factsHtml: function (w, extraNote) {
    w = w || {};
    // 显示符号，中文进 title（悬停）—— 老师 2026-11-03 选的「悬停+详情」。
    var sym = this.posSymbol(w);
    var full = this.posText(w);
    var note = String(w.note || '').trim() || String(extraNote || '').trim();
    return '<dl class="facts">' +
      '<div class="f-cell f-circle"><dt>圆级</dt><dd>' + this.circleHtml(w) + '</dd></div>' +
      '<div class="f-cell f-pos"><dt>词性</dt><dd>' +
        (sym ? '<span class="pos-chip" title="' + esc(full || sym) +
               '" aria-label="' + esc(full || sym) + '">' + esc(sym) + '</span>'
             : '<span class="f-empty">—</span>') +
      '</dd></div>' +
      '<div class="f-cell f-note"><dt>备注</dt><dd>' +
        (note ? esc(note) : '<span class="f-empty">—</span>') +
      '</dd></div>' +
      '</dl>';
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
