// 词性符号化（2026-11-03 老师要的：别在界面上写「名词」「动词·一类」这种中文，
// 用 N / V1 / Aい 这类符号，扫一眼更快）。
//
// ⚠️ 这是**穷举表**，不是「匹配不上就原文兜底」—— 词性表现在一共 25 种取值，
// 每个都有确定符号。跟 web/app.js 的 POS_SYMBOL 是同一份口径，改一边要改另一边。
//
//   名/名词 → N      专/专有名词/专有名词·人名/地名 → N.pr
//   代词 → Pron      疑问词 → Q       副词/副 → Adv     助词 → P
//   连体词 → Adn     连词 → Conj      感叹词/叹 → Int   寒暄语 → Expr
//   表达 → Phr       慣用 → Idm       未标注 → —
//   动词：一类 V1（五段）/ 二类 V2（一段）/ 三类 V3（カ変・サ変）
//   形容词：い形 Aい / な形 Aな
//
// 词性表混着两套写法（老数据「动/1」、新数据「动词/一类」），两边都认。
// 中文没删：进词汇总表的详情行（列表里只显示符号）。

const POS_ALIAS = { 动: '动词', 形: '形容词', 名: '名词', 副: '副词' };

const POS_SYMBOL = {
  名词: 'N', 名: 'N',
  专有名词: 'N.pr', 专: 'N.pr',
  代词: 'Pron', 疑问词: 'Q',
  副词: 'Adv', 副: 'Adv',
  助词: 'P', 连体词: 'Adn', 连词: 'Conj',
  感叹词: 'Int', 叹: 'Int',
  寒暄语: 'Expr', 表达: 'Phr', 慣用: 'Idm',
  未标注: '—'
};

const VERB_SYMBOL = { 一类: 'V1', 1: 'V1', 二类: 'V2', 2: 'V2', 三类: 'V3', 3: 'V3' };
const ADJ_SYMBOL = { い形: 'Aい', 1: 'Aい', な形: 'Aな', 2: 'Aな' };

// 大类归一：老写法（动/形/名/副）跟新写法（动词/形容词…）归到同一把钥匙
function posKey(it) {
  const p = String((it && it.pos) || '').trim();
  return POS_ALIAS[p] || p;
}

// 符号。hideSub=true 时动词只给 V、形容词只给 A ——
// 活用专项答题前露小类等于把「て形怎么变」直接说了。
function posSymbol(it, hideSub) {
  const k = posKey(it);
  if (!k) return '';
  if (k === '动词') {
    if (hideSub) return 'V';
    return VERB_SYMBOL[String((it && it.sub) || '').trim()] || 'V';
  }
  if (k === '形容词') {
    if (hideSub) return 'A';
    return ADJ_SYMBOL[String((it && it.sub) || '').trim()] || 'A';
  }
  return POS_SYMBOL[k] || k; // 表里没有的（新词性）原样显示，别吞掉
}

// 中文全称，给详情行 / 悬停用
function posText(it, hideSub) {
  if (!it) return '';
  let t = posKey(it) || String(it.pos || '');
  if (it.sub && !hideSub) t += '·' + it.sub;
  return t;
}

module.exports = { posSymbol, posText };
