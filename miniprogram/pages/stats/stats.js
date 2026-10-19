// 统计页。
//
// 只做展示，不写任何东西。数据口径全部由后端 /api/stats 决定，
// 跟网页端、CLI 是同一份 —— 前端不自己算，免得三处口径漂移。

const app = getApp();

const DAY_OPTS = [
  { key: 7, label: '近 7 天' },
  { key: 30, label: '近 30 天' },
  { key: 90, label: '近 90 天' }
];

Page({
  data: {
    dayOpts: DAY_OPTS,
    days: 7,
    loading: true,
    err: '',
    jlpt: null,
    n5Pct: 0,
    n4Pct: 0,
    hard: null,
    lessons: [],
    dist: []
  },

  onLoad() {
    this.load();
  },

  onShow() {
    // 从练习页返回时刷新一次，刚练完的数字才对得上
    if (!this.data.loading) this.load();
  },

  onDays(e) {
    this.setData({ days: Number(e.currentTarget.dataset.days) }, () => this.load());
  },

  load() {
    this.setData({ loading: true, err: '' });
    app.request('/api/stats?days=' + this.data.days, 'GET', {}, true)
      .then((res) => {
        const j = res.jlpt || null;
        const d = res.detail || {};
        const dist = [];
        const m = d.accuracy_distribution || {};
        Object.keys(m).forEach((k) => dist.push({ label: k, value: m[k] }));

        this.setData({
          loading: false,
          jlpt: j,
          n5Pct: j ? pct(j.vocab_firm, j.lesson, j.n5) : 0,
          n4Pct: j ? pct(j.vocab_firm, j.lesson, j.n4) : 0,
          hard: d.hard_words || null,
          lessons: (d.by_lesson || []).slice(0, 12),
          dist: dist
        });
      })
      .catch((e) => this.setData({ err: e.message, loading: false }));
  }
});

// JLPT 进度百分比。
//
// ⚠️ 后端 jlpt.n5 / jlpt.n4 里的 vocab / lesson 是**门槛值**（N5 = 800 词 / 第 22 课），
// 不是当前值；当前值是顶层的 vocab_firm 和 lesson。别把门槛当进度直接显示。
// 词汇与课次各占一半：偏科（词够了但课没跟上）不算达标。
function pct(firm, lesson, lvl) {
  if (!lvl) return 0;
  const v = lvl.vocab ? Math.min(100, Math.round((firm * 100) / lvl.vocab)) : 0;
  const l = lvl.lesson ? Math.min(100, Math.round((lesson * 100) / lvl.lesson)) : 0;
  return Math.round((v + l) / 2);
}
