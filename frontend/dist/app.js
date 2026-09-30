/* 拾书 Bookrest 前端
 * 传输层抽象：桌面壳（Wails 绑定）与开发服务器（HTTP）走同一套调用签名。
 * 视图：书架视图（架层+书脊+拖拽）· 封面墙 · 体检报告 · 详情抽屉 · 设置 · 离线只读横幅
 */
(function () {
  'use strict';

  // ---------- 传输层 ----------
  var hasWails = !!(window.go && window.go.main && window.go.main.App);
  var HTTP = {
    get: function (p) { return fetch(p).then(function (r) { return r.json() }); },
    post: function (p, body) {
      return fetch(p, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body || {}) })
        .then(function (r) { return r.json() });
    }
  };
  var T = hasWails ? {
    mode: 'wails',
    libraries: function () { return window.go.main.App.Libraries(); },
    addLibrary: function (root) { return window.go.main.App.AddLibrary(root); },
    setActive: function (root) { return window.go.main.App.SetActiveLibrary(root); },
    removeLibrary: function (root) { return window.go.main.App.RemoveLibrary(root); },
    status: function () { return window.go.main.App.Status(); },
    scan: function () { return window.go.main.App.Scan(); },
    items: function () { return window.go.main.App.Items(); },
    colors: function () { return window.go.main.App.SpineColors(); },
    shelves: function () { return window.go.main.App.Shelves(); },
    createShelf: function (name) { return window.go.main.App.CreateShelf(name); },
    renameShelf: function (id, name) { return window.go.main.App.RenameShelf(id, name); },
    deleteShelf: function (id) { return window.go.main.App.DeleteShelf(id); },
    moveItem: function (id, shelfId, index) { return window.go.main.App.MoveItem(id, shelfId, index); },
    setOverride: function (id, ov) { return window.go.main.App.SetOverride(id, ov); },
    report: function () { return window.go.main.App.Report(); },
    settings: function () { return window.go.main.App.Settings(); },
    saveSettings: function (s) { return window.go.main.App.SaveSettings(s); },
    openFile: function (id) { return window.go.main.App.OpenFile(id); },
    revealFile: function (id) { return window.go.main.App.RevealFile(id); },
    copyPath: function (id) { return window.go.main.App.CopyPath(id); },
    pickDir: function () { return window.go.main.Shell.PickLibraryDir(); },
    on: function (ev, cb) { if (window.runtime) window.runtime.EventsOn(ev, cb); }
  } : {
    mode: 'http',
    libraries: function () { return HTTP.get('/api/libraries'); },
    addLibrary: function (root) { return HTTP.post('/api/library/add', { root: root }); },
    setActive: function (root) { return HTTP.post('/api/library/active', { root: root }); },
    removeLibrary: function (root) { return HTTP.post('/api/library/remove', { root: root }); },
    status: function () { return HTTP.get('/api/status'); },
    scan: function () { return HTTP.post('/api/scan'); },
    items: function () { return HTTP.get('/api/items'); },
    colors: function () { return HTTP.get('/api/colors'); },
    shelves: function () { return HTTP.get('/api/shelves'); },
    createShelf: function (name) { return HTTP.post('/api/shelf/create', { name: name }); },
    renameShelf: function (id, name) { return HTTP.post('/api/shelf/rename', { id: id, name: name }); },
    deleteShelf: function (id) { return HTTP.post('/api/shelf/delete', { id: id }); },
    moveItem: function (id, shelfId, index) { return HTTP.post('/api/move', { id: id, shelfId: shelfId, index: index }); },
    setOverride: function (id, ov) { return HTTP.post('/api/override', { id: id, override: ov }); },
    report: function () { return HTTP.get('/api/report'); },
    settings: function () { return HTTP.get('/api/settings'); },
    saveSettings: function (s) { return HTTP.post('/api/settings', s); },
    openFile: function (id) { return HTTP.post('/api/open', { id: id }); },
    revealFile: function (id) { return HTTP.post('/api/reveal', { id: id }); },
    copyPath: function (id) { return HTTP.post('/api/copy', { id: id }); },
    pickDir: null,
    on: function () { /* dev 模式无事件流 */ }
  };
  function thumbUrl(id) { return '/thumb?id=' + encodeURIComponent(id); }

  // ---------- 状态 ----------
  var S = {
    view: 'shelf',
    items: [], colors: {}, shelves: [], libs: [], settings: {}, status: { online: true },
    search: '', sortBy: 'series', filterTag: '', filterSeries: '', activeShelf: '', report: null
  };

  var $ = function (sel) { return document.querySelector(sel); };
  function esc(s) { return String(s == null ? '' : s).replace(/[&<>"]/g, function (c) { return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c] }); }
  function toast(msg, ms) {
    var t = $('#toast'); t.textContent = msg; t.classList.remove('hidden');
    clearTimeout(t._h); t._h = setTimeout(function () { t.classList.add('hidden') }, ms || 2200);
  }
  function num(v) { var n = parseFloat(v); return isNaN(n) ? 1e9 : n; }

  // ---------- 启动 ----------
  function boot() {
    Promise.all([T.status(), T.settings()]).then(function (r) {
      S.status = r[0]; S.settings = r[1];
      applyTheme();
      renderStatus();
      if (!S.status.root) { renderEmptyState(); return loadSidebar(); }
      refreshAll();
    });
    T.on('scan:start', function () { showScan(true, '扫描中…'); });
    T.on('scan:done', function (sum) {
      showScan(false);
      toast('扫描完成：新增 ' + sum.added + ' / 更新 ' + sum.updated + ' / 移动 ' + sum.moved + ' / 重复 ' + sum.duplicates + '（' + sum.millis + 'ms）', 4000);
      refreshAll();
    });
    T.on('sync:state', function (s) { if (s && s.ok === false) toast('库内快照未回写：' + (s.error || ''), 4000); });
    T.on('sync:conflict', function (c) { toast('检测到另一台设备的改动，已备份库内版本：' + c.backup, 6000); });
    wireEvents();
  }

  function refreshAll() {
    Promise.all([T.libraries(), T.shelves(), T.items()]).then(function (r) {
      S.libs = r[0] || []; S.shelves = r[1] || []; S.items = r[2] || [];
      renderSidebar(); renderStats();
      if (S.view === 'shelf') { ensureColors().then(renderShelfView); } else if (S.view === 'wall') { renderWall(); } else { renderReport(); }
    });
  }
  function ensureColors() {
    return T.colors().then(function (c) { S.colors = c || {}; });
  }

  // ---------- 左栏 ----------
  function loadSidebar() { T.libraries().then(function (l) { S.libs = l || []; renderSidebar(); }); }

  function renderSidebar() {
    var libs = $('#libList');
    libs.innerHTML = S.libs.map(function (l) {
      var active = l.root === S.status.root ? ' active' : '';
      return '<div class="lib-item' + active + '" data-root="' + esc(l.root) + '" title="' + esc(l.root) + '">'
        + '<span class="dot' + (l.online ? '' : ' off') + '"></span>'
        + '<span class="name">' + esc(l.name || l.root) + '</span>'
        + '<span class="count">' + (l.total || 0) + '</span></div>';
    }).join('') || '<div class="muted" style="font-size:12px">尚未添加库</div>';
    libs.querySelectorAll('.lib-item').forEach(function (el) {
      el.onclick = function () { T.setActive(el.dataset.root).then(function () { toast('已切换库'); bootRefresh(); }); };
    });

    var sh = $('#shelfList');
    sh.innerHTML = S.shelves.map(function (s) {
      var active = s.id === S.activeShelf ? ' active' : '';
      var count = S.items.filter(function (i) { return i.shelfId === s.id }).length;
      return '<div class="shelf-item' + active + '" data-id="' + esc(s.id) + '">'
        + '<span class="name">' + esc(s.name) + '</span><span class="count">' + count + '</span></div>';
    }).join('') || '<div class="muted" style="font-size:12px">暂无书架（下方按系列自动排列）</div>';
    sh.querySelectorAll('.shelf-item').forEach(function (el) {
      el.onclick = function () {
        S.activeShelf = (S.activeShelf === el.dataset.id) ? '' : el.dataset.id;
        renderSidebar(); renderShelfView();
      };
      el.oncontextmenu = function (e) { e.preventDefault(); shelfMenu(el.dataset.id); };
    });

    // 过滤：标签 + 系列
    var tags = {}, series = {};
    S.items.forEach(function (i) {
      (i.tags || []).forEach(function (t) { tags[t] = (tags[t] || 0) + 1 });
      if (i.series) series[i.series] = (series[i.series] || 0) + 1;
    });
    var f = '';
    Object.keys(tags).sort().forEach(function (t) {
      f += '<div class="filter-item' + (S.filterTag === t ? ' active' : '') + '" data-tag="' + esc(t) + '"><span class="name">#' + esc(t) + '</span><span class="count">' + tags[t] + '</span></div>';
    });
    Object.keys(series).sort().slice(0, 40).forEach(function (t) {
      f += '<div class="filter-item' + (S.filterSeries === t ? ' active' : '') + '" data-series="' + esc(t) + '"><span class="name">' + esc(t) + '</span><span class="count">' + series[t] + '</span></div>';
    });
    $('#filters').innerHTML = f || '<div class="muted" style="font-size:12px">—</div>';
    $('#filters').querySelectorAll('.filter-item').forEach(function (el) {
      el.onclick = function () {
        if (el.dataset.tag) S.filterTag = S.filterTag === el.dataset.tag ? '' : el.dataset.tag;
        if (el.dataset.series) S.filterSeries = S.filterSeries === el.dataset.series ? '' : el.dataset.series;
        renderSidebar(); renderCurrent();
      };
    });
  }

  function shelfMenu(id) {
    var s = S.shelves.filter(function (x) { return x.id === id })[0]; if (!s) return;
    var name = prompt('重命名书架：', s.name);
    if (name === null) return;
    if (name.trim() === '') { if (confirm('删除书架「' + s.name + '」？（书不会被动到）')) T.deleteShelf(id).then(refreshAll); return; }
    T.renameShelf(id, name.trim()).then(function () { toast('已重命名'); refreshAll(); });
  }

  // ---------- 顶栏 / 状态 ----------
  function renderStats() {
    var list = filtered();
    var seriesN = {}; list.forEach(function (i) { if (i.series) seriesN[i.series] = 1 });
    $('#stats').textContent = '共 ' + S.items.length + ' 本，显示 ' + list.length + ' 本'
      + (S.view === 'shelf' ? '，' + Object.keys(seriesN).length + ' 个系列' : '')
      + (T.mode === 'http' ? '（开发模式）' : '');
  }
  function renderStatus() {
    var b = $('#offline');
    if (S.status.online) { b.classList.add('hidden'); return; }
    b.classList.remove('hidden');
    b.textContent = '⚠ 库离线（' + (S.status.hint || '目录不存在') + '）— 当前为只读模式：书架与封面照常浏览，拖拽与编辑已禁用。';
  }
  function showScan(on, text) {
    var bar = $('#scanbar');
    if (on) { bar.classList.remove('hidden'); $('#scanText').textContent = text || '扫描中…'; }
    else bar.classList.add('hidden');
  }
  function applyTheme() {
    document.documentElement.setAttribute('data-theme', S.settings.theme === 'light' ? 'light' : 'dark');
  }

  // ---------- 过滤 ----------
  function filtered() {
    var q = S.search.toLowerCase();
    return S.items.filter(function (i) {
      if (i.missing) return false;
      if (S.filterTag && (i.tags || []).indexOf(S.filterTag) < 0) return false;
      if (S.filterSeries && i.series !== S.filterSeries) return false;
      if (!q) return true;
      return (i.title + ' ' + i.series + ' ' + i.author + ' ' + i.rel + ' ' + (i.number || '')).toLowerCase().indexOf(q) >= 0;
    });
  }
  function sorted(list) {
    var arr = list.slice();
    if (S.sortBy === 'title') arr.sort(function (a, b) { return (a.title || '').localeCompare(b.title || '', 'zh') });
    else if (S.sortBy === 'recent') arr.sort(function (a, b) { return (b.rel || '').localeCompare(a.rel || '') });
    else arr.sort(function (a, b) {
      if (a.series !== b.series) return (a.series || '').localeCompare(b.series || '', 'zh');
      var d = num(a.number) - num(b.number); return d !== 0 ? d : (a.rel || '').localeCompare(b.rel || '');
    });
    return arr;
  }

  function renderCurrent() {
    renderStats();
    if (S.view === 'shelf') renderShelfView();
    else if (S.view === 'wall') renderWall();
    else renderReport();
  }

  // ---------- 书架视图（架层 + 书脊 + 拖拽） ----------
  function renderShelfView() {
    var list = filtered();
    var rows = [];
    // 1) 用户书架（按自定义顺序）
    S.shelves.forEach(function (s) {
      if (S.activeShelf && S.activeShelf !== s.id) return;
      var items = list.filter(function (i) { return i.shelfId === s.id });
      items.sort(function (a, b) { return (a.index || 0) - (b.index || 0) });
      rows.push({ id: s.id, name: s.name, items: items, custom: true });
    });
    // 2) 未归架的书按系列自动成行
    var unshelved = list.filter(function (i) { return !i.shelfId });
    if (!S.activeShelf) {
      var bySeries = {};
      unshelved.forEach(function (i) { var k = i.series || '(未识别系列)'; (bySeries[k] = bySeries[k] || []).push(i) });
      Object.keys(bySeries).sort(function (a, b) { return a.localeCompare(b, 'zh') }).forEach(function (k) {
        rows.push({ id: '', name: k, items: sorted(bySeries[k]), custom: false });
      });
    }
    if (!rows.length) { $('#content').innerHTML = '<div class="empty-state">没有可显示的条目<br><span class="muted">换个过滤条件，或先扫描库</span></div>'; return; }

    var html = rows.map(function (r) {
      var spines = r.items.map(function (i) { return spineHTML(i) }).join('');
      return '<div class="shelfrow" data-shelf="' + esc(r.id) + '">'
        + '<div class="shelf-head"><span class="shelf-name"><b>' + esc(r.name) + '</b> · ' + r.items.length + ' 卷</span>'
        + '<span class="shelf-tools">'
        + (r.custom ? '<button class="ghost small" data-renameshelf="' + esc(r.id) + '">重命名</button><button class="ghost small danger" data-delshelf="' + esc(r.id) + '">删除</button>' : '')
        + '</span></div>'
        + '<div class="board" data-shelf="' + esc(r.id) + '">' + (spines || '<span class="empty">（空书架，把书脊拖进来）</span>') + '</div></div>';
    }).join('');
    $('#content').innerHTML = html;
    wireShelf();
    if (S.pendingLocate) { locateSpine(S.pendingLocate); S.pendingLocate = ''; }
  }

  function spineHTML(i) {
    var w = Math.max(14, Math.min(38, 12 + (i.pages || 10) / 12));
    var h = Math.max(96, Math.min(190, 96 + (i.pages || 10) / 2));
    var bg = S.colors[i.id] || '#4a4f59';
    var label = i.number || (i.title || '').slice(0, 3);
    return '<div class="spine' + (i.failed ? ' failed' : '') + '" draggable="true" data-id="' + esc(i.id) + '"'
      + ' style="width:' + w + 'px;height:' + h + 'px;background:' + bg + '" title="' + esc(i.title) + '（' + esc(i.rel) + '）">'
      + '<span class="num">' + esc(label) + '</span></div>';
  }

  function wireShelf() {
    var readOnly = !S.status.online;
    var dragId = '';
    $('#content').querySelectorAll('.spine').forEach(function (el) {
      el.onclick = function (e) { e.stopPropagation(); openDetail(el.dataset.id); };
      if (readOnly) { el.removeAttribute('draggable'); return; }
      el.ondragstart = function (e) { dragId = el.dataset.id; el.classList.add('dragging'); e.dataTransfer.effectAllowed = 'move'; };
      el.ondragend = function () { el.classList.remove('dragging'); };
      el.ondragover = function (e) {
        if (!dragId) return; e.preventDefault();
        var r = el.getBoundingClientRect();
        var after = (e.clientX - r.left) > r.width / 2;
        el.classList.toggle('drop-before', !after); el.classList.toggle('drop-after', after);
      };
      el.ondragleave = function () { el.classList.remove('drop-before', 'drop-after'); };
      el.ondrop = function (e) {
        e.preventDefault();
        var row = el.closest('.board');
        var shelfId = row.dataset.shelf || '';
        var after = el.classList.contains('drop-after');
        el.classList.remove('drop-before', 'drop-after');
        var list = Array.prototype.slice.call(row.querySelectorAll('.spine'));
        var idx = list.indexOf(el) + (after ? 1 : 0);
        // 同书架内调整时，移除自身会让后续索引前移
        var cur = S.items.filter(function (x) { return x.id === dragId })[0];
        if (cur && cur.shelfId === shelfId && cur.index < idx) idx -= 1;
        if (!shelfId) { toast('请拖到某个书架里（先新建书架）'); return; }
        T.moveItem(dragId, shelfId, idx).then(function () { refreshAll(); });
      };
    });
    $('#content').querySelectorAll('.board').forEach(function (el) {
      el.ondragover = function (e) { if (dragId) { e.preventDefault(); el.classList.add('drop-target'); } };
      el.ondragleave = function () { el.classList.remove('drop-target'); };
      el.ondrop = function (e) {
        e.preventDefault(); el.classList.remove('drop-target');
        if (!dragId) return;
        var shelfId = el.dataset.shelf;
        if (!shelfId) { toast('这里不是用户书架；先新建书架再拖入'); return; }
        T.moveItem(dragId, shelfId, -1).then(function () { refreshAll(); });
      };
    });
    $('#content').querySelectorAll('[data-renameshelf]').forEach(function (b) { b.onclick = function () { shelfMenu(b.dataset.renameshelf) } });
    $('#content').querySelectorAll('[data-delshelf]').forEach(function (b) {
      b.onclick = function () { if (confirm('删除这个书架？（书不会被动到）')) T.deleteShelf(b.dataset.delshelf).then(refreshAll) };
    });
  }

  function locateSpine(id) {
    var el = document.querySelector('.spine[data-id="' + id + '"]');
    if (el) { el.scrollIntoView({ behavior: 'smooth', block: 'center' }); el.style.outline = '2px solid var(--accent)'; setTimeout(function () { el.style.outline = '' }, 1600); }
  }

  // ---------- 封面墙 ----------
  function renderWall() {
    var list = sorted(filtered());
    if (!list.length) { $('#content').innerHTML = '<div class="empty-state">没有可显示的条目</div>'; return; }
    $('#content').innerHTML = '<div class="wall">' + list.map(function (i) {
      var cover = i.failed
        ? '<div class="noimg">损坏</div>'
        : '<img loading="lazy" src="' + thumbUrl(i.id) + '" alt="" onerror="this.outerHTML=\'<div class=&quot;noimg&quot;>无封面</div>\'">';
      var tags = (i.tags || []).map(function (t) { return '<span class="tag">#' + esc(t) + '</span>' }).join('');
      return '<div class="book" data-id="' + esc(i.id) + '">' + cover
        + '<div class="meta"><div class="t">' + esc(i.title || i.rel) + '</div>'
        + '<div class="s"><span>' + esc(i.series || '') + (i.number ? ' · 第' + esc(i.number) + '卷' : '') + '</span>'
        + '<span>' + (i.pages || '') + (i.pages ? ' 页' : '') + '</span></div>'
        + (tags ? '<div style="margin-top:4px">' + tags + '</div>' : '') + '</div></div>';
    }).join('') + '</div>';
    $('#content').querySelectorAll('.book').forEach(function (el) { el.onclick = function () { openDetail(el.dataset.id) } });
  }

  // ---------- 体检报告 ----------
  function renderReport() {
    $('#content').innerHTML = '<div class="empty-state">正在生成体检报告…</div>';
    T.report().then(function (rep) {
      S.report = rep;
      var K = { gap: '缺卷', dup: '重复', corrupt: '损坏', nocover: '无封面', naming: '命名待整理' };
      var cards = '';
      if (!rep.findings || !rep.findings.length) {
        cards = '<div class="rep-card"><h3>未发现问题</h3><div class="row">共 ' + rep.total + ' 本，四类体检全部通过。</div></div>';
      } else {
        rep.findings.forEach(function (f, idx) {
          var body = '';
          if (f.kind === 'gap') {
            body = '<div class="row"><b>' + esc(f.series) + '</b> 缺 <span class="bad">' + f.missing.join(', ') + '</span>（现有 ' + f.items.length + ' 卷）</div>';
            body += f.items.slice(0, 12).map(function (x) { return '<div class="row">' + esc(x) + '</div>' }).join('');
          } else {
            var items = f.items || [];
            body = items.slice(0, 50).map(function (x) { return '<div class="row">' + esc(x) + '</div>' }).join('');
            if (items.length > 50) body += '<div class="row muted">… 其余 ' + (items.length - 50) + ' 个（导出 CSV 查看全部）</div>';
          }
          cards += '<div class="rep-card"><h3>[' + K[f.kind] + '] ' + (f.kind === 'gap' ? esc(f.series) : ((f.items || []).length + ' 个')) + '</h3>'
            + (f.detail ? '<div class="row muted">' + esc(f.detail) + '</div>' : '') + body
            + '<div class="rep-actions" style="margin-top:8px"><button class="ghost small" data-export="' + idx + '">导出这一类的 CSV</button></div></div>';
        });
      }
      $('#content').innerHTML = '<div class="report">'
        + '<div class="rep-card"><h3>体检报告</h3><div class="row">共 ' + rep.total + ' 本 · 只读分析，未修改任何文件</div>'
        + '<div class="rep-actions" style="margin-top:8px"><button class="small" id="repReload">重新体检</button><button class="small" id="repExportAll">导出全部 CSV</button></div></div>'
        + cards + '</div>';
      $('#repReload').onclick = renderReport;
      $('#repExportAll').onclick = function () { exportCSV(S.report.findings) };
      $('#content').querySelectorAll('[data-export]').forEach(function (b) {
        b.onclick = function () { exportCSV([S.report.findings[parseInt(b.dataset.export, 10)]]) };
      });
    });
  }

  function exportCSV(findings) {
    var K = { gap: '缺卷', dup: '重复', corrupt: '损坏', nocover: '无封面', naming: '命名待整理' };
    var lines = ['类别,系列,缺失卷号,条目'];
    (findings || []).forEach(function (f) {
      if (f.kind === 'gap') {
        (f.items || []).forEach(function (x) { lines.push([K[f.kind], f.series, (f.missing || []).join(' '), x].map(csvCell).join(',')) });
        if (!f.items || !f.items.length) lines.push([K[f.kind], f.series, (f.missing || []).join(' '), ''].map(csvCell).join(','));
      } else {
        (f.items || []).forEach(function (x) { lines.push([K[f.kind], '', '', x].map(csvCell).join(',')) });
      }
    });
    download('bookrest-report-' + new Date().toISOString().slice(0, 10) + '.csv', '\ufeff' + lines.join('\r\n'));
    toast('已导出 CSV（' + (lines.length - 1) + ' 行）');
  }
  function csvCell(v) { v = String(v == null ? '' : v); return /[",\r\n]/.test(v) ? '"' + v.replace(/"/g, '""') + '"' : v; }
  function download(name, text) {
    var blob = new Blob([text], { type: 'text/csv;charset=utf-8' });
    var a = document.createElement('a');
    a.href = URL.createObjectURL(blob); a.download = name; a.click();
    setTimeout(function () { URL.revokeObjectURL(a.href) }, 2000);
  }

  // ---------- 详情抽屉 ----------
  function openDetail(id) {
    var i = S.items.filter(function (x) { return x.id === id })[0];
    if (!i) return;
    var d = $('#detail');
    var shelfName = (S.shelves.filter(function (s) { return s.id === i.shelfId })[0] || {}).name || '未归架';
    var stars = '';
    for (var k = 1; k <= 5; k++) stars += '<span class="' + (i.rating >= k ? 'on' : '') + '" data-star="' + k + '">★</span>';
    d.innerHTML = (i.failed ? '<div class="noimg" style="aspect-ratio:2/3;display:flex;align-items:center;justify-content:center;color:var(--dim)">损坏文件</div>'
      : '<img class="cover" src="' + thumbUrl(i.id) + '" alt="" onerror="this.outerHTML=\'<div class=&quot;noimg&quot; style=&quot;aspect-ratio:2/3&quot;>无封面</div>\'">')
      + '<h2>' + esc(i.title || i.rel) + '</h2>'
      + '<div class="muted" style="font-size:12px">' + esc(i.rel) + '</div>'
      + '<div class="kv">'
      + '<b>系列</b><span class="val">' + esc(i.series || '—') + '</span>'
      + '<b>卷号</b><span class="val">' + esc(i.number || '—') + '</span>'
      + '<b>作者</b><span class="val">' + esc(i.author || '—') + '</span>'
      + '<b>页数 / 格式</b><span class="val">' + (i.pages || '—') + ' 页 · ' + esc(i.ext || '') + '</span>'
      + '<b>在架位置</b><span class="val">' + esc(shelfName) + (i.shelfId ? '（第 ' + (i.index + 1) + ' 位）' : '') + '</span>'
      + '<b>身份</b><span class="val">' + esc(i.id) + '</span>'
      + (i.failed ? '<b>状态</b><span class="val bad">归档无法解析（已列入体检报告）</span>' : '')
      + (i.missing ? '<b>状态</b><span class="val bad">文件缺失（摆放已保留）</span>' : '')
      + '</div>'
      + '<div><b class="muted" style="font-weight:400;font-size:12px">评分</b><div class="stars" id="stars">' + stars + '</div></div>'
      + '<div style="margin-top:10px"><b class="muted" style="font-weight:400;font-size:12px">标签</b><div id="tagList" style="margin-top:4px">'
      + ((i.tags || []).map(function (t) { return '<span class="tag">#' + esc(t) + ' <a href="#" data-rmtag="' + esc(t) + '" style="color:inherit">×</a></span>' }).join('') || '<span class="muted" style="font-size:12px">无</span>')
      + '</div><div class="taginput"><input id="tagInput" placeholder="加标签后回车"><button class="small" id="tagAdd">添加</button></div></div>'
      + '<div class="actions">'
      + '<button class="primary" id="dOpen">用默认程序打开</button>'
      + '<button id="dLocate">在书架上定位</button>'
      + '<button id="dReveal">在文件夹中显示</button>'
      + '<button id="dCopy">复制路径</button>'
      + (i.shelfId ? '<button class="danger" id="dUnshelf">移出书架</button>' : '')
      + '</div>';
    d.classList.add('open');

    function saveOv(patch) {
      var ov = { title: patch.title !== undefined ? patch.title : (i.title !== i.origTitle ? i.title : ''),
                 tags: patch.tags !== undefined ? patch.tags : (i.tags || []),
                 rating: patch.rating !== undefined ? patch.rating : (i.rating || 0) };
      T.setOverride(i.id, ov).then(function () { toast('已保存（只写意图，不改文件）'); refreshAll(); openDetail(id); });
    }
    d.querySelectorAll('#stars span').forEach(function (s) {
      s.onclick = function () { saveOv({ rating: parseInt(s.dataset.star, 10) }) };
    });
    var addTag = function () {
      var v = ($('#tagInput').value || '').trim(); if (!v) return;
      var tags = (i.tags || []).slice(); if (tags.indexOf(v) < 0) tags.push(v);
      saveOv({ tags: tags });
    };
    $('#tagAdd').onclick = addTag;
    $('#tagInput').onkeydown = function (e) { if (e.key === 'Enter') addTag(); };
    d.querySelectorAll('[data-rmtag]').forEach(function (a) {
      a.onclick = function (e) {
        e.preventDefault();
        saveOv({ tags: (i.tags || []).filter(function (t) { return t !== a.dataset.rmtag }) });
      };
    });
    $('#dOpen').onclick = function () { T.openFile(i.id).then(function (p) { toast('已交给系统默认程序：' + p) }).catch(function (e) { toast('打开失败：' + e) }) };
    $('#dReveal').onclick = function () { T.revealFile(i.id).then(function (p) { toast('已在文件管理器中定位') }).catch(function (e) { toast('定位失败：' + e) }) };
    $('#dCopy').onclick = function () {
      T.copyPath(i.id).then(function (p) {
        if (navigator.clipboard) navigator.clipboard.writeText(p);
        toast('路径已复制：' + p, 3000);
      });
    };
    $('#dLocate').onclick = function () {
      if (S.view !== 'shelf') { S.view = 'shelf'; syncTabs(); }
      S.pendingLocate = i.id; closeDetail(); renderCurrent();
    };
    if ($('#dUnshelf')) $('#dUnshelf').onclick = function () { T.moveItem(i.id, '', -1).then(function () { toast('已移出书架'); refreshAll(); closeDetail(); }) };
  }
  function closeDetail() { $('#detail').classList.remove('open') }

  // ---------- 设置 ----------
  function openSettings() {
    var s = S.settings;
    var row = function (label, html) { return '<div class="form-row"><label>' + label + '</label><div>' + html + '</div></div>' };
    $('#modal').innerHTML = '<div class="box"><h3>设置</h3>'
      + row('镜像目录（本机运行时数据）', '<span class="muted">' + esc(s.mirror_dir || '') + '</span>')
      + row('库内快照回写', '<input type="checkbox" id="stWriteBack"' + (s.write_back_enabled ? ' checked' : '') + '>')
      + row('同步索引到库内', '<input type="checkbox" id="stSyncIndex"' + (s.sync_index ? ' checked' : '') + '>')
      + row('同步缩略图到库内', '<input type="checkbox" id="stSyncThumbs"' + (s.sync_thumbs ? ' checked' : '') + '>')
      + row('回写去抖（毫秒）', '<input type="number" id="stDebounce" value="' + (s.sync_debounce_ms || 5000) + '" style="width:110px">')
      + row('扫描并发', '<input type="number" id="stWorkers" value="' + (s.scan_workers || 4) + '" style="width:110px">')
      + row('主题', '<select id="stTheme"><option value="dark"' + (s.theme !== 'light' ? ' selected' : '') + '>深色</option><option value="light"' + (s.theme === 'light' ? ' selected' : '') + '>浅色</option></select>')
      + row('书脊样式', '<select id="stSpine"><option value="wood"' + (s.spine_style !== 'plain' ? ' selected' : '') + '>木质</option><option value="plain"' + (s.spine_style === 'plain' ? ' selected' : '') + '>极简</option></select>')
      + '<div class="muted" style="font-size:12px;margin-top:10px">关闭「库内快照回写」= 纯本机模式：库目录零写入，适合只读挂载。</div>'
      + '<div class="foot"><button class="ghost" id="stCancel">取消</button><button class="primary" id="stSave">保存</button></div></div>';
    $('#modal').classList.remove('hidden');
    $('#stCancel').onclick = closeModal;
    $('#stSave').onclick = function () {
      var next = Object.assign({}, s, {
        write_back_enabled: $('#stWriteBack').checked,
        sync_index: $('#stSyncIndex').checked,
        sync_thumbs: $('#stSyncThumbs').checked,
        sync_debounce_ms: parseInt($('#stDebounce').value, 10) || 5000,
        scan_workers: parseInt($('#stWorkers').value, 10) || 4,
        theme: $('#stTheme').value,
        spine_style: $('#stSpine').value
      });
      T.saveSettings(next).then(function () { S.settings = next; applyTheme(); closeModal(); toast('设置已保存'); });
    };
  }
  function closeModal() { $('#modal').classList.add('hidden'); $('#modal').innerHTML = '' }

  // ---------- 空状态 ----------
  function renderEmptyState() {
    $('#content').innerHTML = '<div class="empty-state">'
      + '<div style="font-size:40px">📚</div>'
      + '<div>还没有添加库目录</div>'
      + '<div class="muted" style="font-size:13px">拾书不会导入或搬动你的文件，只读取你已有的目录</div>'
      + '<button class="primary" id="emptyAdd">选择库目录</button></div>';
    $('#emptyAdd').onclick = addLibrary;
  }
  function addLibrary() {
    var ask = T.pickDir ? T.pickDir() : Promise.resolve(prompt('输入库根目录的完整路径（例如 D:\\Books）：'));
    ask.then(function (root) {
      if (!root) return;
      return T.addLibrary(root.trim()).then(function () { toast('已添加库'); bootRefresh(); });
    }).catch(function (e) { toast('添加失败：' + (e && e.message ? e.message : e)) });
  }
  function bootRefresh() { T.status().then(function (s) { S.status = s; renderStatus(); refreshAll(); }); }

  // ---------- 事件绑定 ----------
  function wireEvents() {
    $('#viewTabs').querySelectorAll('button').forEach(function (b) {
      b.onclick = function () { S.view = b.dataset.view; syncTabs(); renderCurrent(); };
    });
    $('#search').oninput = function (e) { S.search = e.target.value; renderCurrent(); };
    $('#sortBy').onchange = function (e) { S.sortBy = e.target.value; renderCurrent(); };
    $('#btnScan').onclick = function () {
      if (!S.status.online) { toast('库离线，无法扫描'); return; }
      showScan(true);
      T.scan().then(function (sum) { showScan(false); toast('扫描完成：新增 ' + sum.added + ' / 共 ' + sum.total + ' 本（' + sum.millis + 'ms）', 4000); refreshAll(); })
        .catch(function (e) { showScan(false); toast('扫描失败：' + (e && e.message ? e.message : e)) });
    };
    $('#btnAddLib').onclick = addLibrary;
    $('#btnSettings').onclick = openSettings;
    $('#btnNewShelf').onclick = function () {
      var name = prompt('新书架名称：', '我的书架'); if (!name) return;
      T.createShelf(name.trim()).then(function () { toast('已创建书架，把书脊拖进去即可'); refreshAll(); });
    };
    document.body.addEventListener('click', function (e) {
      if (!e.target.closest('#detail') && !e.target.closest('.spine') && !e.target.closest('.book')) closeDetail();
      if (e.target.id === 'modal') closeModal();
    });
    document.addEventListener('keydown', function (e) { if (e.key === 'Escape') { closeDetail(); closeModal(); } });
  }
  function syncTabs() {
    $('#viewTabs').querySelectorAll('button').forEach(function (b) { b.classList.toggle('on', b.dataset.view === S.view) });
  }

  boot();
})();
