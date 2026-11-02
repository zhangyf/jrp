// 首页：练习入口。
//
// 老师的日常是先做列表复习，再做造句，钉子户和活用是加餐。顺序就照这个排。
// 这里不自己发请求拿数据，各页面自己拉 —— 首页只负责跳。

const app = getApp();

Page({
  data: {
    logged: false,
    role: '',
    trial: false, // 体验版：题量少、统计是假数据、写了不保存
    // 主动退出过：不给卡片（没身份点了也白点），就地给「微信登录 / 体验模式」两个按钮。
    // ⚠️ 没有单独的登录页 —— 2026-11-02 老师定「不应该有这一屏」，登录动作就地发生。
    needLogin: false,
    pending: false, // 已扫码等批准：7 个自然日的完整体验期，首页摆一句还剩几天
    daysLeft: 0,
    message: '',
    busy: false, // 正在登录，防止连点
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
    const g = app.globalData;
    const trial = app.isTrial();
    this.setData({
      logged: !!g.logged, role: g.role || '', trial: trial,
      needLogin: !g.logged && app.loggedOut(),
      pending: !!g.pending, daysLeft: g.daysLeft || 0
    });

    // 自定义 tabBar 每个页面各一份实例，切回来时要告诉它「现在是我在前面」。
    if (typeof this.getTabBar === 'function' && this.getTabBar()) {
      this.getTabBar().setData({ selected: 0 });
    }
    if (!g.logged && !this.data.needLogin) {
      // 冷启动还没身份 → 静默领体验版，先让页面能用。
      // ⚠️ 不做 wx.login：登录必须是用户自己点的（2026-11-02 审核要求）。
      // 退出过的人（needLogin）也别自作主张登回去，按钮摆在那等他点。
      if (!g.booting) {
        g.booting = true;
        app.loginTrial((err) => {
          g.booting = false;
          if (err) {
            this.setData({ message: '网络开小差了，下拉重试' });
            return;
          }
          this.setData({
            logged: true, role: app.globalData.role,
            trial: app.isTrial(), message: ''
          });
        });
      }
    }
  },

  // 退出登录：清本地身份回首页（app 里留了标记，冷启动不会把他静默登回去）。
  // 试用态不放这个按钮 —— 试用是默认态，没有「退」一说，要登录点「微信登录后可保存进度」。
  onExit() {
    app.logout();
  },

  // 就地微信登录（wx.login 静默，不弹授权）：浮层和退出态都用它。
  // 登录成功刷新本页状态；试用态的底部链接也走这里 —— 不再有中间那屏登录页。
  onWxLogin() {
    if (this.data.busy) return;
    this.setData({ busy: true, message: '' });
    app.login((err) => {
      this.setData({ busy: false });
      if (err) {
        this.setData({ message: '登录失败：' + err.message });
        return;
      }
      const g = app.globalData;
      this.setData({
        logged: true, role: g.role || '', trial: app.isTrial(),
        needLogin: false, pending: !!g.pending, daysLeft: g.daysLeft || 0
      });
    });
  },

  // 退出态的「先用体验模式逛逛」：领试用身份，回正常试用态。
  onTrial() {
    if (this.data.busy) return;
    this.setData({ busy: true, message: '' });
    app.loginTrial((err) => {
      this.setData({ busy: false });
      if (err) {
        this.setData({ message: '进不去体验版：' + err.message });
        return;
      }
      const g = app.globalData;
      this.setData({
        logged: true, role: g.role || '', trial: app.isTrial(),
        needLogin: false, pending: !!g.pending, daysLeft: g.daysLeft || 0
      });
    });
  },

  go(e) {
    const mode = e.currentTarget.dataset.mode;
    if (!this.data.logged) {
      wx.showToast({ title: '正在登录…', icon: 'none' });
      return;
    }
    // ⚠️ 不再拦 pending：服务端对未批准的用户直接给体验版权限（role 报 trial），
    // 这里再摆一道「待批准」的墙，陌生人（含小程序审核员）就一个功能都点不动。
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
    } else {
      wx.navigateTo({ url: '/pages/practice/practice?mode=' + (mode || 'words') });
    }
  }
});
