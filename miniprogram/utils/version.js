// 首页两处版本标识，两处并存、互不替代（2026-11-25 老师定稿）：
//   ① Logo 右侧 vxx.xx.xx —— 正式版显示微信线上版本号；体验版/开发版微信不给，
//      回退到内置的 APP_VERSION，所以任何时候这一位都不会空着，也不会是 v0.0.0。
//   ② 页面底部浅色十六进制 CODE_VERSION —— 纯内置构建码，体验版也能一眼
//      判断当前跑的是哪一份代码。
//
// ⚠️ 每次给小程序推改动后：APP_VERSION 第三位 +1，CODE_VERSION 换成新的 git 短 hash。
//    两者都不参与业务逻辑，只是肉眼分辨「是不是刚上传的那一版」。
const APP_VERSION = 'v1.0.36';
const CODE_VERSION = 'f0d4c5e';

module.exports = { APP_VERSION, CODE_VERSION };
