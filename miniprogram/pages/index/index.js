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

  // 身份刷成页面状态。onShow 每次调，身份广播（broadcastIdentity）也会叫到 ——
  // 别的页面登了录，留在栈里的首页要能当场跟着变，不能等切回来才对得上
  // （2026-11-03 老师：「小程序里经常能看到这种不一致」）。
  syncTrial() {
    const g = app.globalData;
    // ⚠️ 「体验态」= 没有真实身份（没登录 或 只是试用身份），跟 utils/trial.js 一个口径。
    //    只看 role==='trial' 会漏掉「领试用身份失败（限流/断网）」那一段，
    //    那时候首页会误显示成已登录的样子（带「退出登录」）。
    const trial = !g.logged || !g.role || app.isTrial();
    this.setData({
      logged: !!g.logged, role: g.role || '', trial: trial,
      pending: !!g.pending, daysLeft: g.daysLeft || 0
    });
  },

  onShow() {
    this.syncTrial();

    // 自定义 tabBar 每个页面各一份实例，切回来时要告诉它「现在是我在前面」。
    if (typeof this.getTabBar === 'function' && this.getTabBar()) {
      this.getTabBar().setData({ selected: 0 });
    }
    const g = app.globalData;
    if (!g.logged) {
      // 冷启动还没身份 → 静默领体验版，先让页面能用。
      // ⚠️ 退出过的人也照领：试用身份不是账号，「退出」要挡的只是静默微信登录，
      //    不是把人挡在门外看不了（2026-11-02：那个「已退出登录」门就是错的，已删）。
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
            trial: !app.globalData.logged || !app.globalData.role || app.isTrial(), message: ''
          });
        });
      }
    }
  },

  // 退出登录：清本地身份回首页，之后是试用态（照常能逛，写了不保存）。
  // 试用态不放这个按钮 —— 试用是默认态，没有「退」一说，要登录点「微信登录后可保存进度」。
  onExit() {
    app.logout();
  },

  // 就地微信登录（wx.login 静默，不弹授权）：试用态底部链接用。
  // 登录成功刷新本页状态 —— 不再有中间那屏登录页。
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
        logged: true, role: g.role || '', trial: !g.logged || !g.role || app.isTrial(),
        pending: !!g.pending, daysLeft: g.daysLeft || 0
      });
    });
  },

  go(e) {
    const mode = e.currentTarget.dataset.mode;
    // 试用态（没真实身份）照样能进 —— 进去看得到题，只是写不了（页面自己锁）。
    // 只有「连身份都还没拿到、也不是试用态」才挡一下。
    if (!this.data.logged && !this.data.trial) {
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
    } else if (mode === 'knowledge') {
      wx.navigateTo({ url: '/pages/knowledge/knowledge' });
    } else if (mode === 'listening') {
      wx.navigateTo({ url: '/pages/listening/listening' });
    } else {
      wx.navigateTo({ url: '/pages/practice/practice?mode=' + (mode || 'words') });
    }
  }
});
