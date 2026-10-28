// 自定义底部导航。
//
// 原生 tabBar 的字号改不了（app.json 里只有 color / selectedColor / backgroundColor
// / borderStyle，没有 fontSize），老师嫌「练习 / 统计」两个字太小 —— 只能换自定义
// tabBar（app.json 里 "custom": true + 根目录 custom-tab-bar/）。
//
// ⚠️ 自定义 tabBar 每个 tab 页各有一份实例，切页后不会自己知道该高亮哪个
// → 每个 tab 页的 onShow 里必须调 getTabBar().setData({ selected })
//   （见 index.js / stats.js / addword.js）。
//
// 「录入」是工具不是练习，所以放这里而不是首页色块（2026-10-21 老师要求）。
// ⚠️ list 仍要在 app.json 的 tabBar 里写一份，wx.switchTab 靠它校验合法页面。

// 体验版也能进统计和录入（假数据 / 只展示），所以不再按身份切列表。
const LIST = [
  { pagePath: '/pages/index/index', text: '练习' },
  { pagePath: '/pages/stats/stats', text: '统计' },
  { pagePath: '/pages/addword/addword', text: '录入' }
];

Component({
  data: {
    selected: 0,
    list: LIST
  },

  methods: {
    switchTab(e) {
      const url = e.currentTarget.dataset.path;
      wx.switchTab({ url });
    }
  }
});
