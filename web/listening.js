// 听力（web 端 2026-11-05 补：小程序有这块，web 那排导航里没有）。
//
// 三条口径，改之前先读：
//
// 1. **字幕联动全在时间轴上**。服务端把 SRT 拆成 {start,end,text} 数组下发，
//    这里监听 <video> 的 timeupdate 拿当前秒，线性找当前句高亮；点某句就
//    video.currentTime = 那句的 start。不碰 COS、不碰签名。
//
// 2. **视频地址是 EO 域名裸路径**（https://listening.110105.xyz/{Nx}/{file}），
//    不带 COS 签名，EO 侧回源时自动补真实 key 前缀。字幕缺失不算错（纯播放）。
//
// 3. **实际播放时长上报**（/api/listening/progress）。口径是老师定的：
//    只算**正在播放**的秒数，暂停、拖动跳过的不算。做法：timeupdate 里拿
//    相邻两次 currentTime 的差值，差值在 [0, 1.5] 秒内（正常播放节奏）才累加；
//    seek 造成的大跳变（> 1.5s）直接丢弃。攒够 15 秒或离开播放器时上报一次。
//
// 4. **字幕跟着播放连续滚**。只写字幕框自己的 scrollTop，不用 scrollIntoView：
//    后者会连带滚动全部祖先（含整个页面）把视频顶出屏幕，block:'center' 又是按
//    视口算的，字幕框在页面下半部分时压根滚不到位。改完每换一句都把当前句摆到
//    框内约 1/3 处，不会「高亮走到当前页最后一句就停住」。
//
// ⚠️ 全局只留一个顶层符号 listening：web/*.js 是老式多 script 全局挂载，
// 顶层 function 会覆盖先加载文件里的同名 var（2026-09-21 出过整站白屏事故）。
// 本文件所有内部函数一律 ls 前缀。

var listening = {
  levels: [],   // [{level, videos:[名,...]}]
  cur: '',      // 当前选中的级别
  loaded: false,
  // 播放态
  playing: false,
  title: '',
  cues: [],
  active: -1,
  curLevel: '', // 正在播的这个视频所属级别（上报用）
  followPause: 0, // 用户手动翻字幕后，暂时别抢回滚动位置（毫秒时间戳）

  load: function () {
    var self = this;
    if (this.loaded) { this.render(); return; }
    el('lsBody').innerHTML = '<p class="muted">加载中…</p>';
    app.api('/api/listening').then(function (d) {
      self.levels = d.levels || [];
      if (!self.levels.length) {
        el('lsBody').innerHTML = '<p class="muted">还没有听力素材</p>';
        return;
      }
      self.cur = self.levels[0].level;
      self.loaded = true;
      self.render();
    }).catch(function (e) {
      el('lsBody').innerHTML = '';
      el('lsMsg').textContent = '加载失败：' + (e && e.message ? e.message : '未知错误');
    });
  },

  render: function () {
    var self = this;
    // 级别 chips（复用知识点页那套 .kb-n 样式）
    el('lsLevels').innerHTML = this.levels.map(function (l) {
      return '<button class="kb-n' + (l.level === self.cur ? ' on' : '') +
        '" data-level="' + l.level + '">' + esc(l.level) + '</button>';
    }).join('');
    Array.prototype.forEach.call(el('lsLevels').querySelectorAll('.kb-n'), function (b) {
      b.addEventListener('click', function () {
        if (b.dataset.level !== self.cur) { self.cur = b.dataset.level; self.render(); }
      });
    });

    var hit = this.levels.filter(function (l) { return l.level === self.cur; })[0];
    var videos = (hit && hit.videos) || [];
    renderSummary(el('lsSummary'), [
      { label: '级别', value: self.cur },
      { label: '视频', value: videos.length }
    ]);
    el('lsMsg').textContent = '';

    if (!videos.length) {
      el('lsBody').innerHTML = '<p class="muted">这个级别还没有视频</p>';
      return;
    }
    el('lsBody').innerHTML = '<div class="ls-vlist">' + videos.map(function (v) {
      return '<button class="ls-vitem" data-file="' + esc(v) + '">' +
        '<span class="ls-vplay">▶</span><span class="ls-vname">' + esc(v) + '</span></button>';
    }).join('') + '</div>';
    Array.prototype.forEach.call(el('lsBody').querySelectorAll('.ls-vitem'), function (b) {
      b.addEventListener('click', function () { self.open(b.dataset.file); });
    });
  },

  open: function (file) {
    var self = this;
    el('lsBody').innerHTML = '<p class="muted">加载中…</p>';
    el('lsMsg').textContent = '';
    app.api('/api/listening?level=' + encodeURIComponent(this.cur) +
      '&file=' + encodeURIComponent(file)).then(function (d) {
      self.playing = true;
      self.title = d.title || file;
      self.cues = d.cues || [];
      self.active = -1;
      self.curLevel = self.cur;
      self.renderPlayer(d.video_url, d.note);
    }).catch(function (e) {
      el('lsBody').innerHTML = '<button class="ghost" id="lsBack">← 返回列表</button>' +
        '<p class="muted">加载失败：' + esc(e.message || '') + '</p>';
      var b = el('lsBack');
      if (b) b.addEventListener('click', function () { self.playing = false; self.render(); });
    });
  },

  renderPlayer: function (url, note) {
    var self = this;
    var html = '<button class="ghost" id="lsBack">← 返回列表</button>' +
      '<div class="ls-title">' + esc(this.title) + '</div>' +
      '<video id="lsPlayer" class="ls-player" controls preload="metadata" playsinline></video>' +
      (note ? '<p class="muted">' + esc(note) + '</p>' : '') +
      '<div id="lsCueBox" class="ls-cues">';
    if (this.cues.length) {
      html += this.cues.map(function (c, i) {
        return '<div class="ls-cue" id="ls-cue-' + i + '" data-i="' + i + '">' +
          '<span class="ls-cue-idx">' + (i + 1) + '</span>' +
          '<span class="ls-cue-txt">' + esc(c.text) + '</span></div>';
      }).join('');
    } else {
      html += '<p class="muted">这段没有字幕</p>';
    }
    html += '</div>';
    el('lsBody').innerHTML = html;

    var v = el('lsPlayer');
    v.src = url;
    this.bindPlayer(v);

    Array.prototype.forEach.call(el('lsCueBox').querySelectorAll('.ls-cue'), function (row) {
      row.addEventListener('click', function () {
        var i = parseInt(row.dataset.i, 10);
        var cue = self.cues[i];
        if (!cue) return;
        v.currentTime = cue.start;
        v.play();
        self.followPause = 0;
        self.setActive(i);
      });
    });
    // 用户自己拖字幕回看时，自动跟随让位 6 秒；之后恢复跟着播放滚。
    // 只认 wheel / touchstart，不认 scroll —— 后者会把程序自己写的 scrollTop
    // 也算进去，等于自己把自己暂停了。
    var cueBox = el('lsCueBox');
    if (cueBox) {
      var pause = function () { self.followPause = Date.now() + 6000; };
      cueBox.addEventListener('wheel', pause, { passive: true });
      cueBox.addEventListener('touchstart', pause, { passive: true });
    }
    el('lsBack').addEventListener('click', function () {
      self.flush(); // 离开播放器把没上报的时长交上去
      self.playing = false;
      v.pause();
      self.render();
    });
  },

  bindPlayer: function (v) {
    var self = this;
    this.lastT = -1;
    this.pending = 0;   // 还没上报的秒数
    v.addEventListener('timeupdate', function () {
      var t = v.currentTime;
      // —— 时长累加：只算正常播放节奏的差值，seek 大跳变丢弃 ——
      if (self.lastT >= 0) {
        var d = t - self.lastT;
        if (d > 0 && d <= 1.5) self.pending += d;
      }
      self.lastT = t;
      // 攒够 15 秒上报一次
      if (self.pending >= 15) self.flush();

      // —— 字幕高亮 ——
      self.highlight(t);
    });
    // 暂停 / 结束也交一次，别把尾巴上的几秒弄丢
    v.addEventListener('pause', function () { self.flush(); });
    v.addEventListener('ended', function () { self.flush(); });
    v.addEventListener('error', function () {
      el('lsMsg').textContent = '视频加载失败 · 若已播放一段时间，返回重进刷新地址';
    });
  },

  highlight: function (t) {
    var cues = this.cues;
    if (!cues.length) return;
    var idx = -1;
    for (var i = 0; i < cues.length; i++) {
      if (t >= cues[i].start && t < cues[i].end) { idx = i; break; }
      if (t < cues[i].start) { idx = i - 1; break; }
    }
    if (idx < 0 && cues.length && t >= cues[cues.length - 1].end) idx = cues.length - 1;
    if (idx !== this.active) this.setActive(idx);
  },

  setActive: function (i) {
    this.active = i;
    var box = el('lsCueBox');
    if (!box) return;
    Array.prototype.forEach.call(box.querySelectorAll('.ls-cue'), function (row, k) {
      row.classList.toggle('on', k === i);
    });
    // 字幕跟着播放连续滚：每换一句都把当前句摆到框内约 1/3 处。
    //
    // ⚠️ 两处别退回去：
    //  ① 只在「高亮句超出可见区」时才滚 → 高亮一路走到当前页最后一句才停，
    //     SRT 句子间常有空隙（如 59.1s~66.8s），idx 不变期间更是一动不动。
    //  ② cur.scrollIntoView() → 会连带滚动所有可滚动祖先（包括整个页面），
    //     把视频顶出屏幕；block:'center' 又是按视口算的，字幕框在页面下半部
    //     时根本落不到位。这里只写字幕框自己的 scrollTop，页面绝对不动。
    var cur = el('ls-cue-' + i);
    if (!cur || i < 0) return;
    // 高亮照常更新，只是不抢滚动位置（用户正在手动翻看）
    if (Date.now() < this.followPause) return;
    var cr = cur.getBoundingClientRect();
    var br = box.getBoundingClientRect();
    var h = box.clientHeight || br.height;
    if (!h) return;
    // cr.top - br.top 是「当前句相对框可视区顶」的偏移，加上已滚掉的量才是
    // 它在整段字幕里的位置；减 1/3 框高，让当前句落在偏上而非贴边。
    // 这个式子与平滑动画的中间态无关：box.scrollTop + (cr.top - br.top)
    // 恒等于「当前句在整段字幕里的绝对位置」，动画被打断也不会算偏。
    var target = box.scrollTop + (cr.top - br.top) - h / 3;
    if (target < 0) target = 0;
    // 只赋 scrollTop，平滑交给 CSS 的 scroll-behavior（比 scrollTo 的 options
    // 兼容性好，jsdom 也能直接测到这条路径）。
    box.scrollTop = target;
  },

  // 把攒下的时长上报给后端。失败静默（下次再攒再报，不影响播放）。
  flush: function () {
    var secs = this.pending;
    this.pending = 0;
    if (secs < 1 || !this.curLevel) return;
    var body = JSON.stringify({ level: this.curLevel, seconds: Math.round(secs * 10) / 10 });
    app.api('/api/listening/progress', { method: 'POST', body: body })
      .catch(function () { /* 上报失败不吵用户 */ });
  }
};
