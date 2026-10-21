// 录入新词：手打 或 拍照/相册取词。
//
// 两种入口最后汇到同一个「待录列表」，确认后才 POST /api/words ——
// 中间这层是故意的：OCR 认出来的东西不能闭着眼写进档案，老师得先看过、能改、能删。
//
// 加词的实现在服务端 words_add.go，跟 CLI 的 add-words 是同一份：
// 重复词会跳过，中文释义撞车会在结果里点名（不阻断）。
//
// ⚠️ 两个「不用你填」的设计（2026-10-21 老师要求）：
//   ① 不选课 —— 词不一定属于哪一课，服务端落「未分组」，出题时照样会被练到
//      （出题遍历所有组，不按课过滤）。
//   ② 不填中文 —— 服务端 POST /api/word-def 自己查（先查档案，再问模型）。

const app = getApp();

// 词形里已经带括号 = 已经有汉字写法（みず(水)）或语法注释（さん（敬称）），不用再补
function hasParen(s) {
  return (s || '').indexOf('(') >= 0 || (s || '').indexOf('（') >= 0;
}

// 缩图再发：手机拍的照片动辄 3000×4000、两三兆，原样发给视觉模型的下场是超时。
//
// 2026-10-21 的线上日志是这么写的（老师传了 4 张，只回来 1 张）：
//   vision: 模型 deepseek/...vision-exp 失败：context deadline exceeded
// —— 30 秒都没算完，不是模型不认识，是图太大。压成 JPEG 0.72 之后
// base64 从两三兆掉到两三百 K，模型几秒就回；课本生词表这种内容压了照样认得清。
//
// ⚠️ **判断条件必须是文件体积，不是像素尺寸**（第一版写错了，2026-10-21 修）：
// `chooseMedia` 带 `sizeType:['compressed']` 出来的图，长边常常正好是 1280
// —— 按尺寸看「不用缩」，可文件仍有 800KB~1.5MB，照样超时。
//
// 压缩失败（老机型没有离屏 canvas、图是 HEIC 之类）不能卡住流程：
// 任何一步出错就退回原图，宁可慢一点也要让老师把词录进去。
const SHRINK_OVER_BYTES = 250 * 1024; // 超过这个体积才重绘

function shrinkToBase64(path, cb) {
  const fs = wx.getFileSystemManager();
  const raw = () => {
    try {
      cb(fs.readFileSync(path, 'base64'));
    } catch (e) {
      cb('');
    }
  };
  // 重绘成 JPEG 0.72：尺寸超了就顺带缩到长边 1280，没超也压一下质量
  const redraw = (info) => {
    const w0 = info.width || 0;
    const h0 = info.height || 0;
    if (!w0 || !h0) { raw(); return; }
    const scale = Math.min(1, 1280 / Math.max(w0, h0));
    const w = Math.max(1, Math.round(w0 * scale));
    const h = Math.max(1, Math.round(h0 * scale));
    try {
      const cv = wx.createOffscreenCanvas({ type: '2d', width: w, height: h });
      const ctx = cv.getContext('2d');
      const img = cv.createImage();
      img.onload = () => {
        try {
          ctx.drawImage(img, 0, 0, w, h);
          const url = cv.toDataURL('image/jpeg', 0.72);
          const i = url.indexOf(',');
          cb(i >= 0 ? url.slice(i + 1) : url);
        } catch (e) { raw(); }
      };
      img.onerror = raw;
      img.src = path;
    } catch (e) { raw(); }
  };
  const bySize = () => {
    try {
      fs.getFileInfo({
        filePath: path,
        success(fi) {
          if (fi && fi.size && fi.size <= SHRINK_OVER_BYTES) { raw(); return; }
          wx.getImageInfo({ src: path, success: redraw, fail: raw });
        },
        fail() { wx.getImageInfo({ src: path, success: redraw, fail: raw }); }
      });
    } catch (e) {
      wx.getImageInfo({ src: path, success: redraw, fail: raw });
    }
  };
  try {
    bySize();
  } catch (e) {
    raw();
  }
}

Page({
  data: {
    word: '',

    pending: [], // 待录入：{ word, def, editing }
    shots: [],   // 拍照结果，**按图片分组**：{ path, ok, error, words:[{word,def,checked}] }
    candCount: 0,

    ocrBusy: false,
    ocrNote: '',
    looking: false,
    saving: false,
    result: null // { added, duplicates, dup_definitions[], dup_core_definitions[] }
  },

  // 图片原数据（base64）不进 setData —— 几 MB 的字符串塞进渲染层会把页面卡住，
  // 「重认这张」时才用得上，所以挂在实例上。
  _imgData: {},

  onShow() {
    // 自定义 tabBar 每个页面各一份实例，切回来时要告诉它「现在是我在前面」
    if (typeof this.getTabBar === 'function' && this.getTabBar()) {
      this.getTabBar().setData({ selected: 2 });
    }
  },

  onWord(e) { this.setData({ word: e.detail.value }); },

  // 手打：只写假名，汉字和中文都让服务端补
  addManual() {
    const word = (this.data.word || '').trim();
    if (!word) { wx.showToast({ title: '先写词形', icon: 'none' }); return; }
    this.setData({ word: '', looking: true });
    this.lookup([word], (map) => {
      this.setData({ looking: false });
      const got = map[word] || { word: word, def: '' };
      this.pushPending([got]);
      if (!got.def) {
        wx.showToast({ title: '没查到中文，点条目补一下', icon: 'none' });
      } else if (got.word !== word) {
        wx.showToast({ title: '补成 ' + got.word, icon: 'none' });
      }
    });
  },

  removePending(e) {
    const i = +e.currentTarget.dataset.index;
    const pending = this.data.pending.slice();
    pending.splice(i, 1);
    this.setData({ pending: pending });
  },

  // 列表里直接改中文：查不到 / 查得不准时不用删了重录
  editDef(e) {
    const i = +e.currentTarget.dataset.index;
    this.setData({ ['pending[' + i + '].def']: e.detail.value });
  },

  toggleEdit(e) {
    const i = +e.currentTarget.dataset.index;
    this.setData({ ['pending[' + i + '].editing']: !this.data.pending[i].editing });
  },

  // 拍照 / 相册：一次最多 9 张，服务端交给视觉模型认词（不是 OCR ——
  // 模型看得懂版面，能自己跳过例句和标题，比抄字再切行准得多）。
  //
  // ⚠️ 两个 2026-10-21 老师反馈逼出来的改动：
  //   ① **按图分组**：老师要对着原照片核「这个词认得准不准」，扁平列表对不上号。
  //      所以结果按图片分块，每块带缩略图，点一下看大图。
  //   ② **中文服务端已经补好**：课本照片上常常只有假名没中文，以前要等老师勾完
  //      再点一次「加入待录」才去查，中间那段时间看到的就是一片空白。
  pickImages() {
    const self = this;
    wx.chooseMedia({
      count: 9,
      mediaType: ['image'],
      sourceType: ['album', 'camera'],
      sizeType: ['compressed'],
      success(res) {
        const files = res.tempFiles || [];
        if (!files.length) return;
        self.setData({ ocrBusy: true, ocrNote: '' });
        wx.showLoading({ title: '识别中…', mask: true });

        const paths = files.map((f) => f.tempFilePath);
        const b64s = new Array(paths.length);
        let left = paths.length;
        // 缩图是异步的，等最后一张回来再一起发（顺序靠下标对上，不能乱）
        paths.forEach((p, i) => {
          shrinkToBase64(p, (b64) => {
            b64s[i] = b64 || '';
            left--;
            if (left === 0) self.goVision(paths, b64s);
          });
        });
      }
    });
  },

  // 缩完图才真正发请求：丢掉读不出来的那几张，但保持「下标 ↔ 原图」的对应关系
  goVision(paths, b64s) {
    const images = [];
    const kept = [];
    for (let i = 0; i < paths.length; i++) {
      if (!b64s[i]) continue;
      images.push(b64s[i]);
      kept.push(paths[i]);
    }
    if (!images.length) {
      wx.hideLoading();
      this.setData({ ocrBusy: false, ocrNote: '图片读不出来，换一张试试' });
      return;
    }
    this._imgData = {};
    images.forEach((b64, i) => { this._imgData[i] = b64; });
    this.runVision(images, kept);
  },

  runVision(images, paths) {
    const self = this;
    app.request('/api/vision', 'POST', { images: images }, true, 180000)
      .then((r) => {
        wx.hideLoading();
        const byIndex = r.images || [];
        // 服务端按 index 回，跟本地 paths 一一对上；万一没回（旧版服务端）就整个作废
        const shots = paths.map((p, i) => {
          const got = byIndex[i] || {};
          return {
            path: p,
            ok: !!got.ok,
            error: got.error || '没返回',
            words: (got.candidates || []).map((c) => ({
              word: c.word || '', def: c.def || '', checked: true
            }))
          };
        });
        let n = 0;
        shots.forEach((s) => { n += s.words.length; });
        const failed = [];
        shots.forEach((s, i) => {
          if (!s.ok) failed.push('第' + (i + 1) + '张：' + s.error);
        });
        self.setData({ ocrBusy: false, shots: shots, candCount: n, ocrNote: failed.join('；') });
        if (!n) { wx.showToast({ title: '没认出词条', icon: 'none' }); }
      })
      .catch((e) => {
        wx.hideLoading();
        self.setData({ ocrBusy: false, ocrNote: (e && e.message) || '识别失败' });
      });
  },

  // 单张重认：网关偶尔抽风（限流、超时）只挂掉其中一张，没必要整批重来
  retryShot(e) {
    const i = +e.currentTarget.dataset.index;
    const b64 = this._imgData[i];
    if (!b64) { wx.showToast({ title: '原图已失效，重新选一次', icon: 'none' }); return; }
    const self = this;
    wx.showLoading({ title: '重认中…', mask: true });
    app.request('/api/vision', 'POST', { images: [b64] }, true, 180000)
      .then((r) => {
        wx.hideLoading();
        const got = (r.images || [])[0] || {};
        const shot = {
          path: self.data.shots[i].path,
          ok: !!got.ok,
          error: got.error || '没返回',
          words: (got.candidates || []).map((c) => ({
            word: c.word || '', def: c.def || '', checked: true
          }))
        };
        let n = 0;
        self.data.shots.forEach((s, k) => { n += (k === i ? shot : s).words.length; });
        self.setData({ ['shots[' + i + ']']: shot, candCount: n });
      })
      .catch((err) => {
        wx.hideLoading();
        wx.showToast({ title: (err && err.message) || '重认失败', icon: 'none' });
      });
  },

  // 点缩略图看大图：老师得看清楚照片上的假名/汉字才判断得了认错没有
  previewShot(e) {
    const src = e.currentTarget.dataset.src;
    const urls = this.data.shots.map((s) => s.path).filter(Boolean);
    if (!urls.length) return;
    wx.previewImage({ current: src || urls[0], urls: urls });
  },

  toggleCand(e) {
    const si = +e.currentTarget.dataset.si;
    const wi = +e.currentTarget.dataset.wi;
    const on = !this.data.shots[si].words[wi].checked;
    this.setData({ ['shots[' + si + '].words[' + wi + '].checked']: on });
  },

  // 候选词可以直接改：认错的假名/汉字在手机上改比回电脑改省事
  editCand(e) {
    const si = +e.currentTarget.dataset.si;
    const wi = +e.currentTarget.dataset.wi;
    const field = e.currentTarget.dataset.field;
    this.setData({ ['shots[' + si + '].words[' + wi + '].' + field]: e.detail.value });
  },

  toggleAll() {
    const allOn = this.data.shots.every((s) => s.words.every((c) => c.checked));
    const shots = this.data.shots.map((s) => ({
      path: s.path, ok: s.ok, error: s.error,
      words: s.words.map((c) => Object.assign({}, c, { checked: !allOn }))
    }));
    this.setData({ shots: shots });
  },

  addSelected() {
    const picked = [];
    this.data.shots.forEach((s) => {
      s.words.forEach((c) => {
        if (c.checked && (c.word || '').trim()) {
          picked.push({ word: (c.word || '').trim(), def: (c.def || '').trim() });
        }
      });
    });
    if (!picked.length) { wx.showToast({ title: '先勾几个', icon: 'none' }); return; }

    // 中文服务端认词时已经补过一轮，这里只兜「还是空的 / 只有假名没汉字」的
    const need = picked.filter((p) => !p.def || !hasParen(p.word)).map((p) => p.word);
    const self = this;
    const done = (map) => {
      picked.forEach((p) => {
        const got = map[p.word];
        if (!got) return;
        if (!p.def) p.def = got.def;
        if (got.word && !hasParen(p.word)) p.word = got.word;
      });
      self.setData({ looking: false, shots: [], candCount: 0 });
      self.pushPending(picked);
    };
    if (!need.length) { done({}); return; }
    this.setData({ looking: true });
    this.lookup(need, done);
  },

  clearCands() { this.setData({ shots: [], candCount: 0 }); },

  // 合并进待录列表：同一个词不重复加（列表里已有的保留先加的那条）
  pushPending(items) {
    const seen = {};
    const merged = this.data.pending.slice();
    merged.forEach((p) => { seen[p.word] = 1; });
    items.forEach((p) => {
      if (seen[p.word]) return;
      seen[p.word] = 1;
      merged.push({ word: p.word, def: p.def || '', editing: !p.def });
    });
    this.setData({ pending: merged });
  },

  // 查中文 + 补汉字：先档案后模型，全在服务端（保证跟复习时看到的是同一份）
  // 回调拿到的是 { 原输入: { word: 建议词形, def: 中文 } }
  lookup(words, cb) {
    const self = this;
    app.request('/api/word-def', 'POST', { words: words }, true)
      .then((r) => {
        const map = {};
        (r.items || []).forEach((it) => {
          map[it.input] = { word: it.word || it.input, def: it.def || '' };
        });
        cb(map);
      })
      .catch(() => { self.setData({ looking: false }); cb({}); });
  },

  submit() {
    const n = this.data.pending.length;
    if (!n) { wx.showToast({ title: '还没有要录的词', icon: 'none' }); return; }
    const noDef = this.data.pending.filter((p) => !p.def).length;
    const self = this;
    if (noDef) {
      wx.showModal({
        title: '有 ' + noDef + ' 个词还没中文',
        content: '没中文意思复习时没法出题。要继续录吗？',
        success(r) { if (r.confirm) self.askSubmit(n); }
      });
      return;
    }
    this.askSubmit(n);
  },

  askSubmit(n) {
    const self = this;
    wx.showModal({
      title: '录入 ' + n + ' 个词',
      content: '会生成一份新档案，旧的还在。',
      success(r) { if (r.confirm) self.doSubmit(); }
    });
  },

  doSubmit() {
    const items = this.data.pending.map((p) => ({ word: p.word, def: p.def }));
    this.setData({ saving: true });
    app.request('/api/words', 'POST', { items: items }, true)
      .then((r) => {
        this.setData({
          saving: false,
          pending: [],
          result: {
            added: r.added || 0,
            duplicates: r.duplicates || 0,
            version: r.version || '',
            total: r.total_words || 0,
            dupDefs: r.dup_definitions || [],
            dupCores: r.dup_core_definitions || []
          }
        });
        wx.pageScrollTo({ scrollTop: 0, duration: 200 });
      })
      .catch((e) => {
        this.setData({ saving: false });
        wx.showModal({ title: '没录进去', content: (e && e.message) || '未知错误', showCancel: false });
      });
  },

  again() { this.setData({ result: null }); }
});
