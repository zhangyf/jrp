// web/search.js —— 五页右上角的全局搜索（词汇字面 + 知识点语义）。
//
// ⚠️ 顶层只挂一个 `var search`：web/*.js 是老式多 script 全局挂载，
// 顶层 function 会覆盖先加载文件里的同名 var（2026-09-21 整站白屏事故）。
// 所有逻辑都是对象方法或闭包内的函数表达式，绝不新增顶层符号。
//
// 数据源：GET /api/search?q=（服务端并行两路，挂 withUser，trial/匿名不给用）。
// 结果只在浮层里展示，不落 localStorage、不缓存全库，点结果跳对应页。
// 出现在老师点名的五页（今日练习/造句/钉子户/活用/听力）。听力页那个框
// 是边听边查词用的：跳到词汇表/知识点后回来，播放器不能丢（见 listening.load）。
// 不扩散到词汇/知识点/统计/离线。

var search = {
  // 允许出现搜索框的页（按老师点名的范围）。
  pages: { practice: 1, sentence: 1, hard: 1, conj: 1, listening: 1 },
  timer: null,
  seq: 0,        // 只认最后一次请求的响应，防快速输入时旧响应盖新结果
  lastWords: [],
  lastDocs: [],

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
      if (e.key === 'Escape' || e.key === 'Esc') { self.hide(); input.blur(); }
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
        if (w) self.gotoWord(w.kana || w.word);
      } else {
        var d = self.lastDocs[i];
        if (d && d.jump) self.gotoLesson(d.lesson);
      }
    });

    // 点面板/输入框之外收起；ESC 也能收。
    document.addEventListener('click', function (e) {
      if (!box.contains(e.target)) self.hide();
    });
    document.addEventListener('keydown', function (e) {
      if (e.key === 'Escape' || e.key === 'Esc') self.hide();
    });

    // 包装 app.go：任何切页路径都同步刷新显隐（含 gradeMode 重进当前页）。
    // 运行时给对象方法重新赋值，不是顶层 function 声明，不触发 09-21 那类覆盖。
    var origGo = app.go;
    app.go = function (page) {
      origGo.call(app, page);
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
          '<span class="gsearch-row-main">' + head + '<span class="gsearch-go">→ 词汇表</span></span>' +
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
          (jump ? '<span class="gsearch-go">→ 打开第' + doc.lesson + '课</span>' : '') + '</span>' +
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

  // 点词汇结果：把词填进词汇总表的筛选框再跳过去。
  // lexicon.load() 无论是否已 loaded 都会 render（会读 lexSearch.value），不用手动 render。
  gotoWord: function (kana) {
    var box = el('lexSearch');
    if (box) box.value = kana || '';
    this.hide();
    app.go('lexicon');
  },

  // 点知识点结果：预设课号再跳页。
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
