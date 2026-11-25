// 听力缓存：字幕进本地存储（setStorageSync），视频文件进用户目录（saveFile）。
//
// 2026-11-24 老师要求同时缓存字幕和视频文件，地铁/反复精听不用每次都走网络。
// 2026-11-25 追加「指纹比对」：COS 上同名文件被覆盖（重传视频 / 字幕精校回写）
// 后，本地缓存必须自动失效，绝不能一直播老版本。
//
// 服务端 /api/listening 下发两个指纹：
//   - video_fp = ETag + Size + LastModified（视频几十 MB，不能拉内容算 hash）；
//   - srt_fp   = SRT 内容 sha256 前 16 位（字幕已在服务端内存里，算内容最准）。
// 缓存记录里存 fp，读的时候对不上就删掉当没缓存，由调用方重新拉新的。
//
// ⛔ 三条硬规矩：
//   ① 服务端没下发指纹（老版本服务端 / 字幕缺失）→ 一律不启用缓存：
//      不读、不写、按钮隐藏。宁可每次走网络，也绝不播旧内容。
//   ② 升级前存的缓存条目没有 fp 字段 → 对不上，第一次打开自动失效重下。
//   ③ 播放 URL 带 ?v=<video_fp>，指纹变了 URL 也变，绕开 EO 边缘的旧对象。
//
// 两类存储，配额和淘汰策略不一样，别混：
//   1. 字幕（几十 KB 一条）→ setStorageSync。微信单 key 上限 1MB、总量 10MB，
//      超了直接写失败、不自动淘汰。这里自己控：按 key 顺序 + 条数上限，
//      超过 MAX_CUE 条就删最旧的一条（存了时间戳，按 oldest 顺序删）。
//      字幕很小，MAX_CUE=120 也就几 MB，够用，用不上真正的 LRU。
//
//   2. 视频文件（几 MB~几十 MB）→ wx.downloadFile 落 tmp，再 FileSystemManager
//      .saveFile 存到持久用户目录。微信给本地文件的配额默认 200MB，满了
//      saveFile 直接报错，微信不帮你删。这里实现一个薄 LRU：
//        - 一张索引表 setStorageSync 存 {key, path, size, lastUsed, fp} 数组；
//        - 每次播放命中就刷新 lastUsed（move to front）；
//        - 缓存新视频前，若 总size + 新文件 > MAX_VIDEO_BYTES，按 lastUsed
//          从旧到新 unlink + 删索引，腾够了再存。
//
// ⚠️ 两个坑，写代码时都绕着走：
//   - 文件名含日文和空格（如「第25課 応用课文.mp4」），做 key 前必须先归一化：
//     统一用 encodeURIComponent(level) + '/' + encodeURIComponent(file) 当缓存 key，
//     跟 /api/listening 的查询串完全一致，不会撞 key。
//   - saveFile 之后路径会变（tmp 路径重启/换会话就失效），必须存 saveFile 返回的
//     savedFilePath，不能自己拿 tmp path 拼。索引里的 path 一律是 savedFilePath。

const MAX_CUE = 120;          // 字幕缓存条数上限（超出删最旧，字幕小，够用）
const MAX_VIDEO_BYTES = 180 * 1024 * 1024; // 视频总配额，留 20MB 余量给系统

const CUE_PREFIX = 'jrp_listen_cue|';
const VID_INDEX_KEY = 'jrp_listen_videos';

// 失败原因：原来 fail() 把 errMsg 全吞了，真机上只看到「缓存失败」四个字，
// 分不清是合法域名没配、下载超时、还是 saveFile 配额满。这里记一句短原因给
// 标题栏 toast，完整 errMsg 打 console（真机调试一眼能定位）。
let lastError = '';
// 最近一次失败原因，成功路径不清。⚠️ 自动后台缓存（openVideo）和手动缓存
// （onCacheTap）会并发跑在两条 Promise 上，而 lastError 是模块级单例：
// 后完成的成功会 lastError='' 把先失败的原因冲掉，页面读到的就是空串，
// 只剩「缓存失败」四个字。页面 toast 优先读这份不会被成功覆盖的快照。
let lastFailure = '';
// 完整报错原文（errMsg），用来判断是不是空间类错误、决定要不要 LRU 重试。
let lastDetail = '';
// 正在落盘的目标路径：清孤儿文件时要跳过，别把并发中刚写好的文件删掉。
const pendingDests = {};
const MAX_EVICT_RETRY = 3;

function setError(short, detail) {
  lastError = short;
  lastFailure = short;
  lastDetail = String(detail || short || '');
  try { console.error('[listen-cache] ' + short, detail || ''); } catch (e) {}
}

function getLastError() { return lastError; }
function getLastFailure() { return lastFailure; }

function notify(msg) {
  try { wx.showToast({ title: msg, icon: 'none', duration: 2000 }); } catch (e) {}
}

function isSpaceError(msg) {
  return /maximum size|storage limit|exceeded|quota|space|not enough/i.test(String(msg || ''));
}

function shortDownloadError(e) {
  const m = (e && e.errMsg) || '';
  if (/domain/i.test(m)) return '域名未加白名单';
  if (/timeout/i.test(m)) return '下载超时';
  return '下载失败';
}

// 包住异步回调里的同步逻辑：抛异常也要留下原因并结束 Promise。
// 不这么做的话 Promise 永远悬着（按钮一直「下载中…」），或 lastError 还是空的，
// 页面只剩「缓存失败」四个字，真机上完全分不清是哪一步坏了。
function guard(resolve, fn) {
  try { fn(); } catch (e) {
    setError('本地处理异常', (e && (e.errMsg || e.message)) || String(e));
    resolve(null);
  }
}

// 空指纹 = 不启用缓存。绝不能拿 undefined 去跟缓存里的字符串比。
function fpOf(fp) {
  return typeof fp === 'string' && fp ? fp : '';
}

function cueKey(level, file) {
  return CUE_PREFIX + encodeURIComponent(level || '') + '|' + encodeURIComponent(file || '');
}

function videoKey(level, file) {
  return encodeURIComponent(level || '') + '/' + encodeURIComponent(file || '');
}

// —— 字幕缓存 ——

// 读字幕缓存；没有、版本对不上或记录坏了都返回 null（并删掉旧记录）。
// fp 为空一律返回 null —— 老服务端没指纹时不能赌缓存是新的。
function getCues(level, file, fp) {
  fp = fpOf(fp);
  if (!fp) return null;
  const key = cueKey(level, file);
  try {
    const raw = wx.getStorageSync(key);
    if (!raw) return null;
    const rec = JSON.parse(raw);
    if (rec.fp !== fp || !Array.isArray(rec.cues) || typeof rec.stored !== 'number') {
      wx.removeStorageSync(key);
      return null;
    }
    return { cues: rec.cues, note: rec.note || '' };
  } catch (e) { return null; }
}

// 写字幕缓存，超出上限删最旧。失败静默（下次走网络拉，不影响播放）。
function setCues(level, file, cues, note, fp) {
  fp = fpOf(fp);
  if (!fp) return;
  try {
    const rec = JSON.stringify({
      cues: cues || [], note: note || '', stored: Date.now(), fp: fp
    });
    wx.setStorageSync(cueKey(level, file), rec);
    trimCues();
  } catch (e) { /* 写失败不吵用户 */ }
}

// 字幕缓存逐出：按写入顺序删最旧，直到回到上限内。
// 所有 cue key 靠 CUE_PREFIX 前缀识别，拿 wx.getStorageInfoSync 遍历。
function trimCues() {
  try {
    const keys = (wx.getStorageInfoSync().keys || []).filter((k) => k.indexOf(CUE_PREFIX) === 0);
    if (keys.length <= MAX_CUE) return;
    // 取每条的时间戳（没有则视作最旧），按时间升序删掉超出部分
    const withTs = keys.map((k) => {
      let ts = 0;
      try {
        const rec = JSON.parse(wx.getStorageSync(k) || '{}');
        if (typeof rec.stored === 'number') ts = rec.stored;
      } catch (e) { /* 损坏的优先删 */ }
      return { k, ts };
    });
    withTs.sort((a, b) => a.ts - b.ts);
    const drop = withTs.slice(0, keys.length - MAX_CUE);
    drop.forEach((it) => { try { wx.removeStorageSync(it.k); } catch (e) {} });
  } catch (e) { /* 逐出失败不阻塞 */ }
}

// —— 视频文件缓存 ——

function readIndex() {
  try {
    const arr = wx.getStorageSync(VID_INDEX_KEY);
    return Array.isArray(arr) ? arr : [];
  } catch (e) { return []; }
}

function writeIndex(arr) {
  try { wx.setStorageSync(VID_INDEX_KEY, arr); } catch (e) { /* 写失败不阻塞播放 */ }
}

// 删索引里的某条，连同本地文件。文件可能已被系统清掉，失败不抛。
function dropEntry(arr, idx) {
  try { wx.getFileSystemManager().unlinkSync(arr[idx].path); } catch (e) { /* 文件已没 */ }
  arr.splice(idx, 1);
}

// 命中返回本地路径，未命中返回 null。命中同时刷新 lastUsed（move to front）。
// fp 对不上（含升级前没有 fp 的老条目）→ 删文件 + 删索引，当没缓存。
function getVideo(level, file, fp) {
  fp = fpOf(fp);
  if (!fp) return null;
  const key = videoKey(level, file);
  const arr = readIndex();
  const idx = arr.findIndex((it) => it.key === key);
  if (idx < 0) return null;
  const hit = arr[idx];
  if (hit.fp !== fp) {
    dropEntry(arr, idx);
    writeIndex(arr);
    return null;
  }
  // 文件可能被系统清掉（用户清理、重启路径失效），存在性校验一下
  try {
    wx.getFileSystemManager().accessSync(hit.path); // 有则不抛
  } catch (e) {
    dropEntry(arr, idx);
    writeIndex(arr);
    return null;
  }
  // move to front
  arr.splice(idx, 1);
  hit.lastUsed = Date.now();
  arr.unshift(hit);
  writeIndex(arr);
  return hit.path;
}

// 把远程视频下载并落到持久目录，返回 savedFilePath；失败返回 null（走网络）。
// 下载前先按 LRU 腾空间；落盘时把该 key 下的旧 fp 条目一并清掉。
function cacheVideo(level, file, url, fp, expectedSize) {
  fp = fpOf(fp);
  if (!fp) {
    setError('服务端未下发指纹', '');
    return Promise.resolve(null);
  }
  const key = videoKey(level, file);
  // 已有同版本缓存直接返回，不重复下
  const hit = getVideo(level, file, fp);
  if (hit) {
    lastError = '';
    return Promise.resolve(hit);
  }

  // ⚠️ 下载前先看真实占用 + 服务端给的 size：不够就提示并按 LRU 清，
  //    清完够了才自动下；仍不够直接不下载，省掉 32MB 白下（老师 2026-11-25）。
  try {
    const fs0 = wx.getFileSystemManager();
    if (!prepareRoom(fs0, key, Number(expectedSize) || 0)) {
      setError('空间不足，无法缓存',
        '清理旧缓存后仍不足：need=' + expectedSize + ', used=' + usedBytes(fs0));
      return Promise.resolve(null);
    }
  } catch (e) { /* 拿不到 fs 就照常下载，落盘阶段还有 LRU 重试兜底 */ }

  return new Promise((resolve) => {
    // ⚠️ 每条 resolve(null) 的路径都必须先 setError，否则页面只会显示
    //    「缓存失败」四个字，等于把唯一有用的线索吞了。
    try {
      wx.downloadFile({
        url: url,
        success(res) {
          guard(resolve, function () {
            // ⚠️ 不能只认 200：微信对大文件可能带 Range，COS/EO 回 206 也是成功。
            //    statusCode 缺失（部分机型不给）时信任 success，别误判成 HTTP 0。
            const code = Number(res.statusCode || 0);
            if (code && (code < 200 || code >= 300)) {
              setError('HTTP ' + code, res);
              resolve(null);
              return;
            }
            const tmp = res.tempFilePath;
            const size = (res.totalBytesExpectedToWrite || 0) || guessSize(tmp);
            ensureRoom(size);
            const fs = wx.getFileSystemManager();
            // 主路径 copyFile 到 USER_DATA_PATH，失败回退老 saveFile（实现见下方）；
            // 落盘报空间类错误时按 LRU 清理后自动重试。
            persistWithRetry(fs, tmp, key, fp).then(function (saved) {
              guard(resolve, function () {
                if (!saved) { resolve(null); return; } // persistVideo 已写好失败原因
                const arr = readIndex();
                // 并发下载可能留了旧 fp 的同 key 条目，连同文件一起替掉
                for (let i = arr.length - 1; i >= 0; i--) {
                  if (arr[i].key === key && arr[i].fp !== fp) {
                    try { fs.unlinkSync(arr[i].path); } catch (e) { /* 文件已没 */ }
                    arr.splice(i, 1);
                  }
                }
                const existing = arr.find((it) => it.key === key);
                if (!existing) {
                  arr.unshift({ key, path: saved, size, lastUsed: Date.now(), fp });
                } else if (existing.path !== saved) {
                  // 并发的另一条已写好同版本；两条落在同一 dest 时绝不能删，
                  // 删了会连累别人。只有路径不同才清掉这次多存的文件。
                  try { fs.unlinkSync(saved); } catch (e) { /* 文件已没 */ }
                }
                writeIndex(arr);
                lastError = '';
                resolve(saved);
              });
            });
          });
        },
        fail(e) {
          setError(shortDownloadError(e), (e && e.errMsg) || '');
          resolve(null);
        }
      });
    } catch (e) {
      setError('下载调用失败', (e && (e.errMsg || e.message)) || String(e));
      resolve(null);
    }
  });
}

// —— 持久落盘（2026-11-25 改成双路径）——
// ⚠️ 原来只用 FileSystemManager.saveFile，鸿蒙微信 8.0.23 上必报「保存失败」：
//    wx.saveFile 系列已停止维护，且部分机型对 tempFilePath 有路径校验。
//    主路径改成 copyFile 到 wx.env.USER_DATA_PATH（本地用户文件，文件名可控），
//    它不挑源路径；copyFile 也不行时回退老 saveFile，两条都失败才认输。
//    两类文件共享 200MB 配额，配额真满时两条都会失败 —— 这时把微信原始
//    errMsg 带进短原因，真机上不用开调试也能看到确切报错。
function slug(s) {
  return String(s || '').replace(/[^A-Za-z0-9_-]/g, '_');
}

function keyHash(s) {
  let h = 0;
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) | 0;
  return (h >>> 0).toString(36);
}

function persistPath(key, fp) {
  return wx.env.USER_DATA_PATH + '/jrp_' + keyHash(key) + '_' + slug(fp).slice(0, 24) + '.mp4';
}

function errText(e) {
  return (e && (e.errMsg || e.message)) || String(e || '');
}

// —— 空间管理（2026-11-25 老师定） ——
// 微信没有「查剩余配额」的 API，只能自己算已用；平台上限也不公开（文档 200MB，
// 老 saveFile 还有 10MB 遗留限制），所以两层兜底：
//   ① 下载前：真实占用 + 服务端给的文件大小 > 软上限 → 提示并按 LRU 清，
//      清完够了才下，不够直接不下载（别等 32MB 下完才在落盘时炸）；
//   ② 落盘报空间类错误：继续 LRU 清 + 重试，删到没得删才提示「空间不足，无法缓存」。
function usedBytes(fs) {
  let total = 0;
  let dir = '';
  try { dir = wx.env.USER_DATA_PATH || ''; } catch (e) {}
  try {
    if (dir) {
      const names = fs.readdirSync(dir) || [];
      names.forEach(function (n) {
        if (n.indexOf('jrp_') !== 0) return; // 只算本模块写的，含索引外孤儿
        try {
          const st = fs.statSync(dir + '/' + n);
          total += (st && st.size) || 0;
        } catch (e) { /* 文件已没 */ }
      });
    }
  } catch (e) { /* 老基础库 / 目录读不到 */ }
  // 老 saveFile 缓存不在 USER_DATA_PATH 下，按索引里记的大小补上
  readIndex().forEach(function (it) {
    const p = String(it.path || '');
    if (!dir || p.indexOf(dir) !== 0) total += it.size || 0;
  });
  return total;
}

// 按 LRU 删最旧的一条（跳过当前正在下的 key），返回是否真的删到东西。
function evictOldest(fs, skipKey) {
  const arr = readIndex();
  const victims = arr.filter(function (it) { return it.key !== skipKey; })
    .sort(function (a, b) { return (a.lastUsed || 0) - (b.lastUsed || 0); });
  if (!victims.length) return false;
  const v = victims[0];
  try { fs.unlinkSync(v.path); } catch (e) { /* 文件已没，索引照删 */ }
  const idx = arr.findIndex(function (it) { return it.key === v.key; });
  if (idx >= 0) arr.splice(idx, 1);
  writeIndex(arr);
  return true;
}

// 索引外的孤儿文件（历次失败 / 升级残留）也清掉，否则 usedBytes 一直偏高。
function evictOrphans(fs) {
  let dir = '';
  try { dir = wx.env.USER_DATA_PATH || ''; } catch (e) {}
  if (!dir) return false;
  let removed = false;
  try {
    const keep = {};
    readIndex().forEach(function (it) { keep[String(it.path || '')] = 1; });
    const names = fs.readdirSync(dir) || [];
    names.forEach(function (n) {
      if (n.indexOf('jrp_') !== 0) return;
      const p = dir + '/' + n;
      if (keep[p] || pendingDests[p]) return; // 别删并发中正在落盘的文件
      try { fs.unlinkSync(p); removed = true; } catch (e) {}
    });
  } catch (e) {}
  return removed;
}

// 下载前的空间检查：够了 true；不够先清，清完仍不够 false（不下载、直接提示）。
function prepareRoom(fs, key, need) {
  need = Number(need) || 0;
  if (!need || usedBytes(fs) + need <= MAX_VIDEO_BYTES) return true;
  notify('空间不足，正在清理旧缓存');
  evictOrphans(fs);
  let rounds = 0;
  while (usedBytes(fs) + need > MAX_VIDEO_BYTES && rounds++ < 50) {
    if (!evictOldest(fs, key)) { evictOrphans(fs); break; }
  }
  return usedBytes(fs) + need <= MAX_VIDEO_BYTES;
}

function persistVideo(fs, tmp, key, fp) {
  return new Promise((resolve) => {
    let dest = '';
    function done(v) {
      if (dest) delete pendingDests[dest];
      resolve(v);
    }
    try {
      dest = persistPath(key, fp);
      pendingDests[dest] = true;
      // 不预先 unlink：同 key 同 fp 的两条并发下载会算出同一个 dest，
      // 谁先删都可能把对方刚写好的文件抹掉。copyFile 自己覆盖即可。
      fs.copyFile({
        srcPath: tmp,
        destPath: dest,
        success() { done(dest); },
        fail(e) { fallbackSave(fs, tmp, e, done); }
      });
    } catch (e) {
      fallbackSave(fs, tmp, e, done);
    }
  });
}

// 落盘报空间类错误时：按 LRU 删一条 → 重试，直到成功、轮次用尽或没得删。
function persistWithRetry(fs, tmp, key, fp) {
  return new Promise((resolve) => {
    let tries = 0;
    function attempt() {
      persistVideo(fs, tmp, key, fp).then(function (saved) {
        if (saved) { resolve(saved); return; }
        if (!isSpaceError(lastDetail)) { resolve(''); return; }
        if (tries >= MAX_EVICT_RETRY) {
          setError('空间不足，无法缓存', '按 LRU 清理 ' + tries + ' 轮仍不足：' + lastDetail);
          resolve('');
          return;
        }
        if (!(evictOldest(fs, key) || evictOrphans(fs))) {
          setError('空间不足，无法缓存', '已无可清理的旧缓存：' + lastDetail);
          resolve('');
          return;
        }
        tries++;
        notify('空间不足，正在清理旧缓存');
        attempt();
      });
    }
    attempt();
  });
}

// 备用路径：老 saveFile。两条都失败时短原因带上微信原始 errMsg。
function fallbackSave(fs, tmp, firstErr, resolve) {
  try {
    fs.saveFile({
      tempFilePath: tmp,
      success(sv) {
        const saved = sv && sv.savedFilePath;
        if (!saved) {
          setError('保存路径为空', 'saveFile success 未返回 savedFilePath：' + JSON.stringify(sv || {}));
          resolve('');
          return;
        }
        resolve(saved);
      },
      fail(e) {
        const a = errText(firstErr);
        const b = errText(e);
        const short = (b || a).replace(/^[a-zA-Z]+:fail\s*/i, '').slice(0, 30);
        setError('保存失败 · ' + (short || '未知原因'), 'copyFile: ' + a + ' | saveFile: ' + b);
        resolve('');
      }
    });
  } catch (e) {
    setError('保存失败 · ' + errText(e).slice(0, 30), 'copyFile/saveFile 调用异常：' + errText(e));
    resolve('');
  }
}

// 估算已落 tmp 文件大小（downloadFile 有时不返回 totalBytesExpectedToWrite）。
function guessSize(tmp) {
  try {
    const st = wx.getFileSystemManager().statSync(tmp);
    return st.size || 0;
  } catch (e) { return 0; }
}

// LRU 逐出：保证 saveFile 前 总size + need <= MAX_VIDEO_BYTES。
function ensureRoom(need) {
  try {
    let arr = readIndex();
    let total = arr.reduce((s, it) => s + (it.size || 0), 0);
    // lastUsed 从旧到新排序，旧的先删
    arr.sort((a, b) => (a.lastUsed || 0) - (b.lastUsed || 0));
    const fs = wx.getFileSystemManager();
    let changed = false;
    let i = 0;
    while (i < arr.length && total + need > MAX_VIDEO_BYTES) {
      const victim = arr[i];
      try { fs.unlinkSync(victim.path); } catch (e) { /* 文件已没，索引照删 */ }
      total -= (victim.size || 0);
      arr.splice(i, 1);
      changed = true;
      // 删的是头，不 i++，继续删下一个
    }
    if (changed) writeIndex(arr);
  } catch (e) { /* 逐出失败：saveFile 自己会兜底 fail */ }
}

// 查单个视频的缓存状态：'none'（没缓存）/ 'cached'（已缓存）。
// 下载中态由页面自己记（downloadFile 是异步 Promise，这里不管）。
function getVideoState(level, file, fp) {
  fp = fpOf(fp);
  if (!fp) return 'none'; // 没指纹不启用缓存，但不动用户已存的旧文件
  const key = videoKey(level, file);
  const arr = readIndex();
  const idx = arr.findIndex((it) => it.key === key);
  if (idx < 0) return 'none';
  const hit = arr[idx];
  if (hit.fp !== fp) {
    dropEntry(arr, idx);
    writeIndex(arr);
    return 'none';
  }
  // 文件可能被系统清了，校验一下；没了就当没缓存
  try {
    wx.getFileSystemManager().accessSync(hit.path);
    return 'cached';
  } catch (e) {
    dropEntry(arr, idx);
    writeIndex(arr);
    return 'none';
  }
}

// 删单个视频的本地缓存文件 + 索引条目。返回是否真的删了（文件/索引可能已没）。
function deleteVideo(level, file) {
  const key = videoKey(level, file);
  const arr = readIndex();
  const idx = arr.findIndex((it) => it.key === key);
  let removed = false;
  if (idx >= 0) {
    try { wx.getFileSystemManager().unlinkSync(arr[idx].path); removed = true; } catch (e) { /* 文件已没 */ }
    arr.splice(idx, 1);
    writeIndex(arr);
  }
  return removed;
}

// 手动清空所有视频缓存（预留，暂未挂 UI）。
function clearVideos() {
  try {
    const arr = readIndex();
    const fs = wx.getFileSystemManager();
    arr.forEach((it) => { try { fs.unlinkSync(it.path); } catch (e) {} });
    wx.removeStorageSync(VID_INDEX_KEY);
  } catch (e) {}
}

module.exports = {
  getCues, setCues,
  getVideo, cacheVideo, deleteVideo, getVideoState, clearVideos,
  getLastError, getLastFailure
};
