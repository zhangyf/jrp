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

// 试用身份是冷启动异步领的，进页面那一瞬可能还没拿到 —— 所以 onShow 里每次同步一次。
// 从登录页切换身份回来（试用 → 微信登录）也靠这次刷新把锁解开。
function sync() {
  this.setData({ trial: app().isTrial() });
}

// 点被锁住的入口 → 弹浮层。
// ⚠️ 每次都以 app 的身份为准重新同步一次，不只看 data.trial：
//    冷启动领试用身份是异步的，页面第一帧可能还没拿到身份而渲染成「可写」，
//    这时候点下去必须照样弹浮层（顺手把状态纠正过来）。
function onTapLocked() {
  const t = getApp().isTrial();
  if (t !== this.data.trial) this.setData({ trial: t });
  if (!t) return;
  this.setData({ needLogin: true });
}

// 「这一下算不算被锁」：data.trial 可能因异步还没同步过来，所以两处都看。
// 用法：if (trial.locked(this)) { this.onTapLocked(); return; }
function locked(page) {
  return getApp().isTrial() || !!(page && page.data && page.data.trial);
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
    self.setData({ trial: false });
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
