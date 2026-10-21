// 录入新词：手打 或 拍照/相册取词。
//
// 两种入口最后汇到同一个「待录列表」，确认后才 POST /api/words ——
// 中间这层是故意的：OCR 认出来的东西不能闭着眼写进档案，老师得先看过、能改、能删。
//
// 加词的实现在服务端 words_add.go，跟 CLI 的 add-words 是同一份：
// 重复词会跳过，中文释义撞车会在结果里点名（不阻断）。

const app = getApp();

// 课按数字排：「第10课」要在「第2课」后面
function lessonNo(s) {
  const m = /(\d+)/.exec(s || '');
  return m ? +m[1] : 9999;
}

Page({
  data: {
    groups: ['未分组'],
    groupIndex: 0,

    word: '',
    def: '',

    pending: [], // 待录入：{ word, def }
    cands: [],   // OCR 候选：{ word, def, raw, checked }

    ocrBusy: false,
    ocrNote: '',
    saving: false,
    result: null // { added, duplicates, dup_definitions[], dup_core_definitions[] }
  },

  onLoad() {
    if (!app.globalData.logged) {
      app.login(() => this.boot());
    } else {
      this.boot();
    }
  },

  boot() {
    // 课的下拉复用 /api/lexicon 的 groups（跟词汇总表同一份，不会多出口径）
    app.request('/api/lexicon', 'GET', {}, true)
      .then((res) => {
        const gs = (res.groups || []).slice().sort((a, b) => lessonNo(a) - lessonNo(b));
        this.setData({ groups: ['未分组'].concat(gs) });
      })
      .catch(() => { /* 拿不到就用「未分组」，不挡录入 */ });
  },

  onGroup(e) { this.setData({ groupIndex: +e.detail.value }); },
  onWord(e) { this.setData({ word: e.detail.value }); },
  onDef(e) { this.setData({ def: e.detail.value }); },

  addManual() {
    const word = (this.data.word || '').trim();
    const def = (this.data.def || '').trim();
    if (!word) { wx.showToast({ title: '先写词形', icon: 'none' }); return; }
    if (!def) { wx.showToast({ title: '写一下中文意思', icon: 'none' }); return; }
    this.setData({
      pending: this.data.pending.concat([{ word: word, def: def }]),
      word: '', def: ''
    });
  },

  removePending(e) {
    const i = +e.currentTarget.dataset.index;
    const pending = this.data.pending.slice();
    pending.splice(i, 1);
    this.setData({ pending: pending });
  },

  // 拍照 / 相册：一次最多 9 张，服务端逐张 OCR，认出来的行切成候选词
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

        app.request('/api/ocr', 'POST', { images: images }, true)
          .then((r) => {
            wx.hideLoading();
            const cands = (r.candidates || []).map((c) => ({
              word: c.word || '', def: c.def || '', raw: c.raw || '', checked: true
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

    const seen = {};
    this.data.pending.forEach((p) => { seen[p.word] = 1; });
    const merged = this.data.pending.slice();
    picked.forEach((p) => { if (!seen[p.word]) { seen[p.word] = 1; merged.push(p); } });

    this.setData({ pending: merged, cands: [] });
    wx.showToast({ title: '已加入 ' + merged.length + ' 条', icon: 'none' });
  },

  clearCands() { this.setData({ cands: [] }); },

  submit() {
    const n = this.data.pending.length;
    if (!n) { wx.showToast({ title: '还没有要录的词', icon: 'none' }); return; }
    const group = this.data.groups[this.data.groupIndex] || '未分组';
    const self = this;
    wx.showModal({
      title: '录入 ' + n + ' 个词',
      content: '加进「' + group + '」。录完会生成一份新档案，旧的还在。',
      success(r) { if (r.confirm) self.doSubmit(group); }
    });
  },

  doSubmit(group) {
    const self = this;
    const items = this.data.pending.map((p) => ({ word: p.word, def: p.def }));
    this.setData({ saving: true });
    app.request('/api/words', 'POST', { group: group, items: items }, true)
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
