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

function setError(short, detail) {
  lastError = short;
  try { console.error('[listen-cache] ' + short, detail || ''); } catch (e) {}
}

function getLastError() { return lastError; }

// 诊断 toast（域名未加白名单 / 下载超时 / 保存失败）只给开发版、体验版看。
// 正式发布（release）保持安静：按钮从「下载中…」回到「缓存」就是反馈，
// 不让用户看到技术黑话。完整 errMsg 仍打 console，正式版接真机调试也查得到。
// ⚠️ 老基础库可能没有 getAccountInfoSync，拿不到 envVersion 一律按正式版处理
//    （宁可不弹，也不能在用户手机上冒出调试信息）。
function diagEnabled() {
  try {
    const env = wx.getAccountInfoSync().miniProgram.envVersion;
    return env === 'develop' || env === 'trial';
  } catch (e) { return false; }
}

function shortDownloadError(e) {
  const m = (e && e.errMsg) || '';
  if (/domain/i.test(m)) return '域名未加白名单';
  if (/timeout/i.test(m)) return '下载超时';
  return '下载失败';
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
function cacheVideo(level, file, url, fp) {
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

  return new Promise((resolve) => {
    wx.downloadFile({
      url: url,
      success(res) {
        // ⚠️ 不能只认 200：微信对大文件可能带 Range，COS/EO 回 206 也是成功。
        const code = Number(res.statusCode || 0);
        if (code < 200 || code >= 300) {
          setError('HTTP ' + code, res);
          resolve(null);
          return;
        }
        const tmp = res.tempFilePath;
        const size = (res.totalBytesExpectedToWrite || 0) || guessSize(tmp);
        ensureRoom(size);
        const fs = wx.getFileSystemManager();
        fs.saveFile({
          tempFilePath: tmp,
          success(sv) {
            const arr = readIndex();
            // 并发下载可能留了旧 fp 的同 key 条目，连同文件一起替掉
            for (let i = arr.length - 1; i >= 0; i--) {
              if (arr[i].key === key && arr[i].fp !== fp) {
                try { fs.unlinkSync(arr[i].path); } catch (e) { /* 文件已没 */ }
                arr.splice(i, 1);
              }
            }
            if (!arr.some((it) => it.key === key)) {
              arr.unshift({ key, path: sv.savedFilePath, size, lastUsed: Date.now(), fp });
            } else {
              // 别的并发已经写好了同版本，删掉这次多存的重复文件
              try { fs.unlinkSync(sv.savedFilePath); } catch (e) { /* 文件已没 */ }
            }
            writeIndex(arr);
            lastError = '';
            resolve(sv.savedFilePath);
          },
          fail(e) {
            setError('保存失败', (e && e.errMsg) || '');
            resolve(null); // 配额满 / 鸿蒙 saveFile 不支持 → 走网络
          }
        });
      },
      fail(e) {
        setError(shortDownloadError(e), (e && e.errMsg) || '');
        resolve(null);
      }
    });
  });
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
  getLastError, diagEnabled
};
