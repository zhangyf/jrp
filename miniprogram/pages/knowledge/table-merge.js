// 知识点页表格：把 md 里的「延续空格子」推断成真合并单元格（rowspan）。
//
// 背景：服务端 ParseMarkdown 的 parseTable 只按 | 切格子，空格子原样保留成 ""，
// 没有 rowspan 概念（见 internal/knowledge/markdown.go）。md 里的约定是
// 「空格子 = 同上」——一组连续同类项只在首行写值，后面的行留空，不重复文字。
//
// 这里把这份数据算成 CSS Grid 的显式坐标，交给 WXML 平铺渲染：
//   - 非空且下方是连续空格子 → 向下合并 span 行（真合并大格）
//   - 被上方合并大格占住的位置 → 不再单独输出格子
//   - 表头固定在第 1 行
//   - 边界：若某组第一行就是空格子（没有「上文」可延续），上方没有可合并的
//     文本，就老老实实输出一个空盒，不硬凑合并。这是无效 md 的降级行为。
// ⚠️ 每个格子都给显式 grid-column / grid-row，不用 grid 的 auto-placement：
//    存在跨行格子时 auto-flow 会留洞，把后面的格子挤到错的列上。
// ⚠️ 列宽模板也在这里拼好：小程序 WXML 表达式不支持数组 join，
//    部分机型对 CSS repeat()/minmax()/calc() 也不稳，一列一个定宽最保险。
//
// 纯函数，不碰任何小程序 API，Node 里可直接 require 做单测。

function mergeCells(block) {
  const header = block.header || [];
  const rows = block.rows || [];
  const cols = header.length;
  const n = rows.length;
  if (!cols) return { cells: [], cols: 0, gridCols: '' };

  // occupied[r][c]：该位置已被上方某个合并大格占住，不再单独渲染。
  const occupied = [];
  for (let r = 0; r < n; r++) {
    const line = [];
    for (let c = 0; c < cols; c++) line.push(false);
    occupied.push(line);
  }

  const cells = [];
  // 表头：第 1 行，每格占 1 行。
  for (let c = 0; c < cols; c++) {
    cells.push({
      k: 'h' + c,
      t: header[c] || '',
      head: true,
      merged: false,
      style: 'grid-column: ' + (c + 1) + '; grid-row: 1 / span 1;'
    });
  }

  for (let r = 0; r < n; r++) {
    for (let c = 0; c < cols; c++) {
      if (occupied[r][c]) continue;
      const text = (rows[r] || [])[c] || '';
      let span = 1;
      // 只有非空文本才当合并起点；向下连续的空格子归它。
      if (text !== '') {
        while (r + span < n && ((rows[r + span] || [])[c] || '') === ''
               && !occupied[r + span][c]) {
          occupied[r + span][c] = true;
          span++;
        }
      }
      cells.push({
        k: 'r' + r + 'c' + c,
        t: text,
        head: false,
        merged: span > 1,
        style: 'grid-column: ' + (c + 1) + '; grid-row: ' + (r + 2) +
               ' / span ' + span + ';'
      });
    }
  }

  let gridCols = '';
  for (let c = 0; c < cols; c++) gridCols += (c ? ' ' : '') + '200rpx';
  return { cells: cells, cols: cols, gridCols: gridCols };
}

module.exports = { mergeCells: mergeCells };
