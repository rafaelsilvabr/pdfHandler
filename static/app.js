'use strict';

const ICONS = {
  merge: '<path d="M7 3v4c0 4 10 4 10 8v6M17 3v4c0 4-10 4-10 8v6M4 18l3 3 3-3m4 0 3 3 3-3"/>',
  split: '<path d="M12 21v-6c0-4-7-4-7-8V3m7 12c0-4 7-4 7-8V3M2 6l3-3 3 3m8 0 3-3 3 3"/>',
  compress: '<path d="M4 9h6V3m0 6L3 2m17 13h-6v6m0-6 7 7M16 3h5v5M3 16v5h5"/>',
  rotate: '<path d="M19 8a8 8 0 1 0 1 8M19 3v5h-5"/><rect x="9" y="9" width="6" height="8" rx="1"/>',
  'pdf-to-image': '<path d="M4 9V3h10l4 4v5M14 3v5h4"/><rect x="8" y="12" width="13" height="9" rx="1"/><path d="m9 20 4-4 3 3 2-2 3 3M3 13v7m-2-2 2 2 2-2M17 15h.01"/>',
  'image-to-pdf': '<rect x="3" y="3" width="12" height="9" rx="1"/><path d="m4 11 4-4 3 3 2-2 2 2M10 15v6h11V10h-3M14 16h4m-4 2h4M20 3v4m-2-2 2 2 2-2"/>',
  protect: '<rect x="5" y="10" width="14" height="11" rx="2"/><path d="M8 10V6a4 4 0 0 1 8 0v4m-4 5v2"/>',
  unlock: '<rect x="5" y="10" width="14" height="11" rx="2"/><path d="M8 10V6a4 4 0 0 1 8-1m-4 10v2"/>',
  file: '<path d="M6 3h8l4 4v14H6zM14 3v5h4M9 12h6m-6 4h4"/>',
  arrow: '<path d="M5 12h14m-5-5 5 5-5 5"/>',
  close: '<path d="m6 6 12 12M6 18 18 6"/>',
};
const icon = name => `<svg viewBox="0 0 24 24" aria-hidden="true">${ICONS[name]}</svg>`;
const GROUPS = [
  { id: 'organize', title: 'Organize & refine', number: '01' },
  { id: 'convert', title: 'Convert & create', number: '02' },
  { id: 'security', title: 'Protect & access', number: '03' },
];
const TOOLS = [
  { id: 'merge', title: 'Merge PDFs', desc: 'Bring multiple PDFs together in one document.', category: 'organize', format: 'PDF → PDF', multiple: true, accept: '.pdf', resultFilename: 'merged.pdf', optionsPanel: 'opt-none', preview: 'per-file', action: 'Merge PDFs' },
  { id: 'split', title: 'Split PDF', desc: 'Give every page its own place.', category: 'organize', format: 'PDF → ZIP', multiple: false, accept: '.pdf', resultFilename: 'split.zip', optionsPanel: 'opt-split', preview: 'full', action: 'Split PDF' },
  { id: 'compress', title: 'Compress PDF', desc: 'A smaller file. The same story.', category: 'organize', format: 'PDF → PDF', multiple: false, accept: '.pdf', resultFilename: 'compressed.pdf', optionsPanel: 'opt-compress', preview: 'full', action: 'Compress PDF' },
  { id: 'rotate', title: 'Rotate PDF', desc: 'Put a different perspective on your pages.', category: 'organize', format: 'PDF → PDF', multiple: false, accept: '.pdf', resultFilename: 'rotated.pdf', optionsPanel: 'opt-rotate', preview: 'selectable', action: 'Rotate PDF' },
  { id: 'pdf-to-image', title: 'PDF to Image', desc: 'Turn your pages into PNG or JPG images.', category: 'convert', format: 'PDF → IMG', multiple: false, accept: '.pdf', resultFilename: 'images.zip', optionsPanel: 'opt-pdf-to-image', preview: 'full', action: 'Convert to images' },
  { id: 'image-to-pdf', title: 'Image to PDF', desc: 'Make a document out of your images.', category: 'convert', format: 'IMG → PDF', multiple: true, accept: '.jpg,.jpeg,.png,.tif,.tiff,.webp', resultFilename: 'output.pdf', optionsPanel: 'opt-none', preview: 'images', action: 'Create PDF' },
  { id: 'protect', title: 'Protect PDF', desc: 'Keep your document behind a password.', category: 'security', format: 'PDF → PDF', multiple: false, accept: '.pdf', resultFilename: 'protected.pdf', optionsPanel: 'opt-protect', preview: 'full', action: 'Protect PDF' },
  { id: 'unlock', title: 'Unlock PDF', desc: 'Remove a password. Open things up.', category: 'security', format: 'PDF → PDF', multiple: false, accept: '.pdf', resultFilename: 'unlocked.pdf', optionsPanel: 'opt-unlock', preview: 'full', action: 'Unlock PDF' },
];
const $ = id => document.getElementById(id);
let maxUploadBytes = null;
let uploadConfigPromise = null;

function updateUploadHint() {
  if (!currentTool) return;
  const types = currentTool.id === 'image-to-pdf' ? 'JPG, PNG, TIFF, WEBP · up to 100 images' : `PDF documents · ${currentTool.id === 'merge' ? '2–50 files' : 'one file'}`;
  $('dz-hint').textContent = `${types} · ${maxUploadBytes === null ? 'checking upload limit' : `${formatBytes(maxUploadBytes)} maximum`}`;
}

function ensureUploadConfig() {
  if (!uploadConfigPromise) {
    uploadConfigPromise = (async () => {
      const response = await fetch('/api/config', { cache: 'no-store' });
      if (!response.ok) throw new Error('Could not load upload settings');
      const config = await response.json();
      if (!Number.isInteger(config.maxUploadBytes) || config.maxUploadBytes <= 0) throw new Error('Invalid upload settings');
      maxUploadBytes = config.maxUploadBytes;
      document.querySelectorAll('[data-upload-limit]').forEach(element => element.textContent = formatBytes(maxUploadBytes));
      updateUploadHint();
    })().catch(error => { uploadConfigPromise = null; throw error; });
  }
  return uploadConfigPromise;
}

let currentTool = null;
let currentView = 'home';
let selectedFiles = [];
let previewData = new Map();
let selectedPages = new Set();
let activeCategory = 'all';
let toolLayout = 'list';
let workspaceVersion = 0;
let previewController = new AbortController();
let processing = false;
let resultURL = null;
let imageURLs = [];
let lastToolButton = null;
let homeScrollY = 0;
let sidebarCollapsed = false;
const mobileSidebar = window.matchMedia('(max-width: 640px)');

function applySidebarPreference() {
  const hidden = sidebarCollapsed && !mobileSidebar.matches;
  document.querySelector('.app-shell').classList.toggle('sidebar-collapsed', sidebarCollapsed);
  $('sidebar').inert = hidden;
  if (hidden) $('sidebar').setAttribute('aria-hidden', 'true');
  else $('sidebar').removeAttribute('aria-hidden');
  $('sidebar-toggle').setAttribute('aria-expanded', String(!hidden));
  $('sidebar-toggle').setAttribute('aria-label', hidden ? 'Show sidebar' : 'Hide sidebar');
  $('sidebar-toggle').title = hidden ? 'Show sidebar' : 'Hide sidebar';
}

function recordView(view, toolId = null) {
  const path = view === 'about' ? '/about' : '/';
  if (location.pathname !== path || history.state?.view !== view || history.state?.toolId !== toolId) {
    history.pushState({ view, toolId }, '', path);
  }
}

function applyToolLayout() {
  $('tools-grid').classList.toggle('tools-blocks', toolLayout === 'blocks');
  document.querySelectorAll('.view-option').forEach(button => button.setAttribute('aria-pressed', String(button.dataset.layout === toolLayout)));
}

function restoreView(state, moveFocus = true) {
  if (processing) {
    recordView(currentView, currentTool?.id || null);
    return;
  }
  if (location.pathname === '/about') showAbout(false, moveFocus);
  else {
    const tool = state?.view === 'tool' && TOOLS.find(item => item.id === state.toolId);
    if (tool) openTool(tool, false, moveFocus);
    else showHome(false, moveFocus);
  }
}

function renderNavigation() {
  for (const group of GROUPS) {
    const heading = document.createElement('p');
    heading.className = 'nav-heading';
    heading.textContent = group.id;
    $('tool-navigation').append(heading);
    for (const tool of TOOLS.filter(t => t.category === group.id)) {
      const button = document.createElement('button');
      button.className = 'nav-item';
      button.dataset.tool = tool.id;
      button.innerHTML = `${icon(tool.id)}<span>${tool.title}</span>`;
      button.addEventListener('click', () => openTool(tool));
      $('tool-navigation').append(button);
    }
  }
}

function renderTools() {
  const query = $('tool-search').value.trim().toLowerCase();
  const matches = TOOLS.filter(tool => (activeCategory === 'all' || tool.category === activeCategory) && `${tool.title} ${tool.desc} ${tool.category}`.toLowerCase().includes(query));
  $('tools-grid').replaceChildren();
  for (const group of GROUPS) {
    const tools = matches.filter(tool => tool.category === group.id);
    if (!tools.length) continue;
    const section = document.createElement('section');
    section.className = 'tool-group';
    section.setAttribute('aria-labelledby', `group-${group.id}`);
    section.innerHTML = `<h3 class="group-heading" id="group-${group.id}"><span>${group.number}</span><span>${group.title}</span></h3>`;
    for (const tool of tools) {
      const row = document.createElement('button');
      row.className = 'tool-row';
      row.dataset.tool = tool.id;
      row.innerHTML = `<span class="row-number" aria-hidden="true">${String(TOOLS.indexOf(tool) + 1).padStart(2, '0')}</span><span class="tool-icon">${icon(tool.id)}</span><span class="tool-copy"><strong>${tool.title}</strong><small>${tool.desc}</small></span><span class="tool-format">${tool.format}</span>${icon('arrow')}`;
      row.lastElementChild.classList.add('row-arrow');
      row.addEventListener('click', () => { lastToolButton = row; openTool(tool); });
      section.append(row);
    }
    $('tools-grid').append(section);
  }
  $('tool-count').textContent = `/ ${String(matches.length).padStart(2, '0')}`;
  $('empty-state').hidden = matches.length > 0;
  $('search-status').textContent = `${matches.length} tools available`;
}

function closeMenu() {
  $('sidebar-nav').classList.remove('is-open');
  $('menu-toggle').setAttribute('aria-expanded', 'false');
}

function setNavigation(id) {
  document.querySelectorAll('.nav-item').forEach(button => {
    if ((button.dataset.page || button.dataset.tool || 'all') === id) button.setAttribute('aria-current', 'page');
    else button.removeAttribute('aria-current');
  });
  if (id === 'about') $('footer-about').setAttribute('aria-current', 'page');
  else $('footer-about').removeAttribute('aria-current');
  closeMenu();
}

// Invalidate pending previews whenever their files or workspace change.
function invalidatePreviews() {
  workspaceVersion++;
  previewController.abort();
  previewController = new AbortController();
}

function clearImageURLs() {
  imageURLs.forEach(url => URL.revokeObjectURL(url));
  imageURLs = [];
}

function resetResult() {
  if (resultURL) URL.revokeObjectURL(resultURL);
  resultURL = null;
  $('result').style.display = 'none';
  $('result').replaceChildren();
}

function openTool(tool, updateHistory = true, moveFocus = true) {
  if (processing) return;
  if (currentView === 'home') homeScrollY = window.scrollY;
  invalidatePreviews();
  clearImageURLs();
  resetResult();
  currentTool = tool;
  currentView = 'tool';
  selectedFiles = [];
  previewData.clear();
  selectedPages.clear();
  // Passwords should not remain in hidden panels after changing tools.
  $('protect-password').value = $('unlock-password').value = '';
  $('rotate-pages').value = 'all';
  $('home-view').style.display = 'none';
  $('about-view').hidden = true;
  $('tool-panel').style.display = 'block';
  $('breadcrumb-current').textContent = tool.title;
  document.title = `${tool.title} — PDF Handler`;
  $('panel-icon').innerHTML = icon(tool.id);
  $('panel-title-text').textContent = tool.title;
  $('panel-desc-text').textContent = tool.desc;
  $('file-input').multiple = tool.multiple;
  $('file-input').accept = tool.accept;
  updateUploadHint();
  document.querySelectorAll('[id^="opt-"]').forEach(el => el.style.display = 'none');
  $(tool.optionsPanel).style.display = '';
  $('opt-none').textContent = tool.id === 'merge' ? 'Add at least two PDFs. They will be combined in the order shown below the upload area.' : 'Add your images in the order you want them to appear. Each image becomes one page.';
  $('output-format').textContent = tool.resultFilename.endsWith('.zip') ? 'ZIP archive' : 'PDF document';
  const selectable = tool.preview === 'selectable';
  $('select-hint').style.display = selectable ? 'inline' : 'none';
  $('select-all-btn').style.display = $('clear-sel-btn').style.display = selectable ? 'inline-block' : 'none';
  renderFileList();
  resetPreview();
  setNavigation(tool.id);
  if (updateHistory) recordView('tool', tool.id);
  window.scrollTo(0, 0);
  if (moveFocus) $('panel-title-text').focus({ preventScroll: true });
}

function showHome(updateHistory = true, moveFocus = true) {
  if (processing) return;
  invalidatePreviews();
  clearImageURLs();
  resetResult();
  $('protect-password').value = $('unlock-password').value = '';
  selectedFiles = [];
  previewData.clear();
  currentTool = null;
  currentView = 'home';
  $('tool-panel').style.display = 'none';
  $('about-view').hidden = true;
  $('home-view').style.display = 'block';
  $('breadcrumb-current').textContent = 'All tools';
  document.title = 'PDF Handler — The document workspace';
  setNavigation('all');
  if (updateHistory) recordView('home');
  window.scrollTo(0, homeScrollY);
  if (moveFocus) (lastToolButton?.isConnected ? lastToolButton : $('home-title')).focus({ preventScroll: true });
}

function showAbout(updateHistory = true, moveFocus = true) {
  if (processing) return;
  if (currentView === 'home') homeScrollY = window.scrollY;
  invalidatePreviews();
  clearImageURLs();
  resetResult();
  $('protect-password').value = $('unlock-password').value = '';
  selectedFiles = [];
  previewData.clear();
  selectedPages.clear();
  currentTool = null;
  currentView = 'about';
  $('home-view').style.display = 'none';
  $('tool-panel').style.display = 'none';
  $('about-view').hidden = false;
  $('breadcrumb-current').textContent = 'About';
  document.title = 'About — PDF Handler';
  setNavigation('about');
  if (updateHistory) recordView('about');
  window.scrollTo(0, 0);
  if (moveFocus) $('about-title').focus({ preventScroll: true });
}

function updateProcessButton() {
  const validCount = selectedFiles.length >= (currentTool?.id === 'merge' ? 2 : 1);
  const needsPassword = currentTool?.id === 'protect' || currentTool?.id === 'unlock';
  const password = needsPassword ? $(`${currentTool.id}-password`).value : true;
  $('process-btn').disabled = processing || !validCount || !password;
  if (!processing) $('process-btn').innerHTML = `${currentTool?.action || 'Process document'} ${icon('arrow')}`;
}

async function addFiles(files) {
  if (!currentTool || processing || !files.length) return;
  const tool = currentTool;
  try { await ensureUploadConfig(); }
  catch { showError('Could not load the upload limit. Please try again.'); return; }
  if (currentTool !== tool || processing) return;
  const accepted = currentTool.accept.split(',');
  if (files.some(file => !accepted.includes('.' + file.name.split('.').pop().toLowerCase()))) {
    showError(`Choose ${currentTool.id === 'image-to-pdf' ? 'a supported image format' : 'PDF files'} for this tool.`);
    return;
  }
  const nextFiles = currentTool.multiple ? [...selectedFiles, ...files] : [files[0]];
  if (nextFiles.length > (currentTool.id === 'merge' ? 50 : 100)) { showError('Too many files. Remove some files and try again.'); return; }
  // Leave room for multipart form boundaries under the configured request cap.
  if (nextFiles.reduce((sum, file) => sum + file.size, 0) > maxUploadBytes - 64 * 1024) { showError(`This upload exceeds the ${formatBytes(maxUploadBytes)} request limit. Choose smaller files.`); return; }
  invalidatePreviews();
  selectedFiles = nextFiles;
  if (!currentTool.multiple) { previewData.clear(); selectedPages.clear(); $('rotate-pages').value = 'all'; }
  resetResult();
  renderFileList();
  await refreshPreviews();
}

async function refreshPreviews() {
  if (!selectedFiles.length) { resetPreview(); return; }
  if (currentTool.preview === 'images') { showImagePreviews(); return; }
  const version = workspaceVersion;
  const signal = previewController.signal;
  const full = currentTool.preview !== 'per-file';
  if (full) { showPreviewArea(true); $('preview-label').textContent = ''; $('thumbs-grid').replaceChildren(); }
  const files = [...selectedFiles];
  for (const file of files) {
    if (version !== workspaceVersion) return;
    if (!full && previewData.has(file)) continue;
    const fd = new FormData();
    fd.append('file', file);
    if (!full) fd.append('limit', '1');
    try {
      const res = await fetch('/api/preview', { method: 'POST', body: fd, signal });
      const data = await res.json();
      if (version !== workspaceVersion) return;
      if (!res.ok) throw new Error(data.error || 'Could not load this document');
      previewData.set(file, data);
      if (full) { showPreviewArea(false); renderThumbnails(data); }
      else renderFileList();
    } catch (error) {
      if (version !== workspaceVersion || error.name === 'AbortError') return;
      if (full) { showPreviewArea(false); $('preview-label').textContent = `Preview unavailable. You can still process your file. ${error.message}`; }
    }
    if (full) break;
  }
}

function renderFileList() {
  $('file-list').replaceChildren();
  $('drop-zone').classList.toggle('has-files', selectedFiles.length > 0);
  $('file-count').textContent = selectedFiles.length ? `${String(selectedFiles.length).padStart(2, '0')} selected · ${formatBytes(selectedFiles.reduce((sum, file) => sum + file.size, 0))}` : 'No files selected';
  selectedFiles.forEach((file, index) => {
    const item = document.createElement('div');
    item.className = 'file-item';
    item.innerHTML = `<div class="file-thumb-placeholder">${icon('file')}</div><span class="fname"></span><span class="fsize">${formatBytes(file.size)}</span><button class="remove-file" type="button">${icon('close')}</button>`;
    item.querySelector('.fname').textContent = file.name;
    const thumb = previewData.get(file)?.thumbnails?.[0];
    if (thumb) { const img = document.createElement('img'); img.className = 'file-thumb'; img.src = thumb; img.alt = ''; item.firstChild.replaceWith(img); }
    const remove = item.querySelector('button');
    remove.setAttribute('aria-label', `Remove ${file.name}`);
    remove.disabled = processing;
    remove.addEventListener('click', () => {
      invalidatePreviews();
      selectedFiles.splice(index, 1);
      previewData.delete(file);
      selectedPages.clear();
      $('rotate-pages').value = 'all';
      resetResult();
      renderFileList();
      refreshPreviews();
      const remaining = $('file-list').querySelectorAll('button');
      (remaining[Math.min(index, remaining.length - 1)] || $('drop-zone')).focus();
    });
    $('file-list').append(item);
  });
  updateProcessButton();
}

function showImagePreviews() {
  clearImageURLs();
  showPreviewArea(false);
  $('thumbs-grid').replaceChildren();
  selectedFiles.forEach((file, index) => {
    const url = URL.createObjectURL(file);
    imageURLs.push(url);
    const thumb = document.createElement('div');
    thumb.className = 'thumb';
    thumb.innerHTML = `<img alt="" /><div class="thumb-num">${index + 1}</div>`;
    thumb.querySelector('img').src = url;
    thumb.querySelector('img').alt = file.name;
    // TIFF can be processed by the server even when the browser cannot render it.
    thumb.querySelector('img').addEventListener('error', event => {
      const fallback = document.createElement('div');
      fallback.className = 'image-preview-unavailable';
      fallback.innerHTML = `${icon('file')}<span>Preview unavailable</span>`;
      fallback.title = file.name;
      event.target.replaceWith(fallback);
    });
    $('thumbs-grid').append(thumb);
  });
  $('preview-label').textContent = `${selectedFiles.length} image${selectedFiles.length === 1 ? '' : 's'} · one image per page`;
}

function renderThumbnails(data) {
  $('preview-label').textContent = `${data.pageCount} page${data.pageCount === 1 ? '' : 's'}${data.truncated ? ` · showing first ${data.thumbnails.length}` : ''}`;
  $('thumbs-grid').replaceChildren();
  data.thumbnails.forEach((src, index) => {
    const page = index + 1;
    const thumb = document.createElement('div');
    thumb.className = 'thumb';
    thumb.dataset.page = page;
    thumb.innerHTML = `<img alt="Page ${page}" /><div class="thumb-num">${String(page).padStart(2, '0')}</div>`;
    thumb.querySelector('img').src = src;
    if (currentTool.preview === 'selectable') {
      thumb.classList.add('selectable');
      thumb.tabIndex = 0;
      thumb.setAttribute('role', 'button');
      thumb.setAttribute('aria-label', `Rotate page ${page}`);
      thumb.setAttribute('aria-pressed', String(selectedPages.has(page)));
      thumb.classList.toggle('selected', selectedPages.has(page));
      thumb.addEventListener('click', () => togglePage(page));
      thumb.addEventListener('keydown', event => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); togglePage(page); } });
    }
    $('thumbs-grid').append(thumb);
  });
  if (currentTool.id === 'split') updateSplitHighlight();
}

function togglePage(page) {
  if (processing) return;
  if (selectedPages.has(page)) selectedPages.delete(page);
  else selectedPages.add(page);
  syncPageSelection();
}

function syncPageSelection() {
  document.querySelectorAll('.thumb.selectable').forEach(thumb => {
    const selected = selectedPages.has(Number(thumb.dataset.page));
    thumb.classList.toggle('selected', selected);
    thumb.setAttribute('aria-pressed', String(selected));
  });
  $('rotate-pages').value = selectedPages.size ? [...selectedPages].sort((a, b) => a - b).join(',') : 'all';
}

function updateSplitHighlight() {
  const span = Math.max(1, Number($('split-span').value) || 1);
  document.querySelectorAll('.thumb[data-page]').forEach(thumb => {
    const group = Math.floor((Number(thumb.dataset.page) - 1) / span);
    thumb.classList.toggle('split-group-a', group % 2 === 0);
    thumb.classList.toggle('split-group-b', group % 2 !== 0);
    thumb.title = `Page ${thumb.dataset.page} · output file ${group + 1}`;
  });
}

function showPreviewArea(loading) {
  $('preview-area').style.display = 'block';
  $('preview-loading').style.display = loading ? 'flex' : 'none';
  $('thumbs-grid').style.display = loading ? 'none' : '';
}

function resetPreview() {
  $('preview-area').style.display = 'none';
  $('preview-label').textContent = '';
  $('thumbs-grid').replaceChildren();
}

function setBusy(busy) {
  processing = busy;
  document.querySelectorAll('.nav-item, .brand, .back-btn, .remove-file, .preview-actions button, .workspace-right input, .workspace-right select').forEach(control => control.disabled = busy);
  for (const link of [$('nav-about'), $('footer-about')]) {
    if (busy) { link.setAttribute('aria-disabled', 'true'); link.tabIndex = -1; }
    else { link.removeAttribute('aria-disabled'); link.removeAttribute('tabindex'); }
  }
  $('drop-zone').tabIndex = busy ? -1 : 0;
  $('drop-zone').setAttribute('aria-disabled', String(busy));
  $('tool-panel').setAttribute('aria-busy', String(busy));
  updateProcessButton();
}

async function processFiles() {
  if (!currentTool || $('process-btn').disabled) return;
  const visibleInputs = $(currentTool.optionsPanel).querySelectorAll('input, select');
  for (const input of visibleInputs) if (!input.reportValidity()) return;
  const fd = new FormData();
  selectedFiles.forEach(file => fd.append(currentTool.multiple ? 'files[]' : 'file', file));
  const fields = {
    split: { span: 'split-span' }, compress: { quality: 'compress-quality' },
    rotate: { degrees: 'rotate-degrees', pages: 'rotate-pages' },
    'pdf-to-image': { format: 'pti-format', dpi: 'pti-dpi' },
    protect: { password: 'protect-password' }, unlock: { password: 'unlock-password' },
  };
  for (const [field, id] of Object.entries(fields[currentTool.id] || {})) fd.append(field, $(id).value || (field === 'pages' ? 'all' : ''));
  resetResult();
  setBusy(true);
  $('process-btn').innerHTML = '<span class="spinner" aria-hidden="true"></span>Processing…';
  try {
    const res = await fetch(`/api/${currentTool.id}`, { method: 'POST', body: fd });
    if (!res.ok) { const data = await res.json().catch(() => ({})); throw new Error(data.error || 'Could not process this document. Please try again.'); }
    resultURL = URL.createObjectURL(await res.blob());
    $('result').innerHTML = '<div class="result-box result-success"><strong>Your file is ready.</strong><a class="btn btn-success"></a></div>';
    const link = $('result').querySelector('a');
    link.href = resultURL;
    link.download = currentTool.resultFilename;
    link.textContent = `Download ${currentTool.resultFilename}`;
    $('result').style.display = 'block';
  } catch (error) { showError(error.message); }
  finally { setBusy(false); }
}

function showError(message) {
  resetResult();
  $('result').innerHTML = '<div class="result-box result-error"><strong>Something needs your attention.</strong><span></span></div>';
  $('result').querySelector('span').textContent = message;
  $('result').style.display = 'block';
}

function formatBytes(bytes) {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

$('nav-overview').addEventListener('click', () => showHome());
$('brand-home').addEventListener('click', () => showHome());
$('back-btn').addEventListener('click', () => showHome());
$('about-tools-btn').addEventListener('click', () => showHome());
for (const id of ['nav-about', 'footer-about']) $(id).addEventListener('click', event => {
  if (event.button !== 0 || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return;
  event.preventDefault();
  showAbout();
});
$('sidebar-toggle').addEventListener('click', () => {
  sidebarCollapsed = !sidebarCollapsed;
  applySidebarPreference();
  try { localStorage.setItem('pdfhandler.sidebarCollapsed', String(sidebarCollapsed)); } catch { /* Preferences are optional when storage is restricted. */ }
});
mobileSidebar.addEventListener('change', applySidebarPreference);
window.addEventListener('popstate', event => restoreView(event.state));
$('menu-toggle').addEventListener('click', () => { const open = $('sidebar-nav').classList.toggle('is-open'); $('menu-toggle').setAttribute('aria-expanded', String(open)); });
$('guide-toggle').addEventListener('click', () => { const open = $('guide-content').classList.toggle('is-open'); $('guide-toggle').setAttribute('aria-expanded', String(open)); $('guide-content').setAttribute('aria-hidden', String(!open)); });
$('tool-search').addEventListener('input', renderTools);
document.querySelectorAll('.filter').forEach(button => button.addEventListener('click', () => {
  activeCategory = button.dataset.category;
  document.querySelectorAll('.filter').forEach(filter => filter.setAttribute('aria-pressed', String(filter === button)));
  renderTools();
}));
document.querySelectorAll('.view-option').forEach(button => button.addEventListener('click', () => {
  toolLayout = button.dataset.layout;
  applyToolLayout();
  try { localStorage.setItem('pdfhandler.toolLayout', toolLayout); } catch { /* Layout still works without persistent storage. */ }
}));
$('clear-search').addEventListener('click', () => { $('tool-search').value = ''; document.querySelector('[data-category="all"]').click(); $('tool-search').focus(); });
document.addEventListener('keydown', event => {
  const editing = event.target.matches('input, select, textarea, [contenteditable="true"]');
  if (event.key === '/' && !editing && currentView === 'home' && !event.ctrlKey && !event.metaKey && !event.altKey) { event.preventDefault(); $('tool-search').focus(); }
  if (event.key === 'Escape') { closeMenu(); if (event.target === $('tool-search')) { $('tool-search').value = ''; renderTools(); $('tool-search').blur(); } }
});
$('drop-zone').addEventListener('click', () => { if (!processing) $('file-input').click(); });
$('drop-zone').addEventListener('keydown', event => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); if (!processing) $('file-input').click(); } });
$('drop-zone').addEventListener('dragover', event => { event.preventDefault(); if (!processing) $('drop-zone').classList.add('dragover'); });
$('drop-zone').addEventListener('dragleave', event => { if (!$('drop-zone').contains(event.relatedTarget)) $('drop-zone').classList.remove('dragover'); });
$('drop-zone').addEventListener('drop', event => { event.preventDefault(); $('drop-zone').classList.remove('dragover'); addFiles([...event.dataTransfer.files]); });
$('file-input').addEventListener('change', () => { addFiles([...$('file-input').files]); $('file-input').value = ''; });
$('select-all-btn').addEventListener('click', () => { document.querySelectorAll('.thumb.selectable').forEach(thumb => selectedPages.add(Number(thumb.dataset.page))); syncPageSelection(); });
$('clear-sel-btn').addEventListener('click', () => { selectedPages.clear(); syncPageSelection(); });
$('split-span').addEventListener('input', updateSplitHighlight);
$('rotate-pages').addEventListener('input', () => {
  selectedPages.clear();
  const value = $('rotate-pages').value.trim();
  document.querySelectorAll('.thumb.selectable').forEach(thumb => {
    const page = Number(thumb.dataset.page);
    const selected = value === 'all' || value.split(',').some(part => { const bounds = part.trim().split('-').map(Number); return bounds.length === 1 ? bounds[0] === page : page >= bounds[0] && page <= bounds[1]; });
    if (selected) selectedPages.add(page);
    thumb.classList.toggle('selected', selected);
    thumb.setAttribute('aria-pressed', String(selected));
  });
});
['protect-password', 'unlock-password'].forEach(id => $(id).addEventListener('input', updateProcessButton));
$('process-btn').addEventListener('click', processFiles);
window.addEventListener('pagehide', () => { previewController.abort(); clearImageURLs(); if (resultURL) URL.revokeObjectURL(resultURL); });
renderNavigation();
renderTools();
try { sidebarCollapsed = localStorage.getItem('pdfhandler.sidebarCollapsed') === 'true'; } catch { /* Keep the sidebar visible when storage is restricted. */ }
try { toolLayout = localStorage.getItem('pdfhandler.toolLayout') === 'blocks' ? 'blocks' : 'list'; } catch { /* Use the default list without persistent storage. */ }
applySidebarPreference();
applyToolLayout();
restoreView(history.state, false);
history.replaceState({ view: currentView, toolId: currentTool?.id || null }, '', location.href);
ensureUploadConfig().catch(() => {
  document.querySelectorAll('[data-upload-limit]').forEach(element => element.textContent = 'Unavailable');
});
