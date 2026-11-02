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

// 点被锁住的输入框 → 弹浮层。正常登录态不拦（那时 trial=false，遮罩根本不渲染）
function onTapLocked() {
  if (!this.data.trial) return;
  this.setData({ needLogin: true });
}

function closeLogin() {
  this.setData({ needLogin: false });
}

// 浮层上的「微信登录」：去登录页自己选。
// ⛔ 不写退出标记 —— 那是「退出登录」专用的；写了冷启动就不再静默登进来，
//    连登录页上的「试用账号」入口都看不到了。
function goLogin() {
  this.setData({ needLogin: false });
  wx.reLaunch({ url: '/pages/welcome/welcome' });
}

// 空方法：遮罩上 catchtap / catchtouchmove 需要一个名字来吃掉点击和滑动，
// 不然点浮层里面会冒泡到遮罩把自己关掉。
function noop() {}

module.exports = {
  sync: sync,
  onTapLocked: onTapLocked,
  closeLogin: closeLogin,
  goLogin: goLogin,
  noop: noop
};
