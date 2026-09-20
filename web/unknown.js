// 「不会」= 答错。
//
// 老铁律「空白 = 没写 = 不判分、不进提交、不回写档案」把「没练到」和「练了但不会」
// 混成了一回事。现在加显式「不会」标记：老师主动点，该词/句按答错处理
// （走 RecordWrong，跟写错完全一样）。
//
// 后端零改动：提交给 /api/record 的仍然是 { number, correct: false }，
// 不新增档案状态、不改档案格式、changelog 里并入「写错」计数。
//
// 三条不变式（改这个文件前先读懂）：
//   1. unknown 为真 -> correct 恒为 false 且 manual 为真。
//      manual 让 list.js 的「if (!it.manual) 才重判」天然跳过它，
//      所以切判分档位时「不会」不会被重判冲掉。
//   2. unknown 与 blank 互斥 —— 否则一行既灰化又判错，渲染自相矛盾。
//   3. unknown 的行不渲染「算对」。主动认输了就没有改判可言 ——
//      「算对」是给「判错了但其实是对的」（写了汉字在假名档被判错）用的。

var UNK = {
  // 标记 / 取消标记。it 需要有 unknown / correct / manual 三个字段。
  set: function (it, on) {
    if (on) {
      it.unknown = true;
      it.correct = false;
      it.manual = true;
      it.blank = false;
    } else {
      it.unknown = false;
      it.manual = false;
      it.correct = null;
    }
  },

  // 四态统计。unknown 优先，其次 correct，剩下算「没写」。
  count: function (items) {
    var c = { ok: 0, no: 0, unk: 0, blank: 0 };
    (items || []).forEach(function (it) {
      if (it.unknown) { c.unk++; return; }
      if (it.correct === true) { c.ok++; return; }
      if (it.correct === false) { c.no++; return; }
      c.blank++;
    });
    return c;
  },

  // 「对 X　错 Y　不会 Z　没写 W（没写的不计入档案）」
  line: function (items) {
    var c = UNK.count(items);
    return '对 ' + c.ok + '　错 ' + c.no + '　不会 ' + c.unk + '　没写 ' + c.blank +
      (c.blank ? '（没写的不计入档案）' : '');
  },

  // 既没写也没标「不会」的行数 —— 提交前确认弹窗的触发条件。
  pending: function (items) {
    var n = 0;
    (items || []).forEach(function (it) {
      if (!it.unknown && !String(it.input || '').trim()) n++;
    });
    return n;
  },

  // 提交前确认弹窗。不用 confirm()：它只有确定/取消，按钮文案改不了，老师会点错。
  // opts: { title, lines[], skipText, allText, onSkip, onMarkAll }
  ask: function (o) {
    UNK.close();
    var mask = document.createElement('div');
    mask.className = 'modal-mask';
    mask.innerHTML =
      '<div class="modal">' +
      '<h4>' + esc(o.title) + '</h4>' +
      '<p class="muted">' + (o.lines || []).map(function (s) { return esc(s); }).join('<br>') + '</p>' +
      '<div class="row">' +
      '<button class="ghost" data-act="skip">' + esc(o.skipText) + '</button>' +
      '<button class="primary" data-act="all">' + esc(o.allText) + '</button>' +
      '</div></div>';
    document.body.appendChild(mask);
    UNK._mask = mask;
    mask.querySelector('[data-act=skip]').addEventListener('click', function () {
      UNK.close();
      o.onSkip();
    });
    mask.querySelector('[data-act=all]').addEventListener('click', function () {
      UNK.close();
      o.onMarkAll();
    });
  },

  close: function () {
    if (UNK._mask) {
      UNK._mask.parentNode.removeChild(UNK._mask);
      UNK._mask = null;
    }
  },

  _mask: null
};
