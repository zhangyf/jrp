// 钉子户专项：不做到期过滤，把正确率低于阈值的词全拉出来重练。
//
// 两种视图（和今日练习一致）：列表（默认）/ 卡片，见 practice.js 头上的说明。
//
// 关键：这里的号码是「钉子户 plan」的号码，不是当天 plan 的号码，
// 所以回写必须带 hard:true，否则服务端拿当天 plan 去对号，全部 not_found。

var hard = {
  // --- 卡片模式状态 ---
  words: [],
  queue: [],
  pos: 0,
  results: {},
  unknowns: {},  // number -> true（老师点了「不会」；提交时仍是 correct:false）
  answers: {},   // number -> 老师写的答案（首次），回看要显示「你写的」
  submitted: {}, // number -> true（已回写过；部分提交后再练再提交时只发没回写过的）
  date: '',

  // --- 列表模式 ---
  list: null,

  boot: function () {
    this.list = ListPractice({
      prefix: 'hl',
      mount: el('hardList'),
      bar: el('hardBar'),
      count: el('hardCount'),
      gradeBtn: el('hardGrade'),
      commitBtn: el('hardCommit'),
      msg: el('hardMsg'),
      note: el('hardNote'),
      tomorrow: el('hardTomorrowBox'),
      hard: true,
      draftMode: 'hard',
      // 与练习页同一张回看卡（renderReviewCard，见 app.js）
      reviewBox: el('hardListReview'),
      reviewBtnId: 'hardListRequeue'
    });
  },

  // 只读回看渲染。快照由调用方取好再传进来。
  renderReview: function (r) {
    var done = r.items.filter(function (i) { return !i.blank; });
    var ok = done.filter(function (i) { return i.correct; }).length;
    renderSummary(el('hardSummary'), [
      { label: '今天已练完', value: r.items.length },
      { label: '对', value: ok },
      { label: '错', value: done.length - ok, warn: done.length - ok > 0 }
    ]);
    this.date = r.date || this.date;
    this.list.setReview(this.date, r.items);
    this.list.savedAt = r.saved_at || '';
    this.list.render();   // 回看卡在 enterReview 里画
    if (r.saved_at) {
      el('hardMsg').className = 'feedback ok';
      el('hardMsg').textContent = '今天这轮已回写（' + r.saved_at + '），下面是只读回看';
    }
  },

  load: function () {
    var self = this;
    el('hardCard').classList.add('hidden');
    el('hardDone').classList.add('hidden');
    el('hardList').classList.add('hidden');
    el('hardListReview').classList.add('hidden');
    el('hardBar').classList.add('hidden');
    el('hardMsg').textContent = '';
    el('hardTomorrowBox').innerHTML = '';
    el('hardSummary').innerHTML = '<span class="chip muted">加载中…</span>';

    // 钉子户不做到期过滤，/api/hard 每次都返回同一批词。
    // 所以先查今天有没有已回写的快照：有的话直接只读回看 —— 否则老师会
    // 不知不觉把同一批词再练一遍，一回写就是二次改档案（间隔被改两次）。
    var first = app.view() === 'list'
      ? app.api('/api/review?mode=hard')
      : Promise.resolve({ review: null });

    first.then(function (rd) {
      if (rd && rd.review && rd.review.items && rd.review.items.length) {
        self.renderReview(rd.review);
        return null;   // 后面的 then 收到 null 就什么都不做
      }
      return app.api('/api/hard');
    }).then(function (d) {
      if (!d) return;
      self.date = d.date;
      self.words = d.words || [];
      self.submitted = {};   // 新一轮，回写记录清零
      self.answers = {};
      if (!self.words.length) {
        el('hardSummary').innerHTML =
          '<span class="chip">没有钉子户（正确率 &lt; ' + d.min_accuracy +
          ' 且复习 ≥ ' + d.min_reviews + ' 次才算）</span>';
        return;
      }
      renderSummary(el('hardSummary'), [
        { label: '钉子户', value: self.words.length, warn: true },
        { label: '阈值', value: '正确率<' + d.min_accuracy + ' / 复习≥' + d.min_reviews }
      ]);

      if (app.view() === 'list') {
        self.list.set(self.date, self.words);
        self.list.render();
        self.list.loadDraft();   // 有上次没写完的草稿就回填
        return;
      }
      self.startCard();
    }).catch(function (e) {
      el('hardSummary').innerHTML = '<span class="chip warn">' + esc(e.message) + '</span>';
    });
  },

  // ---------------- 卡片模式 ----------------

  startCard: function () {
    this.queue = this.words.map(function (_, i) { return i; });
    this.pos = 0;
    this.results = {};
    this.unknowns = {};
    el('hardDone').classList.add('hidden');
    el('hardCard').classList.remove('hidden');
    this.render();
    el('hInput').focus();
  },

  render: function () {
    if (this.pos >= this.queue.length) return this.finish();
    var w = this.words[this.queue[this.pos]];
    el('hProgress').textContent = '第 ' + (this.pos + 1) + ' / ' + this.queue.length + '　#' + w.number;
    el('hMeta').textContent = metaText(w);
    // 与今日练习卡片同一套：释义主部 + 词性/备注（见 app.js 的 factsHtml）
    var parts = app.defParts(w.definition);
    el('hDefinition').textContent = parts.main;
    el('hFacts').innerHTML = app.factsHtml(w, parts.note);
    el('hInput').value = '';
    el('hFeedback').textContent = '';
    el('hFeedback').className = 'feedback';
    el('hInput').focus();
  },

  submit: function () {
    if (this.pos >= this.queue.length) return;
    var w = this.words[this.queue[this.pos]];
    // 空白不再是「静默记成错」—— 那是跟列表模式相反的旧行为
    if (!String(el('hInput').value).trim()) {
      app.toast('还没写：不会就点「不会」，不想练点「跳过」');
      el('hInput').focus();
      return;
    }
    var typed = String(el('hInput').value).trim();
    var ok = gradeAnswer(typed, w.word, app.mode());
    if (!(w.number in this.results)) {
      this.results[w.number] = ok;
      if (ok) delete this.unknowns[w.number];
    }
    if (!(w.number in this.answers)) this.answers[w.number] = typed;

    var fb = el('hFeedback');
    if (ok) {
      fb.className = 'feedback ok';
      fb.textContent = '✓ 对了';
      var self = this;
      setTimeout(function () { self.pos++; self.render(); }, 320);
    } else {
      fb.className = 'feedback no';
      fb.innerHTML = '✗ 正确答案：<span class="answer">' + esc(w.word) + '</span>　（已重新入队）';
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
    this.answers[w.number] = '';   // 没写答案，回看显示「不会」

    var fb = el('hFeedback');
    fb.className = 'feedback unknown-fb';
    fb.innerHTML = '<span class="unk-tag">不会</span>（按答错记入档案）　正确答案：<span class="answer">' +
      esc(w.word) + '</span>　（已重新入队）';
    var idx = this.queue[this.pos];
    this.queue.push(idx);
    var self = this;
    setTimeout(function () { self.pos++; self.render(); }, 900);
  },

  reveal: function () {
    if (this.pos >= this.queue.length) return;
    var w = this.words[this.queue[this.pos]];
    el('hFeedback').className = 'feedback';
    el('hFeedback').innerHTML = '答案：<span class="answer">' + esc(w.word) + '</span>（跳过不计分）';
    var self = this;
    setTimeout(function () { self.pos++; self.render(); }, 900);
  },

  skip: function () { this.pos++; this.render(); },

  finish: function () {
    el('hardCard').classList.add('hidden');
    el('hardDone').classList.remove('hidden');
    var correct = 0, wrong = 0, unk = 0;
    for (var k in this.results) {
      if (this.results[k]) correct++;
      else { wrong++; if (this.unknowns[k]) unk++; }
    }
    el('hardCommitResult').className = 'feedback';
    el('hardCommitResult').textContent =
      '本轮完成：正确 ' + correct + '，错误 ' + (wrong - unk) + '，不会 ' + unk +
      '，未作答 ' + (this.words.length - correct - wrong) + '　（未作答不回写）';
  },

  commitCard: function () {
    var self = this;
    // 只发「练过但还没回写」的（部分提交后再提交时不重复计数，见 practice.js 同款注释）
    var build = function () {
      var wr = [];
      for (var k in self.results) {
        if (self.submitted[k]) continue;
        wr.push({ number: parseInt(k, 10), correct: self.results[k] });
      }
      return wr;
    };
    var go = function () {
      var wr = build();
      if (!wr.length) { app.toast('没有新的结果可回写'); return; }
      self.sendCard(wr);
    };

    var pend = this.words.length - Object.keys(this.results).length;
    if (!pend) { go(); return; }
    var done = Object.keys(this.results).length;
    UNK.ask({
      title: '还有 ' + pend + ' 个词没练',
      lines: [
        '已练的 ' + done + ' 个照常回写；没练的 ' + pend + ' 个不判分、不进档案，下次打开还在。',
        '想让它们明天一定再出现，才点「标记为不会」——那会给每个词记一次错。'
      ],
      mainText: '只提交已练的 ' + done + ' 个',
      subText: '剩下的 ' + pend + ' 个标记为不会',
      cancelText: '返回继续练',
      onMain: go,
      onSub: function () {
        self.words.forEach(function (w) {
          if (!(w.number in self.results)) {
            self.results[w.number] = false;
            self.unknowns[w.number] = true;
          }
        });
        go();
      },
      onCancel: function () { app.toast('没提交，接着练'); }
    });
  },

  // 交给服务端存当天快照的详情。只发本批这些词，服务端按题号 merge，
  // 前面几批提交的错词会保留 —— 一天分几次提交时错词要能累积。
  cardReviewItems: function () {
    var self = this;
    return self.words.map(function (w) {
      var n = w.number;
      var done = (n in self.results);
      return {
        number: n, word: w.word, definition: w.definition || '',
        group: w.group || '', status: w.status || '',
        answer: self.answers[n] || '',
        correct: done ? !!self.results[n] : false,
        unknown: !!self.unknowns[n],
        blank: !done
      };
    });
  },

  // 回写成功后刷新回看：拉服务端合并后的当天快照，
  // 分几次提交时每次都能看到累积起来的全部错词（不是只剩最后一批）。
  reloadCardReview: function () {
    var self = this;
    app.api('/api/review?mode=hard').then(function (d) {
      var r = d && d.review;
      if (r && r.items && r.items.length) {
        // 钉子户卡片模式没有巩固轮实现，这里只摆错词、不给「再练」按钮
        renderReviewCard(el('hardListReview'), r.items, { saved_at: r.saved_at });
      }
    }).catch(function () { /* 回看是锦上添花 */ });
  },

  sendCard: function (wr) {
    var self = this;
    var btn = el('hardCommitCard');
    btn.disabled = true;
    app.api('/api/record', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        plan_date: self.date,
        hard: true,          // 号码按钉子户 plan 解析，少了这个会全部 not_found
        word_results: wr,
        sentence_results: [],
        review_items: self.cardReviewItems()   // 当天快照，供回看
      })
    }).then(function (d) {
      btn.disabled = false;
      wr.forEach(function (r) { self.submitted[r.number] = true; });
      var w = d.words || {};
      var left = self.words.length - Object.keys(self.submitted).length;
      el('hardCommitResult').className = 'feedback ok';
      el('hardCommitResult').textContent =
        '已回写：正确 ' + (w.correct || 0) + '，错误 ' + (w.wrong || 0) +
        (w.not_found ? '，未匹配 ' + w.not_found : '') +
        '，档案 ' + (w.version || '') +
        (left > 0 ? '　还剩 ' + left + ' 个没练，接着练完再点一次提交即可' : '');
      self.reloadCardReview();
      app.toast('钉子户结果已回写');
    }).catch(function (e) {
      btn.disabled = false;
      el('hardCommitResult').className = 'feedback no';
      el('hardCommitResult').textContent = '回写失败：' + e.message;
    });
  }
};

// 正确率 + 复习次数 + 错误次数，一眼看出这个词有多顽固
function metaText(w) {
  var parts = [];
  if (w.accuracy != null) parts.push('正确率 ' + Math.round(w.accuracy * 100) + '%');
  if (w.review_count) parts.push('复习 ' + w.review_count + ' 次');
  if (w.error_count) parts.push('错 ' + w.error_count + ' 次');
  if (w.group) parts.push(w.group);
  return parts.join('　');
}

document.addEventListener('DOMContentLoaded', function () {
  el('hSubmit').addEventListener('click', function () { hard.submit(); });
  el('hInput').addEventListener('keydown', function (e) {
    if (e.isComposing || e.keyCode === 229) return; // IME 确认转换的 Enter 不当提交
    if (e.key === 'Enter') hard.submit();
  });
  el('hUnknown').addEventListener('click', function () { hard.unknown(); });
  el('hReveal').addEventListener('click', function () { hard.reveal(); });
  el('hSkip').addEventListener('click', function () { hard.skip(); });
  el('hardCommitCard').addEventListener('click', function () { hard.commitCard(); });

  el('hardGrade').addEventListener('click', function () { hard.list.gradeAll(); });
  el('hardCommit').addEventListener('click', function () { hard.list.commit(); });
});
