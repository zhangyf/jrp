// 听力模块 —— 视频 + 同名 SRT 字幕，边播边看逐句高亮（2026-11-05 老师要求）。
//
// 两条口径，改之前先读：
//
// 1. **字幕联动全在时间轴上**。服务端把 SRT 解析成 {start,end,text} 数组下发，
//    这里监听 video 的 onTimeUpdate 拿当前秒，二分/线性找当前句高亮；
//    点某句就 wx.createVideoContext().seek 到那句的 start。不碰 COS、不碰签名。
//
// 2. **视频地址是带时效的签名 URL**（3 小时）。每次进详情都现拉一次接口拿新 URL，
//    别缓存 —— 过期了 video 会报错，重进页面即恢复。字幕文件缺失不算错（纯播放）。
//
// 3. **未登录（含体验版）锁住**。/api/listening 不在体验版白名单里，trial 用户
//    请求会 403，所以进页面就按 trial.js 锁：点级别/视频先弹登录浮层。

const app = getApp();
const trial = require('../../utils/trial.js');

Page({
  data: {
    levels: [],      // [{level, videos:[名, ...]}]
    curLevel: '',
    videos: [],      // 当前级别的视频名列表
    loading: true,
    err: '',
    // 详情态
    playing: false,
    title: '',
    videoUrl: '',
    cues: [],        // [{start,end,text}]
    active: -1,      // 当前高亮的字幕下标
    note: '',
    // trial 门禁
    trial: false,
    needLogin: false
  },

  onLoad() {
    this.loadList();
  },

  onShow() {
    trial.sync(this);
  },

  syncTrial() {
    trial.sync(this);
  },

  loadList() {
    const self = this;
    this.setData({ loading: true, err: '' });
    app.request('/api/listening', 'GET', {}, true)
      .then((res) => {
        const levels = res.levels || [];
        if (!levels.length) {
          self.setData({ loading: false, err: '还没有听力素材' });
          return;
        }
        self.setData({
          loading: false,
          levels: levels,
          curLevel: levels[0].level,
          videos: levels[0].videos || []
        });
      })
      .catch((e) => self.setData({ loading: false, err: e.message || '加载失败' }));
  },

  pickLevel(e) {
    const lv = e.currentTarget.dataset.level;
    if (lv === this.data.curLevel) return;
    const hit = this.data.levels.filter((l) => l.level === lv)[0];
    this.setData({ curLevel: lv, videos: hit ? hit.videos : [] });
  },

  // 点某个视频 → 拉详情（签名 URL + 字幕）。
  openVideo(e) {
    const file = e.currentTarget.dataset.file;
    if (!file) return;
    if (trial.locked(this)) { this.onTapLocked(); return; }
    const self = this;
    this.setData({ loading: true, err: '', playing: false, active: -1 });
    const q = '/api/listening?level=' + encodeURIComponent(this.data.curLevel) +
      '&file=' + encodeURIComponent(file);
    app.request(q, 'GET', {}, true)
      .then((res) => {
        self.setData({
          loading: false,
          playing: true,
          title: res.title || file,
          videoUrl: res.video_url || '',
          cues: res.cues || [],
          note: res.note || ''
        });
      })
      .catch((e2) => self.setData({ loading: false, err: e2.message || '加载失败' }));
  },

  // 播放进度更新 → 找当前句高亮。字幕不多，线性扫足够（几百句以内无压力）。
  onTimeUpdate(e) {
    const t = e.detail.currentTime;
    const cues = this.data.cues;
    if (!cues.length) return;
    let idx = -1;
    for (let i = 0; i < cues.length; i++) {
      if (t >= cues[i].start && t < cues[i].end) { idx = i; break; }
      // 播到两句之间的空隙，高亮上一句（t < cues[i].start 且 t >= 前一句 end）
      if (t < cues[i].start) { idx = i - 1; break; }
    }
    if (idx < 0 && cues.length && t >= cues[cues.length - 1].end) idx = cues.length - 1;
    if (idx !== this.data.active) {
      this.setData({ active: idx });
      // 高亮句滚动到可见区（字幕容器中间）。id 前缀避免纯数字。
      if (idx >= 0) {
        const self = this;
        wx.createSelectorQuery().select('#cue-' + idx).boundingClientRect((rect) => {
          if (!rect) return;
          wx.createSelectorQuery().select('#cueBox').boundingClientRect((box) => {
            if (!box) return;
            if (rect.top < box.top || rect.bottom > box.bottom) {
              self.setData({ scrollInto: 'cue-' + idx });
            }
          }).exec();
        }).exec();
      }
    }
  },

  // 点某句 → seek 到那句开头。
  seekCue(e) {
    const i = e.currentTarget.dataset.i;
    const cue = this.data.cues[i];
    if (!cue) return;
    this.videoCtx = this.videoCtx || wx.createVideoContext('player', this);
    this.videoCtx.seek(cue.start);
    this.videoCtx.play();
    this.setData({ active: i });
  },

  onVideoError(e) {
    const msg = (e.detail && e.detail.errMsg) || '视频加载失败';
    // 签名 URL 过期是最常见原因：提示重进。
    this.setData({ note: msg + ' · 若已播放一段时间，退出重进刷新地址' });
  },

  back() {
    this.setData({ playing: false, videoUrl: '', cues: [], active: -1, title: '' });
  },

  // utils/trial.js 要求的挂法
  onTapLocked: trial.onTapLocked,
  closeLogin: trial.closeLogin,
  goLogin: trial.goLogin,
  noop: trial.noop
});
