// 列表复习 / 钉子户。
//
// 两个模式共用这一页：拉数据的接口不同（/api/plan vs /api/hard），
// 提交时 hard 标志不同，其余交互完全一样。
//
// ⚠️ 两条跟网页端一致的红线：
//   1. 填了答案但没点「对答案」就提交 = 白练（判分只在对答案时发生）。
//      所以提交前如果发现还有没判分的，先自动判一次。
//   2. review_items 快照必须跟着一起发，不然当天回看页看不到自己写了什么。

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
    answered: 0
  },

  onLoad(q) {
    const mode = q && q.mode === 'hard' ? 'hard' : 'words';
    this.setData({ mode });
    wx.setNavigationBarTitle({ title: mode === 'hard' ? '钉子户' : '列表复习' });
    this.load();
  },

  load() {
    this.setData({ loading: true, err: '' });
    // ⚠️ 不传 date：日期一律由服务器按自己的时钟定，跟网页端一致。
    // 传设备本地日期在跨时区时（老师在东京比服务器快 1 小时）会把「今天」
    // 算成未来日期 → 服务端走只读分支 → 不上传 plan → 回写时序号映射不到词，白练。
    const path = this.data.mode === 'hard'
      ? '/api/hard'
      : '/api/plan?mode=words';
    app.request(path, 'GET', {}, true)
      .then((res) => {
        const words = res.words || [];
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
          unknown: false
        }));
        this.setData({ items, date: res.date || '', loading: false, graded: false, summary: null });
        if (!items.length) {
          wx.showToast({ title: '今天没有要练的', icon: 'none' });
        }
      })
      .catch((e) => this.setData({ err: e.message, loading: false }));
  },

  onInput(e) {
    const i = e.currentTarget.dataset.index;
    const patch = {};
    patch['items[' + i + '].answer'] = e.detail.value;
    // 改了答案就把判分结果清掉，避免"改完还显示上一次的对错"
    patch['items[' + i + '].graded'] = false;
    patch['items[' + i + '].correct'] = false;
    this.setData(patch);
    this.refreshAnswered();
  },

  onUnknown(e) {
    const i = e.currentTarget.dataset.index;
    const it = this.data.items[i];
    const patch = {};
    patch['items[' + i + '].unknown'] = !it.unknown;
    patch['items[' + i + '].graded'] = !it.unknown;
    patch['items[' + i + '].correct'] = false; // 标「不会」就是错
    this.setData(patch);
    this.refreshAnswered();
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
