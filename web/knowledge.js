// 新标日知识点（web 端 2026-11-03 补：小程序有这块，web 那排导航里没有）。
//
// 三条口径，改之前先读：
//
// 1. **markdown 不在这里解析**。服务端已经把 md 拆成结构化块（h1/h2/h3/p/quote/
//    list/table）发下来了，这里只按 type 摆样式。拆块只能在服务端干 ——
//    「哪几块模糊」必须按块切，前端拿到的已经是块。
//
// 2. **未登录也能进，只是看不全**。/api/knowledge 对匿名放行，返回一个 visible
//    数字：下标 >= visible 的块盖模糊。这是展示层的软限制（抓包能拿全文），
//    目的是引导登录，不是防攻击。
//
// 3. **登录后要能当场解开**：每次切课 / 重新进页面都重新拉一遍，不用额外同步。
//
// ⚠️ 全局只留一个顶层符号 knowledge：web/*.js 是老式多 script 全局挂载，
// 顶层 function 会覆盖先加载文件里的同名 var（2026-09-21 出过整站白屏事故）。

var knowledge = {
  lessons: [],   // [1,2,...,21]
  cur: 0,
  title: '',
  blocks: [],
  visible: 0,    // 未登录能看到几块（>= visible 的块盖模糊）
  locked: false, // 服务端说的：这次是不是只给了部分
  loaded: false,

  load: function () {
    var self = this;
    if (this.loaded) { this.render(); return; }
    el('kbBody').innerHTML = '<p class="muted">加载中…</p>';
    app.api('/api/knowledge').then(function (d) {
      self.lessons = (d.lessons || []).map(function (l) { return l.lesson; });
      if (!self.lessons.length) {
        el('kbBody').innerHTML = '<p class="muted">还没有知识点文档</p>';
        return;
      }
      self.bind();
      self.loaded = true;
      // 第一次进落在最新一课（当前进度），之后按上次看到的课
      var last = parseInt(localStorage.getItem('jrp_kb_lesson') || '', 10);
      var cur = self.lessons.indexOf(last) >= 0 ? last : self.lessons[self.lessons.length - 1];
      self.loadLesson(cur);
    }).catch(function (e) {
      self.fail(e);
    });
  },

  // 提示行（页面底部那条）。加载失败、空态都走这里。
  msg: function (text, bad) {
    var box = el('kbMsg');
    box.textContent = text;
    box.className = 'feedback' + (bad ? ' no' : '');
  },

  fail: function (e) {
    el('kbBody').innerHTML = '';
    this.msg('加载失败：' + (e && e.message ? e.message : '未知错误'), true);
  },

  bind: function () {
    var self = this;
    el('kbPrev').addEventListener('click', function () { self.step(-1); });
    el('kbNext').addEventListener('click', function () { self.step(1); });
  },

  loadLesson: function (n) {
    var self = this;
    this.cur = n;
    localStorage.setItem('jrp_kb_lesson', String(n));
    el('kbBody').innerHTML = '<p class="muted">加载中…</p>';
    this.msg('');
    app.api('/api/knowledge?lesson=' + n).then(function (d) {
      self.title = d.title || ('第' + n + '课');
      self.blocks = d.blocks || [];
      self.locked = !!d.locked;
      self.visible = d.locked ? (d.visible || 0) : self.blocks.length;
      self.render();
      window.scrollTo(0, 0);
    }).catch(function (e) {
      self.fail(e);
    });
  },

  step: function (d) {
    var i = this.lessons.indexOf(this.cur);
    var j = i + d;
    if (i < 0 || j < 0 || j >= this.lessons.length) return;
    this.loadLesson(this.lessons[j]);
  },

  render: function () {
    var self = this;

    el('kbTitle').textContent = this.title;
    el('kbCount').textContent = '共 ' + this.lessons.length + ' 课';

    // 课号条：21 个 chip 一行放不下，靠 flex-wrap 自己折行（web 没有 scroll-view）
    el('kbLessons').innerHTML = this.lessons.map(function (n) {
      return '<button class="kb-n' + (n === self.cur ? ' on' : '') +
        '" data-n="' + n + '">' + n + '</button>';
    }).join('');
    Array.prototype.forEach.call(el('kbLessons').querySelectorAll('.kb-n'), function (b) {
      b.addEventListener('click', function () {
        var n = parseInt(b.dataset.n, 10);
        if (n !== self.cur) self.loadLesson(n);
      });
    });

    renderSummary(el('kbSummary'), [
      { label: '当前', value: '第' + this.cur + '课' },
      { label: '段落', value: this.blocks.length },
      { label: '完整版', value: this.locked ? '未登录' : '已解锁', warn: this.locked }
    ]);

    var html = '';
    if (this.locked) {
      html += '<div class="kb-lockbar"><span>未登录 · 本课只展示开头一部分</span>' +
        '<a class="kb-lockbtn" href="login.html">微信登录看完整版</a></div>';
    }
    this.blocks.forEach(function (b, i) {
      // 第一条被挡的块前面插一句，别让人以为页面坏了
      if (self.locked && i === self.visible) {
        html += '<p class="kb-cut">—— 以下为完整版内容，登录后可见 ——</p>';
      }
      html += '<div class="kb-blk' + (i >= self.visible ? ' blur' : '') + '">' +
        self.blockHtml(b) + '</div>';
    });
    if (this.locked) {
      html += '<p class="kb-foot"><a class="kb-lockbtn wide" href="login.html">微信登录看完整版</a></p>';
    }
    el('kbBody').innerHTML = html;

    el('kbPrev').disabled = this.lessons[0] === this.cur;
    el('kbNext').disabled = this.lessons[this.lessons.length - 1] === this.cur;
  },

  blockHtml: function (b) {
    var self = this;
    if (b.type === 'table') {
      var head = (b.header || []).map(function (c) {
        return '<th>' + esc(c) + '</th>';
      }).join('');
      var rows = (b.rows || []).map(function (r) {
        return '<tr>' + (r || []).map(function (c) {
          return '<td>' + esc(c) + '</td>';
        }).join('') + '</tr>';
      }).join('');
      return '<div class="kb-tbl-wrap"><table class="kb-table"><thead><tr>' +
        head + '</tr></thead><tbody>' + rows + '</tbody></table></div>';
    }
    if (b.type === 'list') {
      // 有序列表直接用 <ol>：序号交给浏览器，不用像小程序那样在 JS 里算好
      var tag = b.ordered ? 'ol' : 'ul';
      var items = (b.items || []).map(function (it) {
        return '<li>' + self.spansHtml(it.spans) + '</li>';
      }).join('');
      return '<' + tag + ' class="kb-list">' + items + '</' + tag + '>';
    }
    var inner = this.spansHtml(b.spans);
    if (b.type === 'h1') return '<h3 class="kb-h1">' + inner + '</h3>';
    if (b.type === 'h2') return '<h4 class="kb-h2">' + inner + '</h4>';
    if (b.type === 'h3') return '<h5 class="kb-h3">' + inner + '</h5>';
    if (b.type === 'quote') return '<blockquote class="kb-quote">' + inner + '</blockquote>';
    return '<p class="kb-p">' + inner + '</p>';
  },

  // 行内片段：b = 加粗、c = 等宽（服务端已经把 ** 和 ` 拆成标记了，
  // 前端不用再解析 markdown）
  spansHtml: function (spans) {
    return (spans || []).map(function (sp) {
      var t = esc(sp.t);
      if (sp.c) return '<code>' + t + '</code>';
      if (sp.b) return '<b>' + t + '</b>';
      return t;
    }).join('');
  }
};
