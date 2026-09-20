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

  var nin = normalizeAnswer(input);
  if (!nin) return false;
  var ninH = toHiragana(nin);

  // 无汉字的词在汉字档 / 完整档 / 任一档下没有意义，退回假名档。
  var hasKanji = forms.some(function (f) { return f.kanji !== ''; });
  if (mode !== GradeMode.kana && !hasKanji) mode = GradeMode.kana;

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
  return false;
}
