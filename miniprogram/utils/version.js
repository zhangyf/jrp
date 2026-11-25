// 首页两处版本标识，来源不同，互不替代（2026-11-25 老师定稿）：
//   ① Logo 右侧 vxx.xx.xx —— **动态获取**，只读微信运行时给的线上版本号
//      （wx.getAccountInfoSync().miniProgram.version，仅正式版有值）。
//      ⛔ 不内置、不伪造；体验版/开发版/老基础库取不到号 → 留白。
//   ② 页面底部浅色十六进制 CODE_VERSION —— 纯内置构建码。正因为体验版
//      看不出①，才用它判断当前跑的是哪一份代码。
//
// ⚠️ 每次给小程序推改动，必须把 CODE_VERSION 改成「上一提交」的 git 短 hash。
//    commit 写不进自己的 hash，所以 hex 天生指向代码基线：
//    看到它变了 = 换包了；看到它没变 = 跑的还是上一批代码（2026-11-25 老师的用法）。
//    漏 bump 会让缓存排查时根本分不清跑的是哪份代码，别漏。
const CODE_VERSION = 'e898eb5';

module.exports = { CODE_VERSION };
