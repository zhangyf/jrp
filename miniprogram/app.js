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

  // 退出标记：老师主动退出后，冷启动不许再把他静默登回去 ——
  // 否则「退出登录」点了个寂寞，也永远看不到登录页上的「试用账号」。
  // 只有他在登录页重新点了微信登录 / 试用账号才清除（见 login / loginTrial）。
  logoutFlagKey: 'jrp_logged_out',

  loggedOut() { return !!wx.getStorageSync(this.logoutFlagKey); },
  clearLogoutFlag() { wx.removeStorageSync(this.logoutFlagKey); },

  // 冷启动：有退出标记就什么都不做，停在登录页等人选。
  autoLogin() {
    if (this.loggedOut()) return;
    const cached = wx.getStorageSync('jrp_token');
    if (cached) {
      this.globalData.token = cached;
      this.refreshMe();
      return;
    }
    this.login();
  },

  onLaunch() {
    this.autoLogin();
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
            this.clearLogoutFlag(); // 主动登录过，下次冷启动照旧直接进
            if (cb) cb(null, res);
          })
          .catch((e) => { if (cb) cb(e); });
      },
      fail: (e) => { if (cb) cb(new Error(e.errMsg || 'wx.login 失败')); }
    });
  },

  // 体验版：领一个试用身份。不用微信、不用等批准，点一下就进。
  //
  // 服务端给的是真会话（uid=trial），只是被白名单关在「每日复习/造句/钉子户」
  // 三个版块里 —— 这样判分仍然走 /api/grade 那一份实现，不用在小程序里抄一套。
  loginTrial(cb) {
    this.request('/api/auth/trial', 'POST', {}, false)
      .then((res) => {
        this.globalData.token = res.token || '';
        this.globalData.uid = res.uid || '';
        this.globalData.role = res.role || '';
        this.globalData.name = res.name || '';
        this.globalData.logged = true;
        wx.setStorageSync('jrp_token', this.globalData.token);
        this.clearLogoutFlag();
        if (cb) cb(null, res);
      })
      .catch((e) => { if (cb) cb(e); });
  },

  isTrial() { return this.globalData.role === 'trial'; },

  // 退出（含退出试用）：清掉本地 token 回登录页，并留个「别再自动登我」的标记。
  // 服务端的会话本来就没必要撤 —— 它 30 天自己过期，没人拿着 token 也就没意义。
  logout() {
    this.globalData.token = '';
    this.globalData.uid = '';
    this.globalData.role = '';
    this.globalData.name = '';
    this.globalData.logged = false;
    wx.removeStorageSync('jrp_token');
    wx.setStorageSync(this.logoutFlagKey, 1);
    wx.reLaunch({ url: '/pages/welcome/welcome' });
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
        // token 过期了：清掉，重新静默登录（退出过就别自作主张登回去）。
        this.globalData.logged = false;
        wx.removeStorageSync('jrp_token');
        if (this.loggedOut()) return;
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
