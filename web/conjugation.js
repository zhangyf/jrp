// 活用专项练习（2026-09-28 老师新增）。
//
// 老师的需求原话：「给出列表中的原始动词和形容词，我来写出活用后的形式」。
//  - 动词：一张卡两个空 —— 原形 + て形（做法 A，一张卡把一个词的两种变形一起过）
//  - 形容词：一张卡三个空 —— 过去式 / 否定形 / 过去否定形（都带「です」）
//
// ⚠️ 这一页只练不记档：不调 /api/record、不碰复习档案、不影响 ReviewCount 和
// 正确率，也不写草稿。答错了当场给正确答案，纯自测。
//
// 答案与判分全部在服务端算好（见 api_conjugation.go）：服务端返回
// { label, answers:[...] }，前端把 answers 用「/」连起来喂给 gradeAnswer，
// 走「任一档」—— 写假名或汉字都算对。变形规则只有一份，不许在前端复制一遍。

var conj = {
  items: [],
  pool: 0,
  type: 'verb',
  graded: false,

  boot: function () {
    el('conjType').addEventListener('change', function () {
      conj.type = this.value;
      conj.load();
    });
    el('conjCount').addEventListener('change', function () { conj.load(); });
    el('conjReload').addEventListener('click', function () { conj.load(); });
    el('conjGrade').addEventListener('click', function () { conj.grade(); });
    el('conjClear').addEventListener('click', function () { conj.clear(); });
  },

  load: function () {
    var self = this;
    this.type = el('conjType').value;
    this.graded = false;
    this.items = [];
    el('conjBody').innerHTML = '';
    el('conjMsg').textContent = '';
    el('conjMsg').className = 'feedback';
    el('conjSummary').innerHTML = '<span class="chip muted">加载中…</span>';

    var n = el('conjCount').value || '10';
    app.api('/api/conjugation?type=' + encodeURIComponent(this.type) + '&count=' + n)
      .then(function (d) {
        self.items = d.items || [];
        self.pool = d.pool || self.items.length;
        if (!self.items.length) {
          el('conjSummary').innerHTML = '<span class="chip">没有可出的题</span>';
          return;
        }
        renderSummary(el('conjSummary'), [
          { label: '本次', value: self.items.length + ' 题' },
          { label: '可用词池', value: self.pool },
          { label: '类型', value: self.type === 'verb' ? '动词' : '形容词' }
        ]);
        el('conjPool').textContent = '词池 ' + self.pool + ' 个词（随机抽）';
        self.render();
      })
      .catch(function (e) {
        el('conjSummary').innerHTML = '<span class="chip warn">' + esc(e.message) + '</span>';
      });
  },

  render: function () {
    var html = this.items.map(function (it, i) {
      var tasks = it.tasks.map(function (t, j) {
        return '<label class="conj-task">' + esc(t.label) +
          '<input class="conj-input" data-i="' + i + '" data-t="' + j + '"' +
          ' type="text" autocomplete="off" spellcheck="false"></label>';
      }).join('');
      return '<div class="conj-card" id="conjcard-' + i + '">' +
        '<div class="conj-head">' +
          '<span class="conj-word">' + esc(it.word) + '</span>' +
          // 答题前只给大类（动词/形容词），小类（一类・二类…）先藏着，
          // 点「对答案」时才补出来 —— 见 grade()。
          '<span class="tag" id="conjpos-' + i + '" title="对答案后显示分类">' +
            esc(app.posText(it, true)) + '</span>' +
          '<span class="muted">' + esc(app.defParts(it.definition).main) + '</span>' +
        '</div>' +
        '<div class="conj-tasks">' + tasks + '</div>' +
        '<div class="conj-feedback feedback" id="conjfb-' + i + '"></div>' +
        '</div>';
    }).join('');
    el('conjBody').innerHTML = html;
    this.bindKeys();
    var first = document.querySelector('.conj-input');
    if (first) first.focus();
  },

  // 回车 = 跳到下一个空（不是提交）。IME 确认候选词的那次回车必须放过去，
  // 否则会把没确认完的合成串带进下一个输入框 —— 列表模式踩过这个坑（list.js）。
  bindKeys: function () {
    var inputs = Array.prototype.slice.call(document.querySelectorAll('.conj-input'));
    inputs.forEach(function (inp, idx) {
      inp.addEventListener('keydown', function (e) {
        if (e.isComposing || e.keyCode === 229) return; // IME 确认转换的 Enter
        if (e.key !== 'Enter') return;
        e.preventDefault();
        if (idx + 1 < inputs.length) inputs[idx + 1].focus();
        else conj.grade();
      });
    });
  },

  values: function () {
    var out = [];
    this.items.forEach(function (_, i) {
      var arr = [];
      document.querySelectorAll('.conj-input[data-i="' + i + '"]').forEach(function (inp) {
        arr.push(inp.value);
      });
      out.push(arr);
    });
    return out;
  },

  grade: function () {
    var self = this;
    var vals = this.values();
    var ok = 0, bad = 0, blank = 0;

    this.items.forEach(function (it, i) {
      var fb = [];
      var cardClass = 'conj-card';
      it.tasks.forEach(function (t, j) {
        var v = vals[i][j];
        if (!String(v || '').trim()) {
          blank++;
          fb.push('<span class="muted">' + esc(t.label) + '：未写</span>');
          return;
        }
        // 多写法用「/」分隔交给 gradeAnswer；任一档 = 假名/汉字都认
        var good = gradeAnswer(v, t.answers.join('/'), 'either');
        if (good) {
          ok++;
          fb.push('<span class="conj-ok">' + esc(t.label) + ' ✓</span>');
        } else {
          bad++;
          cardClass = 'conj-card bad';
          fb.push('<span class="conj-no">' + esc(t.label) + ' ✗ 应为 ' +
            '<span class="answer">' + esc(t.answers.join(' / ')) + '</span></span>');
        }
      });
      el('conjcard-' + i).className = cardClass;
      // 对完答案才把小类显示出来（动词一类/二类/三类、形容词い形/な形）
      var tg = el('conjpos-' + i);
      if (tg) { tg.textContent = app.posText(it); tg.title = ''; }
      var box = el('conjfb-' + i);
      box.className = 'conj-feedback feedback ' + (fb.some(function (s) { return s.indexOf('conj-no') >= 0; }) ? 'no' : 'ok');
      box.innerHTML = fb.join('　');
    });

    this.graded = true;
    el('conjMsg').className = 'feedback ' + (bad ? 'no' : (blank ? '' : 'ok'));
    var tail = '';
    if (bad) tail = '　（错了的看红字，改完再点一次「对答案」）';
    else if (blank) tail = '　（没写的空格不判分）';
    else tail = '　全对 🎉';
    el('conjMsg').textContent = '对 ' + ok + (bad ? '　错 ' + bad : '') +
      (blank ? '　未写 ' + blank : '') + tail;
  },

  clear: function () {
    var self = this;
    document.querySelectorAll('.conj-input').forEach(function (inp) { inp.value = ''; });
    this.items.forEach(function (_, i) {
      el('conjcard-' + i).className = 'conj-card';
      // 清空重做 = 回到没看答案的状态，小类重新藏起来
      var tg = el('conjpos-' + i);
      if (tg) {
        tg.textContent = app.posText(self.items[i], true);
        tg.title = '对答案后显示分类';
      }
      var box = el('conjfb-' + i);
      box.className = 'conj-feedback feedback';
      box.innerHTML = '';
    });
    el('conjMsg').textContent = '';
    el('conjMsg').className = 'feedback';
    var first = document.querySelector('.conj-input');
    if (first) first.focus();
  }
};

document.addEventListener('DOMContentLoaded', function () {
  conj.boot();
});
