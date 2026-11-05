// 活用练习：给原词，写出活用形。
//
// ⚠️ 只练不计档 —— 不调 /api/record，不写档案、不进统计（跟网页端一致）。
// 判分复用 /api/grade：把服务端给的多个写法用「/」连起来当目标词，
// 写任意一个就算对（如 じゃありません/ではありません）。

const app = getApp();
const { posSymbol } = require('../../utils/pos.js');
const trial = require('../../utils/trial.js');

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
    pool: 0,
    // 试用态（未登录）：输入框锁死，点一下弹登录浮层 —— 详见 utils/trial.js
    trial: true, // ⚠️ 默认锁：onShow / 广播同步到真身份才解开（见 utils/trial.js）
    needLogin: false
  },

  // 冷启动领试用身份是异步的，进页面那一刻可能还没拿到 —— onShow 里每次同步一次
  onShow: trial.sync,
  syncTrial: trial.sync, // 身份变了由 app.broadcastIdentity() 叫到
  onTapLocked: trial.onTapLocked,
  closeLogin: trial.closeLogin,
  goLogin: trial.goLogin,
  noop: trial.noop,

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
          posSym: posSymbol(it),
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

  // 「换一批」：试用态不响应真实动作，弹登录浮层（按钮层已置灰，双保险）
  reload() {
    if (trial.locked(this)) { this.onTapLocked(); return; }
    this.load();
  },

  grade() {
    if (trial.locked(this)) { this.onTapLocked(); return; }
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
