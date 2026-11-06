// web/search.js —— 五页右上角的全局搜索（词汇字面 + 知识点语义）。
//
// ⚠️ 顶层只挂一个 `var search`：web/*.js 是老式多 script 全局挂载，
// 顶层 function 会覆盖先加载文件里的同名 var（2026-09-21 整站白屏事故）。
// 所有逻辑都是对象方法或闭包内的函数表达式，绝不新增顶层符号。
//
// 数据源：GET /api/search?q=（服务端并行两路，挂 withUser，trial/匿名不给用）。
// 结果只在浮层里展示，不落 localStorage、不缓存全库。
// ⚠️ 点结果不再跳页（2026-11-06 老师要求）：改用页内浮窗展示词条/知识点详情，
//    练习页 DOM 不切走、播放器/做题进度不丢；想主动去词汇表/知识点页，浮窗里
//    有个「在词汇表查看 / 打开完整知识点」按钮，点它才跳。
// 出现在老师点名的五页（今日练习/造句/钉子户/活用/听力）。听力页那个框
// 是边听边查词用的，浮窗正好不打断播放。不扩散到词汇/知识点/统计/离线。

var search = {
  // 允许出现搜索框的页（按老师点名的范围）。
  pages: { practice: 1, sentence: 1, hard: 1, conj: 1, listening: 1 },
  timer: null,
  seq: 0,        // 只认最后一次请求的响应，防快速输入时旧响应盖新结果
  lastWords: [],
  lastDocs: [],
  _mask: null,   // 页内浮窗的 DOM 引用（同一时刻只开一个）

  boot: function () {
    var self = this;
    var box = el('gsearch');
    if (!box) return;
    var input = el('gsearchInput');
    var panel = el('gsearchPanel');

    input.addEventListener('input', function () {
      clearTimeout(self.timer);
      var q = input.value.trim();
      if (!q) { self.hide(); return; }
      self.timer = setTimeout(function () { self.run(q); }, 300);
    });
    input.addEventListener('keydown', function (e) {
      if (e.key === 'Escape' || e.key === 'Esc') { self.closeModal(); self.hide(); input.blur(); }
      else if (e.key === 'Enter') { clearTimeout(self.timer); self.run(input.value.trim()); }
    });

    // 结果面板事件委托：不用 closest（jsdom/老 WebView 兼容性），手写向上找。
    panel.addEventListener('click', function (e) {
      var node = e.target;
      while (node && node !== panel && !(node.getAttribute && node.getAttribute('data-kind'))) {
        node = node.parentNode;
      }
      if (!node || node === panel) return;
      var i = parseInt(node.getAttribute('data-i'), 10);
      if (node.getAttribute('data-kind') === 'word') {
        var w = self.lastWords[i];
        if (w) self.showWord(w);
      } else {
        var d = self.lastDocs[i];
        if (d) self.showDoc(d);
      }
    });

    // 点面板/输入框之外收起；ESC 也能收。
    document.addEventListener('click', function (e) {
      if (!box.contains(e.target)) self.hide();
    });
    document.addEventListener('keydown', function (e) {
      if (e.key === 'Escape' || e.key === 'Esc') { self.closeModal(); self.hide(); }
    });

    // 包装 app.go：任何切页路径都同步刷新显隐（含 gradeMode 重进当前页），
    // 同时关掉浮窗 —— 切页时浮窗没意义了。
    // 运行时给对象方法重新赋值，不是顶层 function 声明，不触发 09-21 那类覆盖。
    var origGo = app.go;
    app.go = function (page) {
      origGo.call(app, page);
      self.closeModal();
      self.onPage(page);
    };
    this.onPage(app.page);
  },

  onPage: function (page) {
    var box = el('gsearch');
    if (!box) return;
    var show = !!this.pages[page];
    box.classList.toggle('hidden', !show);
    if (!show) this.hide();
  },

  hide: function () {
    var p = el('gsearchPanel');
    if (p) p.classList.add('hidden');
  },

  run: function (q) {
    var self = this;
    if (!q) { this.hide(); return; }
    var my = ++this.seq;
    var panel = el('gsearchPanel');
    panel.innerHTML = '<div class="gsearch-loading">搜索中…</div>';
    panel.classList.remove('hidden');
    app.api('/api/search?q=' + encodeURIComponent(q)).then(function (d) {
      if (my !== self.seq) return; // 过期响应丢弃
      self.render(d || {});
    }).catch(function (err) {
      if (my !== self.seq) return;
      var msg = err && err.message ? err.message : '请登录后重试';
      panel.innerHTML = '<div class="gsearch-empty">搜索不可用：' + esc(msg) + '</div>';
    });
  },

  render: function (d) {
    var panel = el('gsearchPanel');
    var words = d.words || [], docs = d.docs || [];
    this.lastWords = words;
    this.lastDocs = docs;
    var html = '';

    if (words.length) {
      html += '<div class="gsearch-group">词汇 · ' + words.length + '</div>';
      words.forEach(function (w, i) {
        var head = esc(w.kana || w.word);
        if (w.kanji) head += '（' + esc(w.kanji) + '）';
        var meta = [w.def, w.pos, w.sub, w.word_note, w.note]
          .filter(Boolean).map(esc).join(' · ');
        html += '<div class="gsearch-row clickable" data-kind="word" data-i="' + i + '">' +
          '<span class="gsearch-row-main">' + head + '<span class="gsearch-go">查看</span></span>' +
          '<span class="gsearch-row-sub">' + (meta || '&nbsp;') + '</span></div>';
      });
    }
    if (docs.length) {
      html += '<div class="gsearch-group">知识点 · ' + docs.length + '</div>';
      docs.forEach(function (doc, i) {
        var jump = !!doc.jump;
        html += '<div class="gsearch-row' + (jump ? ' clickable' : '') + '"' +
          (jump ? ' data-kind="doc" data-i="' + i + '"' : '') + '>' +
          '<span class="gsearch-row-main">' + esc(doc.title || '知识点') +
          (jump ? '<span class="gsearch-go">查看</span>' : '') + '</span>' +
          '<span class="gsearch-row-sub">' + esc(doc.text || '') + '</span></div>';
      });
    }
    if (!words.length && !docs.length) {
      html = '<div class="gsearch-empty">没有匹配的词汇或知识点</div>';
    } else {
      if (d.source === 'literal') {
        html += '<div class="gsearch-note">智能检索暂不可用，知识点已用字面匹配</div>';
      } else if (d.source === 'metainsight') {
        html += '<div class="gsearch-note">知识点来自智能检索（语义）</div>';
      }
    }
    panel.innerHTML = html;
    panel.classList.remove('hidden');
  },

  // —— 页内浮窗（2026-11-06）：点结果不再跳页，浮窗盖在练习页上展示详情，
  //    练习状态（做题进度 / 播放器）完全不丢。想主动跳走，浮窗里有按钮。——

  // 点词汇结果：浮窗展示词条。字段跟词汇总表同一套口径（释义只摆主部，
  // 括号那截进「备注」），但搜索只回精简字段，没有复习/错次/阶段。
  showWord: function (w) {
    this.hide();
    var head = esc(w.kana || w.word);
    if (w.kanji) head += '（' + esc(w.kanji) + '）';
    var kv = [];
    if (w.def) kv.push(['释义', esc(w.def) + (w.note ? '<span class="muted">（' + esc(w.note) + '）</span>' : '')]);
    if (w.pos) kv.push(['词性', esc(w.pos) + (w.sub ? '·' + esc(w.sub) : '')]);
    if (w.word_note) kv.push(['备注', esc(w.word_note)]);
    if (w.group) kv.push(['分组', esc(w.group)]);
    if (w.number) kv.push(['词号', '#' + w.number]);
    this.openModal(head, kv, {
      jumpText: '在词汇表查看',
      onJump: function () { search.gotoWord(w.kana || w.word); }
    });
  },

  // 点知识点结果：浮窗展示命中片段；jump 的给「打开完整知识点」按钮才跳页。
  showDoc: function (d) {
    this.hide();
    var body = esc(d.text || '（无摘要）').replace(/\n/g, '<br>');
    var opts = null;
    if (d.jump) {
      opts = {
        jumpText: '打开第' + d.lesson + '课完整内容',
        onJump: function () { search.gotoLesson(d.lesson); }
      };
    }
    this.openModal(esc(d.title || '知识点'), null, opts, body);
  },

  // openModal 开一个页内浮窗。kv = [["标签","HTML内容"],...]（词条用）；
  // bodyHtml 是自由 HTML（知识点片段用）。jumpOpts 有就多一个跳转按钮。
  // 复用 modal-mask 样式（z-index 60，压住搜索面板 45 和页面内容），点蒙层/ESC/关闭都关。
  openModal: function (titleHtml, kv, jumpOpts, bodyHtml) {
    this.closeModal();
    var mask = document.createElement('div');
    mask.className = 'modal-mask';
    mask.setAttribute('data-search-modal', '1');

    var inner = '';
    if (kv && kv.length) {
      inner = kv.map(function (r) {
        return '<div class="sm-kv"><span class="sm-k">' + esc(r[0]) + '</span>' +
          '<span class="sm-v">' + r[1] + '</span></div>';
      }).join('');
    } else if (bodyHtml) {
      inner = '<div class="sm-text">' + bodyHtml + '</div>';
    }
    var jumpBtn = '';
    if (jumpOpts && jumpOpts.jumpText) {
      jumpBtn = '<button class="ghost" data-act="jump">' + esc(jumpOpts.jumpText) + '</button>';
    }
    mask.innerHTML =
      '<div class="modal search-modal">' +
      '<h4>' + titleHtml + '</h4>' +
      '<div class="sm-body">' + inner + '</div>' +
      '<div class="row">' + jumpBtn + '<button class="primary" data-act="close">关闭</button></div>' +
      '</div>';
    document.body.appendChild(mask);
    this._mask = mask;

    var self = this;
    mask.addEventListener('click', function (e) {
      if (e.target === mask) { self.closeModal(); return; }
      var b = e.target;
      while (b && b !== mask && !(b.getAttribute && b.getAttribute('data-act'))) b = b.parentNode;
      if (!b || b === mask) return;
      var act = b.getAttribute('data-act');
      if (act === 'close') self.closeModal();
      else if (act === 'jump') { self.closeModal(); if (jumpOpts && jumpOpts.onJump) jumpOpts.onJump(); }
    });
  },

  closeModal: function () {
    if (this._mask) {
      if (this._mask.parentNode) this._mask.parentNode.removeChild(this._mask);
      this._mask = null;
    }
  },

  // 主动跳词汇表：把词填进词汇总表的筛选框再跳过去。
  // lexicon.load() 无论是否已 loaded 都会 render（会读 lexSearch.value），不用手动 render。
  gotoWord: function (kana) {
    var box = el('lexSearch');
    if (box) box.value = kana || '';
    this.hide();
    app.go('lexicon');
  },

  // 主动跳知识点：预设课号再跳页。
  // knowledge.load() 首次会读 localStorage 落在指定课；已 loaded 时它只 render
  // 不切课，所以这里补一次 loadLesson(n)。两条路径都能落到第 n 课。
  gotoLesson: function (n) {
    if (isNaN(n)) return;
    localStorage.setItem('jrp_kb_lesson', String(n));
    this.hide();
    app.go('knowledge');
    if (knowledge.loaded) knowledge.loadLesson(n);
  }
};
