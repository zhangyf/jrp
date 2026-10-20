// 活用练习：给原词，写出活用形。
//
// ⚠️ 只练不计档 —— 不调 /api/record，不写档案、不进统计（跟网页端一致）。
// 判分复用 /api/grade：把服务端给的多个写法用「/」连起来当目标词，
// 写任意一个就算对（如 じゃありません/ではありません）。

const app = getApp();

const TYPES = [
  { key: 'verb', label: '动词' },
  { key: 'verb_nai', label: '动词ない形' },
  { key: 'adj', label: '形容词' }
];

// 动词活用速查表：把 て形／ます形／ない形 三张表合并成一张。
// 三个表共用「一类（五段）/二类（一段）/サ变/カ变」骨架，
// 一类动词的 ます形・ない形四行规则相同，模仿原表的合并单元格——
// contM/contN 为 true 的格子是上行规则的延续，只留底色不写字。
const GRAMMAR_ROWS = [
  { k: 'a1', t: '一类（五段）', tail: 'う・つ・る', te: 'って（促音便）', masu: '词尾う段→い段＋ます', nai: '词尾う段→あ段＋ない＊' },
  { k: 'a2', t: '', tail: 'ぬ・ぶ・む', te: 'んで（拨音便）', masu: '', nai: '', contM: true, contN: true },
  { k: 'a3', t: '', tail: 'く・ぐ', te: 'いて／いで（イ音便）', masu: '', nai: '', contM: true, contN: true },
  { k: 'a4', t: '', tail: 'す', te: 'して', masu: '', nai: '', contM: true, contN: true },
  { k: 'b1', t: '二类（一段）', tail: '見る 等', te: '去词尾る＋て', masu: '去词尾る＋ます', nai: '去词尾る＋ない' },
  { k: 'c1', t: '三类 サ变', tail: 'する', te: 'して', masu: 'します', nai: 'しない' },
  { k: 'c2', t: '三类 カ变', tail: '来る', te: 'きて', masu: 'きます', nai: 'こない' }
];

// 注意事项 + 例词：每条一行（<text> 逐条渲染）。行首别用空格对齐——
// text 会合并连续空格，统一用「例・」前缀。
const GRAMMAR_NOTES = [
  '＊う结尾的动词，ない形变「わ」不变「あ」：買う → 買わない（✗買あない）',
  '例・買う → 買って／買います／買わない',
  '例・飲む → 飲んで／飲みます／飲まない',
  '例・書く → 書いて／書きます／書かない',
  '例・話す → 話して／話します／話さない',
  '例・見る → 見て／見ます／見ない｜する → して／します／しない｜来る → きて／きます／こない'
];

Page({
  data: {
    types: TYPES,
    type: 'verb',
    count: 10,
    items: [],
    loading: true,
    err: '',
    graded: false,
    pool: 0,
    // 默认收起（老师 2026-10-20 要求）：点标题才展开，别一进页就挡住题
    grammarOpen: false,
    grammar: GRAMMAR_ROWS,
    grammarNotes: GRAMMAR_NOTES
  },

  onLoad() {
    this.load();
  },

  toggleGrammar() {
    this.setData({ grammarOpen: !this.data.grammarOpen });
  },

  onType(e) {
    this.setData({ type: e.currentTarget.dataset.type }, () => this.load());
  },

  load() {
    this.setData({ loading: true, err: '', graded: false });
    app.request('/api/conjugation?type=' + this.data.type + '&count=' + this.data.count,
      'GET', {}, true)
      .then((res) => {
        const items = (res.items || []).map((it, i) => ({
          idx: i,
          word: it.word || '',
          definition: it.definition || '',
          pos: it.pos || '',
          sub: it.sub || '',
          tasks: (it.tasks || []).map((t) => ({
            label: t.label || '',
            answers: t.answers || [],
            // expected 喂给 /api/grade 当目标词（多写法用 / 分隔，写任意一个算对）；
            // expectedText 只是给页面显示用 —— WXML 里不能直接调 .join()，得在这拼好。
            expected: (t.answers || []).join('/'),
            expectedText: (t.answers || []).join(' / '),
            input: '',
            correct: false
          }))
        }));
        this.setData({ items, pool: res.pool || 0, loading: false });
      })
      .catch((e) => this.setData({ err: e.message, loading: false }));
  },

  onInput(e) {
    const i = e.currentTarget.dataset.index;
    const t = e.currentTarget.dataset.task;
    const patch = {};
    patch['items[' + i + '].tasks[' + t + '].input'] = e.detail.value;
    this.setData(patch);
  },

  grade() {
    const items = this.data.items;
    const payload = { mode: 'either', items: [] };
    items.forEach((it) => {
      it.tasks.forEach((t) => {
        payload.items.push({ word: t.expected, answer: t.input });
      });
    });
    if (!payload.items.length) {
      wx.showToast({ title: '还没有题', icon: 'none' });
      return;
    }
    wx.showLoading({ title: '判分中' });
    app.request('/api/grade', 'POST', payload, true)
      .then((res) => {
        wx.hideLoading();
        const rs = res.results || [];
        let k = 0;
        const patch = {};
        items.forEach((it, i) => {
          it.tasks.forEach((t, j) => {
            patch['items[' + i + '].tasks[' + j + '].correct'] = !!rs[k];
            k += 1;
          });
        });
        patch.graded = true;
        this.setData(patch);
      })
      .catch((e) => {
        wx.hideLoading();
        wx.showToast({ title: e.message, icon: 'none' });
      });
  }
});
