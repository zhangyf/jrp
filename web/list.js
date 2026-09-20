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
    committed: false, // 已回写过就别再给按钮了（否则点「重新判分」能二次回写）
    // 只读回看：回写成功后（或当天已练完再打开）整份列表变成不可编辑的回顾。
    // 老师要的是「练完还能看到当天每个词写了什么、哪题错了」，不是一提交就清空。
    reviewOnly: false,
    pendingWrong: [],
    // 草稿：写一半关掉也不丢。存 COS 而不是 localStorage，换设备能接着写。
    // 见文件末尾「草稿」段落。
    draftMode: opts.draftMode || 'words',
    draftBar: null
  };

  var el = function (id) { return document.getElementById(id); };

  // --- 装载本轮的词 ---
  self.set = function (date, words) {
    self.date = date;
    self.round = 1;
    self.graded = false;
    self.committed = false;
    self.reviewOnly = false;
    self.pendingWrong = [];
    self.draftBar = null;
    self.items = words.map(function (w) {
      return {
        number: w.number, word: w.word, definition: w.definition,
        status: w.status || '', group: w.group || '',
        input: '', correct: null, manual: false, blank: false, unknown: false
      };
    });
  };

  // --- 装载「当天已练完」的只读快照 ---
  // 数据来源 /api/review（回写成功时服务端存的那份）。
  // 这里直接把判分结果拿来用，不重判 —— 回看要显示的是当时发生了什么，
  // 老师现在切了判分档位也不该改写历史。
  self.setReview = function (date, items) {
    self.date = date;
    self.round = 1;
    self.graded = true;
    self.committed = true;   // 回看态下永远不能再回写
    self.reviewOnly = true;
    self.draftBar = null;
    self.pendingWrong = [];
    self.items = items.map(function (r) {
      return {
        number: r.number, word: r.word, definition: r.definition,
        status: r.status || '', group: r.group || '',
        input: r.answer || '',
        correct: r.blank ? null : !!r.correct,
        manual: !!r.manual, blank: !!r.blank, unknown: !!r.unknown
      };
    });
  };

  // 回写时一并交给服务端存快照的详情（含没写的行，回看才完整）
  self.reviewItems = function () {
    return self.items.map(function (it) {
      return {
        number: it.number, word: it.word, definition: it.definition,
        group: it.group, status: it.status,
        answer: it.input || '',
        correct: !!it.correct,
        unknown: !!it.unknown,
        manual: !!it.manual,
        blank: !!it.blank || it.correct === null
      };
    });
  };

  // --- 渲染：一次性把全部词铺出来 ---
  self.render = function () {
    var m = self.o.mount;
    var ro = self.reviewOnly;
    m.innerHTML = self.items.map(function (it, i) {
      return '<div class="word-item" id="' + self.rowId(i) + '">' +
        '<div class="wi-head">' +
        '<span class="num">' + it.number + '</span>' +
        (it.status ? '<span class="tag' + (it.status === '☠️钉子户' ? ' nail' : '') + '">' +
          esc(statusText(it.status)) + '</span>' : '') +
        '<span class="def">' + esc(it.definition) + '</span>' +
        (ro ? '' : '<button class="unk" data-i="' + i + '" aria-pressed="false">不会</button>') +
        '</div>' +
        '<div class="row"><input type="text" data-i="' + i + '" autocomplete="off" spellcheck="false" ' +
        (ro ? 'disabled ' : '') +
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
    if (o.requeueBtn) o.requeueBtn.classList.add('hidden');
    // 第二轮是从「回写成功」直接切过来的，那条成功提示要留着，别清掉
    if (self.round === 1) { o.msg.textContent = ''; o.msg.className = 'feedback'; }
    o.note.textContent = self.round === 2
      ? '第二轮：只列错词，本轮仅当场巩固，不重复计入档案' : '';
    self.updateCount();
    if (self.reviewOnly) { self.enterReview(); return; }

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
        self.scheduleDraft();
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
    self.scheduleDraft();
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
    // 「算对」只给判错的行（以及老师手工改过的行）—— 判对的没东西可改。
    // 只读回看一律不给：结果已经进档案了，改了也只是自欺欺人。
    ans.innerHTML =
      '<div>你写的：<span class="' + (it.correct ? 'ok' : 'no') + '">' + esc(it.input) + '</span></div>' +
      '<div>正确答案：<span class="answer">' + esc(it.word) + '</span></div>' +
      (!self.reviewOnly && (it.correct !== true || it.manual)
        ? pickHtml(i, !!it.correct) + altHint(it) : '');
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
      self.scheduleDraft();
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
        sentence_results: [],
        review_items: self.reviewItems()   // 存当天快照，供练完后回看
      })
    }).then(function (d) {
      o.commitBtn.disabled = false;
      self.committed = true;
      // 已落库，草稿没用了 —— 不删的话下次打开会恢复出一堆早就写完的答案
      self.dropDraftBar();
      self.clearDraft();
      var w = d.words || {};
      var line = '已回写：正确 ' + (w.correct || 0) + '，错误 ' + (w.wrong || 0) +
        (w.not_found ? '，未匹配 ' + w.not_found : '') + '，档案 ' + (w.version || '');
      o.msg.className = 'feedback ok';
      o.msg.textContent = line;
      renderTomorrow(o.tomorrow, d.tomorrow);

      // 不再自动切进第二轮：那样答对的词当场就消失了，老师连自己写了什么都看不到。
      // 改成整份列表原地变只读回看，错词巩固交给「再练错词」按钮。
      self.enterReview();
      app.toast('已回写');
    }).catch(function (e) {
      o.commitBtn.disabled = false;
      o.msg.className = 'feedback no';
      o.msg.textContent = '回写失败：' + e.message;
    });
  };

  // --- 只读回看 ---
  // 回写成功后、或当天已练完再打开页面时进入。整份列表不可编辑，
  // 但每题的对错、老师写的答案、正确答案全部保留，能一直看到当天结束。
  self.enterReview = function () {
    var o = self.o;
    self.reviewOnly = true;

    // 渲染时输入的答案要回填到框里，否则只读框空着看不出写了什么
    Array.prototype.forEach.call(o.mount.querySelectorAll('input[type=text]'), function (inp) {
      var i = parseInt(inp.dataset.i, 10);
      inp.value = self.items[i] ? (self.items[i].input || '') : '';
      inp.disabled = true;
    });

    self.items.forEach(function (it, i) { self.renderResult(i); });

    o.gradeBtn.classList.add('hidden');
    o.commitBtn.classList.add('hidden');

    self.pendingWrong = self.items
      .filter(function (it) { return it.correct === false; })
      .map(function (it) { return it.number; });

    if (o.requeueBtn) {
      if (self.pendingWrong.length && self.round === 1) {
        o.requeueBtn.classList.remove('hidden');
        o.requeueBtn.textContent = '再练这 ' + self.pendingWrong.length + ' 个错词';
      } else {
        o.requeueBtn.classList.add('hidden');
      }
    }

    // 回看态下刷新汇总：告诉老师今天练了多少、对多少
    var done = self.items.filter(function (it) { return it.correct !== null; });
    var ok = done.filter(function (it) { return it.correct === true; }).length;
    var skipped = self.items.length - done.length;
    o.count.textContent = '共 ' + self.items.length + ' 词：对 ' + ok +
      '，错 ' + (done.length - ok) + (skipped ? '，跳过 ' + skipped : '');
    o.note.textContent = '今天这轮已回写，下面是只读回看（不可修改）';
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
    // 第二轮是新的练习，必须能输入 —— 不清掉 render() 又会走回只读回看
    self.reviewOnly = false;
    self.draftBar = null;   // render() 会清空 mount，横幅自然没了
    self.o.commitBtn.classList.add('hidden');
    if (self.o.requeueBtn) self.o.requeueBtn.classList.add('hidden');
    self.render();
    window.scrollTo({ top: 0 });
  };

  // ================= 草稿（写一半关掉也不丢） =================
  //
  // 存在 COS 的 /api/draft，按「日期 + 模式」分键，所以：
  //   - 手机写到一半，电脑上打开能接着写
  //   - 今天的今日练习和钉子户各存各的，不互相覆盖
  //   - 隔天的旧草稿永远不会串到今天
  //
  // 只存老师亲手动过的三样：答案 / 「不会」/ 「算对」。
  // 不存对错结果 —— 它由 (答案, 单词, 判分档位) 决定，恢复时重算，
  // 这样切了判分档位再恢复也不会带上过期的判定。

  var draftTimer = null;

  // 防抖 1.2 秒：每敲一个字就发一次请求太吵，停手了才存
  self.scheduleDraft = function () {
    // 第二轮只巩固不回写，存它的草稿没意义
    if (self.round !== 1) return;
    clearTimeout(draftTimer);
    draftTimer = setTimeout(self.saveDraft, 1200);
  };

  self.draftQuery = function () {
    return '?mode=' + encodeURIComponent(self.draftMode) +
      '&date=' + encodeURIComponent(self.date);
  };

  self.saveDraft = function () {
    var items = self.items.map(function (it) {
      return {
        number: it.number,
        answer: it.input || '',
        unknown: !!it.unknown,
        manual: !!it.manual
      };
    });
    app.api('/api/draft', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        date: self.date,
        mode: self.draftMode,
        grade_mode: app.mode(),
        items: items
      })
    }).catch(function () {
      // 草稿存不上不该打断练习，静默失败
    });
  };

  self.clearDraft = function () {
    clearTimeout(draftTimer);
    app.api('/api/draft' + self.draftQuery(), { method: 'DELETE' })
      .catch(function () {});
  };

  // 装载完题目后调用：有草稿就回填，并挂一条「已恢复」横幅。
  self.loadDraft = function () {
    app.api('/api/draft' + self.draftQuery()).then(function (d) {
      var dr = d && d.draft;
      if (!dr || !dr.items || !dr.items.length) return;

      var by = {};
      dr.items.forEach(function (x) { by[x.number] = x; });
      var hit = 0;
      self.items.forEach(function (it) {
        var x = by[it.number];
        if (!x) return;              // 词号对不上（换过题）就跳过
        if (x.answer) it.input = x.answer;
        if (x.unknown) UNK.set(it, true);
        if (x.manual) it.manual = true;  // 勾过「算对」的，重判时不覆盖
        hit++;
      });
      if (!hit) return;

      // render() 没给 input 写 value，得手动塞回去
      var m = self.o.mount;
      self.items.forEach(function (it, i) {
        var inp = m.querySelector('input[data-i="' + i + '"]');
        if (inp && it.input) inp.value = it.input;
        self.renderResult(i);
      });
      self.updateCount();
      self.refreshSummary();
      self.showDraftBar(dr);
    }).catch(function () {
      // 读不到草稿 = 没有，不影响正常使用
    });
  };

  self.showDraftBar = function (dr) {
    var old = el(self.o.prefix + '-draftBar');
    if (old && old.parentNode) old.parentNode.removeChild(old);

    var bar = document.createElement('div');
    bar.className = 'draft-bar';
    bar.id = self.o.prefix + '-draftBar';
    bar.innerHTML = '已恢复上次未完成的练习' +
      (dr.saved_at ? '（存于 ' + esc(dr.saved_at) + '）' : '') +
      '　<button type="button" class="linkbtn">清空草稿</button>';
    self.o.mount.insertBefore(bar, self.o.mount.firstChild);
    self.draftBar = bar;

    bar.querySelector('button').addEventListener('click', function () {
      self.items.forEach(function (it, i) {
        it.input = ''; it.unknown = false; it.manual = false;
        it.blank = false; it.correct = null;
        var inp = self.o.mount.querySelector('input[data-i="' + i + '"]');
        if (inp) inp.value = '';
        self.renderResult(i);
      });
      self.updateCount();
      self.refreshSummary();
      self.dropDraftBar();
      self.clearDraft();
      app.toast('草稿已清空');
    });
  };

  self.dropDraftBar = function () {
    if (self.draftBar && self.draftBar.parentNode) {
      self.draftBar.parentNode.removeChild(self.draftBar);
    }
    self.draftBar = null;
  };

  return self;
}
