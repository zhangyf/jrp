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
    // pending：已扫码但管理员还没批准。有 7 个自然日的完整体验期，
    // 到期服务端直接 403，这两个字段只是让首页摆一句还剩几天。
    pending: false,
    daysLeft: 0,
    logged: false,
    booting: false // 冷启动正在领体验版，防止 onShow 反复触发重复领
  },

  // 退出标记：老师主动退出后，冷启动**不许再静默 wx.login**（那等于把他登回账号，
  // 「退出登录」点了个寂寞）。但**不挡领试用身份** —— 试用态是默认态，退出后照常能逛，
  // 只写了不保存。只有他重新点了「微信登录」才清除（见 login）。
  logoutFlagKey: 'jrp_logged_out',

  loggedOut() { return !!wx.getStorageSync(this.logoutFlagKey); },
  clearLogoutFlag() { wx.removeStorageSync(this.logoutFlagKey); },

  // 冷启动：
  //   有 token → 验票续用；
  //   没 token → **静默领体验版**（2026-11-02 审核要求：先能浏览，登录必须是用户自己选的）。
  //   退出过也一样领 —— 试用身份不是账号，不违反「别把我静默登回去」。
  // ⚠️ 不再默认 wx.login：那是替陌生人注册 pending 账号，审核也把「进门就要登录」算违规。
  autoLogin() {
    const cached = wx.getStorageSync('jrp_token');
    if (cached) {
      this.globalData.token = cached;
      // ⚠️ 先当成已登录：验票是异步的，那一瞬 globalData.logged 还是 false，
      //    所有页面会按「没身份」先锁一遍（老师每次冷启动都闪一下灰的）。
      //    真过期了 refreshMe 会把它改回来并重新登录。
      this.globalData.logged = true;
      this.refreshMe();
      return;
    }
    this.loginTrial();
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
            this.globalData.pending = !!res.pending;
            this.globalData.daysLeft = res.days_left || 0;
            this.globalData.name = res.name || '';
            this.globalData.logged = true;
            wx.setStorageSync('jrp_token', this.globalData.token);
            this.clearLogoutFlag(); // 主动登录过，下次冷启动照旧直接进
            this.broadcastIdentity(); // 解锁所有已打开的页面
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
        this.globalData.pending = !!res.pending;
        this.globalData.daysLeft = res.days_left || 0;
        this.globalData.name = res.name || '';
        this.globalData.logged = true;
        wx.setStorageSync('jrp_token', this.globalData.token);
        // ⚠️ 不清退出标记：领试用身份是自动的，不是用户主动登录。
        //    清了的话「退出 → 冷启动自动领试用 → 标记没了 → 下次 token 过期
        //    就被静默 wx.login 登回账号」，退出就白点了。标记只有 login() 清。
        this.broadcastIdentity();
        if (cb) cb(null, res);
      })
      .catch((e) => { if (cb) cb(e); });
  },

  isTrial() { return this.globalData.role === 'trial'; },

  // 身份一变（登录 / 领试用 / 退出 / 验票拿到新 role）就通知所有已打开的页面
  // 重新同步「锁定态」。页面只要挂了 `syncTrial: trial.sync` 就会被叫到。
  // 没有它的话，登录成功后留在栈里的上一个页面还是锁着的，要等下次 onShow 才解开。
  broadcastIdentity() {
    const pages = getCurrentPages();
    pages.forEach((p) => {
      if (p && typeof p.syncTrial === 'function') p.syncTrial();
    });
  },

  // 退出登录：清掉本地 token 回首页，并留个「别再静默 wx.login」的标记。
  // 之后落回试用态照常能逛（写了不保存）；重新登录只在用户点「微信登录」时发生。
  // 服务端的会话本来就没必要撤 —— 它 30 天自己过期，没人拿着 token 也就没意义。
  logout() {
    this.globalData.token = '';
    this.globalData.uid = '';
    this.globalData.role = '';
    this.globalData.pending = false;
    this.globalData.daysLeft = 0;
    this.globalData.name = '';
    this.globalData.logged = false;
    wx.removeStorageSync('jrp_token');
    wx.setStorageSync(this.logoutFlagKey, 1);
    this.broadcastIdentity(); // 剩下的页面立刻锁回去
    wx.reLaunch({ url: '/pages/index/index' });
  },

  refreshMe() {
    this.request('/api/auth/me', 'GET', {}, true)
      .then((res) => {
        this.globalData.uid = res.uid || '';
        this.globalData.role = res.role || '';
        this.globalData.pending = !!res.pending;
        this.globalData.daysLeft = res.days_left || 0;
        this.globalData.name = res.name || '';
        this.globalData.logged = true;
        this.broadcastIdentity(); // role 可能变了（批准/冻结），同步一次锁定态
      })
      .catch(() => {
        // token 过期了：清掉。退出过的只补试用身份（别把他静默登回账号），
        // 没退出过的静默 wx.login 回原账号 —— 跟冷启动一个待遇。
        this.globalData.logged = false;
        wx.removeStorageSync('jrp_token');
        if (this.loggedOut()) { this.loginTrial(); return; }
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
