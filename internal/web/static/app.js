(() => {
  'use strict';

  const $ = (id) => document.getElementById(id);

  const fields = {
    memoryLimit: $('memoryLimit'),
    headroom: $('headroom'),
    threadCount: $('threadCount'),
    loadedClasses: $('loadedClasses'),
    jvmClasses: $('jvmClasses'),
    appClasses: $('appClasses'),
    agentClasses: $('agentClasses'),
    adjustment: $('adjustment'),
    lowProfile: $('lowProfile'),
    javaToolOptions: $('javaToolOptions'),
    jvmVersion: $('jvmVersion'),
  };

  const el = {
    headroomOut: $('headroomOut'),
    status: $('status'),
    error: $('error'),
    bar: $('bar'),
    legend: $('legend'),
    notes: $('notes'),
    regionRows: $('regionRows'),
    optionsOut: $('optionsOut'),
    copy: $('copyOptions'),
    formula: $('formula'),
    resolved: $('resolved'),
    popover: $('infoPopover'),
    classPath: $('classPath'),
    classDetails: $('classDetails'),
    classDetailsSummary: $('classDetailsSummary'),
    classDetailsHint: $('classDetailsHint'),
    classAppPanel: $('classAppPanel'),
    classManualPanel: $('classManualPanel'),
    manualTotalPanel: $('manualTotalPanel'),
    classPathStatus: $('classPathStatus'),
    pickFolder: $('pickFolder'),
    pickJar: $('pickJar'),
    countClasses: $('countClasses'),
    results: document.querySelector('.results'),
    statTotal: $('statTotal'),
    statTotalSub: $('statTotalSub'),
    statHeap: $('statHeap'),
    statHeapSub: $('statHeapSub'),
    statNonHeap: $('statNonHeap'),
    statNonHeapSub: $('statNonHeapSub'),
    statThreads: $('statThreads'),
    statThreadsSub: $('statThreadsSub'),
  };

  const STORAGE_KEY = 'memory-calc-web.inputs.v1';
  const PERSIST = Object.keys(fields);
  const defaultClassPathHint = el.classPathStatus.textContent;
  const pickerLabels = new Map([
    [el.pickFolder, 'Choose folder…'],
    [el.pickJar, 'Choose JAR…'],
    [el.countClasses, 'Count'],
  ]);

  let timer = null;
  let seq = 0;
  let regionInfo = new Map();
  let activeInfo = null;
  let classPathBusy = false;
  let lastLoadedClassCount = null;

  // ----- state persistence -------------------------------------------------

  function save() {
    const data = {};
    for (const key of PERSIST) {
      const field = fields[key];
      data[key] = field.type === 'checkbox' ? field.checked : field.value;
    }

    const classMode = document.querySelector('input[name="classMode"]:checked');
    const manualMode = document.querySelector('input[name="manualClassMode"]:checked');
    data._classMode = classMode ? classMode.value : 'app';
    data._manualClassMode = manualMode ? manualMode.value : 'total';

    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(data));
    } catch {
      // localStorage may be unavailable; persistence is a convenience only.
    }
  }

  function restore() {
    let data;
    try {
      data = JSON.parse(localStorage.getItem(STORAGE_KEY) || 'null');
    } catch {
      return;
    }
    if (!data) return;

    for (const key of PERSIST) {
      if (!(key in data)) continue;
      const field = fields[key];
      if (field.type === 'checkbox') {
        field.checked = Boolean(data[key]);
      } else {
        field.value = data[key];
      }
    }

    if (data._classMode) {
      const radio = document.querySelector(`input[name="classMode"][value="${data._classMode}"]`);
      if (radio) radio.checked = true;
    }
    if (data._manualClassMode) {
      const radio = document.querySelector(`input[name="manualClassMode"][value="${data._manualClassMode}"]`);
      if (radio) radio.checked = true;
    }
  }

  // ----- class count modes -------------------------------------------------

  function currentClassMode() {
    const radio = document.querySelector('input[name="classMode"]:checked');
    return radio ? radio.value : 'app';
  }

  function currentManualClassMode() {
    const radio = document.querySelector('input[name="manualClassMode"]:checked');
    return radio ? radio.value : 'total';
  }

  function applyClassMode() {
    const appMode = currentClassMode() === 'app';
    const breakdown = !appMode && currentManualClassMode() === 'breakdown';

    el.classAppPanel.hidden = !appMode;
    el.classManualPanel.hidden = appMode;
    el.manualTotalPanel.hidden = appMode || currentManualClassMode() !== 'total';
    el.classDetails.hidden = !(appMode || breakdown);

    // In Count from app mode the app count comes from the picker. In
    // Manual > Breakdown all four values are editable.
    fields.appClasses.readOnly = appMode;

    el.classDetailsSummary.textContent = appMode ? 'Class count assumptions' : 'Class count breakdown';
    el.classDetailsHint.textContent = appMode
      ? 'App classes comes from the count above. These values are combined with it, then multiplied by 0.35.'
      : 'Loaded class count is derived from these values and adjustment, then multiplied by 0.35.';

    if (appMode) {
      el.classDetails.open = false;
    } else if (breakdown) {
      el.classDetails.open = true;
    }
  }

  function handleClassModeChange() {
    const manualTotal = currentClassMode() === 'manual' && currentManualClassMode() === 'total';
    if (manualTotal && fields.loadedClasses.value.trim() === '') {
      fields.loadedClasses.value = lastLoadedClassCount != null ? String(lastLoadedClassCount) : '0';
    }

    applyClassMode();
    save();
    schedule();
  }

  // ----- request -----------------------------------------------------------

  function numberOrNull(input) {
    const raw = input.value.trim();
    if (raw === '') return null;
    const value = Number(raw);
    return Number.isFinite(value) ? value : null;
  }

  function buildPayload() {
    const manualTotal = currentClassMode() === 'manual' && currentManualClassMode() === 'total';
    const loadedClassCount = manualTotal ? (numberOrNull(fields.loadedClasses) ?? 0) : null;

    return {
      memoryLimit: fields.memoryLimit.value.trim(),
      headroomPercent: Number(fields.headroom.value) || 0,
      threadCount: numberOrNull(fields.threadCount),
      loadedClassCount,
      classCounts: {
        jvm: Number(fields.jvmClasses.value) || 0,
        app: Number(fields.appClasses.value) || 0,
        agent: Number(fields.agentClasses.value) || 0,
        adjustment: fields.adjustment.value.trim(),
      },
      lowProfile: fields.lowProfile.checked,
      javaToolOptions: fields.javaToolOptions.value,
      jvmVersion: fields.jvmVersion.value.trim(),
    };
  }

  function schedule() {
    save();
    clearTimeout(timer);
    timer = setTimeout(run, 160);
  }

  async function run() {
    const current = ++seq;
    setStatus('Calculating', 'busy');
    el.results.classList.add('loading');

    try {
      const response = await fetch('/api/calculate', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(buildPayload()),
      });
      const data = await response.json();
      if (current !== seq) return;
      render(data);
    } catch (err) {
      if (current !== seq) return;
      el.results.classList.remove('loading');
      el.results.classList.add('has-error');
      showError(`Unable to reach the calculator: ${err.message}`);
      clearResults();
      setStatus('Error', 'bad');
    }
  }

  // ----- class counting ----------------------------------------------------

  function setClassPathStatus(message, kind) {
    el.classPathStatus.textContent = message;
    el.classPathStatus.className = kind ? `hint ${kind}` : 'hint';
  }

  function setPickerBusy(busy, active) {
    classPathBusy = busy;

    for (const [button, label] of pickerLabels) {
      button.disabled = busy;
      button.textContent = busy && button === active ? 'Working…' : label;
    }
  }

  async function postJSON(url, body) {
    const response = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    return response.json();
  }

  async function pickAndCount(kind, button) {
    if (classPathBusy) return;

    setPickerBusy(true, button);
    setClassPathStatus('Waiting for a selection…');

    try {
      const data = await postJSON('/api/pick-path', { kind });
      if (!data.ok) {
        setClassPathStatus(data.error || 'Unable to open the file dialog.', 'bad');
        return;
      }
      if (data.canceled) {
        setClassPathStatus(defaultClassPathHint);
        return;
      }

      el.classPath.value = data.path;
      await countClassesFromPath();
    } catch (err) {
      setClassPathStatus(`Unable to open the file dialog: ${err.message}`, 'bad');
    } finally {
      setPickerBusy(false);
    }
  }

  async function countClassesFromPath() {
    const path = el.classPath.value.trim();
    if (!path) {
      setClassPathStatus('Choose a folder or JAR, or paste a path first.', 'bad');
      el.classPath.focus();
      return;
    }

    setClassPathStatus('Counting classes…');

    try {
      const data = await postJSON('/api/count-classes', { path });
      if (!data.ok) {
        setClassPathStatus(data.error || 'Unable to count classes.', 'bad');
        return;
      }

      fields.appClasses.value = String(data.count);
      // Clear an explicit override so the counted value feeds the derivation.
      fields.loadedClasses.value = '';
      el.classPath.value = data.path;

      if (data.count > 0) {
        setClassPathStatus(
          `Found ${formatInt(data.count)} classes in ${data.path}. App classes updated; loaded class count is now derived.`,
          'ok',
        );
      } else {
        setClassPathStatus(`Found no classes in ${data.path}. App classes set to 0.`, 'bad');
      }

      schedule();
    } catch (err) {
      setClassPathStatus(`Unable to count classes: ${err.message}`, 'bad');
    }
  }

  // ----- rendering ---------------------------------------------------------

  function render(data) {
    el.results.classList.remove('loading');
    hideInfo();

    if (!data.ok) {
      el.results.classList.add('has-error');
      showError(data.error || 'Calculation failed.');
      clearResults();
      renderNotes(data.notes || []);
      renderResolved(data.inputs);
      el.formula.textContent = data.inputs && data.inputs.loadedClassFormula
        ? `Formula: ${data.inputs.loadedClassFormula}`
        : '';
      setStatus('Error', 'bad');
      return;
    }

    el.results.classList.remove('has-error');
    hideError();
    setStatus('Live', '');

    const { inputs, totals, regions } = data;
    lastLoadedClassCount = inputs.loadedClassCount;

    if (currentClassMode() === 'app') {
      el.classDetailsSummary.textContent = `Class count assumptions · ${formatInt(inputs.loadedClassCount)} loaded`;
    } else if (currentManualClassMode() === 'breakdown') {
      el.classDetailsSummary.textContent = `Class count breakdown · ${formatInt(inputs.loadedClassCount)} loaded`;
    }

    el.statTotal.textContent = totals.total;
    el.statTotalSub.textContent = `${formatInt(totals.totalBytes)} bytes`;

    el.statHeap.textContent = totals.heap;
    el.statHeapSub.textContent = `${formatPercent(totals.heapPercent)} of total`;

    el.statNonHeap.textContent = totals.nonHeap;
    el.statNonHeapSub.textContent = `${formatPercent(totals.nonHeapPercent)} of total`;

    el.statThreads.textContent = formatInt(inputs.threadCount);
    el.statThreadsSub.textContent = inputs.threadCountSource;

    renderResolved(inputs);
    el.formula.textContent = inputs.loadedClassFormula ? `Formula: ${inputs.loadedClassFormula}` : '';

    renderBar(regions || [], totals);
    renderLegend(regions || [], totals);
    renderRegions(regions || []);
    renderNotes(data.notes || []);

    const options = data.options || {};
    el.optionsOut.textContent = options.final || '—';
    el.copy.disabled = !options.final;
  }

  function renderBar(regions, totals) {
    el.bar.innerHTML = '';

    const segments = regions
      .filter((region) => region.bytes > 0)
      .map((region) => ({ key: region.key, label: region.label, size: region.size, percent: region.percent }));

    if (totals.unallocatedBytes > 0 && totals.totalBytes > 0) {
      segments.push({
        key: 'free',
        label: 'Unallocated',
        size: totals.unallocated,
        percent: (totals.unallocatedBytes / totals.totalBytes) * 100,
      });
    }

    for (const segment of segments) {
      const node = document.createElement('div');
      node.className = `seg seg-${segment.key}`;
      node.dataset.key = segment.key;
      node.style.width = `${Math.max(segment.percent, 0.15)}%`;
      node.title = `${segment.label}: ${segment.size} (${formatPercent(segment.percent)})`;
      el.bar.appendChild(node);
    }
  }

  function renderLegend(regions, totals) {
    el.legend.innerHTML = '';

    const items = regions
      .filter((region) => region.bytes > 0)
      .map((region) => ({ key: region.key, label: region.label, size: region.size, percent: region.percent }));

    if (totals.unallocatedBytes > 0 && totals.totalBytes > 0) {
      items.push({
        key: 'free',
        label: 'Unallocated',
        size: totals.unallocated,
        percent: (totals.unallocatedBytes / totals.totalBytes) * 100,
      });
    }

    for (const item of items) {
      const node = document.createElement('button');
      node.type = 'button';
      node.className = 'legend-item';
      node.dataset.key = item.key;

      const swatch = document.createElement('span');
      swatch.className = `swatch seg-${item.key}`;

      const label = document.createElement('span');
      label.textContent = item.label;

      const value = document.createElement('strong');
      value.textContent = `${item.size} · ${formatPercent(item.percent)}`;

      node.append(swatch, label, value);

      node.addEventListener('mouseenter', () => highlight(item.key, true));
      node.addEventListener('mouseleave', () => highlight(item.key, false));
      node.addEventListener('focus', () => highlight(item.key, true));
      node.addEventListener('blur', () => highlight(item.key, false));

      el.legend.appendChild(node);
    }
  }

  function renderRegions(regions) {
    regionInfo = new Map(regions.map((region) => [region.key, region]));

    el.regionRows.innerHTML = regions
      .map(
        (region) => `
          <tr data-key="${escapeHTML(region.key)}">
            <td>
              <span class="swatch seg-${escapeHTML(region.key)}"></span><span class="region-name">${escapeHTML(region.label)}</span>${
                region.description
                  ? `<button type="button" class="info" data-key="${escapeHTML(region.key)}" aria-label="More about ${escapeHTML(region.label)}" aria-describedby="infoPopover">i</button>`
                  : ''
              }
              ${region.detail ? `<div class="region-detail">${escapeHTML(region.detail)}</div>` : ''}
            </td>
            <td class="num mono">${escapeHTML(region.size)}</td>
            <td class="num">${formatPercent(region.percent)}</td>
            <td class="mono flag">${escapeHTML(region.flag)}</td>
            <td><span class="prov ${escapeHTML(region.provenance)}">${escapeHTML(provenanceLabel(region.provenance))}</span></td>
          </tr>`,
      )
      .join('');
  }

  function showInfo(button) {
    if (activeInfo === button) return;

    const info = regionInfo.get(button.dataset.key);
    if (!info || !info.description) return;

    activeInfo = button;
    el.popover.innerHTML = '';

    const title = document.createElement('strong');
    title.textContent = info.label;
    el.popover.appendChild(title);

    for (const paragraph of info.description.split('\n\n')) {
      const node = document.createElement('p');
      node.textContent = paragraph;
      el.popover.appendChild(node);
    }

    positionInfo(button);
  }

  function positionInfo(button) {
    const pad = 10;
    const rect = button.getBoundingClientRect();

    el.popover.style.visibility = 'hidden';
    el.popover.hidden = false;

    const width = el.popover.offsetWidth;
    const height = el.popover.offsetHeight;

    let left = rect.left + rect.width / 2 - width / 2;
    left = Math.min(Math.max(pad, left), window.innerWidth - width - pad);

    let top = rect.bottom + 9;
    if (top + height > window.innerHeight - pad) {
      top = rect.top - height - 9;
    }
    top = Math.max(pad, top);

    el.popover.style.left = `${Math.round(left)}px`;
    el.popover.style.top = `${Math.round(top)}px`;
    el.popover.style.visibility = '';
  }

  function hideInfo() {
    activeInfo = null;
    el.popover.hidden = true;
    el.popover.innerHTML = '';
  }

  function renderNotes(notes) {
    el.notes.innerHTML = notes
      .map(
        (note) => `
          <div class="note ${note.kind === 'warn' ? 'warn' : ''}">
            <span class="note-dot" aria-hidden="true"></span>
            <span>${escapeHTML(note.text)}</span>
          </div>`,
      )
      .join('');
  }

  function renderResolved(inputs) {
    el.resolved.innerHTML = '';
    if (!inputs || !inputs.memoryLimit) return;

    const chips = [
      { main: `Total ${inputs.memoryLimit}` },
      { main: `Loaded classes ${formatInt(inputs.loadedClassCount)}`, sub: inputs.loadedClassSource },
      { main: `Head room ${inputs.headroomPercent}%` },
    ];

    if (inputs.threadCountSource) {
      chips.push({ main: `Threads ${formatInt(inputs.threadCount)}`, sub: sourceLabel(inputs.threadCountSource) });
    }

    if (inputs.lowProfile) {
      const chip = { main: 'Low profile on' };
      if (inputs.scalingFactor > 0) {
        chip.sub = `×${inputs.scalingFactor.toFixed(2)} scaling`;
      }
      chips.push(chip);
    }

    for (const chip of chips) {
      const node = document.createElement('span');
      node.className = 'chip';
      node.append(document.createTextNode(chip.main));

      if (chip.sub) {
        const small = document.createElement('small');
        small.textContent = chip.sub;
        node.appendChild(small);
      }

      el.resolved.appendChild(node);
    }
  }

  // ----- helpers -----------------------------------------------------------

  function setStatus(text, className) {
    el.status.textContent = text;
    el.status.className = `status ${className}`.trim();
  }

  function showError(message) {
    el.error.hidden = false;
    el.error.textContent = message;
  }

  function hideError() {
    el.error.hidden = true;
    el.error.textContent = '';
  }

  function clearResults() {
    hideInfo();

    for (const node of [el.statTotal, el.statHeap, el.statNonHeap, el.statThreads]) {
      node.textContent = '—';
    }
    for (const node of [el.statTotalSub, el.statHeapSub, el.statNonHeapSub, el.statThreadsSub]) {
      node.innerHTML = '&nbsp;';
    }

    el.bar.innerHTML = '';
    el.legend.innerHTML = '';
    el.regionRows.innerHTML = '';
    el.notes.innerHTML = '';
    el.optionsOut.textContent = '—';
    el.copy.disabled = true;
  }

  function highlight(key, on) {
    const selector = `[data-key="${CSS.escape(key)}"]`;

    el.bar.querySelectorAll(selector).forEach((node) => node.classList.toggle('active', on));
    el.legend.querySelectorAll(selector).forEach((node) => node.classList.toggle('active', on));
    el.regionRows.querySelectorAll(`tr${selector}`).forEach((node) => node.classList.toggle('active', on));
  }

  function formatInt(value) {
    if (!Number.isFinite(value)) return '0';
    return new Intl.NumberFormat().format(value);
  }

  function formatPercent(value) {
    if (!Number.isFinite(value)) return '0%';
    return `${value.toFixed(1)}%`;
  }

  function provenanceLabel(value) {
    switch (value) {
      case 'default':
        return 'default';
      case 'calculated':
        return 'calculated';
      case 'user':
        return 'user-set';
      default:
        return value || 'unknown';
    }
  }

  function sourceLabel(value) {
    switch (value) {
      case 'explicit':
        return 'input';
      case 'scaled':
        return 'scaled';
      default:
        return value;
    }
  }

  function escapeHTML(value) {
    return String(value ?? '')
      .replaceAll('&', '&amp;')
      .replaceAll('<', '&lt;')
      .replaceAll('>', '&gt;')
      .replaceAll('"', '&quot;')
      .replaceAll("'", '&#39;');
  }

  // ----- wiring ------------------------------------------------------------

  for (const field of Object.values(fields)) {
    field.addEventListener('input', schedule);
    field.addEventListener('change', schedule);
  }

  for (const field of Object.values(fields)) {
    if (field.tagName === 'INPUT' && field.type !== 'range' && field.type !== 'checkbox') {
      field.addEventListener('keydown', (event) => {
        if (event.key === 'Enter') {
          event.preventDefault();
          clearTimeout(timer);
          run();
        }
      });
    }
  }

  fields.headroom.addEventListener('input', () => {
    el.headroomOut.textContent = `${fields.headroom.value}%`;
  });

  document.querySelectorAll('.presets button[data-memory]').forEach((button) => {
    button.addEventListener('click', () => {
      fields.memoryLimit.value = button.dataset.memory;
      document.querySelectorAll('.presets button').forEach((other) => {
        other.classList.toggle('active', other === button);
      });
      clearTimeout(timer);
      save();
      run();
    });
  });

  fields.memoryLimit.addEventListener('input', () => {
    document.querySelectorAll('.presets button').forEach((button) => {
      button.classList.toggle('active', button.dataset.memory === fields.memoryLimit.value.trim());
    });
  });

  el.pickFolder.addEventListener('click', () => pickAndCount('folder', el.pickFolder));
  el.pickJar.addEventListener('click', () => pickAndCount('file', el.pickJar));

  el.countClasses.addEventListener('click', async () => {
    if (classPathBusy) return;
    setPickerBusy(true, el.countClasses);
    try {
      await countClassesFromPath();
    } finally {
      setPickerBusy(false);
    }
  });

  el.classPath.addEventListener('keydown', (event) => {
    if (event.key === 'Enter') {
      event.preventDefault();
      el.countClasses.click();
    }
  });

  el.classPath.addEventListener('input', () => {
    if (!classPathBusy) setClassPathStatus(defaultClassPathHint);
  });

  document.querySelectorAll('input[name="classMode"], input[name="manualClassMode"]').forEach((radio) => {
    radio.addEventListener('change', handleClassModeChange);
  });

  el.regionRows.addEventListener('mouseover', (event) => {
    const row = event.target.closest('tr[data-key]');
    if (row) highlight(row.dataset.key, true);
  });

  el.regionRows.addEventListener('mouseout', (event) => {
    const row = event.target.closest('tr[data-key]');
    if (row) highlight(row.dataset.key, false);
  });

  el.regionRows.addEventListener('mouseover', (event) => {
    const info = event.target.closest('.info');
    if (info) showInfo(info);
  });

  el.regionRows.addEventListener('mouseout', (event) => {
    const info = event.target.closest('.info');
    if (info && info === activeInfo) hideInfo();
  });

  el.regionRows.addEventListener('focusin', (event) => {
    const info = event.target.closest('.info');
    if (info) showInfo(info);
  });

  el.regionRows.addEventListener('focusout', (event) => {
    const info = event.target.closest('.info');
    if (info && info === activeInfo) hideInfo();
  });

  window.addEventListener(
    'scroll',
    () => {
      if (activeInfo) positionInfo(activeInfo);
    },
    true,
  );

  window.addEventListener('resize', () => {
    if (activeInfo) positionInfo(activeInfo);
  });

  document.addEventListener('keydown', (event) => {
    if (event.key === 'Escape') hideInfo();
  });

  el.copy.addEventListener('click', async () => {
    const text = el.optionsOut.textContent.trim();
    if (!text || text === '—') return;

    try {
      await navigator.clipboard.writeText(text);
    } catch {
      const area = document.createElement('textarea');
      area.value = text;
      document.body.appendChild(area);
      area.select();
      document.execCommand('copy');
      area.remove();
    }

    const original = el.copy.textContent;
    el.copy.textContent = 'Copied';
    setTimeout(() => {
      el.copy.textContent = original;
    }, 1200);
  });

  // ----- boot --------------------------------------------------------------

  restore();
  applyClassMode();
  el.headroomOut.textContent = `${fields.headroom.value}%`;
  document.querySelectorAll('.presets button').forEach((button) => {
    button.classList.toggle('active', button.dataset.memory === fields.memoryLimit.value.trim());
  });
  run();
})();
