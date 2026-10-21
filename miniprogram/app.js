// jrp 小程序入口。
//
// 这里只做两件事：静默登录（wx.login 不需要用户授权），以及统一带 Bearer 的请求封装。
// 小程序没有 cookie jar（真机不会帮你存/回传 Set-Cookie），所以全程用
// Authorization: Bearer <token> —— 后端同一张会话表也认这个，跟网页端是同一个账号。

const BASE = 'https://jrp.110105.xyz';

App({
  globalData: {
    base: BASE,
    token: '',
    uid: '',
    role: '',
    name: '',
    logged: false
  },

  onLaunch() {
    const cached = wx.getStorageSync('jrp_token');
    if (cached) {
      this.globalData.token = cached;
      this.refreshMe();
    } else {
      this.login();
    }
  },

  // login 用 wx.login 的 code 换 openid → 后端发会话 token。静默，不弹授权。
  login(cb) {
    wx.login({
      success: (r) => {
        if (!r.code) {
          if (cb) cb(new Error('wx.login 没拿到 code'));
          return;
        }
        this.request('/api/auth/wxlogin', 'POST', { code: r.code }, false)
          .then((res) => {
            this.globalData.token = res.token || '';
            this.globalData.uid = res.uid || '';
            this.globalData.role = res.role || '';
            this.globalData.name = res.name || '';
            this.globalData.logged = true;
            wx.setStorageSync('jrp_token', this.globalData.token);
            if (cb) cb(null, res);
          })
          .catch((e) => { if (cb) cb(e); });
      },
      fail: (e) => { if (cb) cb(new Error(e.errMsg || 'wx.login 失败')); }
    });
  },

  refreshMe() {
    this.request('/api/auth/me', 'GET', {}, true)
      .then((res) => {
        this.globalData.uid = res.uid || '';
        this.globalData.role = res.role || '';
        this.globalData.name = res.name || '';
        this.globalData.logged = true;
      })
      .catch(() => {
        // token 过期了：清掉，重新静默登录。
        this.globalData.logged = false;
        wx.removeStorageSync('jrp_token');
        this.login();
      });
  },

  // request 统一封装。needAuth 为 true 时带上 Bearer。
  // timeout 不传就用微信默认的 60 秒；拍照取词传图大、模型可能要换链重试，得单独给更长。
  request(path, method, data, needAuth, timeout) {
    return new Promise((resolve, reject) => {
      const header = { 'content-type': 'application/json' };
      if (needAuth && this.globalData.token) {
        header.Authorization = 'Bearer ' + this.globalData.token;
      }
      wx.request({
        url: BASE + path,
        method: method || 'GET',
        data: data || {},
        header,
        timeout: timeout || 60000,
        success: (r) => {
          if (r.statusCode >= 200 && r.statusCode < 300 && r.data && r.data.success) {
            resolve(r.data);
          } else {
            const msg = (r.data && r.data.error) || ('请求失败 ' + r.statusCode);
            reject(new Error(msg));
          }
        },
        fail: (e) => reject(new Error(e.errMsg || '网络错误'))
      });
    });
  }
});
