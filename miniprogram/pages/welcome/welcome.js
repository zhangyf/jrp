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
    message: ''
  },

  onShow() {
    // 已经登录过（token 还在、且没过期）就直接进练习页。
    // 老师自己每天打开小程序不该看到这个登录页。
    if (app.globalData.logged && app.globalData.token) {
      wx.switchTab({ url: '/pages/index/index' });
      return;
    }
    // token 可能是上次存的，先问一次服务端还认不认；认就直接进
    const cached = wx.getStorageSync('jrp_token');
    if (cached && !app.globalData.logged) {
      app.globalData.token = cached;
      app.refreshMe();
      // refreshMe 是异步的，给它一点时间；认了的话下次 onShow 就跳走了
      setTimeout(() => {
        if (app.globalData.logged && app.globalData.token) {
          wx.switchTab({ url: '/pages/index/index' });
        }
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
