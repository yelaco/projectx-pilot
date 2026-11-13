'use strict';
function filterActivity() {
  const level = document.querySelector('#activity-level')?.value || '';
  const query = (document.querySelector('#activity-search')?.value || '').toLowerCase();
  document.querySelectorAll('#activity-rows tr').forEach(row => {
    row.hidden = (level && row.dataset.level !== level) || !row.textContent.toLowerCase().includes(query);
  });
}
document.querySelector('#activity-level')?.addEventListener('change', filterActivity);
document.querySelector('#activity-search')?.addEventListener('input', filterActivity);
const put = (name, value) => document.querySelectorAll('[data-monitor="' + name + '"]').forEach(node => { node.textContent = value; });
document.addEventListener('click', async event => {
  const reveal = event.target.closest('[data-reveal]');
  if (reveal) {
    const field = document.getElementById(reveal.dataset.reveal);
    if (field) { const visible = field.type === 'password'; field.type = visible ? 'text' : 'password'; reveal.textContent = visible ? 'Hide' : 'Reveal'; reveal.setAttribute('aria-pressed', String(visible)); }
    return;
  }
  const button = event.target.closest('[data-copy]');
  if (!button) return;
  const field = document.getElementById(button.dataset.copy);
  if (!field || !field.value) return;
  try { await navigator.clipboard.writeText(field.value); button.textContent = 'Copied'; }
  catch { field.focus(); field.select(); button.textContent = 'Selected — copy manually'; }
});
const automationForm = document.querySelector('form[action="/automation"]');
const managerSelect = automationForm?.querySelector('[name="trade_manager_id"]');
function updateAccountSelection() {
  const account = automationForm?.querySelector('[name="account_id"]');
  if (managerSelect && account) { account.disabled = Boolean(managerSelect.value); account.required = !managerSelect.value; }
}
managerSelect?.addEventListener('change', updateAccountSelection);
updateAccountSelection();
let connectionLogPaused = false;
function filterConnectionLog() {
  const component = document.querySelector('#connection-log-component')?.value || '';
  const query = (document.querySelector('#connection-log-search')?.value || '').toLowerCase();
  document.querySelectorAll('#connection-log-rows tr').forEach(row => {
    row.hidden = (component && row.dataset.component !== component) || !row.textContent.toLowerCase().includes(query);
  });
}
document.querySelector('#connection-log-component')?.addEventListener('change', filterConnectionLog);
document.querySelector('#connection-log-search')?.addEventListener('input', filterConnectionLog);
document.querySelector('#connection-log-pause')?.addEventListener('click', event => {
  connectionLogPaused = !connectionLogPaused;
  event.currentTarget.textContent = connectionLogPaused ? 'Resume log' : 'Pause log';
  event.currentTarget.setAttribute('aria-pressed', String(connectionLogPaused));
  put('connection-log-health', connectionLogPaused ? 'Log view paused · services continue running' : 'Live events · updates every 3 seconds');
});
function renderConnectionLog(rows) {
  const body = document.querySelector('#connection-log-rows');
  if (!body || connectionLogPaused) return;
  body.replaceChildren();
  for (const row of (rows || []).filter(row => row.Component === 'webhook' || row.Component === 'tunnel')) {
    const tr = document.createElement('tr'); tr.dataset.component = row.Component; tr.dataset.level = row.Level;
    for (const key of ['At', 'Level', 'Component', 'Message']) {
      const td = document.createElement('td');
      if (key === 'Level') { const badge = document.createElement('span'); badge.className = 'level'; badge.textContent = row[key]; td.appendChild(badge); }
      else { td.textContent = row[key]; }
      tr.appendChild(td);
    }
    body.appendChild(tr);
  }
  if (!body.children.length) { const tr = document.createElement('tr'); const td = document.createElement('td'); td.colSpan = 4; td.className = 'empty'; td.textContent = 'No webhook or tunnel events yet. Enable the preset or send an alert to start.'; tr.appendChild(td); body.appendChild(tr); }
  filterConnectionLog();
}
const monitorInterval = document.querySelector('#connection-log-rows, [data-monitor="execution-state"], [data-profile-arming]') ? 3000 : 10000;
async function monitor() {
  if (!document.hidden) {
    try {
      const response = await fetch('/api/monitor', {credentials:'same-origin', cache:'no-store'});
      if (!response.ok) throw new Error('unavailable');
      const v = await response.json();
      put('execution-state', v.Execution?.State || 'idle');
      put('execution-mode', v.Execution?.RunRequested ? 'Automation running' : v.Execution?.Armed ? (v.Execution?.Automated ? 'Automatic trading active' : 'Manual trading active') : v.Execution?.Configured ? 'Execution configured · paused' : 'Read-only mode');
      document.querySelectorAll('[data-profile-arming]').forEach(node => {
        const running = v.Execution?.RunRequested && v.Execution?.RunProfileID === node.dataset.profileArming;
        const active = v.Execution?.Armed && v.Execution?.Automated && v.Execution?.AutomationProfileID === node.dataset.profileArming;
        const reason = v.Execution?.RunProfileID === node.dataset.profileArming ? v.Execution.RunMessage : '';
        const otherRun = v.Execution?.RunRequested && v.Execution.RunProfileID !== node.dataset.profileArming;
        const otherArmed = v.Execution?.Armed && v.Execution.Automated && !active;
        node.textContent = running ? v.Execution.RunMessage : active ? 'Session active' : otherRun ? 'Blocked · automation ' + v.Execution.RunProfileID + ' is running' : otherArmed ? 'Blocked · automation ' + v.Execution.AutomationProfileID + ' is armed' : v.Execution?.Armed && !v.Execution.Automated ? 'Paused · manual Practice session armed' : reason && reason !== 'Paused' ? 'Paused · ' + reason : 'Paused';
        document.querySelectorAll('[data-profile-start]').forEach(form => { if (form.dataset.profileStart === node.dataset.profileArming) form.hidden = running; });
        document.querySelectorAll('[data-profile-pause]').forEach(form => { if (form.dataset.profilePause === node.dataset.profileArming) form.hidden = !running && !active; });
      });
      const outcomes = document.querySelector('#automation-outcomes');
      if (outcomes) {
        outcomes.replaceChildren();
        for (const result of (v.AutomationResults || []).filter(row => !outcomes.dataset.resultProfile || row.ProfileID === outcomes.dataset.resultProfile).slice(0, 10)) {
          const row = document.createElement('p');
          row.textContent = result.State + ' · ' + result.ID + ' · ' + result.Message;
          outcomes.appendChild(row);
        }
        if (!outcomes.children.length) outcomes.textContent = 'No automatic alerts processed.';
      }
 put('execution-exposure', (v.Execution?.Orders || 0) + ' / ' + (v.Execution?.Positions || 0));
 put('execution-observed', v.Execution?.ObservedAt || 'Not observed');
 put('execution-message', v.Execution?.Message || '');
 document.querySelectorAll('[data-submission-reason]').forEach(node => { const detail=v.Execution?.SubmissionDetail; node.textContent=detail?.IntentID===node.dataset.submissionReason ? 'ProjectX rejection reason: ' + detail.Reason : ''; });
 put('connection', v.State.connected ? 'Connected' : 'Not connected');
      put('account-name', v.State.selected_account?.name || 'Not selected');
      put('account-balance', v.State.selected_account ? new Intl.NumberFormat('en-US', {style:'currency', currency:'USD'}).format(v.State.selected_account.balance) : '—');
      put('receiving', v.ReceivingEnabled ? 'Enabled' : 'Disabled');
      document.querySelectorAll('[data-public-url]').forEach(field => { field.value = v.PublicURL || ''; });
      put('receiver', v.ReceiverListening ? 'Listening locally' : 'Unavailable');
      put('tunnel', v.Tunnel.State);
      document.querySelectorAll('[data-profile-url]').forEach(field => { field.value = v.WebhookURLs?.[field.dataset.profileUrl] || ''; });
      document.querySelectorAll('[data-copy]').forEach(button => { const field = document.getElementById(button.dataset.copy); if ((field?.hasAttribute('data-profile-url') || field?.hasAttribute('data-public-url'))) button.disabled = !field.value; });
      put('public-url-kind', v.PublicURLKind || 'Not configured');
      put('tunnel-error', v.Tunnel.Error || '');
      put('uptime', v.UptimeSeconds + ' seconds');
      put('pending-deliveries', v.DeliveryCounts?.pending_review || 0);
      put('pending-signals', v.SignalCounts?.pending_review || 0);
      put('snapshot', v.State.updated_at === '0001-01-01T00:00:00Z' ? 'No successful snapshot' : v.State.updated_at);
      put('connection-error', v.State.error || '');
      document.querySelectorAll('[data-monitor="connection-error"]').forEach(node => node.classList.toggle('error', Boolean(v.State.error))); 
      put('monitor-health', v.Error || 'Monitoring updates every 10 seconds. Configuration forms stay untouched.');
      if (!connectionLogPaused) put('connection-log-health', v.Error || 'Live events · updates every 3 seconds');
      renderConnectionLog(v.ConnectionActivity || v.Activity);
      const body = document.querySelector('#activity-rows');
      if (body) {
        body.replaceChildren();
        for (const row of (v.Activity || []).slice(0, Number(body.dataset.limit) || 100)) {
          const tr = document.createElement('tr'); tr.dataset.level = row.Level;
          for (const key of ['At','Level','Component','Message']) {
            const td = document.createElement('td');
            if (key === 'Level') { const badge = document.createElement('span'); badge.className = 'level'; badge.textContent = row[key]; td.appendChild(badge); }
            else { td.textContent = row[key]; }
            tr.appendChild(td);
          }
          body.appendChild(tr);
        }
        filterActivity();
      }
    } catch { put('monitor-health', 'Monitoring update failed. Reload or check the local service.'); put('connection-log-health', 'Log update failed. Check the local admin service.'); }
  }
  setTimeout(monitor, monitorInterval);
}
if (document.querySelector('[data-live-monitor]')) setTimeout(monitor, monitorInterval);

const mappingMode = document.querySelector('[data-mapping-mode]');
function updateMappingMode() {
  if (!mappingMode) return;
  document.querySelectorAll('[data-mapping-group]').forEach(group => {
    const active = group.dataset.mappingGroup === mappingMode.value;
    group.disabled = !active;
    group.hidden = !active;
  });
}
mappingMode?.addEventListener('change', updateMappingMode);
updateMappingMode();

const sessionMode = document.querySelector('[data-session-mode]');
function updateSessionMode() {
  const custom = document.querySelector('[data-session-custom]');
  if (!sessionMode || !custom) return;
  custom.disabled = sessionMode.value !== 'custom';
  custom.hidden = custom.disabled;
}
sessionMode?.addEventListener('change', updateSessionMode);
updateSessionMode();
