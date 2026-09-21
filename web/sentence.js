// 造句练习：看中文写日文，逐句即时比对，老师可以覆盖自动判定。
//
// 空白 = 没写 = 不判分、不进提交。想记的话老师得主动点「不会」——
// 那就按答错处理（跟写错一模一样，错句按 3/7/14 天重出）。

var sentence = {
  items: [],
  date: '',

  draftTimer: null,
  draftBar: null,

  load: function () {
    var self = this;
    el('sentenceList').innerHTML = '';
    el('sentenceDone').classList.add('hidden');
    el('sentenceNext').classList.add('hidden');
    el('sentenceCommitResult').textContent = '';
    el('sentenceCommitResult').className = 'feedback';
    self.dropDraftBar();
    el('sentenceSummary').innerHTML = '<span class="chip muted">加载中…</span>';

    app.api('/api/plan?mode=sentences').then(function (d) {
      self.date = d.date;
      self.items = (d.sentences || []).map(function (s) {
        return { number: s.number, chinese: s.chinese, answer: s.answer,
                 input: '', correct: null, unknown: false, manual: false };
      });
      if (!self.items.length) {
        el('sentenceSummary').innerHTML = '<span class="chip warn">句库没建或为空</span>';
        return;
      }
      renderSummary(el('sentenceSummary'), [
        { label: '今天', value: self.items.length + ' 句' },
        { label: '提示', value: '长句自动判分仅供参考，逐句可改' }
      ]);
      el('sentenceList').innerHTML = self.items.map(function (s, i) {
        return '<div class="sentence-item" id="si-' + i + '">' +
          '<div class="cn"><b>' + s.number + '.</b> ' + esc(s.chinese) +
          '<button class="unk" data-i="' + i + '" aria-pressed="false">不会</button></div>' +
          '<div class="row"><input type="text" data-i="' + i + '" autocomplete="off" spellcheck="false" placeholder="写日文，回车提交"></div>' +
          '<div class="ans"></div>' +
          '</div>';
      }).join('');
      el('sentenceList').classList.remove('hidden');
      el('sentenceDone').classList.remove('hidden');
      Array.prototype.forEach.call(el('sentenceList').querySelectorAll('input'), function (inp) {
        inp.addEventListener('keydown', function (e) {
          // IME 确认转换的那次 Enter（isComposing / keyCode 229）不能当提交：
          // 否则判分发生在打字/转换中途，留下一个过期的「错」，
          // 之后改字也只更新 input 不刷新判定 —— 「写的跟答案一样却判错」的根源。
          if (e.isComposing || e.keyCode === 229) return;
          if (e.key === 'Enter') self.grade(parseInt(inp.dataset.i, 10));
        });
        // 标了「不会」又写了字 —— 以写的为准，自动解除
        inp.addEventListener('input', function () {
          var i = parseInt(inp.dataset.i, 10);
          var s = self.items[i];
          s.input = inp.value.trim();
          if (s.unknown && s.input) {
            UNK.set(s, false);
            self.renderItem(i);
          }
          // 已经判过再改字：当场重判，别留着旧结论
          if (!s.unknown && s.correct !== null && s.input) {
            s.correct = normSentence(s.input) === normSentence(s.answer);
            self.renderItem(i);
          }
          self.scheduleDraft();
        });
      });
      Array.prototype.forEach.call(el('sentenceList').querySelectorAll('button.unk'), function (btn) {
        btn.addEventListener('click', function () {
          self.toggleUnknown(parseInt(btn.dataset.i, 10));
        });
      });
      // 有没写完的草稿就回填。后端把当天的造句题锁定了，所以题号一定对得上。
      self.loadDraft();
    }).catch(function (e) {
      el('sentenceSummary').innerHTML = '<span class="chip warn">' + esc(e.message) + '</span>';
    });
  },

  grade: function (i) {
    var s = this.items[i];
    var inp = el('sentenceList').querySelector('input[data-i="' + i + '"]');
    s.input = inp.value.trim();
    if (!s.input) return;
    if (s.unknown) return;                 // 「不会」就是错，别再按写的重判
    s.correct = normSentence(s.input) === normSentence(s.answer);
    this.renderItem(i);
    // 自动跳到下一题
    var next = el('sentenceList').querySelector('input[data-i="' + (i + 1) + '"]');
    if (next) next.focus();
  },

  // 「不会」= 答错，跟写错一模一样。点了立刻显示正确答案。
  toggleUnknown: function (i) {
    var s = this.items[i];
    UNK.set(s, !s.unknown);
    this.renderItem(i);
    this.scheduleDraft();
  },

  renderItem: function (i) {
    var s = this.items[i];
    var box = el('si-' + i);
    box.classList.toggle('graded-ok', s.correct === true && !s.unknown);
    box.classList.toggle('graded-no', s.correct === false && !s.unknown);
    box.classList.toggle('unknown', !!s.unknown);

    var btn = box.querySelector('button.unk');
    if (btn) {
      btn.classList.toggle('on', !!s.unknown);
      btn.setAttribute('aria-pressed', s.unknown ? 'true' : 'false');
    }

    var ans = box.querySelector('.ans');
    if (s.unknown) {
      ans.innerHTML =
        '<div><span class="unk-tag">不会</span>（按答错记入档案，会按 3/7/14 天重出）</div>' +
        '<div>正确答案：' + esc(s.answer) + '</div>';
      return;
    }
    if (s.correct === null) { ans.innerHTML = ''; return; }

    ans.innerHTML =
      '<div>你写的：<span class="' + (s.correct ? 'ok' : 'no') + '">' + esc(s.input) + '</span></div>' +
      '<div>正确答案：' + esc(s.answer) + '</div>' +
      '<div class="row"><label><input type="checkbox" data-i="' + i + '" ' +
      (s.manual ? 'checked' : '') + '> 算对</label>' +
      '<span class="muted">自动判分仅供参考；判错了但你写的其实对，就勾「算对」</span></div>';
    // 「算对」是纯手动改判，绝不跟着自动判定走（2026-09-21 老师反馈：
    // 判对自动勾上看着像系统乱动，一取消又变红，完全猜不透）。
    // 对错看颜色：绿=自动判对，红=自动判错。勾上=强制改对，取消=退回自动判定。
    var cb = box.querySelector('input[type=checkbox]');
    cb.addEventListener('change', function () {
      if (cb.checked) {
        UNK.set(s, false);     // 算对与「不会」互斥
        s.correct = true;
        s.manual = true;       // 老师手改，草稿恢复/重判时不覆盖
      } else {
        s.manual = false;      // 取消手改，退回按写的自动判定
        s.correct = s.input ? normSentence(s.input) === normSentence(s.answer) : null;
      }
      box.classList.toggle('graded-ok', s.correct === true && !s.unknown);
      box.classList.toggle('graded-no', s.correct === false && !s.unknown);
      sentence.scheduleDraft();
    });
  },

  // ================= 草稿：写一半关掉 / 刷新都不丢 =================
  //
  // 跟练习页同一套机制，存 COS 的 /api/draft（mode=sentences，按日期分键），
  // 换设备也能接着写。配后端「当天造句 plan 锁定」才成立：
  // 不提交就永远是同一批题，草稿才接得上；提交回写后 plan 被删，下次才换新一批。
  //
  // 只存老师亲手动过的三样：写的句子 / 「不会」/ 「算对」。
  // 对错不存 —— 它由 (写的句子, 原句) 决定，恢复时重算。

  scheduleDraft: function () {
    var self = this;
    clearTimeout(self.draftTimer);
    self.draftTimer = setTimeout(function () { self.saveDraft(); }, 1200);   // 停手 1.2 秒才存
  },

  saveDraft: function () {
    var self = this;
    if (!self.date) return;
    app.api('/api/draft', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        date: self.date,
        mode: 'sentences',
        grade_mode: app.mode(),
        items: self.items.map(function (s) {
          return {
            number: s.number,
            answer: s.input || '',
            unknown: !!s.unknown,
            manual: !!s.manual,
            prompt: s.answer        // 原句指纹：恢复时核对，防止串到换过的题上
          };
        })
      })
    }).catch(function () {   // 草稿存不上不该打断练习
    });
  },

  clearDraft: function () {
    var self = this;
    clearTimeout(self.draftTimer);
    if (!self.date) return;
    app.api('/api/draft?mode=sentences&date=' + encodeURIComponent(self.date), { method: 'DELETE' })
      .catch(function () {});
  },

  // 装载完题目后调用：有草稿就回填，并挂一条「已恢复」横幅。
  loadDraft: function () {
    var self = this;
    if (!self.date) return Promise.resolve();
    return app.api('/api/draft?mode=sentences&date=' + encodeURIComponent(self.date))
      .then(function (d) {
        var dr = d && d.draft;
        if (!dr || !dr.items || !dr.items.length) return;

        var by = {};
        dr.items.forEach(function (x) { by[x.number] = x; });
        var hit = 0;
        self.items.forEach(function (s) {
          var x = by[s.number];
          if (!x) return;                  // 序号对不上（换过题）就跳过
          // 序号对得上、句子却不是同一句（手工重跑过 gen-plan）也必须跳过，
          // 否则上一批写的句子会被糊到这一批上。
          if (x.prompt && x.prompt !== s.answer) return;
          if (x.unknown) { UNK.set(s, true); }        // unknown 优先：它自带 manual
          else {
            if (x.answer) s.input = x.answer;
            if (x.manual) { s.manual = true; s.correct = true; }
            else if (s.input) s.correct = normSentence(s.input) === normSentence(s.answer);
          }
          hit++;
        });
        if (!hit) return;

        self.items.forEach(function (s, i) {
          var inp = el('sentenceList').querySelector('input[data-i="' + i + '"]');
          if (inp && s.input) inp.value = s.input;
          self.renderItem(i);
        });
        self.showDraftBar(dr);
      }).catch(function () {   // 读不到草稿 = 没有
      });
  },

  showDraftBar: function (dr) {
    var self = this;
    self.dropDraftBar();
    var list = el('sentenceList');
    var bar = document.createElement('div');
    bar.className = 'draft-bar';
    bar.id = 'sentenceDraftBar';
    bar.innerHTML = '已恢复上次没写完的造句' +
      (dr.saved_at ? '（存于 ' + esc(dr.saved_at) + '）' : '') +
      '　<button type="button" class="linkbtn">清空草稿</button>';
    list.parentNode.insertBefore(bar, list);
    self.draftBar = bar;

    bar.querySelector('button').addEventListener('click', function () {
      self.items.forEach(function (s, i) {
        s.input = ''; s.unknown = false; s.manual = false; s.correct = null;
        var inp = el('sentenceList').querySelector('input[data-i="' + i + '"]');
        if (inp) inp.value = '';
        self.renderItem(i);
      });
      self.dropDraftBar();
      self.clearDraft();
      app.toast('草稿已清空');
    });
  },

  dropDraftBar: function () {
    var bar = el('sentenceDraftBar');
    if (bar && bar.parentNode) bar.parentNode.removeChild(bar);
    this.draftBar = null;
  },

  commit: function () {
    var self = this;
    var build = function () {
      var rs = [];
      self.items.forEach(function (s) {
        if (s.unknown) {   // 「不会」按答错提交
          rs.push({ number: s.number, correct: false, answer: s.answer, chinese: s.chinese });
          return;
        }
        if (s.correct === null) return; // 没写的跳过
        rs.push({ number: s.number, correct: s.correct, answer: s.answer, chinese: s.chinese });
      });
      return rs;
    };
    var go = function () {
      var rs = build();
      if (!rs.length) { app.toast('还没有判过任何一句'); return; }
      self.send(rs);
    };

    var pend = UNK.pending(this.items);
    if (!pend) { go(); return; }
    UNK.ask({
      title: '还有 ' + pend + ' 句既没写也没标「不会」',
      lines: [
        '这些句不判分、不进档案。',
        '点「全部标记为不会」会把这 ' + pend + ' 句各记一次错（会按 3/7/14 天重出）。'
      ],
      skipText: '跳过',
      allText: '全部标记为不会',
      onSkip: go,
      onMarkAll: function () {
        self.items.forEach(function (s) {
          if (!s.unknown && !String(s.input || '').trim()) UNK.set(s, true);
        });
        self.items.forEach(function (_, i) { self.renderItem(i); });
        go();
      }
    });
  },

  send: function (rs) {
    var self = this;
    el('sentenceCommit').disabled = true;
    app.api('/api/record', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        plan_date: self.date,
        mode: 'sentences',
        word_results: [],
        sentence_results: rs
      })
    }).then(function (d) {
      el('sentenceCommit').disabled = false;
      var s = d.sentences || {};
      el('sentenceCommitResult').className = 'feedback ok';
      el('sentenceCommitResult').textContent =
        '已回写：正确 ' + (s.correct || 0) + '，错误 ' + (s.wrong || 0) +
        '（错句会按 3/7/14 天重出）';
      // 这批已经归档，草稿没用了；后端同时清掉了当天锁定的 plan，
      // 所以下一次装载才是新的一批 —— 换题发生在提交之后，不是刷新之后。
      self.dropDraftBar();
      self.clearDraft();
      el('sentenceNext').classList.remove('hidden');
      app.toast('造句结果已回写');
    }).catch(function (e) {
      el('sentenceCommit').disabled = false;
      el('sentenceCommitResult').className = 'feedback no';
      el('sentenceCommitResult').textContent = '回写失败：' + e.message;
    });
  }
};

// 与 Go 侧 sentence.go 的 normSentence 一致：
// 先把全角 ASCII（U+FF01～U+FF5E：全角数字/字母/标点）折成半角，
// 再删掉空白（含全角空格）、句号、读点、逗号、斜杠、感叹号问号分号。
// 宽度折叠：日语 IME 打「２万円」（全角２）和原句半角「2」应算同一句。
function normSentence(s) {
  var t = (s || '').replace(/[！-～]/g, function (c) {
    return String.fromCharCode(c.charCodeAt(0) - 0xFEE0);
  });
  return t.replace(/[\s　。．、,./／!?:;]/g, '');
}

document.addEventListener('DOMContentLoaded', function () {
  el('sentenceCommit').addEventListener('click', function () { sentence.commit(); });
  // 「换下一批」只在回写成功后出现：提交之前无论怎么刷新都是同一批。
  el('sentenceNext').addEventListener('click', function () { sentence.load(); });
});
