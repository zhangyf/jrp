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

Page({
  data: {
    word: '',

    pending: [], // 待录入：{ word, def, editing }
    cands: [],   // OCR 候选：{ word, def, raw, checked }

    ocrBusy: false,
    ocrNote: '',
    looking: false,
    saving: false,
    result: null // { added, duplicates, dup_definitions[], dup_core_definitions[] }
  },

  onShow() {
    // 自定义 tabBar 每个页面各一份实例，切回来时要告诉它「现在是我在前面」
    if (typeof this.getTabBar === 'function' && this.getTabBar()) {
      this.getTabBar().setData({ selected: 2 });
    }
  },

  onWord(e) { this.setData({ word: e.detail.value }); },

  // 手打：只写词形，中文让服务端查
  addManual() {
    const word = (this.data.word || '').trim();
    if (!word) { wx.showToast({ title: '先写词形', icon: 'none' }); return; }
    this.setData({ word: '', looking: true });
    this.lookup([word], (defs) => {
      this.setData({ looking: false });
      this.pushPending([{ word: word, def: defs[word] || '' }]);
      if (!defs[word]) {
        wx.showToast({ title: '没查到中文，点条目补一下', icon: 'none' });
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
  // 模型看得懂版面，能自己跳过例句和标题，比抄字再切行准得多）
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

        const images = [];
        for (let i = 0; i < files.length; i++) {
          try {
            images.push(wx.getFileSystemManager().readFileSync(files[i].tempFilePath, 'base64'));
          } catch (err) { /* 单张读不出就跳过 */ }
        }
        if (!images.length) {
          wx.hideLoading();
          self.setData({ ocrBusy: false, ocrNote: '图片读不出来，换一张试试' });
          return;
        }

        app.request('/api/vision', 'POST', { images: images }, true)
          .then((r) => {
            wx.hideLoading();
            const cands = (r.candidates || []).map((c) => ({
              word: c.word || '', def: c.def || '', checked: true
            }));
            const failed = r.failed || [];
            self.setData({
              ocrBusy: false,
              cands: cands,
              ocrNote: failed.length ? failed.join('；') : ''
            });
            if (!cands.length) {
              wx.showToast({ title: '没认出词条', icon: 'none' });
            }
          })
          .catch((e) => {
            wx.hideLoading();
            self.setData({ ocrBusy: false, ocrNote: (e && e.message) || '识别失败' });
          });
      }
    });
  },

  toggleCand(e) {
    const i = +e.currentTarget.dataset.index;
    this.setData({ ['cands[' + i + '].checked']: !this.data.cands[i].checked });
  },

  // 候选词可以直接改：OCR 认错的假名/汉字在手机上改比回电脑改省事
  editCand(e) {
    const i = +e.currentTarget.dataset.index;
    const field = e.currentTarget.dataset.field;
    this.setData({ ['cands[' + i + '].' + field]: e.detail.value });
  },

  toggleAll() {
    const allOn = this.data.cands.every((c) => c.checked);
    const cands = this.data.cands.map((c) => Object.assign({}, c, { checked: !allOn }));
    this.setData({ cands: cands });
  },

  addSelected() {
    const picked = this.data.cands
      .filter((c) => c.checked && (c.word || '').trim())
      .map((c) => ({ word: (c.word || '').trim(), def: (c.def || '').trim() }));
    if (!picked.length) { wx.showToast({ title: '先勾几个', icon: 'none' }); return; }

    // OCR 没切出中文的那些，一并让服务端查
    const need = picked.filter((p) => !p.def).map((p) => p.word);
    const self = this;
    const done = (defs) => {
      picked.forEach((p) => { if (!p.def && defs[p.word]) p.def = defs[p.word]; });
      self.setData({ looking: false });
      self.pushPending(picked);
      self.setData({ cands: [] });
    };
    if (!need.length) { done({}); return; }
    this.setData({ looking: true });
    this.lookup(need, done);
  },

  clearCands() { this.setData({ cands: [] }); },

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

  // 查中文：先档案后模型，全在服务端（保证跟复习时看到的中文是同一份）
  lookup(words, cb) {
    const self = this;
    app.request('/api/word-def', 'POST', { words: words }, true)
      .then((r) => cb(r.defs || {}))
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
