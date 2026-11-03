// 新标日III知识点 —— 按课看知识点总结（2026-11-03 老师要求）。
//
// 三条口径，改之前先读：
//
// 1. **markdown 不在这里解析**。服务端已经把 md 拆成结构化块（h1/h2/h3/p/quote/
//    list/table）发下来了，这里只按 type 摆样式。小程序没有 markdown 渲染能力，
//    而且「哪几块模糊」必须按块切 —— 拆块这件事只能在服务端做。
//
// 2. **未登录也能进，只是看不全**。接口 /api/knowledge 对匿名放行，
//    返回一个 visible 数字：下标 >= visible 的块盖模糊。这是展示层的软限制
//    （抓包能拿全文），目的是引导登录，不是防攻击。
//    ⚠️ 体验版（试用身份）也算未登录 —— 它的凭证是公开的。
//
// 3. **登录后要能当场解开**。app.broadcastIdentity() 会叫到 syncTrial，
//    里面重新拉一遍当前课；utils/trial.js 的 goLogin 登录成功后也会调 load()。

const app = getApp();
const trial = require('../../utils/trial.js');
const LAST_KEY = 'jrp_k_lesson'; // 记住上次看到第几课，下次进来直接落在那儿

// prepBlocks 给有序列表的每一项补一个序号。
// ⚠️ 别在 WXML 里写 `index + 1 + '.'` —— WXML 表达式的运算支持很有限，
//    数字跟字符串拼在一起容易渲染成 NaN 或空。能算的一律在 JS 里算好。
function prepBlocks(blocks) {
  (blocks || []).forEach((b) => {
    if (b.type === 'list' && b.items) {
      b.items.forEach((it, i) => { it.no = String(i + 1) + '.'; });
    }
  });
  return blocks;
}

Page({
  data: {
    lessons: [],   // [1,2,...,21]
    cur: 0,
    title: '',
    blocks: [],
    visible: 0,    // 未登录时能看到几块（>= visible 的块盖模糊）
    locked: false, // 服务端说的：这次是不是只给了部分
    loading: true,
    err: '',
    trial: false,   // 未登录（含体验版）
    needLogin: false // 登录浮层
  },

  onLoad() {
    this.loadList();
  },

  onShow() {
    trial.sync(this);
  },

  // 身份变了（登录 / 退出 / 验票拿到新 role）→ 重新拉当前课，模糊当场解开或盖回去。
  syncTrial() {
    trial.sync(this);
    if (this.data.cur) this.loadLesson(this.data.cur);
  },

  loadList() {
    const self = this;
    app.request('/api/knowledge', 'GET', {}, true)
      .then((res) => {
        const lessons = (res.lessons || []).map((l) => l.lesson);
        if (!lessons.length) {
          self.setData({ loading: false, err: '还没有知识点文档' });
          return;
        }
        // 第一次进落在课上最新那一课（当前进度），之后按上次看的课。
        const last = wx.getStorageSync(LAST_KEY);
        const cur = lessons.indexOf(last) >= 0 ? last : lessons[lessons.length - 1];
        self.setData({ lessons: lessons, cur: cur }, () => self.loadLesson(cur));
      })
      .catch((e) => self.setData({ loading: false, err: e.message || '加载失败' }));
  },

  loadLesson(n) {
    const self = this;
    this.setData({ loading: true, err: '', cur: n });
    wx.setStorageSync(LAST_KEY, n);
    app.request('/api/knowledge?lesson=' + n, 'GET', {}, true)
      .then((res) => {
        const blocks = prepBlocks(res.blocks || []);
        self.setData({
          loading: false,
          title: res.title || ('第' + n + '课'),
          blocks: blocks,
          visible: res.locked ? (res.visible || 0) : blocks.length,
          locked: !!res.locked
        });
      })
      .catch((e) => self.setData({ loading: false, err: e.message || '加载失败' }));
  },

  pick(e) {
    const n = e.currentTarget.dataset.l;
    if (n === this.data.cur) return;
    wx.pageScrollTo({ scrollTop: 0, duration: 0 });
    this.loadLesson(n);
  },

  prev() { this.step(-1); },
  next() { this.step(1); },
  step(d) {
    const ls = this.data.lessons;
    const i = ls.indexOf(this.data.cur);
    const j = i + d;
    if (i < 0 || j < 0 || j >= ls.length) return;
    wx.pageScrollTo({ scrollTop: 0, duration: 0 });
    this.loadLesson(ls[j]);
  },

  // 下面四个是 utils/trial.js 要求的挂法（登录浮层用）
  onTapLocked: trial.onTapLocked,
  closeLogin: trial.closeLogin,
  goLogin: trial.goLogin,
  noop: trial.noop
});
