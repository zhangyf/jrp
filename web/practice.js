// 今日单词练习。
//
// 两种视图：
//   - 列表（默认）：一屏列出全部到期词，全写完点「对答案」统一判，再点「回写档案」。
//     错词会自动进第二轮，第二轮只巩固、不回写（见 list.js 头上的说明）。
//   - 卡片：一次一张，答错立刻推回队尾再练一遍。
// 两种视图共用同一套回写接口，语义一致：
//   空白 = 没写 = 完全不处理；点「不会」= 按答错处理，跟写错一模一样。

var practice = {
  // --- 卡片模式状态 ---
  words: [],
  queue: [],
  pos: 0,
  results: {},   // number -> correct（只记首次作答）
  unknowns: {},  // number -> true（老师点了「不会」；提交时仍是 correct:false）
  date: '',

  // --- 列表模式 ---
  list: null,

  boot: function () {
    this.list = ListPractice({
      prefix: 'pl',
      mount: el('practiceList'),
      bar: el('practiceBar'),
      count: el('practiceCount'),
      gradeBtn: el('practiceGrade'),
      commitBtn: el('practiceCommit'),
      requeueBtn: el('practiceRequeue'),
      msg: el('practiceMsg'),
      note: el('practiceNote'),
      tomorrow: el('tomorrowBox'),
      hard: false,
      draftMode: 'words'
    });

    el('practiceRequeue').addEventListener('click', function () {
      if (this.list.pendingWrong.length) this.list.requeue(this.list.pendingWrong);
    }.bind(this));
  },

  // 今天已经练完（回写后到期日被推到未来，/api/plan 就空了）→ 拉当天快照只读回看。
  // 只有列表视图支持，卡片是逐卡的，没有「一屏回看」这回事。
  showReview: function () {
    var self = this;
    var empty = function () {
      el('practiceSummary').innerHTML = '<span class="chip">今天没有到期的词</span>';
    };
    if (app.view() !== 'list') { empty(); return; }

    el('practiceSummary').innerHTML = '<span class="chip muted">加载中…</span>';
    app.api('/api/review?mode=words').then(function (d) {
      var r = d.review;
      if (!r || !r.items || !r.items.length) { empty(); return; }

      var done = r.items.filter(function (i) { return !i.blank; });
      var ok = done.filter(function (i) { return i.correct; }).length;
      renderSummary(el('practiceSummary'), [
        { label: '今天已练完', value: r.items.length },
        { label: '对', value: ok },
        { label: '错', value: done.length - ok, warn: done.length - ok > 0 }
      ]);

      self.date = r.date || self.date;
      self.list.setReview(self.date, r.items);
      self.list.render();   // render 内部识别 reviewOnly，自动进只读态
      if (r.saved_at) {
        el('practiceMsg').className = 'feedback ok';
        el('practiceMsg').textContent = '今天这轮已回写（' + r.saved_at + '），下面是只读回看';
      }
    }).catch(function () { empty(); });
  },

  load: function () {
    var self = this;
    el('practiceCard').classList.add('hidden');
    el('practiceDone').classList.add('hidden');
    el('practiceList').classList.add('hidden');
    el('practiceBar').classList.add('hidden');
    el('practiceMsg').textContent = '';
    el('tomorrowBox').innerHTML = '';
    el('practiceSummary').innerHTML = '<span class="chip muted">加载中…</span>';

    app.api('/api/plan?mode=words').then(function (d) {
      self.date = d.date;
      self.words = d.words || [];
      if (el('nailsOnly').checked) {
        self.words = self.words.filter(function (w) { return w.status === '☠️钉子户'; });
      }
      if (!self.words.length) {
        // 今天已经练完（回写后到期日被推到未来）→ 拉当天快照只读回看，
        // 否则老师练完就再也看不到自己写了什么。
        self.showReview();
        return;
      }
      var chips = [{ label: '到期', value: self.words.length }];
      var by = d.by_status || {};
      ['☠️钉子户', '🔴待巩固', '🔄待测试', '🟡基本掌握', '🟢抽查'].forEach(function (k) {
        if (by[k]) chips.push({ label: statusText(k), value: by[k], warn: k === '☠️钉子户' });
      });
      renderSummary(el('practiceSummary'), chips);

      if (app.view() === 'list') {
        self.list.set(self.date, self.words);
        self.list.render();
        self.list.loadDraft();   // 有上次没写完的草稿就回填
        return;
      }
      self.startCard();
    }).catch(function (e) {
      el('practiceSummary').innerHTML = '<span class="chip warn">' + esc(e.message) + '</span>';
    });
  },

  // ---------------- 卡片模式 ----------------

  startCard: function () {
    this.queue = this.words.map(function (_, i) { return i; });
    this.pos = 0;
    this.results = {};
    this.unknowns = {};
    el('practiceDone').classList.add('hidden');
    el('practiceCard').classList.remove('hidden');
    this.render();
    el('wInput').focus();
  },

  render: function () {
    if (this.pos >= this.queue.length) return this.finish();
    var w = this.words[this.queue[this.pos]];
    el('wProgress').textContent = '第 ' + (this.pos + 1) + ' / ' + this.queue.length + '　#' + w.number;
    var tag = el('wStatus');
    tag.textContent = statusText(w.status);
    tag.className = 'tag' + (w.status === '☠️钉子户' ? ' nail' : '');
    el('wDefinition').textContent = w.definition;
    el('wInput').value = '';
    el('wFeedback').textContent = '';
    el('wFeedback').className = 'feedback';
    el('wInput').focus();
  },

  submit: function () {
    if (this.pos >= this.queue.length) return;
    var w = this.words[this.queue[this.pos]];
    // 空白不再是「静默记成错」—— 那是跟列表模式相反的旧行为。
    // 不会就点「不会」，不想练就点「跳过」。
    if (!String(el('wInput').value).trim()) {
      app.toast('还没写：不会就点「不会」，不想练点「跳过」');
      el('wInput').focus();
      return;
    }
    var ok = gradeAnswer(el('wInput').value, w.word, app.mode());

    // 只记首次作答
    if (!(w.number in this.results)) {
      this.results[w.number] = ok;
      if (ok) delete this.unknowns[w.number];
    }

    var fb = el('wFeedback');
    if (ok) {
      fb.className = 'feedback ok';
      fb.textContent = '✓ 对了';
      var self = this;
      setTimeout(function () { self.pos++; self.render(); }, 320);
    } else {
      fb.className = 'feedback no';
      fb.innerHTML = '✗ 正确答案：<span class="answer">' + esc(w.word) + '</span>　（已重新入队，稍后再练一次）';
      // 答错立即重练：推回队尾
      var idx = this.queue[this.pos];
      this.queue.push(idx);
      var self2 = this;
      setTimeout(function () { self2.pos++; self2.render(); }, 900);
    }
  },

  // 「不会」= 答错，跟写错完全一样：记 false、重新入队、稍后再练。
  unknown: function () {
    if (this.pos >= this.queue.length) return;
    var w = this.words[this.queue[this.pos]];
    if (!(w.number in this.results)) this.results[w.number] = false;
    this.unknowns[w.number] = true;

    var fb = el('wFeedback');
    fb.className = 'feedback unknown-fb';
    fb.innerHTML = '<span class="unk-tag">不会</span>（按答错记入档案）　正确答案：<span class="answer">' +
      esc(w.word) + '</span>　（已重新入队，稍后再练一次）';
    var idx = this.queue[this.pos];
    this.queue.push(idx);
    var self = this;
    setTimeout(function () { self.pos++; self.render(); }, 900);
  },

  reveal: function () {
    if (this.pos >= this.queue.length) return;
    var w = this.words[this.queue[this.pos]];
    el('wFeedback').className = 'feedback';
    el('wFeedback').innerHTML = '答案：<span class="answer">' + esc(w.word) + '</span>（跳过不计分）';
    var self = this;
    setTimeout(function () { self.pos++; self.render(); }, 900);
  },

  skip: function () {
    this.pos++;
    this.render();
  },

  finish: function () {
    el('practiceCard').classList.add('hidden');
    var box = el('practiceDone');
    box.classList.remove('hidden');
    var correct = 0, wrong = 0, unk = 0;
    for (var k in this.results) {
      if (this.results[k]) correct++;
      else { wrong++; if (this.unknowns[k]) unk++; }
    }
    el('practiceResult').innerHTML =
      '<div class="card"><h3>本轮完成</h3><div class="kv">' +
      '<span class="chip">正确 <b>' + correct + '</b></span>' +
      '<span class="chip">错误 <b>' + (wrong - unk) + '</b></span>' +
      '<span class="chip">不会 <b>' + unk + '</b></span>' +
      '<span class="chip">未作答 <b>' + (this.words.length - correct - wrong) + '</b></span>' +
      '</div><p class="muted">空白 = 没写，按老规矩完全不处理，不会记进档案。' +
      '点「不会」= 按答错记进档案，这里单列出来。</p></div>';
    el('commitResult').textContent = '';
  },

  commitCard: function () {
    var self = this;
    var build = function () {
      var wr = [];
      for (var k in self.results) wr.push({ number: parseInt(k, 10), correct: self.results[k] });
      return wr;
    };
    var go = function () {
      var wr = build();
      if (!wr.length) { app.toast('没有可回写的结果'); return; }
      self.sendCard(wr);
    };

    var pend = this.words.length - Object.keys(this.results).length;
    if (!pend) { go(); return; }
    UNK.ask({
      title: '还有 ' + pend + ' 个词没练也没标「不会」',
      lines: [
        '这些词不判分、不进档案。',
        '点「全部标记为不会」会把这 ' + pend + ' 个词各记一次错（明天会再出现）。'
      ],
      skipText: '跳过',
      allText: '全部标记为不会',
      onSkip: go,
      onMarkAll: function () {
        self.words.forEach(function (w) {
          if (!(w.number in self.results)) {
            self.results[w.number] = false;
            self.unknowns[w.number] = true;
          }
        });
        go();
      }
    });
  },

  sendCard: function (wr) {
    var self = this;
    var btn = el('practiceCommitCard');
    btn.disabled = true;
    app.api('/api/record', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        plan_date: self.date,
        hard: false,
        word_results: wr,
        sentence_results: []
      })
    }).then(function (d) {
      btn.disabled = false;
      var w = d.words || {};
      var msg = '已回写：正确 ' + (w.correct || 0) + '，错误 ' + (w.wrong || 0) +
        '，档案 ' + (w.version || '') + '（原 ' + (w.old_filename || '') + '）';
      el('commitResult').className = 'feedback ok';
      el('commitResult').textContent = msg;
      renderTomorrow(el('tomorrowBoxCard'), d.tomorrow);
      app.toast('已回写');
    }).catch(function (e) {
      btn.disabled = false;
      el('commitResult').className = 'feedback no';
      el('commitResult').textContent = '回写失败：' + e.message;
    });
  }
};

// 明日预览。append=true 时追加到已有内容后面（离线回填报复用同一个节点）。
function renderTomorrow(node, t, append) {
  if (!t || t.error) { if (!append) node.innerHTML = ''; return; }
  var by = t.by_status || {};
  var parts = ['☠️钉子户', '🔴待巩固', '🔄待测试', '🟡基本掌握', '🟢抽查']
    .filter(function (k) { return by[k]; })
    .map(function (k) { return statusText(k) + ' ' + by[k]; });
  var html = '<div class="card"><h3>明天（' + esc(t.date) + '）到期 ' +
    '<b>' + t.due_count + '</b> 词</h3>' +
    (parts.length ? '<div class="kv"><span class="chip">' + esc(parts.join('　')) + '</span></div>' : '') +
    '<p class="muted">这是按艾宾浩斯间隔算出来的，答对会拉长间隔，答错会缩短。</p></div>';
  if (append) node.innerHTML += html; else node.innerHTML = html;
}

document.addEventListener('DOMContentLoaded', function () {
  el('wSubmit').addEventListener('click', function () { practice.submit(); });
  el('wInput').addEventListener('keydown', function (e) {
    if (e.isComposing || e.keyCode === 229) return; // IME 确认转换的 Enter 不当提交
    if (e.key === 'Enter') practice.submit();
  });
  el('wUnknown').addEventListener('click', function () { practice.unknown(); });
  el('wReveal').addEventListener('click', function () { practice.reveal(); });
  el('wSkip').addEventListener('click', function () { practice.skip(); });
  el('practiceCommitCard').addEventListener('click', function () { practice.commitCard(); });
  el('nailsOnly').addEventListener('change', function () { practice.load(); });

  el('practiceGrade').addEventListener('click', function () { practice.list.gradeAll(); });
  el('practiceCommit').addEventListener('click', function () { practice.list.commit(); });
});
