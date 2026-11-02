// 词汇总表：查词用，不练也不计档。
//
// 数据全量拉一次（约 900 词），搜索在本地做 —— 手机上每敲一个字就打一次接口太慢。
// 老师只留「列表 + 搜索」，三个筛选下拉（词性/阶段/课）在 2026-10-21 去掉了。
//
// ⚠️ 900 行不能一次渲染完，会卡：先渲染 60 条，上拉再补（onReachBottom）。

const app = getApp();
const trial = require('../../utils/trial.js');
const PAGE = 60;

// 档案里的 status 带 emoji（🟢已掌握），列表里只留文字 + 一个色点，
// 否则每行前面挂个表情很吵，而且不同机型 emoji 宽度不一致对不齐。
function splitStatus(s) {
  const t = (s || '').replace(/[\u{1F300}-\u{1FAFF}\u{2600}-\u{27BF}\u{FE0F}]/gu, '').trim();
  let cls = 'st-none';
  if (t.indexOf('已掌握') >= 0) cls = 'st-ok';
  else if (t.indexOf('基本掌握') >= 0) cls = 'st-mid';
  else if (t.indexOf('待巩固') >= 0) cls = 'st-bad';
  else if (t.indexOf('待测试') >= 0) cls = 'st-new';
  return { text: t || '—', cls: cls };
}

function rate(it) {
  if (!it.reviews) return 0;
  return Math.round(((it.reviews - it.errors) / it.reviews) * 100);
}

Page({
  data: {
    loading: true,
    err: '',
    all: [],
    view: [],
    shown: 0,
    total: 0,
    filtered: 0,

    q: '',
    openIdx: -1,
    // 试用态（未登录）：搜索框锁死，点一下弹登录浮层 —— 详见 utils/trial.js
    trial: false,
    needLogin: false
  },

  // 冷启动领试用身份是异步的，进页面那一刻可能还没拿到 —— onShow 里每次同步一次
  onShow: trial.sync,
  onTapLocked: trial.onTapLocked,
  closeLogin: trial.closeLogin,
  goLogin: trial.goLogin,
  noop: trial.noop,

  onLoad() {
    // 直接进这页（比如分享/扫码）时可能还没身份。
    // ⛔ 不能在这里 app.login()：那是替陌生人注册账号，也违反「先浏览后登录」。
    //    领试用身份就够看数据了（服务端对 trial 返回内置词表）。
    if (!app.globalData.logged) {
      app.loginTrial(() => this.load());
    } else {
      this.load();
    }
  },

  load() {
    this.setData({ loading: true, err: '' });
    app.request('/api/lexicon', 'GET', {}, true)
      .then((res) => {
        const items = (res.items || []).map((it) => {
          const st = splitStatus(it.status);
          return Object.assign({}, it, {
            stText: st.text,
            stCls: st.cls,
            rate: rate(it),
            // 服务端叫 word_note，页面里统一读 wordNote
            wordNote: it.word_note || '',
            // 搜索用的拼串，渲染前算好（WXML 里不能 join）
            hay: ((it.kana || '') + (it.kanji || '') + (it.def || '') + (it.word || '')).toLowerCase()
          });
        });

        this.setData({ all: items, total: items.length, loading: false });
        this.apply();
      })
      .catch((e) => {
        this.setData({ loading: false, err: (e && e.message) || '加载失败' });
      });
  },

  onSearch(e) {
    this.setData({ q: e.detail.value });
    clearTimeout(this._t);
    this._t = setTimeout(() => this.apply(), 250);
  },

  // 搜 + 取前 N 条。顺序按词号（= 档案顺序），跟 web 默认一致。
  apply() {
    const d = this.data;
    const q = (d.q || '').trim().toLowerCase();

    const rows = q
      ? d.all.filter((it) => it.hay.indexOf(q) >= 0)
      : d.all.slice();

    this._rows = rows;
    this.setData({ filtered: rows.length, shown: PAGE, openIdx: -1, view: rows.slice(0, PAGE) });
  },

  onReachBottom() {
    if (!this._rows || this.data.shown >= this._rows.length) return;
    const n = Math.min(this.data.shown + PAGE, this._rows.length);
    this.setData({ shown: n, view: this._rows.slice(0, n) });
  },

  // 点一行展开详情（同时只展开一个，避免整屏撑开找不到刚才看的那条）
  toggle(e) {
    const i = +e.currentTarget.dataset.index;
    this.setData({ openIdx: this.data.openIdx === i ? -1 : i });
  },

  onUnload() { clearTimeout(this._t); }
});
