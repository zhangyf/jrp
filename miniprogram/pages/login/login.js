// 扫码确认登录页。
//
// 进入路径：PC 网页上那张小程序码 → 微信扫 → 微信按 scene 打开这个页面。
// scene 里装的就是登录 ticket（22 字符，wxacode 的 scene 上限是 32）。
//
// 注意：个人主体小程序拿不到手机号、头像昵称也是匿名的，所以这里不显示
// 「XXX 你好」，就问一句「用这个微信登录 jrp？」。

const app = getApp();

Page({
  data: {
    ticket: '',
    state: 'loading', // loading | confirm | done | error
    message: '',
    roleText: ''
  },

  onLoad(options) {
    // 小程序码进入：scene 是 URL encode 过的。普通链接/调试进入时可能是 ticket 参数。
    let ticket = '';
    if (options.scene) {
      try {
        ticket = decodeURIComponent(options.scene);
      } catch (e) {
        ticket = options.scene;
      }
    }
    if (!ticket && options.ticket) ticket = options.ticket;

    if (!ticket) {
      this.setData({ state: 'error', message: '这不是登录二维码扫进来的（缺少 ticket）。请回到网页上重新扫。' });
      return;
    }
    this.setData({ ticket });

    if (app.globalData.logged && app.globalData.token) {
      this.setData({ state: 'confirm', roleText: this.roleOf(app.globalData.role) });
    } else {
      app.login((err) => {
        if (err) {
          this.setData({ state: 'error', message: '登录失败：' + err.message });
          return;
        }
        this.setData({ state: 'confirm', roleText: this.roleOf(app.globalData.role) });
      });
    }
  },

  roleOf(role) {
    if (role === 'owner') return '（站点所有者）';
    if (role === 'pending') return '（还没被批准，登录后可能看不到练习内容）';
    return '';
  },

  onConfirm() {
    const app_ = app;
    app_.request('/api/auth/confirm', 'POST', { ticket: this.data.ticket }, true)
      .then(() => {
        this.setData({ state: 'done', message: '已确认，电脑上的页面马上就会进入。' });
      })
      .catch((e) => {
        this.setData({ state: 'error', message: e.message + '。请回到网页刷新二维码再试。' });
      });
  },

  onCancel() {
    wx.navigateBack({
      fail: () => { wx.switchTab({ url: '/pages/index/index', fail: () => {} }); }
    });
  }
});
