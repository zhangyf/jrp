// 单词判分。
//
// ⚠️ 本文件必须与 Go 侧 grade.go 逐字保持一致 —— 这是修掉
// 「网站认汉字、Excel 只认假名」这条不一致的唯一防线。
// 改任何一个正则或分支，两边都要同步改。
//
// 词的存储约定是「假名(汉字)」：假名是主写法，汉字放括号里。
//   おんがく(音楽)      假名 + 汉字
//   ひるごはん(昼ご飯)  汉字部分本身含假名
//   はじめまして        纯假名，无汉字
//   カレー              纯片假名

var GradeMode = { kana: 'kana', kanji: 'kanji', full: 'full', either: 'either' };

function parseGradeMode(s) {
  s = (s || '').trim().toLowerCase();
  if (s === 'kanji') return GradeMode.kanji;
  if (s === 'full') return GradeMode.full;
  if (s === 'either') return GradeMode.either;
  return GradeMode.kana;
}

// 「X(Y)」拆成假名部分 / 汉字部分 / 全形。半角与全角括号都认。
var PAREN_RE = /^(.+?)[（(](.+?)[）)]$/;

function parseAnswerForms(word) {
  var out = [];
  var seen = {};
  (word || '').split('/').forEach(function (part) {
    part = part.trim();
    if (!part) return;
    var f = { kana: '', kanji: '', full: '' };
    var m = part.match(PAREN_RE);
    if (m) {
      f.kana = m[1].trim();
      f.kanji = m[2].trim();
      f.full = f.kana + f.kanji;
    } else {
      f.kana = part;
      f.full = part;
    }
    var key = f.kana + '\u0000' + f.kanji;
    if (seen[key]) return;
    seen[key] = true;
    out.push(f);
  });
  return out;
}

// 归一化：去空白、括号字符、中黑点、全角空格。
// 只删括号「字符」，不删括号「内容」—— 所以 としょかん(図書館) → としょかん図書館。
function normalizeAnswer(s) {
  return (s || '')
    .replace(/\s+/g, '')
    .replace(/[()（）・･　]/g, '')
    .trim();
}

// 输入拆成若干「候选答案」。
// 多写法的词（そう/ああ）档案里用「/」分隔，但日语 IME 打不出半角斜杠 ——
// IME 的「/」键打出来是中黑点「・」，也有人打顿号「、」。这些分隔符把输入
// 拆开逐段判，任何一段命中任一写法即算对；拆完全为空按没写处理。
// ⚠️ 与 Go 侧 inputVariants（grade.go）保持一致。
function inputVariants(input) {
  var out = (input || '').split(/[\/／・･、]/)
    .map(normalizeAnswer)
    .filter(function (n) { return n; });
  // 兜底：万一词本身含中黑点（「あい・うえ」类复合词），老逻辑靠
  // 「删点整串比」通过 —— 整串归一化结果也放进候选，别让拆段把它挤掉。
  var whole = normalizeAnswer(input);
  if (whole) out.push(whole);
  return out;
}

// 片假名 → 平假名，用于假名档放宽比对。
function toHiragana(s) {
  return (s || '').replace(/[\u30A1-\u30F6]/g, function (c) {
    return String.fromCharCode(c.charCodeAt(0) - 0x60);
  });
}

// 假名方式的比对：写假名（片假名折叠后也算）或照抄全形都算对。与 Go matchKanaWay 一致。
function matchKanaWay(f, nin, ninH) {
  if (normalizeAnswer(f.kana) === nin) return true;
  if (toHiragana(normalizeAnswer(f.kana)) === ninH) return true;
  return f.kanji !== '' && normalizeAnswer(f.full) === nin;
}

// 汉字方式的比对：写汉字或照抄全形都算对。与 Go matchKanjiWay 一致。
function matchKanjiWay(f, nin) {
  return f.kanji !== '' &&
    (normalizeAnswer(f.kanji) === nin || normalizeAnswer(f.full) === nin);
}

// gradeAnswer 判定手写答案是否正确。空输入一律判错。
function gradeAnswer(input, word, mode) {
  var forms = parseAnswerForms(word);
  if (!forms.length) return false;
  var variants = inputVariants(input);
  if (!variants.length) return false;

  // 无汉字的词在汉字档 / 完整档 / 任一档下没有意义，退回假名档。
  var hasKanji = forms.some(function (f) { return f.kanji !== ''; });
  if (mode !== GradeMode.kana && !hasKanji) mode = GradeMode.kana;

  // 输入的每一段（可能由「・」「、」等分隔出多段）逐个跟全部写法比。
  for (var v = 0; v < variants.length; v++) {
    var nin = variants[v];
    var ninH = toHiragana(nin);
    for (var i = 0; i < forms.length; i++) {
      var f = forms[i];
      if (mode === GradeMode.kanji) {
        if (matchKanjiWay(f, nin)) return true;
      } else if (mode === GradeMode.full) {
        if (f.kanji && normalizeAnswer(f.full) === nin) return true;
      } else if (mode === GradeMode.either) {
        // 任一档：假名或汉字，写哪个都算对
        if (matchKanaWay(f, nin, ninH) || matchKanjiWay(f, nin)) return true;
      } else {
        if (matchKanaWay(f, nin, ninH)) return true;
      }
    }
  }
  return false;
}
