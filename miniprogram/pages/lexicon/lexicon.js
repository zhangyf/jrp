// 词汇总表：查词用，不练也不计档。
//
// 数据全量拉一次（约 900 词），筛选和搜索都在本地做 —— 手机上每次筛一下就
// 打一次接口太慢，而且这份数据一次也就一两百 KB。
//
// ⚠️ 900 行不能一次渲染完，会卡：先渲染 60 条，上拉再补（onReachBottom）。

const app = getApp();
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

// 阶段按学习顺序排，别按拼音/字母排（字母序会把「待测试」排到「已掌握」前面）
const STATUS_ORDER = ['已掌握', '基本掌握', '待巩固', '待测试'];

function sortStatus(a, b) {
  const ia = STATUS_ORDER.indexOf(a);
  const ib = STATUS_ORDER.indexOf(b);
  if (ia >= 0 && ib >= 0) return ia - ib;
  if (ia >= 0) return -1;
  if (ib >= 0) return 1;
  return a < b ? -1 : (a > b ? 1 : 0);
}

// 课按数字排：「第10课」要排在「第2课」后面，不能按字符串比
function lessonNo(s) {
  const m = /(\d+)/.exec(s || '');
  return m ? +m[1] : 9999;
}

function sortGroups(a, b) {
  const d = lessonNo(a) - lessonNo(b);
  return d !== 0 ? d : (a < b ? -1 : (a > b ? 1 : 0));
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
    posList: ['全部词性'],
    posIndex: 0,
    statusList: ['全部阶段'],
    statusIndex: 0,
    groupList: ['全部课'],
    groupIndex: 0,

    openIdx: -1
  },

  onLoad() {
    // 直接进这页（比如分享/扫码）时可能还没登录，先补一次静默登录再拉数据
    if (!app.globalData.logged) {
      app.login(() => this.load());
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

        const posSet = {};
        const stSet = {};
        const grpSet = {};
        items.forEach((it) => {
          if (it.pos) posSet[it.pos] = 1;
          if (it.stText && it.stText !== '—') stSet[it.stText] = 1;
          if (it.group) grpSet[it.group] = 1;
        });

        this.setData({
          all: items,
          total: items.length,
          posList: ['全部词性'].concat(Object.keys(posSet).sort()),
          statusList: ['全部阶段'].concat(Object.keys(stSet).sort(sortStatus)),
          groupList: ['全部课'].concat(Object.keys(grpSet).sort(sortGroups)),
          loading: false
        });
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

  onPickPos(e) { this.setData({ posIndex: +e.detail.value }); this.apply(); },
  onPickStatus(e) { this.setData({ statusIndex: +e.detail.value }); this.apply(); },
  onPickGroup(e) { this.setData({ groupIndex: +e.detail.value }); this.apply(); },

  resetFilter() {
    this.setData({ q: '', posIndex: 0, statusIndex: 0, groupIndex: 0 });
    this.apply();
  },

  // 筛 + 排序 + 取前 N 条。排序按词号（= 档案顺序），跟 web 默认一致。
  apply() {
    const d = this.data;
    const q = (d.q || '').trim().toLowerCase();
    const pos = d.posIndex > 0 ? d.posList[d.posIndex] : '';
    const st = d.statusIndex > 0 ? d.statusList[d.statusIndex] : '';
    const grp = d.groupIndex > 0 ? d.groupList[d.groupIndex] : '';

    const rows = d.all.filter((it) => {
      if (pos && (it.pos || '') !== pos) return false;
      if (st && it.stText !== st) return false;
      if (grp && (it.group || '') !== grp) return false;
      if (q && it.hay.indexOf(q) < 0) return false;
      return true;
    });

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
