// 首页：练习入口。
//
// 老师的日常是先做列表复习，再做造句，钉子户和活用是加餐。顺序就照这个排。
// 这里不自己发请求拿数据，各页面自己拉 —— 首页只负责跳。

const app = getApp();

Page({
  data: {
    logged: false,
    role: '',
    roleText: '',
    pending: false,
    message: '',
    // 自定义导航栏（原生导航栏放不了图片，logo 画在自己画的导航里）
    statusBarH: 20,
    navH: 44,
    // 右侧要给微信胶囊按钮（… ◎）留位，默认 96px 兜底
    navPadRight: 96
  },

  onLoad() {
    // 状态栏高度 + 胶囊按钮位置 → 自定义导航行的真实高度
    try {
      const win = wx.getWindowInfo ? wx.getWindowInfo() : wx.getSystemInfoSync();
      const caps = wx.getMenuButtonBoundingClientRect();
      const statusBarH = win.statusBarHeight || 20;
      const navH = Math.max((caps.top - statusBarH) * 2 + caps.height, 40);
      // 胶囊左边到屏幕右缘 + 8px 间距：登录徽章收在这里面，不能再往右
      const navPadRight = Math.max(win.windowWidth - caps.left + 8, 96);
      this.setData({ statusBarH, navH, navPadRight });
    } catch (e) { /* 取不到就用默认值 */ }
  },

  onShow() {
    const g = app.globalData;
    this.setData({
      logged: !!g.logged,
      role: g.role || '',
      roleText: this.roleOf(g.role)
    });
    if (!g.logged) {
      app.login((err) => {
        if (err) {
          this.setData({ message: '登录失败：' + err.message });
          return;
        }
        this.setData({
          logged: true,
          role: app.globalData.role,
          roleText: this.roleOf(app.globalData.role),
          message: ''
        });
      });
    }
  },

  roleOf(role) {
    // 顶部徽章空间小，文案要短；pending 的完整解释在 go() 的弹窗里
    if (role === 'owner') return '站点所有者';
    if (role === 'member') return '成员';
    if (role === 'pending') return '待批准';
    if (role === 'disabled') return '已停用';
    return '';
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
    } else {
      wx.navigateTo({ url: '/pages/practice/practice?mode=' + (mode || 'words') });
    }
  }
});
