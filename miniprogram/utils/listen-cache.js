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
// 后完成的成功会 lastError='' 把先失败的原因冲掉，页面读到的就是空串。
let lastFailure = '';
// 完整报错原文（errMsg），用来判断是不是空间类错误、决定要不要 LRU 重试。
let lastDetail = '';
// ⛔ 清理数不放模块级单例：openVideo 的后台缓存和手动缓存会并发，后启动的那次
//    会把单例重置，页面终态 toast 就会丢数或串到另一条的数字（同 lastError 老坑）。
//    改成 cacheVideo 内部按调用累计，通过 onStage('done', n) 回传给本次点击。
// 正在落盘的目标路径：清孤儿文件时要跳过，别把并发中刚写好的文件删掉。
const pendingDests = {};
const MAX_EVICT_RETRY = 3;
// ⛔ 不要再搞「观测上限」：曾经把一次落盘失败的 used+need 写进
//    `jrp_listen_limit`，只减不增、永不复位。结果是空盘 used=0 时 cap 恰好等于
//    need，`>=` 恒成立 → 连下载都不试就报「文件超过微信缓存上限」，缓存永久自锁
//    （2026-11-25 老师实测 `need=32795534,used=0,清理0项`）。微信不公开真实配额，
//    靠一次失败反推上限根本不可靠；老设备残留的这个 key 现在不读、不写，无害。
const STALL_SECONDS = 15;      // 多少秒没有任何进展就标「可能卡住」
const PROGRESS_MIN_MS = 200;   // 进度回调节流：微信约每 100ms 一次，别刷 setData

// 正在下载的 DownloadTask（按 key 存数组：自动后台缓存和手动缓存可能并发）。
// cancelCache() 用来 abort，让「卡住了」有个出口，而不是干等 120s 超时。
const activeTasks = {};
let callSeq = 0;
// 老版本写进本地存储的废弃 key：升级后第一次缓存时抹掉，避免以后有人误读又自锁。
let legacyLimitCleared = false;
function clearLegacyLimit() {
  if (legacyLimitCleared) return;
  legacyLimitCleared = true;
  try { wx.removeStorageSync('jrp_listen_limit'); } catch (e) {}
}

function registerTask(key, handle) {
  if (!activeTasks[key]) activeTasks[key] = [];
  activeTasks[key].push(handle);
}
function unregisterTask(key, handle) {
  const arr = activeTasks[key] || [];
  const i = arr.indexOf(handle);
  if (i >= 0) arr.splice(i, 1);
  if (!arr.length) delete activeTasks[key];
}
// 取消某个视频正在进行的所有下载（含并发的自动缓存那一支）。返回取消了几条。
function cancelCache(level, file) {
  const key = videoKey(level, file);
  const arr = (activeTasks[key] || []).slice();
  arr.forEach(function (h) { try { h.cancel(); } catch (e) {} });
  return arr.length;
}

function mbText(n) {
  return (Number(n || 0) / (1024 * 1024)).toFixed(1);
}

function setError(short, detail) {
  lastError = short;
  lastFailure = short;
  lastDetail = String(detail || short || '');
  try { console.error('[listen-cache] ' + short, detail || ''); } catch (e) {}
}

function getLastError() { return lastError; }
function getLastFailure() { return lastFailure; }
function getLastDetail() { return lastDetail; }

function isSpaceError(msg) {
  return /maximum size|storage limit|exceeded|quota|space|not enough/i.test(String(msg || ''));
}

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

// 包住异步回调里的同步逻辑：抛异常也要留下原因并结束 Promise。
// 不这么做的话 Promise 永远悬着（按钮一直「下载中…」），或 lastError 还是空的，
// 页面只剩「缓存失败」四个字，真机上完全分不清是哪一步坏了。
// resolve 传的是 cacheVideo 里的 done（会顺手停掉卡住检测定时器、注销取消句柄）。
function guard(resolve, fn) {
  try { fn(); } catch (e) {
    setError('本地处理异常', (e && (e.errMsg || e.message)) || String(e));
    resolve(null);
  }
}

const STAGE_LABELS = {
  preparing: '检查缓存空间', cleaning: '清理旧缓存', downloading: '下载',
  writing: '写入本地文件', retry: '清理重试'
};

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
//
// onStage(stage, info) 是「详情」事件流（2026-11-25 老师要求能看到在干嘛）：
//   stage: preparing / cleaning / downloading / writing / retry / stalled / cancelled / done
//   info : { text, label, percent, written, total, elapsed, cleaned, at, stalled, ... }
//   done 的第二个参数仍是数字（本次清了几项），保持老约定。
// 只做观测，不改缓存判定：指纹、LRU、落盘逻辑一条没动。
function cacheVideo(level, file, url, fp, expectedSize, onStage) {
  onStage = typeof onStage === 'function' ? onStage : function () {};
  clearLegacyLimit();
  let cleaned = 0;
  const startedAt = Date.now();
  const total = Number(expectedSize) || 0;
  let currentStage = 'preparing';
  let lastTickAt = startedAt;
  let lastEmitAt = 0;
  let lastPercent = -1;
  let settled = false;
  let cancelled = false;
  let stallTimer = null;
  let nextStallAt = STALL_SECONDS * 1000;
  let task = null;
  let key = '';

  const elapsed = () => Math.floor((Date.now() - startedAt) / 1000);
  const at = () => STAGE_LABELS[currentStage] || currentStage;

  // real=false 表示「只是刷新耗时文案，没有实质进展」，不刷新卡住计时。
  function emit(stage, extra, real) {
    if (stage !== 'stalled' && STAGE_LABELS[stage]) currentStage = stage;
    const info = {
      text: '', label: '', percent: -1, written: 0, total: total,
      elapsed: elapsed(), cleaned: cleaned, at: at(),
      stalled: false, stallSeconds: 0
    };
    if (extra) Object.keys(extra).forEach(function (k) { info[k] = extra[k]; });
    try { onStage(stage, info); } catch (e) {}
    // ⚠️ 只有实质动作（阶段切换 / 百分比真的涨了）才刷新卡住计时。
    //    若进度一直卡在 42% 还照常 tick，stalled 就永远不会触发。
    if (real !== false && stage !== 'stalled') tick();
  }

  // 有实质动作（阶段切换 / 收到进度）就刷新「最后活动时间」，并把下一次
  // 卡住告警的阈值复位到 15 秒 —— 否则恢复过一次后，第二次卡住要等 30/45 秒才报。
  function tick() { lastTickAt = Date.now(); nextStallAt = STALL_SECONDS * 1000; }

  function stopStall() {
    if (stallTimer) { try { clearInterval(stallTimer); } catch (e) {} stallTimer = null; }
  }
  function startStall() {
    if (stallTimer) return;
    try {
      stallTimer = setInterval(function () {
        if (settled || cancelled) return;
        const quiet = Date.now() - lastTickAt;
        if (quiet < nextStallAt) return;
        const secs = Math.floor(quiet / 1000);
        nextStallAt += STALL_SECONDS * 1000;
        emit('stalled', {
          text: '已 ' + secs + ' 秒无进展，当前停在「' + at() + '」',
          stalled: true, stallSeconds: secs
        });
      }, 2000);
    } catch (e) {}
  }

  function finish() { try { onStage('done', cleaned); } catch (e) {} }
  // 提前返回（fp 为空 / 命中 / 空间不够）和 Promise 内都走这里，保证定时器、
  // 取消句柄、'done' 事件都只收一次。resolve 传 null 时表示没有 Promise 要结。
  function end(v, resolve) {
    if (settled) return;
    settled = true;
    stopStall();
    unregisterTask(key, handle);
    finish();
    if (resolve) resolve(v);
  }

  const handle = {
    resolve: null,
    cancel: function () {
      if (cancelled) return;
      cancelled = true;
      setError('已取消缓存', '');
      emit('cancelled', { text: '已取消本次缓存', label: '已取消' });
      try { if (task && typeof task.abort === 'function') task.abort(); } catch (e) {}
      // ⚠️ 不依赖微信 abort 后一定回调 fail：这里直接收口，否则 Promise 永远悬着。
      if (handle.resolve) handle.resolve(null);
    }
  };

  fp = fpOf(fp);
  if (!fp) {
    setError('服务端未下发指纹', '');
    end(null, null);
    return Promise.resolve(null);
  }
  key = videoKey(level, file);
  // 已有同版本缓存直接返回，不重复下
  const hit = getVideo(level, file, fp);
  if (hit) {
    lastError = '';
    end(null, null);
    return Promise.resolve(hit);
  }

  // ⚠️ 下载前先看真实占用 + 服务端给的 size：不够就按 LRU 清（同时通知页面把
  //    按钮锁成「清理中…」），清完够了才自动下；仍不够直接不下载 + 明确终态，
  //    绝不让人反复点、反复白下 32MB（老师 2026-11-25 反馈）。
  emit('preparing', { text: '正在检查本地缓存空间…', label: '检查中…' });
  try {
    const fs0 = wx.getFileSystemManager();
    const room = prepareRoom(fs0, key, Number(expectedSize) || 0, emit);
    cleaned += room.cleaned;
    if (!room.ok) {
      setError(room.cleaned
        ? '空间不足，已清理 ' + room.cleaned + ' 项仍不够'
        : '文件超过缓存软上限，无法缓存',
        '清理 ' + room.cleaned + ' 项后仍不足：need=' + expectedSize +
        ', used=' + usedBytes(fs0) + ', cap=' + MAX_VIDEO_BYTES);
      end(null, null);
      return Promise.resolve(null);
    }
  } catch (e) { /* 拿不到 fs 就照常下载，落盘阶段还有 LRU 重试兜底 */ }

  registerTask(key, handle);
  startStall();
  emit('downloading', {
    text: '准备下载 · ' + mbText(total) + ' MB',
    label: '下载 0%', percent: 0
  });

  return new Promise((resolve) => {
    const done = (v) => end(v, resolve);
    handle.resolve = done;

    // 微信 DownloadTask.onProgressUpdate：progress(0~100) / totalBytesWritten /
    // totalBytesExpectedToWrite。⛔ 别每个回调都 setData，节流到 200ms 且进度
    // 至少跳 1%；进度没动时每 ~1 秒刷一次「已用 N 秒」，让时间看起来在走。
    function reportProgress(res) {
      if (settled || cancelled) return;
      const written = Number(res && res.totalBytesWritten) || 0;
      const expect = Number(res && res.totalBytesExpectedToWrite) || total || 0;
      let pct = Number(res && res.progress);
      if (!isFinite(pct) || pct < 0) pct = expect > 0 ? Math.floor(written / expect * 100) : 0;
      pct = Math.max(0, Math.min(100, Math.round(pct)));
      const now = Date.now();
      const changed = pct !== lastPercent;
      if (changed && now - lastEmitAt < PROGRESS_MIN_MS) return;
      if (!changed && now - lastEmitAt < 1000) return;
      lastPercent = pct;
      lastEmitAt = now;
      // ⚠️ 只有百分比真的涨了才算进展；同百分比的「已用 N 秒」刷新不能续命，
      //    否则下载卡在 42% 时 15 秒卡住检测永远不触发。
      emit('downloading', {
        text: '正在下载 · ' + mbText(written) + ' / ' + mbText(expect) +
          ' MB · ' + pct + '% · 已用 ' + elapsed() + ' 秒',
        label: '下载 ' + pct + '%', percent: pct, written: written, total: expect
      }, changed);
    }

    // ⚠️ 每条 resolve(null) 的路径都必须先 setError，否则页面只会显示
    //    「缓存失败」四个字，等于把唯一有用的线索吞了。
    try {
      task = wx.downloadFile({
        url: url,
        success(res) {
          guard(done, function () {
            // ⚠️ 不能只认 200：微信对大文件可能带 Range，COS/EO 回 206 也是成功。
            //    statusCode 缺失（部分机型不给）时信任 success，别误判成 HTTP 0。
            const code = Number(res.statusCode || 0);
            if (code && (code < 200 || code >= 300)) {
              setError('HTTP ' + code, res);
              done(null);
              return;
            }
            const tmp = res.tempFilePath;
            const size = (res.totalBytesExpectedToWrite || 0) || guessSize(tmp);
            cleaned += ensureRoom(size); // 老 LRU 兜底清掉的也计入终态提示
            const fs = wx.getFileSystemManager();
            // 下载完成 ≠ 缓存完成：32MB 写入本地也要几秒，明确告诉用户这一步。
            tick();
            emit('writing', {
              text: '下载完成 ' + mbText(size) + ' MB，正在写入本地文件…',
              label: '写入中…', percent: 100, written: size
            });
            // 主路径 copyFile 到 USER_DATA_PATH，失败回退老 saveFile（实现见下方）；
            // 落盘报空间类错误时按 LRU 清理后自动重试。
            persistWithRetry(fs, tmp, key, fp, emit).then(function (r) {
              guard(done, function () {
                cleaned += (r && r.cleaned) || 0;
                const saved = r && r.saved;
                if (!saved) {
                  done(null);
                  return;
                }
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
                done(saved);
              });
            });
          });
        },
        fail(e) {
          if (cancelled) { done(null); return; } // cancel() 已写过原因并收口
          setError(shortDownloadError(e), (e && e.errMsg) || '');
          done(null);
        }
      });
      if (task && typeof task.onProgressUpdate === 'function') {
        task.onProgressUpdate(reportProgress);
      }
    } catch (e) {
      setError('下载调用失败', (e && (e.errMsg || e.message)) || String(e));
      done(null);
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

// 按 LRU 删最旧的一条（跳过当前正在下的 key），返回删掉几条。
function evictOldest(fs, skipKey) {
  const arr = readIndex();
  const victims = arr.filter(function (it) { return it.key !== skipKey; })
    .sort(function (a, b) { return (a.lastUsed || 0) - (b.lastUsed || 0); });
  if (!victims.length) return 0;
  const v = victims[0];
  try { fs.unlinkSync(v.path); } catch (e) { /* 文件已没，索引照删 */ }
  const idx = arr.findIndex(function (it) { return it.key === v.key; });
  if (idx >= 0) arr.splice(idx, 1);
  writeIndex(arr);
  return 1;
}

// 索引外的孤儿文件（历次失败 / 升级残留）也清掉，否则 usedBytes 一直偏高。
function evictOrphans(fs) {
  let dir = '';
  try { dir = wx.env.USER_DATA_PATH || ''; } catch (e) {}
  if (!dir) return 0;
  let removed = 0;
  try {
    const keep = {};
    readIndex().forEach(function (it) { keep[String(it.path || '')] = 1; });
    const names = fs.readdirSync(dir) || [];
    names.forEach(function (n) {
      if (n.indexOf('jrp_') !== 0) return;
      const p = dir + '/' + n;
      if (keep[p] || pendingDests[p]) return; // 别删并发中正在落盘的文件
      try { fs.unlinkSync(p); removed++; } catch (e) {}
    });
  } catch (e) {}
  return removed;
}

// 下载前的空间检查：不够就通知页面锁按钮 + 按 LRU 清，返回 {ok, cleaned}。
// ⚠️ cap 只用我们自己的软上限 180MB。**绝不能**拿「上次失败时的 used+need」当
//    平台上限：那是一次失败的观测值，不是配额；一旦持久化就会把空盘也判成不够。
//    微信真实上限由落盘时自己说话，落盘报空间错再走 persistWithRetry 的 LRU 重试。
function prepareRoom(fs, key, need, emit) {
  emit = typeof emit === 'function' ? emit : function () {};
  need = Number(need) || 0;
  const cap = MAX_VIDEO_BYTES;
  if (!need || usedBytes(fs) + need <= cap) return { ok: true, cleaned: 0 };
  emit('cleaning', {
    text: '空间不足，正在按 LRU 清理旧缓存…',
    label: '清理中…'
  });
  let cleaned = evictOrphans(fs);
  let rounds = 0;
  while (usedBytes(fs) + need > cap && rounds++ < 50) {
    const n = evictOldest(fs, key);
    if (!n) { cleaned += evictOrphans(fs); break; }
    cleaned += n;
  }
  return { ok: usedBytes(fs) + need <= cap, cleaned: cleaned };
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

// 落盘报空间类错误时：通知页面锁按钮 → LRU 删一条 → 重试。
// 删无可删说明不是占用问题，是这个文件本身超过平台上限 —— 明确说出来。
// ⛔ 这里**不记任何「观测上限」**：一次失败推不出配额，写进 storage 只会自锁。
//    「别反复白下 32MB」由页面 blocked 终态负责（再点只提示原因、不重下）。
//    过程提示一律交给按钮状态，utils 不弹 toast。
function persistWithRetry(fs, tmp, key, fp, emit) {
  emit = typeof emit === 'function' ? emit : function () {};
  return new Promise((resolve) => {
    let tries = 0;
    let cleaned = 0;
    function attempt() {
      persistVideo(fs, tmp, key, fp).then(function (saved) {
        if (saved) { resolve({ saved: saved, cleaned: cleaned }); return; }
        if (!isSpaceError(lastDetail)) { resolve({ saved: '', cleaned: cleaned }); return; }
        if (tries >= MAX_EVICT_RETRY) {
          setError('空间不足，已清理 ' + cleaned + ' 项仍不够',
            '按 LRU 清理 ' + tries + ' 轮仍不足：' + lastDetail);
          resolve({ saved: '', cleaned: cleaned });
          return;
        }
        const n = evictOldest(fs, key) || evictOrphans(fs);
        if (!n) {
          setError('文件超过微信缓存上限，无法缓存', '已无可清理的旧缓存：' + lastDetail);
          resolve({ saved: '', cleaned: cleaned });
          return;
        }
        cleaned += n;
        tries++;
        emit('retry', {
          text: '写入报空间不足，已清 ' + cleaned + ' 项，第 ' + tries + '/' + MAX_EVICT_RETRY + ' 次重试…',
          label: '重试 ' + tries + '/' + MAX_EVICT_RETRY,
          round: tries, maxRounds: MAX_EVICT_RETRY
        });
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

// LRU 逐出：保证 saveFile 前 总size + need <= MAX_VIDEO_BYTES。返回删了几项，
// 让本次调用的终态提示把这一步的清理也报出来（别让老师以为没清）。
function ensureRoom(need) {
  let removed = 0;
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
      removed++;
      // 删的是头，不 i++，继续删下一个
    }
    if (changed) writeIndex(arr);
  } catch (e) { /* 逐出失败：saveFile 自己会兜底 fail */ }
  return removed;
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
  cancelCache, getLastError, getLastFailure, getLastDetail, diagEnabled
};
