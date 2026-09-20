// 离线闭环：导出 xlsx 拿去飞机上练，回来把填好的文件传回来解析。
//
// 两条铁律：
//   1. 解析只出预览，绝不直接落库 —— 长句手写的自动判分不可靠，必须老师过目。
//   2. 空白 = 没写 = 完全不处理，不进提交结果。想记就得勾「不会」——
//      那就按答错处理（跟写错一模一样）。
//
// 自动判分在前端用 grade.js 重算（和 Go 侧 grade.go 逐字对齐），
// 所以老师改判分档位时不用重新上传文件。

var offline = {
  meta: null,     // { plan_date, mode, language }
  words: [],      // { number, definition, answer, input, blank, correct, unknown }
  sentences: [],  // { number, chinese, answer, input, blank, correct, unknown }

  export: function () {
    var date = el('exportDate').value || todayStr();
    var mode = el('exportMode').value;
    var q = '/api/export?mode=' + encodeURIComponent(mode) +
      '&date=' + encodeURIComponent(date) +
      '&grade=' + encodeURIComponent(app.mode());
    if (app.token) q += '&token=' + encodeURIComponent(app.token);
    // 下载不能走 fetch（Authorization 头带不上），拼 URL 直接跳，
    // 服务端支持 ?token= 兜底。
    window.location.href = q;
  },

  parse: function () {
    var f = el('importFile').files[0];
    if (!f) { app.toast('先选一个 xlsx'); return; }

    var fd = new FormData();
    fd.append('file', f);

    el('importInfo').className = 'feedback';
    el('importInfo').textContent = '解析中…';
    el('importWords').innerHTML = '';
    el('importSentences').innerHTML = '';
    el('importCommit').classList.add('hidden');
    el('importCommitResult').textContent = '';

    var self = this;
    app.api('/api/import?grade=' + encodeURIComponent(app.mode()), {
      method: 'POST',
      body: fd
    }).then(function (d) {
      self.meta = { plan_date: d.plan_date, mode: d.mode, language: d.language };
      var blank = {};
      (d.skipped_blank || []).forEach(function (n) { blank[n] = true; });

      self.words = (d.words || []).map(function (w) {
        return {
          number: w.number, definition: w.definition, answer: w.answer,
          input: w.input, blank: !!blank[w.number], unknown: false
        };
      });
      self.sentences = (d.sentences || []).map(function (s) {
        return {
          number: s.number, chinese: s.chinese, answer: s.answer,
          input: s.input, blank: !String(s.input || '').trim(), unknown: false
        };
      });
      self.regrade();
      self.render();
    }).catch(function (e) {
      el('importInfo').className = 'feedback no';
      el('importInfo').textContent = '解析失败：' + e.message;
    });
  },

  // 用当前判分档位重算（切档位时不用重新上传）
  regrade: function () {
    if (!this.meta) return;
    var m = app.mode();
    this.words.forEach(function (w) {
      if (w.unknown) { w.correct = false; return; }   // 「不会」= 答错，不受档位影响
      if (w.blank) return;
      w.correct = gradeAnswer(w.input, w.answer, m);
    });
    this.sentences.forEach(function (s) {
      if (s.unknown) { s.correct = false; return; }
      if (s.blank) return;
      s.correct = normSentence(s.input) === normSentence(s.answer);
    });
    if (el('importWords').innerHTML) this.render();
  },

  render: function () {
    var m = this.meta;
    var modeText = { daily: '单词+造句', hard: '钉子户专项', sentences: '纯造句' }[m.mode] || m.mode;
    var blankW = this.words.filter(function (w) { return w.blank; }).length;
    var blankS = this.sentences.filter(function (s) { return s.blank; }).length;
    var unkW = this.words.filter(function (w) { return w.unknown; }).length;
    var unkS = this.sentences.filter(function (s) { return s.unknown; }).length;

    el('importInfo').className = 'feedback ok';
    el('importInfo').innerHTML =
      '计划日期 <b>' + esc(m.plan_date) + '</b>　类型 ' + esc(modeText) +
      '　单词 ' + (this.words.length - blankW) + '/' + this.words.length +
      '　造句 ' + (this.sentences.length - blankS) + '/' + this.sentences.length +
      (unkW + unkS ? '　<span class="unk-tag">不会 ' + (unkW + unkS) + '</span>' : '') +
      '<br><span class="muted">勾「不会」= 按答错记入档案，勾了就没有「算对」可改。' +
      '两个都不勾的空白行灰掉，完全不计入。</span>';

    el('importWords').innerHTML = this.words.length ? this.table(
      '单词', ['#', '释义', '你写的', '正确答案', '算对', '不会'],
      this.words.map(function (w, i) {
        return '<tr class="' + rowCls(w) + '">' +
          '<td>' + w.number + '</td>' +
          '<td>' + esc(w.definition) + '</td>' +
          '<td class="you">' + esc(w.input) + '</td>' +
          '<td>' + esc(w.answer) + '</td>' +
          pickCell('w', i, w) +
          '</tr>';
      }).join('')
    ) : '';

    el('importSentences').innerHTML = this.sentences.length ? this.table(
      '造句', ['#', '中文', '你写的', '参考答案', '算对', '不会'],
      this.sentences.map(function (s, i) {
        return '<tr class="' + rowCls(s) + '">' +
          '<td>S' + s.number + '</td>' +
          '<td>' + esc(s.chinese) + '</td>' +
          '<td class="you">' + esc(s.input) + '</td>' +
          '<td>' + esc(s.answer) + '</td>' +
          pickCell('s', i, s) +
          '</tr>';
      }).join('')
    ) : '';

    var self = this;
    Array.prototype.forEach.call(
      document.querySelectorAll('#importWords input,#importSentences input'), function (cb) {
        cb.addEventListener('change', function () {
          var arr = cb.dataset.k === 'w' ? self.words : self.sentences;
          var it = arr[parseInt(cb.dataset.i, 10)];
          if (cb.dataset.unk === '1') {
            UNK.set(it, cb.checked);       // 「不会」与「算对」互斥
            if (cb.checked) it.correct = false;
          } else if (cb.checked) {
            UNK.set(it, false);            // 算对与「不会」互斥
            it.correct = true;
          } else {
            it.correct = false;
          }
          self.render();
        });
      });

    var any = this.words.some(function (w) { return !w.blank || w.unknown; }) ||
      this.sentences.some(function (s) { return !s.blank || s.unknown; });
    el('importCommit').classList.toggle('hidden', !any);
  },

  // 收集要提交的结果：unknown 按答错，blank 且没标不会的跳过。
  collect: function () {
    var wr = [], sr = [];
    this.words.forEach(function (w) {
      if (w.unknown) { wr.push({ number: w.number, correct: false }); return; }
      if (w.blank) return;
      wr.push({ number: w.number, correct: !!w.correct });
    });
    this.sentences.forEach(function (s) {
      if (s.unknown) {
        sr.push({ number: s.number, correct: false, answer: s.answer, chinese: s.chinese });
        return;
      }
      if (s.blank) return;
      sr.push({ number: s.number, correct: !!s.correct, answer: s.answer, chinese: s.chinese });
    });
    return { wr: wr, sr: sr };
  },

  table: function (title, heads, body) {
    return '<div class="card"><h3>' + esc(title) + '</h3><table><thead><tr>' +
      heads.map(function (h) { return '<th>' + esc(h) + '</th>'; }).join('') +
      '</tr></thead><tbody>' + body + '</tbody></table></div>';
  },

  commit: function () {
    var self = this;
    var go = function () {
      var c = self.collect();
      var wr = c.wr, sr = c.sr;
      if (!wr.length && !sr.length) { app.toast('没有可提交的内容'); return; }
      self.send(wr, sr);
    };

    var pend = UNK.pending(this.words) + UNK.pending(this.sentences);
    if (!pend) { go(); return; }
    UNK.ask({
      title: '还有 ' + pend + ' 项既没写也没勾「不会」',
      lines: [
        '这些项不判分、不进档案。',
        '点「全部标记为不会」会把这 ' + pend + ' 项各记一次错（明天会再出现）。'
      ],
      skipText: '跳过',
      allText: '全部标记为不会',
      onSkip: go,
      onMarkAll: function () {
        self.words.forEach(function (w) {
          if (!w.unknown && !String(w.input || '').trim()) UNK.set(w, true);
        });
        self.sentences.forEach(function (s) {
          if (!s.unknown && !String(s.input || '').trim()) UNK.set(s, true);
        });
        self.render();
        go();
      }
    });
  },

  send: function (wr, sr) {
    var self = this;

    el('importCommit').disabled = true;
    app.api('/api/record', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        plan_date: self.meta.plan_date,
        mode: self.meta.mode,
        hard: self.meta.mode === 'hard',
        word_results: wr,
        sentence_results: sr
      })
    }).then(function (d) {
      el('importCommit').disabled = false;
      var w = d.words || {}, s = d.sentences || {};
      var msg = '已提交：';
      if (wr.length) msg += '单词 对' + (w.correct || 0) + '/错' + (w.wrong || 0) +
        (w.not_found ? '/未匹配' + w.not_found : '') + '，档案 ' + (w.version || '') + '　';
      if (sr.length) msg += '造句 对' + (s.correct || 0) + '/错' + (s.wrong || 0);
      el('importCommitResult').className = 'feedback ok';
      el('importCommitResult').textContent = msg;
      renderTomorrow(el('importCommitResult'), d.tomorrow, true);
      app.toast('离线结果已回写');
    }).catch(function (e) {
      el('importCommit').disabled = false;
      el('importCommitResult').className = 'feedback no';
      el('importCommitResult').textContent = '提交失败：' + e.message;
    });
  }
};

// 「不会」的行橙底；空白行灰掉
function rowCls(it) {
  if (it.unknown) return 'unknown';
  return it.blank ? 'blank' : '';
}

// 「算对」和「不会」两个勾选框。
//   空白行没得改判（没写就是没写），但「不会」随时能勾 —— 空白可能就是不会。
//   勾了「不会」就没有「算对」—— 主动认输，不需要改判。
function pickCell(kind, i, it) {
  var okBox = (it.blank || it.unknown) ? '' :
    '<input type="checkbox" data-k="' + kind + '" data-i="' + i + '"' +
    (it.correct ? ' checked' : '') + '>';
  return '<td>' + okBox + '</td>' +
    '<td><input type="checkbox" data-k="' + kind + '" data-i="' + i + '" data-unk="1"' +
    (it.unknown ? ' checked' : '') + '></td>';
}

document.addEventListener('DOMContentLoaded', function () {
  el('exportBtn').addEventListener('click', function () { offline.export(); });
  el('importBtn').addEventListener('click', function () { offline.parse(); });
  el('importCommit').addEventListener('click', function () { offline.commit(); });
});
