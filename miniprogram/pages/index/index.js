// 占位首页。第一版只放登录状态和一个「打开网页版」入口 ——
// 小程序审核要求有个能看的首页，练习页（列表复习 / 造句 / 钉子户 / 统计）是下一批。

const app = getApp();

Page({
  data: {
    logged: false,
    uid: '',
    role: '',
    roleText: '',
    message: ''
  },

  onShow() {
    const g = app.globalData;
    this.setData({
      logged: !!g.logged,
      uid: g.uid || '',
      role: g.role || '',
      roleText: this.roleOf(g.role)
    });
    if (!g.logged) {
      app.login((err) => {
        if (err) {
          this.setData({ message: '登录失败：' + err.message });
          return;
        }
        this.setData({
          logged: true,
          uid: app.globalData.uid,
          role: app.globalData.role,
          roleText: this.roleOf(app.globalData.role),
          message: ''
        });
      });
    }
  },

  roleOf(role) {
    if (role === 'owner') return '站点所有者';
    if (role === 'member') return '成员';
    if (role === 'pending') return '待批准（等管理员放行）';
    if (role === 'disabled') return '已停用';
    return '';
  },

  onCopyWeb() {
    wx.setClipboardData({
      data: 'https://jrp.110105.xyz',
      success: () => {
        wx.showToast({ title: '网址已复制', icon: 'none' });
      }
    });
  }
});
