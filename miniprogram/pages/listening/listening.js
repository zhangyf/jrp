// 听力模块 —— 视频 + 同名 SRT 字幕，边播边看逐句高亮（2026-11-05 老师要求）。
//
// 两条口径，改之前先读：
//
// 1. **字幕联动全在时间轴上**。服务端把 SRT 解析成 {start,end,text} 数组下发，
//    这里监听 video 的 onTimeUpdate 拿当前秒，二分/线性找当前句高亮；
//    点某句就 wx.createVideoContext().seek 到那句的 start。不碰 COS、不碰签名。
//
// 2. **视频地址是 EO 域名裸路径**（https://listening.110105.xyz/{Nx}/{file}），
//    不带 COS 签名，EO 侧回源时自动补真实 key 前缀。字幕缺失不算错（纯播放）。
//
// 3. **未登录（含体验版）锁住**。/api/listening 不在体验版白名单里，trial 用户
//    请求会 403，所以进页面就按 trial.js 锁：点级别/视频先弹登录浮层。
//
// 4. **字幕跟着播放连续滚**：每换一句都更新 scroll-into-view，别只在「跑出可见区」
//    时才滚 —— 那样高亮会一路走到当前页最后一句才停，SRT 句子间还常有空隙，
//    idx 不变期间更是一动不动。web 端同理（只写字幕框自己的 scrollTop）。
//
// 5. **记住上次听到哪里**：按「级别|标题」把 currentTime 存本地存储
//    （key 跟 web 端一致：jrp_listen_pos|<level>|<title>），进播放器用 video 的
//    initial-time 跳过去，重开不用从头听。纯前端，不碰后端。

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
    scrollInto: '',  // scroll-view 的 scroll-into-view，每换一句给新 id 才会滚
    note: '',
    // trial 门禁
    trial: false,
    needLogin: false,
    // 上次进度（秒）：进播放器时由 video 的 initial-time 跳过去
    initialTime: 0
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
    this.setData({ loading: true, err: '', playing: false, active: -1, scrollInto: '' });
    const q = '/api/listening?level=' + encodeURIComponent(this.data.curLevel) +
      '&file=' + encodeURIComponent(file);
    app.request(q, 'GET', {}, true)
      .then((res) => {
        self.lastT = -1;
        self.pending = 0;
        self.lastSave = 0;
        const title = res.title || file;
        self.setData({
          loading: false,
          playing: true,
          title: title,
          videoUrl: res.video_url || '',
          cues: res.cues || [],
          note: res.note || '',
          // 上次听到哪里：>1 秒才跳（太小没意义），initial-time 交给 video
          initialTime: lsGetPos(self.data.curLevel, title) > 1 ? lsGetPos(self.data.curLevel, title) : 0
        });
      })
      .catch((e2) => self.setData({ loading: false, err: e2.message || '加载失败' }));
  },

  // 播放进度更新 → 找当前句高亮。字幕不多，线性扫足够（几百句以内无压力）。
  //
  // 同时累加「实际播放时长」上报：口径是老师定的（2026-11-05）——只算正在播放
  // 的秒数，暂停、拖动跳过的不算。做法：相邻两次 onTimeUpdate 的 currentTime
  // 差值在 (0, 1.5] 秒内（正常播放节奏）才累加；seek 大跳变直接丢弃。攒够 15
  // 秒上报一次，onHide / onUnload / back 时把尾巴交上去。
  onTimeUpdate(e) {
    const t = e.detail.currentTime;
    // —— 时长累加 ——
    if (this.lastT >= 0) {
      const d = t - this.lastT;
      if (d > 0 && d <= 1.5) this.pending = (this.pending || 0) + d;
    }
    this.lastT = t;
    if (this.pending >= 15) this.flush();

    // 记住进度：每 ~5 秒存一次（onTimeUpdate 一秒好几次，别每次都写存储）
    const now = Date.now();
    if (!this.lastSave || now - this.lastSave > 5000) {
      this.lastSave = now;
      lsSetPos(this.data.curLevel, this.data.title, t);
    }

    // —— 字幕高亮 ——
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
      // ⚠️ 每换一句都设 scroll-into-view，不能只在「跑出可见区」时才设：
      //    那样字幕会等高亮走到当前屏最后一句才跳一下，中间大段空隙完全不动。
      //    连续设值配合 scroll-with-animation，字幕就跟着播放平滑往下走。
      //    ⚠️ scroll-into-view 只在值变化时生效，同一句重复设同一个值不会滚，
      //    所以这里带上了下标，每句都是新值。
      const patch = { active: idx };
      // 用户正手动翻字幕回看时，高亮照常更新，但别把滚动位置抢回去
      if (Date.now() >= (this.followPause || 0)) {
        patch.scrollInto = idx >= 0 ? 'cue-' + idx : '';
      }
      this.setData(patch);
    }
  },

  // 手指碰字幕列表 → 自动跟随让位 6 秒，之后恢复跟着播放滚。
  // 只认 touchstart，不认 bindscroll（后者会把程序自己触发的滚动也算进去）。
  pauseFollow() {
    this.followPause = Date.now() + 6000;
  },

  // 把攒下的时长上报给后端。失败静默（下次再攒再报，不影响播放）。
  flush() {
    const secs = this.pending || 0;
    this.pending = 0;
    if (secs < 1 || !this.data.curLevel) return;
    app.request('/api/listening/progress', 'POST',
      { level: this.data.curLevel, seconds: Math.round(secs * 10) / 10 }, true)
      .catch(() => { /* 上报失败不吵用户 */ });
  },

  onHide() { this.flush(); this.savePos(); },
  onUnload() { this.flush(); this.savePos(); },

  // 把当前播放位置存到本地（退出/切后台时调用，别把尾巴弄丢）
  savePos() {
    if (this.lastT >= 0) lsSetPos(this.data.curLevel, this.data.title, this.lastT);
  },

  // 点某句 → seek 到那句开头。
  seekCue(e) {
    const i = e.currentTarget.dataset.i;
    const cue = this.data.cues[i];
    if (!cue) return;
    this.videoCtx = this.videoCtx || wx.createVideoContext('player', this);
    this.videoCtx.seek(cue.start);
    this.videoCtx.play();
    // 点句跳转后也让字幕跟着落位（播放中 timeupdate 随后会继续接管跟随）
    this.followPause = 0;
    this.setData({ active: i, scrollInto: 'cue-' + i });
  },

  onVideoError(e) {
    const msg = (e.detail && e.detail.errMsg) || '视频加载失败';
    // 签名 URL 过期是最常见原因：提示重进。
    this.setData({ note: msg + ' · 若已播放一段时间，退出重进刷新地址' });
  },

  back() {
    this.flush(); // 离开播放器把没上报的时长交上去
    this.savePos(); // 记住进度，下次进来接着听
    this.lastT = -1;
    this.pending = 0;
    this.setData({ playing: false, videoUrl: '', cues: [], active: -1, title: '', initialTime: 0 });
  },

  // utils/trial.js 要求的挂法
  onTapLocked: trial.onTapLocked,
  closeLogin: trial.closeLogin,
  goLogin: trial.goLogin,
  noop: trial.noop
});

// —— 上次进度：本地存储按「级别|标题」存秒数，key 跟 web 端一致。失败静默 ——
function lsPosKey(level, title) {
  return 'jrp_listen_pos|' + (level || '') + '|' + (title || '');
}
function lsGetPos(level, title) {
  try {
    const raw = wx.getStorageSync(lsPosKey(level, title));
    const n = raw === '' || raw === null ? 0 : parseFloat(raw);
    return isNaN(n) ? 0 : n;
  } catch (e) { return 0; }
}
function lsSetPos(level, title, t) {
  try { wx.setStorageSync(lsPosKey(level, title), String(Math.floor(t))); } catch (e) {}
}
