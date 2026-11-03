// 三圆手册分级：三圆(核心) > 二圆 > 一圆。
// 2026-11-02 按《标日初上 三圆词汇速记手册》给词汇总表逐个打标；
// 2026-11-03 起单词复习（练习页 / 回看）也要显示 —— 抽到这里，
// 免得词库页和练习页各写一套配色（同一件事不许画成两种样子）。
//
// 手册没收的词 circle 为空 → text 空字符串，页面 wx:if 掉，不显示。
const CIRCLE_CLS = { 三圆: 'c3', 二圆: 'c2', 一圆: 'c1' };

function splitCircle(c) {
  return { text: c || '', cls: CIRCLE_CLS[c] || '' };
}

module.exports = { splitCircle };
