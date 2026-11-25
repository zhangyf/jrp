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
//
// 6. **缓存按指纹比对**（2026-11-25）：服务端下发 video_fp / srt_fp，本地缓存
//    记录带 fp，进播放器先比对；COS 同名文件被覆盖（重传视频 / 字幕精校回写）
//    后旧缓存自动失效、重新拉新的。服务端没给指纹就整个不启用缓存、按钮隐藏
//    —— 宁可每次走网络，也绝不播旧内容。详见 utils/listen-cache.js。

const app = getApp();
const trial = require('../../utils/trial.js');
const cache = require('../../utils/listen-cache.js');
const version = require('../../utils/version.js');

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
    initialTime: 0,
    // 视频缓存按钮：none / downloading / cleaning / cached / blocked
    cacheState: 'none',
    cacheLabel: '缓存',
    // 缓存过程详情（2026-11-25 老师要求能看见进度、知道卡在哪一步）
    cacheShowDetail: false,
    cacheDetail: '',      // 一行说明：阶段 + 字节 + 百分比 + 耗时
    cachePercent: -1,     // -1 = 不显示进度条
    cacheStalled: false,  // 15 秒无进展 → 详情行转警示色
    // 服务端没下发指纹时隐藏缓存按钮（老版本服务端不赌缓存是新的）
    cacheEnabled: false
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
    this.setData({
      loading: true, err: '', playing: false, active: -1, scrollInto: '', cacheEnabled: false,
      cacheShowDetail: false, cacheDetail: '', cachePercent: -1, cacheStalled: false
    });
    const level = this.data.curLevel;
    self.curFile = file; // 记下原始文件名（含扩展名），缓存按钮删/下都要用，别靠拼 title
    self.curVideoFp = '';
    self.curVideoUrl = ''; // 手动缓存要用远程地址，别拿可能被换成本地路径的 videoUrl
    self.cacheSeq = (self.cacheSeq || 0) + 1; // 换视频作废上一批阶段事件
    self.cacheCancelled = false;
    self.cacheStageInfo = {};
    const q = '/api/listening?level=' + encodeURIComponent(level) +
      '&file=' + encodeURIComponent(file);

    // ⚠️ 指纹在响应里，字幕缓存必须等拿到 res.srt_fp 才能判命中。
    // 标题（res.title）也只有服务端有，所以无论如何都要发这一次请求。
    app.request(q, 'GET', {}, true)
      .then((res) => {
        self.lastT = -1;
        self.pending = 0;
        self.lastSave = 0;
        const title = res.title || file;
        const videoFp = res.video_fp || '';
        const srtFp = res.srt_fp || '';
        self.curVideoFp = videoFp;
        self.curVideoUrl = res.video_url || '';
        self.curVideoSize = res.size || 0; // 服务端给的 COS 大小，下载前算空间用

        // 字幕：指纹一致才用缓存；不一致（COS 上 SRT 被精校覆盖）就用服务端
        // 的新字幕并覆盖写缓存。没指纹时 getCues/setCues 自动不启用。
        const cachedCue = cache.getCues(level, file, srtFp);
        let cues = res.cues || [];
        let note = res.note || '';
        if (cachedCue) {
          cues = cachedCue.cues;
          if (cachedCue.note) note = cachedCue.note;
        } else if (cues.length) {
          cache.setCues(level, file, cues, note, srtFp);
        }
        self.setData({
          loading: false,
          playing: true,
          title: title,
          videoUrl: res.video_url || '',
          cues: cues,
          note: note,
          cacheEnabled: !!videoFp,
          // 上次听到哪里：>1 秒才跳（太小没意义），initial-time 交给 video
          initialTime: lsGetPos(level, title) > 1 ? lsGetPos(level, title) : 0
        });
        // 同步标题栏缓存按钮的三态（指纹对不上的旧缓存会被就地清掉）。
        self.setCacheState(cache.getVideoState(level, file, videoFp));
        // 视频缓存：本地命中且版本一致才直接用；否则后台下新版，下次秒开。
        // 拿到 video_url（带 ?v=fp）后再缓存，异步不阻塞播放。
        if (videoFp && res.video_url) {
          const local = cache.getVideo(level, file, videoFp);
          if (local) {
            self.setData({ videoUrl: local });
          } else {
            // ⚠️ 后台自动缓存也要接详情事件流：老师看到的「下载中」常常就是这里，
            //    不传 onStage 就永远只有一个死文案。seq 与手动点击共用，手动一接管
            //    自动的事件自动失效，两条并发不会互相刷详情。
            // ⚠️ 退出页面再进来：back() 不 abort，下载还在跑。cacheVideo 内部按
            //    key+fp 去重，这里拿到的是同一条任务的 Promise，onStage 会先收到
            //    「续接上次下载」的进度快照，再接着收后续进度，不会重新下 32MB。
            const autoSeq = self.cacheSeq;
            const onAutoStage = (stage, info) => self.applyCacheStage(stage, info, autoSeq, file);
            self.setCacheState('downloading');
            self.setData({ cacheShowDetail: true, cacheDetail: '准备开始…', cachePercent: 0, cacheStalled: false });
            cache.cacheVideo(level, file, res.video_url, videoFp, res.size, onAutoStage).then((p) => {
              if (self.curFile !== file) return; // 已经切了别的视频，别污染当前页
              if (p && self.data.videoUrl === res.video_url) {
                self.setData({ videoUrl: p });
              }
              // 后台下载静默收尾（不弹 toast），但按钮终态和失败详情照常落。
              // settleCache 内部按 seq 判归属：手动点击接管后这条自动收口不会覆盖。
              self.settleCache(p, autoSeq, file, true);
            });
          }
        }
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

  // 把缓存状态映射成标题栏按钮的文案，一次 setData 搞定。
  setCacheState(state) {
    const labels = {
      cached: '已缓存', downloading: '下载中…', cleaning: '清理中…',
      blocked: '空间不足', none: '缓存'
    };
    this.setData({ cacheState: state, cacheLabel: labels[state] || '缓存' });
  },

  // 详情行复位（成功 / 删除 / 切视频时调用）。
  clearCacheDetail() {
    this.setData({
      cacheShowDetail: false, cacheDetail: '', cachePercent: -1, cacheStalled: false
    });
  },

  // cacheVideo 的 onStage 事件流 → 标题栏按钮 + 详情行 + 进度条。
  // ⚠️ seq 用来挡并发：openVideo 的后台缓存和手动缓存会同时发事件，
  //    只认本次点击那一条，否则两条互相覆盖，详情数字会跳（同 lastError 老坑）。
  applyCacheStage(stage, info, seq, file) {
    if (this.curFile !== file || seq !== this.cacheSeq) return;
    if (stage === 'done') { this.curCleaned = Number(info) || 0; return; }
    if (stage === 'cancelled') { this.cacheCancelled = true; return; }
    const d = info || {};
    this.cacheStageInfo = d;
    const patch = {
      cacheShowDetail: true,
      cacheDetail: d.text || '',
      cacheStalled: !!d.stalled
    };
    // percent 为 -1 表示这个阶段不碰进度条（stalled 时保留上一次的百分比）。
    if (typeof d.percent === 'number' && d.percent >= 0) patch.cachePercent = d.percent;
    // 只在进行中时改按钮态，绝不把 cached / blocked / none 冲掉。
    const st = this.data.cacheState;
    if (st === 'downloading' || st === 'cleaning') {
      if (stage === 'cleaning' || stage === 'retry' || stage === 'preparing') {
        patch.cacheState = 'cleaning';
        patch.cacheLabel = d.label || '清理中…';
      } else if (stage === 'downloading' || stage === 'writing') {
        patch.cacheState = 'downloading';
        patch.cacheLabel = d.label || '下载中…';
      }
    }
    this.setData(patch);
  },

  // 进行中点按钮：给完整诊断，并允许取消（原来直接 return，点了没反应）。
  showCacheDetail(level, file) {
    const d = this.cacheStageInfo || {};
    const detail = cache.getLastDetail();
    const lines = [
      '当前阶段：' + (d.at || '未知'),
      '详情：' + (d.text || '暂无'),
      '已用：' + (d.elapsed || 0) + ' 秒',
      '清理旧缓存：' + (d.cleaned || 0) + ' 项'
    ];
    if (typeof d.written === 'number' && d.written > 0) {
      lines.push('已下载：' + (d.written / 1048576).toFixed(1) + ' / ' +
        (d.total / 1048576).toFixed(1) + ' MB');
    }
    if (typeof d.round === 'number') lines.push('重试：' + d.round + '/' + d.maxRounds);
    if (d.stalled) lines.push('注意：已 ' + d.stallSeconds + ' 秒无进展');
    // 原始 errMsg 是技术黑话，正式版不在弹窗里显示；进度/阶段/耗时照常给。
    if (detail && cache.diagEnabled()) lines.push('最近错误：' + String(detail).slice(0, 80));
    lines.push('构建码：' + version.CODE_VERSION);
    const self = this;
    wx.showModal({
      title: '缓存进度',
      content: lines.join('\n'),
      confirmText: '取消本次缓存',
      cancelText: '继续等待',
      confirmColor: '#d9534f',
      success(res) {
        if (!res.confirm) return;
        const n = cache.cancelCache(level, file);
        if (!n) {
          wx.showToast({ title: '当前没有进行中的下载', icon: 'none' });
          return;
        }
        // ⚠️ cancelled 事件可能被并发的 seq 挡掉，这里主动收口，别等 Promise。
        self.cacheCancelled = true;
        self.setCacheState('none');
        self.clearCacheDetail();
        wx.showToast({ title: '已取消缓存', icon: 'none' });
      }
    });
  },

  // 手动点击 / 自动后台缓存共用的终态处理。
  // ⚠️ 修一个既有 bug：原来自动缓存结束后按钮永远停在「下载中…」—— 终态判断
  //    把自动缓存自己设的 downloading 当成「手动锁定态」挡掉了。现在按 seq 判归属，
  //    只有当前这条仍有效才写终态；被手动接管（seq 变了）就什么都不碰。
  settleCache(p, seq, file, silent) {
    const self = this;
    if (self.curFile !== file || seq !== self.cacheSeq) return;
    const cleaned = self.curCleaned || 0;
    if (p) {
      self.setCacheState(cache.getVideoState(self.data.curLevel, file, self.curVideoFp || ''));
      self.clearCacheDetail();
      if (!silent) {
        wx.showToast({
          title: cleaned ? '已缓存（清理 ' + cleaned + ' 项旧缓存）' : '已缓存',
          icon: 'none', duration: 2500
        });
      }
      return;
    }
    if (self.cacheCancelled) {
      self.cacheCancelled = false;
      self.setCacheState('none');
      self.clearCacheDetail();
      return;
    }
    const why = cache.getLastFailure() || cache.getLastError() ||
      ('缓存失败(无原因/' + version.CODE_VERSION + ')');
    // 空间类失败把按钮锁成终态：再点只提示原因，不再白下 32MB。
    if (/上限|仍不够|空间不足/.test(why)) {
      self.cacheBlockedReason = why;
      self.setCacheState('blocked');
    } else {
      self.setCacheState('none');
    }
    // ⚠️ 正式版静默诊断（PR #36 口径）：进度/阶段是正常反馈，正式版照常显示；
    //    但失败原因、原始 errMsg、构建码属于调试信息，只在 develop / trial 给。
    //    正式版失败后详情行只留一句中性文案，按钮回 none/blocked 就是反馈。
    const diag = cache.diagEnabled();
    const raw = diag ? String(cache.getLastDetail() || '').replace(/\s+/g, ' ').slice(0, 60) : '';
    self.setData({
      cacheShowDetail: true, cachePercent: -1, cacheStalled: false,
      cacheDetail: diag
        ? why + (raw && raw !== why ? ' · ' + raw : '') + ' · ' + version.CODE_VERSION
        : '缓存未完成，重进本视频可重试'
    });
    if (!silent && diag) wx.showToast({ title: why, icon: 'none', duration: 3000 });
  },

  // 标题栏缓存按钮：
  //   none → 手动下载；downloading / cleaning → 看详情（可取消）；
  //   cached → 弹确认删本地缓存；blocked → 只提示原因、不再重复下载。
  onCacheTap() {
    const level = this.data.curLevel;
    const file = this.curFile || '';
    const fp = this.curVideoFp || '';
    const self = this;
    if (!file) return;
    // 老服务端没下发指纹：按钮本该隐藏，这里兜底也不许缓存（绝不播旧内容）。
    if (!fp) {
      wx.showToast({ title: '当前版本不支持缓存', icon: 'none' });
      return;
    }
    const st = this.data.cacheState;
    if (st === 'downloading' || st === 'cleaning') {
      this.showCacheDetail(level, file);
      return;
    }
    if (st === 'blocked') {
      wx.showToast({ title: this.cacheBlockedReason || '空间不足，无法缓存', icon: 'none', duration: 3000 });
      return;
    }
    if (st === 'cached') {
      wx.showModal({
        title: '删除本地缓存',
        content: '删除后下次播放会重新走网络下载。',
        confirmText: '删除',
        confirmColor: '#d9534f',
        success(res) {
          if (!res.confirm) return;
          cache.deleteVideo(level, file);
          // 本地文件已删，把 src 换回远程地址，别让播放器指着不存在的文件。
          if (self.curVideoUrl) self.setData({ videoUrl: self.curVideoUrl });
          self.setCacheState(cache.getVideoState(level, file, fp));
          self.clearCacheDetail();
          wx.showToast({ title: '已删除', icon: 'none' });
        }
      });
      return;
    }
    // none → 下载。用服务端给的远程地址（带 ?v=fp），不能用可能已被换成本地路径的 videoUrl。
    const url = this.curVideoUrl || this.data.videoUrl;
    if (!url) return;
    const seq = (self.cacheSeq || 0) + 1;
    self.cacheSeq = seq;
    self.curCleaned = 0;
    self.cacheCancelled = false;
    self.cacheStageInfo = {};
    self.setData({ cacheState: 'downloading', cacheLabel: '下载中…', cacheShowDetail: true, cacheDetail: '准备开始…', cachePercent: 0, cacheStalled: false });
    const onStage = (stage, info) => self.applyCacheStage(stage, info, seq, file);
    cache.cacheVideo(level, file, url, fp, this.curVideoSize || 0, onStage).then((p) => {
      if (self.curFile !== file || seq !== self.cacheSeq) return; // 切了视频 / 已被接管
      // 正在用远程地址播的话，切到本地路径（缓存完就该用本地）。
      if (p && self.data.videoUrl !== p) self.setData({ videoUrl: p });
      self.settleCache(p, seq, file, false);
    });
  },

  back() {
    this.flush(); // 离开播放器把没上报的时长交上去
    this.savePos(); // 记住进度，下次进来接着听
    this.lastT = -1;
    this.pending = 0;
    this.setData({
      playing: false, videoUrl: '', cues: [], active: -1, title: '', initialTime: 0,
      cacheState: 'none', cacheLabel: '缓存', cacheEnabled: false,
      cacheShowDetail: false, cacheDetail: '', cachePercent: -1, cacheStalled: false
    });
    this.cacheSeq = (this.cacheSeq || 0) + 1; // 退出播放器作废在途阶段事件
    this.curFile = '';
    this.curVideoFp = '';
    this.curVideoUrl = '';
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
