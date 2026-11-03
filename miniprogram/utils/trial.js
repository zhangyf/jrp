// 试用态（未登录）：能看不能写。
//
// 小程序审核要求「先浏览体验功能服务，再自行选择授权登录」，所以冷启动
// 直接领试用身份进首页 —— 门户上不再拦一道登录。
//
// 但试用身份写不进档案（/api/record 不在体验版白名单里），让人照常填完
// 才发现「没保存进度」，比一开始就锁住更难受。老师 2026-11-02 定的口径：
// **所有输入框锁死，点一下弹登录浮层**（比"填了不保存"清楚）。
//
// 用在一个页面上要三处配合：
//   1. data 里加 trial / needLogin 两个字段
//   2. 页面 js 挂这几个方法（⚠️ 是挂方法不是调函数：里面用 this 取页面实例）
//        onShow: trial.sync            // 页面本来就有 onShow 的，在里面加一行 trial.sync(this)
//                                      // —— 两种都行，sync 认参数也认 this（见 sync 的注释）
//        onTapLocked: trial.onTapLocked
//        closeLogin: trial.closeLogin
//        goLogin: trial.goLogin
//        noop: trial.noop
//   3. 页面 wxml 末尾加 <include src="../../trial-sheet.wxml" />；
//      每个输入框写成一对 wx:if/wx:else —— 试用态渲染一块同 class 的
//      <view class="answer locked" bindtap="onTapLocked">，正常态才是真 input。
//      ⚠️ 别改成「真 input + disabled + 透明遮罩」：input/textarea 是原生组件，
//         部分机型上盖在普通 view 上面，遮罩接不到点击，点了会没反应。

function app() { return getApp(); }

// 锁定态 = **没有确凿的真实身份**。
//
// ⛔ 判定方向是「默认锁，只有拿到真身份才开」，反过来写（找出理由才锁）迟早漏：
//    · 只看 `role === 'trial'`：领试用身份会失败（限流 429、断网、首帧没回来），
//      那时 role 是空的 → 被当成已登录，输入框全放开；
//    · 只看 `!logged`：冷启动验票是异步的，那一瞬 logged 还是 false → 该开的不开。
//
// 真实身份的三个条件必须同时成立：logged 为真、role 非空、且不是 trial。
function lockedState() {
  const a = app();
  const real = a.globalData.logged && !!a.globalData.role && !a.isTrial();
  return !real;
}

// 试用身份是冷启动异步领的，进页面那一瞬可能还没拿到 —— 所以 onShow 里每次同步一次。
// 登录后 app 会广播（broadcastIdentity）再把所有页面同步一遍。
//
// ⚠️ 两种挂法都得能跑（2026-11-03 的真事故）：
//     onShow: trial.sync          → 方法调用，this 就是页面实例
//     onShow(){ trial.sync(this) } → 页面从参数进来，this 是 trial 模块对象
//   以前只认 this，于是第二种挂法里 this.setData 不存在 → onShow 直接抛异常，
//   data.trial 永远是初始的 true（默认锁）—— 表现就是「进了页面还是未登录，
//   点一下输入框才恢复」（onTapLocked 是挂在页面上的，this 正常，所以点一下就对了）。
//   ⚠️ 两个都不是（比如直接 trial.sync()）就静默返回，别抛。
function sync(page) {
  const p = (page && typeof page.setData === 'function') ? page
    : (this && typeof this.setData === 'function') ? this
      : null;
  if (!p) return;
  // 有凭证却没身份（refreshMe 还在飞 / 上一次验票失败了）→ 主动核一次。
  // 核完 app 会广播，各页 syncTrial 再来一遍，那时候就解锁了。
  // ⛔ 没 token 不核（那是真没登录，领试用身份归 app 管，别在这儿自作主张）；
  // ⛔ role 已经是 trial 也不核（没有真身份可核）。所以最多多打一个请求。
  const a = app();
  if (a.globalData.token && (!a.globalData.logged || !a.globalData.role)) {
    a.refreshMe();
  }
  p.setData({ trial: lockedState() });
}

// 点被锁住的入口 → 弹浮层。
// ⚠️ 每次都以 app 的身份为准重新同步一次，不只看 data.trial：
//    冷启动领试用身份是异步的，页面第一帧可能还没拿到身份而渲染成「可写」，
//    这时候点下去必须照样弹浮层（顺手把状态纠正过来）。
function onTapLocked() {
  const t = lockedState();
  if (t !== this.data.trial) this.setData({ trial: t });
  if (!t) return;
  this.setData({ needLogin: true });
}

// 「这一下算不算被锁」：data.trial 可能因异步还没同步过来，所以两处都看。
// 用法：if (trial.locked(this)) { this.onTapLocked(); return; }
function locked(page) {
  return lockedState() || !!(page && page.data && page.data.trial);
}

function closeLogin() {
  this.setData({ needLogin: false });
}

// 浮层上的「微信登录」：当场就登，不再跳登录页（登录页已删 —— 老师 2026-11-02：
// 「我的意思是不应该有这一屏」）。wx.login 是静默的，不弹任何授权。
// 登录成功后如果页面有 load()（练习/造句/活用/词汇），按真身份把数据重拉一遍，
// 顺便把锁解开；addword 没有数据要拉，解锁就够。
function goLogin() {
  const self = this;
  this.setData({ needLogin: false });
  getApp().login((err) => {
    if (err) {
      wx.showToast({ title: '登录失败：' + err.message, icon: 'none' });
      return;
    }
    sync.call(self); // 解锁（app 也会广播一遍，这里是本页立即生效）
    if (typeof self.load === 'function') self.load();
  });
}

// 空方法：遮罩上 catchtap / catchtouchmove 需要一个名字来吃掉点击和滑动，
// 不然点浮层里面会冒泡到遮罩把自己关掉。
function noop() {}

module.exports = {
  sync: sync,
  locked: locked,
  onTapLocked: onTapLocked,
  closeLogin: closeLogin,
  goLogin: goLogin,
  noop: noop
};
