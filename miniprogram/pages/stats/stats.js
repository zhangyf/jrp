// 统计页。
//
// 只做展示，不写任何东西。数据口径全部由后端 /api/stats 决定，
// 跟网页端、CLI 是同一份 —— 前端不自己算，免得三处口径漂移。
//
// ⚠️ /api/stats 返回四段：snapshots / changes / detail / jlpt。
//    **趋势全部在 snapshots 里**（每天一个存档点：total / mastered / basic /
//    needs_consol / untested / errors）。早先这页只用了 detail + jlpt，
//    等于把趋势整段丢了 —— 老师看到的「跟 web 不一样、看不出趋势」就是它。
//    版面现在跟 web 的 web/stats.js 对齐：
//      KPI（含区间增量）→ 掌握度构成 → 词库走势 → 正确率分布
//      → 钉子户 → 复习最多的词 → 每日明细（折叠）
//    按课分布 web 端已去掉（词汇总表里能筛，作用重复），这里也不摆。
//
// 小程序画不了 SVG：走势用「每日堆叠柱」代替 web 的堆叠折线，
// 一根柱 = 一个存档点，四档从下往上 = 已掌握/基本掌握/待巩固/未测试，
// 柱顶就是当天总词量 —— 跟 web 那张堆叠折线表达的是同一件事。

const app = getApp();

const DAY_OPTS = [
  { key: 7, label: '近 7 天' },
  { key: 30, label: '近 30 天' },
  { key: 90, label: '近 90 天' }
];

// 跟 web/chart.js 的 PALETTE 保持一致
const C = {
  accent: '#2f6f4f', // 已掌握
  light: '#7ba05b',  // 基本掌握
  warn: '#d9a13b',   // 待巩固
  bad: '#b4392f',
  orange: '#cf7a34',
  line: '#e2e2dd'    // 未测试
};

const DIST_ORDER = ['0-30%', '30-60%', '60-80%', '80-90%', '90-100%'];
const DIST_COLOR = {
  '0-30%': C.bad, '30-60%': C.orange, '60-80%': C.warn,
  '80-90%': C.light, '90-100%': C.accent
};

const MAX_BARS = 30; // 90 天全画出来柱子太密，抽样到 30 根

Page({
  data: {
    dayOpts: DAY_OPTS,
    // 默认 30 天：7 天样本太短，看不出走势（跟 web 统计页的下拉默认值一致）
    days: 30,
    loading: true,
    err: '',
    period: '',
    jlpt: null,
    n5Pct: 0,
    n4Pct: 0,
    kpis: [],
    share: [],
    shareTotal: 0,
    bars: [],
    trendMax: 0,
    trendHas: false,
    trendFirst: '',
    trendLast: '',
    hard: null,
    dist: [],
    distTotal: 0,
    top: [],
    daily: [],
    showDaily: false
  },

  onLoad() {
    this.load();
  },

  onShow() {
    // 自定义 tabBar 每个页面各一份实例，切回来时要告诉它「现在是我在前面」
    if (typeof this.getTabBar === 'function' && this.getTabBar()) {
      this.getTabBar().setData({ selected: 1 });
    }
    // 从练习页返回时刷新一次，刚练完的数字才对得上
    if (!this.data.loading) this.load();
  },

  onDays(e) {
    this.setData({ days: Number(e.currentTarget.dataset.days) }, () => this.load());
  },

  onToggleDaily() {
    this.setData({ showDaily: !this.data.showDaily });
  },

  load() {
    this.setData({ loading: true, err: '' });
    app.request('/api/stats?days=' + this.data.days, 'GET', {}, true)
      .then((res) => {
        const snaps = res.snapshots || [];
        const j = res.jlpt || null;
        const d = res.detail || {};
        const first = snaps[0];
        const last = snaps[snaps.length - 1];
        const hasCmp = snaps.length >= 2;
        const delta = (k) => (hasCmp ? last[k] - first[k] : null);

        // --- KPI：最新快照为当前值，跟区间第一天比增量 ---
        const total = last ? (last.total || 0) : 0;
        const kpis = last ? [
          { label: '总词量', value: last.total || 0, delta: delta('total'), up: true, sub: '' },
          { label: '已掌握', value: last.mastered || 0, delta: delta('mastered'), up: true, sub: share(last.mastered, total) },
          { label: '基本掌握', value: last.basic || 0, delta: delta('basic'), up: true, sub: share(last.basic, total) },
          { label: '待巩固', value: last.needs_consol || 0, delta: delta('needs_consol'), up: false, sub: share(last.needs_consol, total) },
          { label: '累计错误', value: last.errors || 0, delta: delta('errors'), up: false, sub: '次' }
        ] : [];

        // --- 掌握度构成：一条横条按四档切分 ---
        const segs = [
          { label: '已掌握', value: last ? (last.mastered || 0) : 0, color: C.accent },
          { label: '基本掌握', value: last ? (last.basic || 0) : 0, color: C.light },
          { label: '待巩固', value: last ? (last.needs_consol || 0) : 0, color: C.warn },
          { label: '未测试', value: last ? (last.untested || 0) : 0, color: C.line }
        ].map((s) => ({
          label: s.label, value: s.value, color: s.color,
          pct: total ? Math.round((s.value * 100) / total) : 0,
          w: total ? ((s.value * 100) / total).toFixed(2) : 0
        }));
        const shareTotal = segs.reduce((n, s) => n + s.value, 0);

        // --- 走势：每日一根堆叠柱 ---
        const picked = [];
        if (snaps.length) {
          const step = snaps.length > MAX_BARS ? Math.ceil(snaps.length / MAX_BARS) : 1;
          for (let i = 0; i < snaps.length; i += step) picked.push(snaps[i]);
          const tail = snaps[snaps.length - 1];
          if (picked[picked.length - 1] !== tail) picked.push(tail);
        }
        const trendMax = picked.reduce((m, s) => Math.max(m, s.total || 0), 0);
        const bars = picked.map((s, i) => {
          const t = s.total || 0;
          const seg = (v) => (t ? ((v * 100) / t).toFixed(2) : 0);
          // 日期标签只给首/中/尾，30 根全标会挤成一团
          const showLabel = i === 0 || i === picked.length - 1 ||
            i === Math.floor(picked.length / 2);
          return {
            day: (s.date || '').slice(5),
            showLabel,
            total: t,
            h: trendMax ? Math.max(2, Math.round((t * 100) / trendMax)) : 0,
            mastered: seg(s.mastered || 0),
            basic: seg(s.basic || 0),
            needs: seg(s.needs_consol || 0),
            untested: seg(s.untested || 0)
          };
        });

        // --- 正确率分布：固定五档顺序 + 红到绿的配色（跟 web 一致） ---
        const m = d.accuracy_distribution || {};
        const distVals = DIST_ORDER.filter((k) => m[k] !== undefined).map((k) => m[k] || 0);
        const distMax = distVals.reduce((a, b) => Math.max(a, b), 0);
        const dist = DIST_ORDER.filter((k) => m[k] !== undefined).map((k) => ({
          label: k, value: m[k] || 0, color: DIST_COLOR[k],
          w: distMax ? ((m[k] * 100) / distMax).toFixed(1) : 0
        }));

        // --- 复习最多的词 ---
        const top = (d.top_reviewed || []).map((t) => {
          const acc = Math.round((t.accuracy || 0) * 100);
          const color = acc < 30 ? C.bad : (acc < 60 ? C.orange : (acc < 80 ? C.warn : C.accent));
          return { word: t.word || '', reviews: t.reviews || 0, errors: t.errors || 0, acc, color };
        });

        // --- 每日明细：倒序，默认折叠 ---
        const daily = snaps.slice().reverse().map((s) => ({
          date: s.date || '', total: s.total || 0, mastered: s.mastered || 0,
          needs: s.needs_consol || 0, errors: s.errors || 0
        }));

        this.setData({
          loading: false,
          jlpt: j,
          n5Pct: j ? pct(j.vocab_firm, j.lesson, j.n5) : 0,
          n4Pct: j ? pct(j.vocab_firm, j.lesson, j.n4) : 0,
          period: hasCmp
            ? '对比区间 ' + first.date + ' → ' + last.date + '（' + snaps.length + ' 个存档点）'
            : (last ? '只有 ' + last.date + ' 一个存档点，暂无对比' : ''),
          kpis,
          share: segs,
          shareTotal,
          bars,
          trendMax,
          trendHas: snaps.length >= 2,
          trendFirst: (first && first.date || '').slice(5),
          trendLast: (last && last.date || '').slice(5),
          hard: d.hard_words || null,
          hardRows: hardRows(d.hard_words),
          dist,
          distTotal: distVals.reduce((a, b) => a + b, 0),
          top,
          daily
        });
      })
      .catch((e) => this.setData({ err: e.message, loading: false }));
  }
});

function share(a, b) { return b ? Math.round((a * 100) / b) + '%' : '—'; }

// 钉子户三档的显示行。⚠️ 「<」必须从 JS 给 —— WXML 不解析 HTML 实体，
// 标签文本里写 &lt; 会原样显示成 "&lt;"（2026-10-19 老师截图抓到）。
function hardRows(hw) {
  if (!hw || !hw.total) return [];
  return [
    { label: '严重（<30%）', value: hw.severe || 0 },
    { label: '中度（30-45%）', value: hw.moderate || 0 },
    { label: '轻度（≥45%）', value: hw.mild || 0 }
  ];
}

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
