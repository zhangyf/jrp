// 三圆手册分级：三圆(核心) > 二圆 > 一圆。
// 2026-11-02 按《标日初上 三圆词汇速记手册》给词汇总表逐个打标；
// 2026-11-03 起单词复习（练习页 / 回看 / 钉子户）也要显示 —— 抽到这里，
// 免得词库页和练习页各写一套画法（同一件事不许画成两种样子）。
//
// 老师 2026-11-03 定的画法：**画红圈不写字** —— 三圆 3 个圈、二圆 2 个、一圆 1 个。
// dots 是给 WXML 的 wx:for 用的（[1,2,3]），长度就是圈的个数；
// 文字只留在词汇总表的详情行里（列表里扫个数比读文字快）。
// 手册没收的词 circle 为空 → dots 空数组，页面 wx:if 掉，不显示。
const CIRCLE_DOTS = { 三圆: 3, 二圆: 2, 一圆: 1 };

function splitCircle(c) {
  const n = CIRCLE_DOTS[c] || 0;
  const dots = [];
  for (let i = 1; i <= n; i++) dots.push(i);
  return { text: c || '', dots, count: n };
}

module.exports = { splitCircle };
