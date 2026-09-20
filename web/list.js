// 一屏全列的练习列表。今日练习和钉子户共用这套引擎（造句页本来就是列表，没用这个）。
//
// 流程：一屏列出全部词 → 全部写完点「对答案」→ 逐条判分 + 可人工改判 → 点「回写档案」
//      → 有错词就自动进第二轮（只列错词）。
//
// 三条铁律，改代码时别破：
//   1. 空白 = 没写 = 完全不处理：不判分、不进提交结果。
//      想记的话老师得主动点「不会」—— 那就按答错处理，跟写错一模一样。
//   2. 第二轮只巩固，绝不回写。同一个词一天只能进档案一次 —— 否则
//      RecordWrong 后又 RecordCorrect 连打两遍，艾宾浩斯间隔和 LastReview 会被改两次。
//   3. 「不会」的行不给「算对」—— 主动认输，没得改判。
//      「算对」只给判错的行：老师写了汉字被假名档判错，靠它改成对。
//      判对的行也不给（没东西可改）。状态机见 unknown.js。

function ListPractice(opts) {
  var self = {
    o: opts,          // DOM 节点 + 回调
    items: [],        // { number, word, definition, status, group, input, correct, manual, blank, unknown }
    date: '',
    round: 1,         // 1 = 正常轮（回写）；2 = 错词巩固（不回写）
    graded: false,
    committed: false // 已回写过就别再给按钮了（否则点「重新判分」能二次回写）
  };

  var el = function (id) { return document.getElementById(id); };

  // --- 装载本轮的词 ---
  self.set = function (date, words) {
    self.date = date;
    self.round = 1;
    self.graded = false;
    self.committed = false;
    self.items = words.map(function (w) {
      return {
        number: w.number, word: w.word, definition: w.definition,
        status: w.status || '', group: w.group || '',
        input: '', correct: null, manual: false, blank: false, unknown: false
      };
    });
  };

  // --- 渲染：一次性把全部词铺出来 ---
  self.render = function () {
    var m = self.o.mount;
    m.innerHTML = self.items.map(function (it, i) {
      return '<div class="word-item" id="' + self.rowId(i) + '">' +
        '<div class="wi-head">' +
        '<span class="num">' + it.number + '</span>' +
        (it.status ? '<span class="tag' + (it.status === '☠️钉子户' ? ' nail' : '') + '">' +
          esc(statusText(it.status)) + '</span>' : '') +
        '<span class="def">' + esc(it.definition) + '</span>' +
        '<button class="unk" data-i="' + i + '" aria-pressed="false">不会</button>' +
        '</div>' +
        '<div class="row"><input type="text" data-i="' + i + '" autocomplete="off" spellcheck="false" ' +
        'placeholder="写日语，回车跳下一个"></div>' +
        '<div class="ans"></div>' +
        '</div>';
    }).join('');
    m.classList.remove('hidden');

    var o = self.o;
    o.bar.classList.remove('hidden');
    o.gradeBtn.classList.remove('hidden');
    o.gradeBtn.textContent = '对答案';
    o.commitBtn.classList.add('hidden');
    // 第二轮是从「回写成功」直接切过来的，那条成功提示要留着，别清掉
    if (self.round === 1) { o.msg.textContent = ''; o.msg.className = 'feedback'; }
    o.note.textContent = self.round === 2
      ? '第二轮：只列错词，本轮仅当场巩固，不重复计入档案' : '';
    self.updateCount();

    var inputs = m.querySelectorAll('input[type=text]');
    Array.prototype.forEach.call(inputs, function (inp) {
      inp.addEventListener('input', function () {
        var i = parseInt(inp.dataset.i, 10);
        var it = self.items[i];
        it.input = inp.value;
        // 标了「不会」又写了字 —— 以写的为准，自动解除
        if (it.unknown && String(inp.value).trim()) {
          UNK.set(it, false);
          self.renderResult(i);
          self.refreshSummary();
        }
        self.updateCount();
      });
      inp.addEventListener('keydown', function (e) {
        if (e.key !== 'Enter') return;
        e.preventDefault();
        var i = parseInt(inp.dataset.i, 10);
        var next = m.querySelector('input[data-i="' + (i + 1) + '"]');
        if (next) next.focus();
        else { inp.blur(); self.o.bar.scrollIntoView({ block: 'center' }); }
      });
    });
    Array.prototype.forEach.call(m.querySelectorAll('button.unk'), function (btn) {
      btn.addEventListener('click', function () {
        self.toggleUnknown(parseInt(btn.dataset.i, 10));
      });
    });
    if (inputs.length) inputs[0].focus();
  };

  self.rowId = function (i) { return self.o.prefix + '-wi-' + i; };

  self.updateCount = function () {
    var filled = 0;
    self.items.forEach(function (it) { if (String(it.input || '').trim()) filled++; });
    self.o.count.textContent = '已填 ' + filled + ' / ' + self.items.length;
  };

  // --- 对答案：统一判分 ---
  self.gradeAll = function () {
    var mode = app.mode();
    self.items.forEach(function (it, i) {
      // 「不会」= 答错，不重判（it.manual 其实也挡住了，这里再挡一次更明确）
      if (it.unknown) { it.blank = false; self.renderResult(i); return; }
      if (!String(it.input || '').trim()) {
        it.blank = true;
        it.correct = null;
        self.renderResult(i);
        return;
      }
      it.blank = false;
      // 人工改过的行不重判，否则老师勾的「算对」会被冲掉
      if (!it.manual) it.correct = gradeAnswer(it.input, it.word, mode);
      self.renderResult(i);
    });

    self.graded = true;
    self.o.gradeBtn.textContent = '重新判分';
    self.refreshSummary();
  };

  // 汇总文案 + 回写按钮显隐。一处算，改判/标不会后都调这个。
  self.refreshSummary = function () {
    self.o.msg.className = 'feedback';
    self.o.msg.textContent = UNK.line(self.items);
    // 只有第一轮才给回写按钮；已回写过就别再显形
    self.o.commitBtn.classList.toggle('hidden',
      self.round !== 1 || self.committed || !self.collect().length);
  };

  // 判分前后都能点「不会」：点了立刻变橙 + 显示正确答案；再点一次复原。
  self.toggleUnknown = function (i) {
    var it = self.items[i];
    UNK.set(it, !it.unknown);
    if (!it.unknown && self.graded) {
      // 取消标记后回到「按写的判」，没写就还是没写
      it.blank = !String(it.input || '').trim();
      it.correct = it.blank ? null : gradeAnswer(it.input, it.word, app.mode());
    }
    self.renderResult(i);
    self.refreshSummary();
  };

  self.renderResult = function (i) {
    var it = self.items[i];
    var box = el(self.rowId(i));
    if (!box) return;
    box.classList.toggle('graded-ok', it.correct === true && !it.unknown);
    box.classList.toggle('graded-no', it.correct === false && !it.unknown);
    box.classList.toggle('unknown', !!it.unknown);
    box.classList.toggle('blank', !!it.blank && !it.unknown);

    var btn = box.querySelector('button.unk');
    if (btn) {
      btn.classList.toggle('on', !!it.unknown);
      btn.setAttribute('aria-pressed', it.unknown ? 'true' : 'false');
    }

    var ans = box.querySelector('.ans');
    if (it.unknown) {
      // 主动认输就没有「算对」可言 —— 只给答案，想改就再点一次「不会」
      ans.innerHTML =
        '<div><span class="unk-tag">不会</span>（按答错记入档案）</div>' +
        '<div>正确答案：<span class="answer">' + esc(it.word) + '</span></div>';
      return;
    }
    // 还没对答案：别提前泄答案
    if (!self.graded) { ans.innerHTML = ''; return; }
    if (it.blank) {
      ans.innerHTML = '<span class="muted">没写，跳过</span>';
      return;
    }
    // 「算对」只给判错的行（以及老师手工改过的行）—— 判对的没东西可改
    ans.innerHTML =
      '<div>你写的：<span class="' + (it.correct ? 'ok' : 'no') + '">' + esc(it.input) + '</span></div>' +
      '<div>正确答案：<span class="answer">' + esc(it.word) + '</span></div>' +
      (it.correct !== true || it.manual ? pickHtml(i, !!it.correct) + altHint(it) : '');
    bindPick(ans, it, i);
  };

  function pickHtml(i, checked) {
    return '<label class="pick"><input type="checkbox" data-i="' + i + '"' +
      (checked ? ' checked' : '') + '> 算对</label>';
  }

  // 写了汉字被判错，多半是判分档位的问题不是真不会 —— 直接告诉老师换哪档会判对。
  // either 是三档的超集，不进这个数组（either 档下数学上必然输出空）。
  function altHint(it) {
    var mode = app.mode();
    var names = { kana: '假名档', kanji: '汉字档', full: '完整档', either: '任一档' };
    var alts = [];
    ['kana', 'kanji', 'full'].forEach(function (m) {
      if (m !== mode && gradeAnswer(it.input, it.word, m)) alts.push(names[m]);
    });
    return alts.length ? '<span class="hint muted">（按' + alts.join('或') + '算对）</span>' : '';
  }

  function bindPick(ans, it, i) {
    var cb = ans.querySelector('input[type=checkbox]');
    if (!cb) return;
    cb.addEventListener('change', function () {
      if (cb.checked) {
        UNK.set(it, false);        // 算对与「不会」互斥
        it.correct = true;
        it.manual = true;          // 老师动过手，重判时不覆盖
        it.blank = false;
      } else {
        // 取消「算对」= 回到按写的判，清掉 manual 让重判能重新算
        it.unknown = false;
        it.correct = false;
        it.manual = false;
      }
      self.renderResult(i);
      self.refreshSummary();
    });
  }

  // 切判分档位时重判（只在这次没提交过、已判过分的前提下有意义）
  self.regrade = function () {
    if (!self.graded || self.round !== 1) return;
    self.gradeAll();
  };

  // --- 收集要提交的结果：空白和未判的不进 ---
  self.collect = function () {
    var out = [];
    self.items.forEach(function (it) {
      if (it.blank || it.correct === null) return;
      out.push({ number: it.number, correct: it.correct });
    });
    return out;
  };

  // --- 回写 ---
  self.commit = function () {
    var pend = UNK.pending(self.items);
    var go = function () {
      var wr = self.collect();
      if (!wr.length) { app.toast('没有可回写的结果'); return; }
      self.sendRecord(wr);
    };
    if (!pend) { go(); return; }

    UNK.ask({
      title: '还有 ' + pend + ' 个词既没写也没标「不会」',
      lines: [
        '这些词不判分、不进档案、也不进巩固轮。',
        '点「全部标记为不会」会把这 ' + pend + ' 个词各记一次错（明天会再出现）。'
      ],
      skipText: '跳过',
      allText: '全部标记为不会',
      onSkip: go,
      onMarkAll: function () {
        self.items.forEach(function (it) {
          if (!it.unknown && !String(it.input || '').trim()) UNK.set(it, true);
        });
        self.gradeAll();
        go();
      }
    });
  };

  self.sendRecord = function (wr) {
    var o = self.o;
    o.commitBtn.disabled = true;
    app.api('/api/record', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        plan_date: self.date,
        hard: !!o.hard,
        word_results: wr,
        sentence_results: []
      })
    }).then(function (d) {
      o.commitBtn.disabled = false;
      self.committed = true;
      var w = d.words || {};
      var line = '已回写：正确 ' + (w.correct || 0) + '，错误 ' + (w.wrong || 0) +
        (w.not_found ? '，未匹配 ' + w.not_found : '') + '，档案 ' + (w.version || '');
      o.msg.className = 'feedback ok';
      o.msg.textContent = line;
      renderTomorrow(o.tomorrow, d.tomorrow);

      var wrongNums = self.items
        .filter(function (it) { return it.correct === false; })
        .map(function (it) { return it.number; });
      if (wrongNums.length) self.requeue(wrongNums);
      else {
        o.commitBtn.classList.add('hidden');
        o.note.textContent = '全对，没有需要巩固的词';
      }
      app.toast('已回写');
    }).catch(function (e) {
      o.commitBtn.disabled = false;
      o.msg.className = 'feedback no';
      o.msg.textContent = '回写失败：' + e.message;
    });
  };

  // --- 第二轮：只列错词，不回写 ---
  self.requeue = function (nums) {
    var keep = self.items.filter(function (it) { return nums.indexOf(it.number) >= 0; });
    self.items = keep.map(function (it) {
      return {
        number: it.number, word: it.word, definition: it.definition,
        status: it.status, group: it.group,
        input: '', correct: null, manual: false, blank: false, unknown: false
      };
    });
    self.round = 2;
    self.graded = false;
    self.o.commitBtn.classList.add('hidden');
    self.render();
    window.scrollTo({ top: 0 });
  };

  return self;
}
