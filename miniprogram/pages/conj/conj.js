// 活用练习：给原词，写出活用形。
//
// ⚠️ 只练不计档 —— 不调 /api/record，不写档案、不进统计（跟网页端一致）。
// 判分复用 /api/grade：把服务端给的多个写法用「/」连起来当目标词，
// 写任意一个就算对（如 じゃありません/ではありません）。

const app = getApp();

const TYPES = [
  { key: 'verb', label: '动词' },
  { key: 'verb_nai', label: '动词ない形' },
  { key: 'adj', label: '形容词' }
];

Page({
  data: {
    types: TYPES,
    type: 'verb',
    count: 10,
    items: [],
    loading: true,
    err: '',
    graded: false,
    pool: 0
  },

  onLoad() {
    this.load();
  },

  onType(e) {
    this.setData({ type: e.currentTarget.dataset.type }, () => this.load());
  },

  load() {
    this.setData({ loading: true, err: '', graded: false });
    app.request('/api/conjugation?type=' + this.data.type + '&count=' + this.data.count,
      'GET', {}, true)
      .then((res) => {
        const items = (res.items || []).map((it, i) => ({
          idx: i,
          word: it.word || '',
          definition: it.definition || '',
          pos: it.pos || '',
          sub: it.sub || '',
          tasks: (it.tasks || []).map((t) => ({
            label: t.label || '',
            answers: t.answers || [],
            // expected 喂给 /api/grade 当目标词（多写法用 / 分隔，写任意一个算对）；
            // expectedText 只是给页面显示用 —— WXML 里不能直接调 .join()，得在这拼好。
            expected: (t.answers || []).join('/'),
            expectedText: (t.answers || []).join(' / '),
            input: '',
            correct: false
          }))
        }));
        this.setData({ items, pool: res.pool || 0, loading: false });
      })
      .catch((e) => this.setData({ err: e.message, loading: false }));
  },

  onInput(e) {
    const i = e.currentTarget.dataset.index;
    const t = e.currentTarget.dataset.task;
    const patch = {};
    patch['items[' + i + '].tasks[' + t + '].input'] = e.detail.value;
    this.setData(patch);
  },

  grade() {
    const items = this.data.items;
    const payload = { mode: 'either', items: [] };
    items.forEach((it) => {
      it.tasks.forEach((t) => {
        payload.items.push({ word: t.expected, answer: t.input });
      });
    });
    if (!payload.items.length) {
      wx.showToast({ title: '还没有题', icon: 'none' });
      return;
    }
    wx.showLoading({ title: '判分中' });
    app.request('/api/grade', 'POST', payload, true)
      .then((res) => {
        wx.hideLoading();
        const rs = res.results || [];
        let k = 0;
        const patch = {};
        items.forEach((it, i) => {
          it.tasks.forEach((t, j) => {
            patch['items[' + i + '].tasks[' + j + '].correct'] = !!rs[k];
            k += 1;
          });
        });
        patch.graded = true;
        this.setData(patch);
      })
      .catch((e) => {
        wx.hideLoading();
        wx.showToast({ title: e.message, icon: 'none' });
      });
  }
});
