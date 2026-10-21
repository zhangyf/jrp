// 首页：练习入口。
//
// 老师的日常是先做列表复习，再做造句，钉子户和活用是加餐。顺序就照这个排。
// 这里不自己发请求拿数据，各页面自己拉 —— 首页只负责跳。

const app = getApp();

Page({
  data: {
    logged: false,
    role: '',
    pending: false,
    message: '',
    // 自定义导航栏（原生导航栏放不了图片，logo 画在自己画的导航里）
    statusBarH: 20,
    navH: 44
  },

  onLoad() {
    // 状态栏高度 + 胶囊按钮位置 → 自定义导航行的真实高度
    try {
      const win = wx.getWindowInfo ? wx.getWindowInfo() : wx.getSystemInfoSync();
      const caps = wx.getMenuButtonBoundingClientRect();
      const statusBarH = win.statusBarHeight || 20;
      const navH = Math.max((caps.top - statusBarH) * 2 + caps.height, 40);
      this.setData({ statusBarH, navH });
    } catch (e) { /* 取不到就用默认值 */ }
  },

  onShow() {
    // 自定义 tabBar 每个页面各一份实例，切回来时要告诉它「现在是我在前面」
    if (typeof this.getTabBar === 'function' && this.getTabBar()) {
      this.getTabBar().setData({ selected: 0 });
    }
    const g = app.globalData;
    this.setData({ logged: !!g.logged, role: g.role || '' });
    if (!g.logged) {
      app.login((err) => {
        if (err) {
          this.setData({ message: '登录失败：' + err.message });
          return;
        }
        this.setData({ logged: true, role: app.globalData.role, message: '' });
      });
    }
  },

  go(e) {
    const mode = e.currentTarget.dataset.mode;
    if (!this.data.logged) {
      wx.showToast({ title: '正在登录…', icon: 'none' });
      return;
    }
    if (this.data.role === 'pending') {
      wx.showModal({
        title: '账号待批准',
        content: '要让管理员在服务器上跑一次 jrp user approve 才能开始练习。',
        showCancel: false
      });
      return;
    }
    if (this.data.role === 'disabled') {
      wx.showModal({ title: '账号已停用', content: '找管理员开通。', showCancel: false });
      return;
    }
    if (mode === 'sentences') {
      wx.navigateTo({ url: '/pages/sentences/sentences' });
    } else if (mode === 'conj') {
      wx.navigateTo({ url: '/pages/conj/conj' });
    } else if (mode === 'lexicon') {
      wx.navigateTo({ url: '/pages/lexicon/lexicon' });
    } else if (mode === 'addword') {
      wx.navigateTo({ url: '/pages/addword/addword' });
    } else {
      wx.navigateTo({ url: '/pages/practice/practice?mode=' + (mode || 'words') });
    }
  }
});
