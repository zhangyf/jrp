// 造句：看中文写整句。
//
// 判句走 /api/judge-sentence（服务端：字面一致直接判对，字面不一致才问模型）。
// 模型判一句要几秒，所以「对答案」是**并行**发所有已写的句子，不是一句句排队。

const app = getApp();

Page({
  data: {
    date: '',
    items: [],
    loading: true,
    err: '',
    grading: false,
    submitting: false
  },

  onLoad() {
    this.load();
  },

  load() {
    this.setData({ loading: true, err: '' });
    // 不传 date，同列表复习：日期由服务器定，避免跨时区的只读分支。
    app.request('/api/plan?mode=sentences', 'GET', {}, true)
      .then((res) => {
        const list = res.sentences || [];
        const items = list.map((s, i) => ({
          number: s.number || i + 1,
          chinese: s.chinese || '',
          answer: s.answer || '', // 原句，提交时用来定位
          input: '',
          correct: false,
          graded: false,
          reason: ''
        }));
        this.setData({ items, date: res.date || '', loading: false });
      })
      .catch((e) => this.setData({ err: e.message, loading: false }));
  },

  onInput(e) {
    const i = e.currentTarget.dataset.index;
    const patch = {};
    patch['items[' + i + '].input'] = e.detail.value;
    patch['items[' + i + '].graded'] = false;
    this.setData(patch);
  },

  grade() {
    const items = this.data.items;
    const targets = [];
    items.forEach((it, i) => {
      if ((it.input || '').trim() !== '') targets.push(i);
    });
    if (!targets.length) {
      wx.showToast({ title: '还没写句子', icon: 'none' });
      return Promise.resolve();
    }

    this.setData({ grading: true });
    wx.showLoading({ title: '判 ' + targets.length + ' 句' });

    return Promise.all(targets.map((i) => {
      const it = items[i];
      return app.request('/api/judge-sentence', 'POST', {
        answer: it.answer,
        chinese: it.chinese,
        input: it.input
      }, true).then((res) => ({ i, res }))
        .catch((e) => ({ i, res: { correct: false, reason: e.message } }));
    })).then((outs) => {
      wx.hideLoading();
      const patch = {};
      outs.forEach((o) => {
        patch['items[' + o.i + '].graded'] = true;
        patch['items[' + o.i + '].correct'] = !!o.res.correct;
        patch['items[' + o.i + '].reason'] = o.res.reason || '';
      });
      patch.grading = false;
      this.setData(patch);
    });
  },

  submit() {
    const items = this.data.items;
    const written = items.filter((it) => (it.input || '').trim() !== '');
    if (!written.length) {
      wx.showToast({ title: '还没写句子', icon: 'none' });
      return;
    }
    // 写了没判的就先判一遍再落库 —— 跟列表复习同一条红线。
    if (written.some((it) => !it.graded)) {
      this.grade().then(() => this.doSubmit()).catch(() => {});
      return;
    }
    this.doSubmit();
  },

  doSubmit() {
    const done = this.data.items.filter((it) => it.graded);
    if (!done.length) {
      wx.showToast({ title: '先对答案再提交', icon: 'none' });
      return;
    }
    this.setData({ submitting: true });
    app.request('/api/record', 'POST', {
      plan_date: this.data.date,
      language: 'ja',
      sentence_results: done.map((it) => ({
        number: it.number,
        correct: it.correct,
        answer: it.answer,
        chinese: it.chinese
      }))
    }, true)
      .then(() => {
        this.setData({ submitting: false });
        const ok = done.filter((it) => it.correct).length;
        wx.showModal({
          title: '已提交 ' + done.length + ' 句',
          content: '对 ' + ok + ' / 错 ' + (done.length - ok),
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
