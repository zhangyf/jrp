// 造句练习：看中文写日文，逐句即时比对，老师可以覆盖自动判定。
//
// 空白 = 没写 = 不判分、不进提交。想记的话老师得主动点「不会」——
// 那就按答错处理（跟写错一模一样，错句按 3/7/14 天重出）。

var sentence = {
  items: [],
  date: '',

  load: function () {
    var self = this;
    el('sentenceList').innerHTML = '';
    el('sentenceDone').classList.add('hidden');
    el('sentenceSummary').innerHTML = '<span class="chip muted">加载中…</span>';

    app.api('/api/plan?mode=sentences').then(function (d) {
      self.date = d.date;
      self.items = (d.sentences || []).map(function (s) {
        return { number: s.number, chinese: s.chinese, answer: s.answer,
                 input: '', correct: null, unknown: false };
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
        });
      });
      Array.prototype.forEach.call(el('sentenceList').querySelectorAll('button.unk'), function (btn) {
        btn.addEventListener('click', function () {
          self.toggleUnknown(parseInt(btn.dataset.i, 10));
        });
      });
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
      (s.correct ? 'checked' : '') + '> 算对</label>' +
      '<span class="muted">自动判分仅供参考（长句手写易误判），请自己确认</span></div>';
    var cb = box.querySelector('input[type=checkbox]');
    cb.addEventListener('change', function () {
      if (cb.checked) UNK.set(s, false);   // 算对与「不会」互斥
      s.correct = cb.checked;
      box.classList.toggle('graded-ok', s.correct);
      box.classList.toggle('graded-no', !s.correct);
    });
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
      app.toast('造句结果已回写');
    }).catch(function (e) {
      el('sentenceCommit').disabled = false;
      el('sentenceCommitResult').className = 'feedback no';
      el('sentenceCommitResult').textContent = '回写失败：' + e.message;
    });
  }
};

// 与 Go 侧 sentence.go 的 normSentence 一致：
// 删掉空白（含全角空格）、句号、读点、逗号、斜杠
function normSentence(s) {
  return (s || '').replace(/[\s　。．、,./／]/g, '');
}

document.addEventListener('DOMContentLoaded', function () {
  el('sentenceCommit').addEventListener('click', function () { sentence.commit(); });
});
