// 造句：看中文写整句。
//
// 判句走 /api/judge-sentence（服务端：字面一致直接判对，字面不一致才问模型）。
// 模型判一句要几秒，所以「对答案」是**并行**发所有已写的句子，不是一句句排队。

const app = getApp();

// 语法知识卡（老师 2026-10-20 给的两张表，默认收起、点标题展开）：
// ① 助词用法（に/で/へ/と/から・まで/を） ② 词语修饰（名词・动词・形容词互接）。
// 例句括号里的课号是标日初级上册的出处。contC: true 的格子是上行词类的延续格
// （模仿原表 rowspan 合并，只留底色不写字）。
const PARTICLE_ROWS = [
  {
    p: 'に',
    uses: '时间 / 存在的场所 / 行为的对象 / 移动的目的 / 附着点 / 移动的目的地',
    ex: [
      '森さんは7時に起きます。（第5课）',
      '部屋に机といすがあります。（第4课）',
      '私は小野さんにお土産をあげます。（第8课）',
      '荷物を出しに行きます。（第13课）',
      '李さんは電車に乗りました。（第15课）',
      '李さんは病院に行きました。（第15课）'
    ]
  },
  {
    p: 'で',
    uses: '手段、方法 / 工具、原材料 / 动作发生的场所 / 范围 / 基准 / 主体样态',
    ex: [
      '上海まで飛行機で行きます。（第6课）',
      '新聞紙で紙飛行機を作りました。（第8课）',
      '李さんは図書館で勉強します。（第7课）',
      '日本で一番高い山は富士山です。（第12课）',
      'はがきは5枚で500円です。（第13课）',
      '一人でご飯を食べます。（第20课）'
    ]
  },
  { p: 'へ', uses: '移动的方向', ex: ['吉田さんは来月中国へ行きます。（第6课）'] },
  { p: 'と', uses: '共同参与者', ex: ['小野さんは友達と帰りました。（第6课）'] },
  { p: 'から・まで', uses: '时间 / 空间的起点 · 终点', ex: ['東京から広島まで新幹線で行きます。（第5/6课）'] },
  {
    p: 'を',
    uses: '动作的对象 / 经过、离开的地点',
    ex: ['李さんは毎日コーヒーを飲みます。（第7课）', '李さんは毎日7時に家を出ます。']
  }
];

const MODIFY_ROWS = [
  { k: 'm1', cls: '名词', joint: 'N ＋ で ＋ N', ex: '会社員で 部長です（第16课）' },
  { k: 'm2', cls: '', joint: 'N ＋ に ＋ V', ex: '医者に なりました（第18课）', contC: true },
  { k: 'm3', cls: '动词', joint: 'Vて ＋ V', ex: '帰って 勉強します（第14课）' },
  { k: 'm4', cls: '形容词', joint: 'Aい ＋ N', ex: '辛い 料理（第9课）' },
  { k: 'm5', cls: '', joint: 'ANな ＋ N', ex: '綺麗な 町（第10课）', contC: true },
  { k: 'm6', cls: '', joint: 'Aく ＋ V', ex: '大きく なります（第18课）', contC: true },
  { k: 'm7', cls: '', joint: 'ANに ＋ V', ex: '綺麗に します（第18课）', contC: true },
  { k: 'm8', cls: '', joint: 'Aく＋て＋Aい/AN', ex: '安くて おいしい（第16课）', contC: true },
  { k: 'm9', cls: '', joint: 'ANで＋Aい/AN', ex: '静かで 広い（第16课）', contC: true }
];

Page({
  data: {
    date: '',
    items: [],
    loading: true,
    err: '',
    grading: false,
    submitting: false,
    grammarOpen: false,
    particles: PARTICLE_ROWS,
    modify: MODIFY_ROWS
  },

  toggleGrammar() {
    this.setData({ grammarOpen: !this.data.grammarOpen });
  },

  onLoad() {
    this.load();
  },

  load() {
    this.setData({ loading: true, err: '' });
    // 不传 date，同列表复习：日期由服务器定，避免跨时区的只读分支。
    app.request('/api/plan?mode=sentences', 'GET', {}, true)
      .then((res) => {
        const list = res.sentences || [];
        const items = list.map((s, i) => ({
          number: s.number || i + 1,
          chinese: s.chinese || '',
          answer: s.answer || '', // 原句，提交时用来定位
          input: '',
          correct: false,
          graded: false,
          reason: ''
        }));
        this.setData({ items, date: res.date || '', loading: false });
      })
      .catch((e) => this.setData({ err: e.message, loading: false }));
  },

  onInput(e) {
    const i = e.currentTarget.dataset.index;
    const patch = {};
    patch['items[' + i + '].input'] = e.detail.value;
    patch['items[' + i + '].graded'] = false;
    this.setData(patch);
  },

  grade() {
    const items = this.data.items;
    const targets = [];
    items.forEach((it, i) => {
      if ((it.input || '').trim() !== '') targets.push(i);
    });
    if (!targets.length) {
      wx.showToast({ title: '还没写句子', icon: 'none' });
      return Promise.resolve();
    }

    this.setData({ grading: true });
    wx.showLoading({ title: '判 ' + targets.length + ' 句' });

    return Promise.all(targets.map((i) => {
      const it = items[i];
      return app.request('/api/judge-sentence', 'POST', {
        answer: it.answer,
        chinese: it.chinese,
        input: it.input
      }, true).then((res) => ({ i, res }))
        .catch((e) => ({ i, res: { correct: false, reason: e.message } }));
    })).then((outs) => {
      wx.hideLoading();
      const patch = {};
      outs.forEach((o) => {
        patch['items[' + o.i + '].graded'] = true;
        patch['items[' + o.i + '].correct'] = !!o.res.correct;
        patch['items[' + o.i + '].reason'] = o.res.reason || '';
      });
      patch.grading = false;
      this.setData(patch);
    });
  },

  submit() {
    const items = this.data.items;
    const written = items.filter((it) => (it.input || '').trim() !== '');
    if (!written.length) {
      wx.showToast({ title: '还没写句子', icon: 'none' });
      return;
    }
    // 写了没判的就先判一遍再落库 —— 跟列表复习同一条红线。
    if (written.some((it) => !it.graded)) {
      this.grade().then(() => this.doSubmit()).catch(() => {});
      return;
    }
    this.doSubmit();
  },

  doSubmit() {
    const done = this.data.items.filter((it) => it.graded);
    if (!done.length) {
      wx.showToast({ title: '先对答案再提交', icon: 'none' });
      return;
    }
    this.setData({ submitting: true });
    app.request('/api/record', 'POST', {
      plan_date: this.data.date,
      language: 'ja',
      sentence_results: done.map((it) => ({
        number: it.number,
        correct: it.correct,
        answer: it.answer,
        chinese: it.chinese
      }))
    }, true)
      .then(() => {
        this.setData({ submitting: false });
        const ok = done.filter((it) => it.correct).length;
        wx.showModal({
          title: '已提交 ' + done.length + ' 句',
          content: '对 ' + ok + ' / 错 ' + (done.length - ok),
          showCancel: false,
          success: () => this.load()
        });
      })
      .catch((e) => {
        this.setData({ submitting: false });
        wx.showToast({ title: e.message, icon: 'none' });
      });
  }
});
