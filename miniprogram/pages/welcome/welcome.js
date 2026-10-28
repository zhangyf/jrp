// 启动页 / 登录页（2026-10-28 老师要求加体验版）。
//
// 两个入口：微信登录（原来的静默登录），或者试用账号。
//
// ⚠️ 这是 app.json 里 pages 的第一项，所以每次冷启动都先到这里。
//    **已经登录的（老师自己）会自动跳走**，不会多这一步 —— 见 onShow 里的判断。
//
// 试用账号不是前端假数据：服务端发一个真会话（uid=trial），但只允许三个版块，
// 判分照样走 /api/grade。这样不会出现两套判分规则。

const app = getApp();

Page({
  data: {
    busy: false,
    checking: false, // 有缓存 token 时在验票，先别把登录按钮亮出来
    message: ''
  },

  onShow() {
    // 已经登录过（token 还在、且没过期）就直接进练习页。
    // 老师自己每天打开小程序不该看到这个登录页。
    if (app.globalData.logged && app.globalData.token) {
      wx.switchTab({ url: '/pages/index/index' });
      return;
    }
    // token 可能是上次存的，先问一次服务端还认不认；认就直接进。
    // 这段等待里显示「正在登录…」而不是两个按钮 —— 否则老师每次冷启动
    // 都会先闪一下登录页，看着像掉登录了。
    const cached = wx.getStorageSync('jrp_token');
    if (cached && !app.globalData.logged && !app.loggedOut()) {
      app.globalData.token = cached;
      this.setData({ checking: true });
      app.refreshMe();
      // refreshMe 是异步的，给它一点时间；认了就跳走，没认（过期/被踢）
      // 就把按钮亮出来让人重新登。
      setTimeout(() => {
        if (app.globalData.logged && app.globalData.token) {
          wx.switchTab({ url: '/pages/index/index' });
          return;
        }
        this.setData({ checking: false });
      }, 800);
    }
  },

  onWxLogin() {
    if (this.data.busy) return;
    this.setData({ busy: true, message: '' });
    app.login((err) => {
      this.setData({ busy: false });
      if (err) {
        this.setData({ message: '登录失败：' + err.message });
        return;
      }
      wx.switchTab({ url: '/pages/index/index' });
    });
  },

  onTrial() {
    if (this.data.busy) return;
    this.setData({ busy: true, message: '' });
    app.loginTrial((err) => {
      this.setData({ busy: false });
      if (err) {
        this.setData({ message: '进不去体验版：' + err.message });
        return;
      }
      wx.switchTab({ url: '/pages/index/index' });
    });
  }
});
