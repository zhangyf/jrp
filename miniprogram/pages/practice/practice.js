// 列表复习 / 钉子户。
//
// 两个模式共用这一页：拉数据的接口不同（/api/plan vs /api/hard），
// 提交时 hard 标志不同，其余交互完全一样。
//
// 跟网页端对齐的三件事（web 是 list.js + practice.js 的 showReview/loadDraft）：
//   1. 回写后到期词被推到未来，/api/plan 会变空 —— 这时不是显示「没有要练的」，
//      而是拉 /api/review 当天快照，整页变只读回看（练了什么、哪题错都在）。
//   2. 写一半的答案存 /api/draft（COS），换设备能接着写；打开页面先回填草稿。
//   3. 填了答案但没判分就提交 = 白练 —— 提交前自动先判一次。
//
// ⚠️ 拉计划不传 date：日期由服务器定。传设备本地日期在东京（快 1 小时）
// 会把「今天」判成未来 → read_only → 回写题号映射不到词，白练。

const app = getApp();

Page({
  data: {
    mode: 'words',
    date: '',
    items: [],
    loading: true,
    err: '',
    graded: false,
    submitting: false,
    summary: null,
    answered: 0,
    // 只读回看：今天已回写完（plan 空了）时整页不可编辑，
    // 显示当时的答案和对错 —— 跟 web 端列表模式的回看态一致。
    reviewOnly: false,
    // 第二轮：只练今天的错词。⚠️ 不回写 —— 同一个词一天只能进档案一次，
    // 回写两次会把艾宾浩斯间隔和 LastReview 各改一遍（web 的 list.js 同样处理）。
    requeue: false,
    // 体验版（试用账号）：题是服务端给的一套固定词，能写能判分，
    // 但 /api/record 不在体验版白名单里 —— 提交按钮收掉，进度不落档案。
    readOnly: false
  },

  onLoad(q) {
    const mode = q && q.mode === 'hard' ? 'hard' : 'words';
    this.setData({ mode });
    wx.setNavigationBarTitle({ title: mode === 'hard' ? '钉子户' : '列表复习' });
    this.load();
  },

  // 草稿 / 快照的模式名，跟服务端白名单（words / hard）对齐
  serverMode() {
    return this.data.mode === 'hard' ? 'hard' : 'words';
  },

  load() {
    this.setData({ loading: true, err: '', requeue: false });
    if (this.data.mode === 'hard') {
      // ⚠️ 钉子户不做到期过滤，/api/hard 每次都返回同一批词 —— 所以跟 web 的
      // hard.js 一样：先查今天有没有已回写的快照，有就直接只读回看。
      // 否则会不知不觉把同一批词再练一遍，一回写就是二次改档案（间隔被改两次）。
      app.request('/api/review?mode=hard', 'GET', {}, true)
        .then((res) => {
          const r = res.review;
          if (r && r.items && r.items.length) { this.applyReview(r); return; }
          this.loadPlan('/api/hard');
        })
        .catch((e) => this.setData({ err: e.message, loading: false }));
      return;
    }
    this.loadPlan('/api/plan?mode=words');
  },

  loadPlan(path) {
    app.request(path, 'GET', {}, true)
      .then((res) => {
        const words = res.words || [];
        // 体验版：/api/plan /api/hard 会带 read_only + trial
        this.setData({ date: res.date || '', readOnly: !!res.read_only });
        if (!words.length) {
          // plan 空 = 今天的到期词都回写过了 → 拉当天快照只读回看。
          // 快照也没有（真的一天没练）才是真的「没有要练的」。
          this.loadReview();
          return;
        }
        const items = words.map((w, i) => ({
          number: w.number || i + 1,
          word: w.word || '',
          definition: w.definition || '',
          group: w.group || '',
          status: w.status || '',
          pos: w.pos || '',
          sub: w.sub || '',
          note: w.note || '',
          answer: '',
          correct: false,
          graded: false,
          unknown: false,
          manual: false
        }));
        this.setData({
          items, loading: false, graded: false, summary: null,
          answered: 0, reviewOnly: false
        });
        if (this.data.readOnly) return; // 体验版没有草稿，也不用回填
        this.loadDraft();
      })
      .catch((e) => this.setData({ err: e.message, loading: false }));
  },

  // 只读回看：GET /api/review?mode=words|hard（回写成功时服务端存的当天快照）。
  // 直接用快照里的判分结果，不重判 —— 回看显示的是当时发生了什么。
  loadReview() {
    app.request('/api/review?mode=' + this.serverMode(), 'GET', {}, true)
      .then((res) => {
        const r = res.review;
        if (!r || !r.items || !r.items.length) {
          this.setData({ loading: false, items: [], reviewOnly: false });
          wx.showToast({ title: '今天没有要练的', icon: 'none' });
          return;
        }
        this.applyReview(r);
      })
      .catch((e) => this.setData({ err: e.message, loading: false }));
  },

  // 「再练这 N 个错词」：回看态里的入口，跟 web 回看卡上那个按钮一个意思。
  // 只取判错的行（含主动标「不会」的），不回写 —— 见 data 里 requeue 的说明。
  onRequeue() {
    const wrongs = this.data.items.filter((it) => !it.blank && !it.correct);
    if (!wrongs.length) {
      wx.showToast({ title: '今天没有错的词', icon: 'none' });
      return;
    }
    const items = wrongs.map((it) => ({
      number: it.number,
      word: it.word,
      definition: it.definition,
      group: it.group,
      status: it.status,
      pos: it.pos,
      sub: it.sub,
      note: it.note,
      answer: '',
      correct: false,
      graded: false,
      unknown: false,
      manual: false,
      blank: false
    }));
    this.setData({
      items,
      requeue: true,
      reviewOnly: false,
      graded: false,
      answered: 0,
      summary: null
    });
    wx.pageScrollTo({ scrollTop: 0, duration: 200 });
  },

  applyReview(r) {
    const items = r.items.map((x) => ({
      number: x.number,
      word: x.word || '',
      definition: x.definition || '',
      group: x.group || '',
      status: x.status || '',
      pos: x.pos || '',
      sub: x.sub || '',
      note: x.note || '',
      answer: x.answer || '',
      // graded = 这题参与过判分（没写的行只是跳过，不算对错）
      graded: !x.blank,
      correct: !!x.correct,
      unknown: !!x.unknown,
      manual: !!x.manual,
      blank: !!x.blank
    }));
    const done = items.filter((i) => !i.blank);
    const ok = done.filter((i) => i.correct).length;
    this.setData({
      items,
      date: r.date || this.data.date,
      loading: false,
      reviewOnly: true,
      summary: {
        total: items.length, ok, wrong: done.length - ok,
        blank: items.length - done.length
      }
    });
  },

  // 草稿回填：GET /api/draft?mode=X&date=Y。手机写到一半、电脑上接着写（反之亦然）。
  // 只回填答案和「不会」；对错不存，判分时重算。
  loadDraft() {
    const q = '?mode=' + this.serverMode() + '&date=' + encodeURIComponent(this.data.date);
    app.request('/api/draft' + q, 'GET', {}, true)
      .then((res) => {
        const dr = res.draft;
        if (!dr || !dr.items || !dr.items.length) return;
        const by = {};
        dr.items.forEach((x) => { by[x.number] = x; });
        const patch = {};
        let hit = 0;
        this.data.items.forEach((it, i) => {
          const x = by[it.number];
          if (!x) return; // 词号对不上（换过题）就跳过
          if (x.answer) patch['items[' + i + '].answer'] = x.answer;
          if (x.unknown) {
            patch['items[' + i + '].unknown'] = true;
            patch['items[' + i + '].graded'] = true;
            patch['items[' + i + '].correct'] = false; // 标「不会」就是错
            patch['items[' + i + '].manual'] = false;
          }
          // 勾过「算对」的行没有可重算的对错（重判要跳过 manual），
          // correct 必须一并恢复，否则提交时会被当成未判漏掉。
          if (x.manual && !x.unknown) {
            patch['items[' + i + '].manual'] = true;
            patch['items[' + i + '].correct'] = true;
            patch['items[' + i + '].graded'] = true;
          }
          hit++;
        });
        if (!hit) return;
        this.setData(patch);
        this.refreshAnswered();
      })
      .catch(() => { /* 读不到草稿 = 没有，不影响正常使用 */ });
  },

  // 草稿保存：防抖 1.2 秒（跟 web 一致，每敲一个字发一次太吵）。
  // 静默失败 —— 草稿存不上不该打断练习。
  scheduleDraft() {
    if (this.data.reviewOnly || this.data.readOnly || !this.data.date) return;
    clearTimeout(this._draftTimer);
    this._draftTimer = setTimeout(() => this.saveDraft(), 1200);
  },

  saveDraft() {
    const items = this.data.items.map((it) => ({
      number: it.number,
      answer: it.answer || '',
      unknown: !!it.unknown,
      manual: !!it.manual
    }));
    app.request('/api/draft', 'POST', {
      date: this.data.date,
      mode: this.serverMode(),
      items
    }, true).catch(() => {});
  },

  clearDraft() {
    clearTimeout(this._draftTimer);
    const q = '?mode=' + this.serverMode() + '&date=' + encodeURIComponent(this.data.date);
    app.request('/api/draft' + q, 'DELETE', {}, true).catch(() => {});
  },

  onUnload() {
    clearTimeout(this._draftTimer);
  },

  onInput(e) {
    const i = e.currentTarget.dataset.index;
    const patch = {};
    patch['items[' + i + '].answer'] = e.detail.value;
    // 改了答案就把这一行的判分状态整体作废，避免"改完还是上一次的结论"：
    //   graded/correct —— 不能留上一次的对错
    //   manual —— 勾过「算对」的也要清，否则重判会跳过它，改了等于没改
    //   unknown —— 写了答案就不再是「不会」
    patch['items[' + i + '].graded'] = false;
    patch['items[' + i + '].correct'] = false;
    patch['items[' + i + '].manual'] = false;
    patch['items[' + i + '].unknown'] = false;
    this.setData(patch);
    this.refreshAnswered();
    this.scheduleDraft();
  },

  // 输入框失焦时把这一题重判一次：改完立刻看到新对错，不用再点「对答案」。
  // 只发这一题（复用 regradeOne），别整屏重判把别处的人工改判冲掉。
  onBlur(e) {
    const i = e.currentTarget.dataset.index;
    const it = this.data.items[i];
    if (!it || it.graded || it.unknown) return;      // 已判过 / 标了不会，不用管
    if (!(it.answer || '').trim()) return;           // 没写答案没什么可判
    this.regradeOne(i);
  },

  onUnknown(e) {
    const i = e.currentTarget.dataset.index;
    const it = this.data.items[i];
    const patch = {};
    patch['items[' + i + '].unknown'] = !it.unknown;
    patch['items[' + i + '].graded'] = !it.unknown;
    patch['items[' + i + '].correct'] = false; // 标「不会」就是错
    patch['items[' + i + '].manual'] = false;  // 「不会」与「算对」互斥
    this.setData(patch);
    this.refreshAnswered();
    this.scheduleDraft();
  },

  // 「算对」：判错的行可以人工改判成对 —— 老师写了汉字被判错、或判分档位
  // 的问题，不是真不会。跟 web 的复选框一个意思（manual = 老师动过手）：
  //   · 勾上：按对记，重判时不覆盖
  //   · 取消：回到「按写的判」，这一题重新判一次
  // ⚠️ 主动标了「不会」的行不给改判（web 也是这样）。
  onPick(e) {
    const i = e.currentTarget.dataset.index;
    const it = this.data.items[i];
    if (!it.manual) {
      this.setData({
        ['items[' + i + '].manual']: true,
        ['items[' + i + '].correct']: true,
        ['items[' + i + '].unknown']: false,
        ['items[' + i + '].graded']: true
      });
      this.scheduleDraft();
      return;
    }
    this.setData({ ['items[' + i + '].manual']: false });
    this.regradeOne(i);
    this.scheduleDraft();
  },

  // 取消「算对」后重判这一题（只发这一题，别整屏重判把别的改判冲掉）
  regradeOne(i) {
    const it = this.data.items[i];
    const answer = (it.answer || '').trim();
    if (!answer) {
      // 没写答案就没什么可判的：恢复成「没写」
      this.setData({
        ['items[' + i + '].correct']: false,
        ['items[' + i + '].graded']: false
      });
      return;
    }
    app.request('/api/grade', 'POST', {
      mode: 'either',
      items: [{ word: it.word, answer: answer }]
    }, true)
      .then((res) => {
        this.setData({
          ['items[' + i + '].correct']: !!(res.results || [])[0],
          ['items[' + i + '].graded']: true
        });
      })
      .catch(() => {});
  },

  refreshAnswered() {
    const n = this.data.items.filter((it) => (it.answer || '').trim() !== '' || it.unknown).length;
    this.setData({ answered: n });
  },

  // 判分：整屏一次请求（不是每敲一个字打一次）。判分规则在服务端，跟网页同源。
  // 返回 Promise，提交时会串在前面 —— 「填了没判就回写 = 白练」这条红线靠它兜住。
  grade() {
    const items = this.data.items;
    const payload = {
      mode: 'either',
      items: items.map((it) => ({ word: it.word, answer: it.unknown ? '' : it.answer }))
    };
    wx.showLoading({ title: '判分中' });
    return app.request('/api/grade', 'POST', payload, true)
      .then((res) => {
        wx.hideLoading();
        const rs = res.results || [];
        const patch = {};
        items.forEach((it, i) => {
          patch['items[' + i + '].graded'] = true;
          // ⚠️ 老师勾过「算对」的行不重判，否则手动改判会被冲掉
          if (it.manual) { patch['items[' + i + '].correct'] = true; return; }
          patch['items[' + i + '].correct'] = it.unknown ? false : !!rs[i];
        });
        patch.graded = true;
        this.setData(patch);
      })
      .catch((e) => {
        wx.hideLoading();
        wx.showToast({ title: e.message, icon: 'none' });
        throw e;
      });
  },

  submit() {
    // 体验版不落档案 —— 双保险：按钮层已经藏了，这里再挡一次
    if (this.data.readOnly) {
      wx.showToast({ title: '体验版不保存进度', icon: 'none' });
      return;
    }
    // 巩固轮不进档案 —— 双保险：按钮层已经藏了，这里再挡一次
    if (this.data.requeue) {
      wx.showToast({ title: '巩固轮不计入档案', icon: 'none' });
      return;
    }
    const items = this.data.items;
    const touched = (it) => it.graded || (it.answer || '').trim() !== '' || it.unknown;
    if (!items.some(touched)) {
      wx.showToast({ title: '还没写答案', icon: 'none' });
      return;
    }
    // 写了但还没判分的：先判一次再落库，别让老师多点一次。
    const pending = items.some((it) => !it.graded && ((it.answer || '').trim() !== '' || it.unknown));
    if (pending) {
      this.grade().then(() => this.doSubmit()).catch(() => {});
      return;
    }
    this.doSubmit();
  },

  doSubmit() {
    const items = this.data.items;
    const done = items.filter((it) => it.graded || ((it.answer || '').trim() !== '' && !it.unknown));

    const wordResults = [];
    const reviewItems = [];
    done.forEach((it) => {
      const correct = it.unknown ? false : it.correct;
      wordResults.push({ number: it.number, correct: correct });
      reviewItems.push({
        number: it.number,
        word: it.word,
        definition: it.definition,
        group: it.group,
        status: it.status,
        answer: it.answer,
        correct: correct,
        unknown: !!it.unknown,
        manual: !!it.manual,
        blank: (it.answer || '').trim() === '' && !it.unknown
      });
    });

    this.setData({ submitting: true });
    app.request('/api/record', 'POST', {
      plan_date: this.data.date,
      language: 'ja',
      hard: this.data.mode === 'hard',
      word_results: wordResults,
      review_items: reviewItems
    }, true)
      .then(() => {
        this.setData({ submitting: false });
        // 已落库，草稿没用了 —— 不删的话下次打开会恢复出一堆早就写完的答案
        this.clearDraft();
        const ok = wordResults.filter((r) => r.correct).length;
        wx.showModal({
          title: '已提交 ' + wordResults.length + ' 题',
          content: '对 ' + ok + ' / 错 ' + (wordResults.length - ok),
          showCancel: false,
          success: () => this.load()
        });
      })
      .catch((e) => {
        this.setData({ submitting: false });
        wx.showToast({ title: e.message, icon: 'none' });
      });
  }
});
