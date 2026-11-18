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
function filterSystemHealth() {
 const query=(document.querySelector('#health-search')?.value || '').toLowerCase();
 const issuesOnly=!!document.querySelector('#health-issues-only')?.checked;
 let visible=0;
 document.querySelectorAll('#system-health-checks .health-check').forEach(card => {
  const issue=['attention','blocked','unknown'].includes(card.dataset.state);
  card.hidden=(issuesOnly && !issue) || !card.textContent.toLowerCase().includes(query);
  if(!card.hidden) visible++;
 });
 const empty=document.querySelector('#health-empty');if(empty)empty.hidden=visible>0;
}
document.querySelector('#health-search')?.addEventListener('input',filterSystemHealth);
document.querySelector('#health-issues-only')?.addEventListener('change',filterSystemHealth);
function renderSystemHealth(health) {
 const body=document.querySelector('#system-health-checks');if(!body)return;
 body.replaceChildren();
 for(const check of health.Checks || []) {
  const card=document.createElement('article');card.className='health-check';card.dataset.state=check.State;
  const heading=document.createElement('div');heading.className='health-check-heading';
  const symbol=document.createElement('span');symbol.className='health-symbol';symbol.setAttribute('aria-hidden','true');symbol.textContent=check.Symbol;
  const title=document.createElement('div');const group=document.createElement('small');group.textContent=check.Group;const name=document.createElement('h2');name.textContent=check.Name;title.append(group,name);
  const state=document.createElement('span');state.className='health-state';state.textContent=check.Label;heading.append(symbol,title,state);card.appendChild(heading);
  const summary=document.createElement('p');summary.textContent=check.Summary;card.appendChild(summary);
  if(check.Detail){const detail=document.createElement('p');detail.className='footnote';detail.textContent=check.Detail;card.appendChild(detail);}
  const footer=document.createElement('div');footer.className='health-check-footer';const at=document.createElement('small');at.textContent=check.ObservedAt && !check.ObservedAt.startsWith('0001') ? 'Observed '+check.ObservedAt+' UTC' : 'No timestamped observation';footer.appendChild(at);
  if(check.Href && /^\/(?!\/)/.test(check.Href)){const link=document.createElement('a');link.href=check.Href;link.textContent=check.Action+' →';footer.appendChild(link);}
  card.appendChild(footer);body.appendChild(card);
 }
 for(const [key,value,label] of [['attention',health.Attention,'need attention'],['blocked',health.Blocked,'blocked'],['unknown',health.Unknown,'not verified'],['ok',health.OK,'OK']]){const node=document.querySelector('[data-health-count="'+key+'"]');if(node)node.textContent=value+' '+label;}
 put('health-captured','Snapshot: '+health.CapturedAt+' UTC');
 put('connection',health.ConnectionConnected?'Connected':'Not connected');
 const stale=document.querySelector('#health-stale-warning');if(stale)stale.hidden=true;
 filterSystemHealth();
}
filterSystemHealth();
function updateRunnerLock(row,runner) {
 const reason=runner.LockReason || '';
 const summary=row.querySelector('[data-runner-lock-summary]');
 if(summary){summary.textContent=reason?'Local lock: '+reason:'No current-session lock.';summary.classList.toggle('error',!!reason);}
 const guidance=row.querySelector('[data-runner-lock-guidance]');if(guidance)guidance.textContent=reason?[(runner.LockPolicy || {}).Category,(runner.LockPolicy || {}).Guidance].filter(Boolean).join(' · '):'';
 let matching=0;
 for(const control of row.querySelectorAll('[data-lock-review]')){
  const intent=(runner.Status?.Intents || []).find(i=>i.ID===control.dataset.lockIntent);
  const matches=!!reason&&reason===control.dataset.lockReason&&runner.LockSession===control.dataset.lockSession&&intent?.Session===runner.LockSession;
  // Never offer a form after its actual lock/session disappears. Preserve an
  // unchanged active form so a polling refresh cannot erase typed passwords.
  if(!matches){control.remove();}else{matching++;}
 }
 const changed=row.querySelector('[data-runner-lock-changed]');if(changed)changed.hidden=!reason||matching>0||reason==='Local lock status unavailable';
 const history=row.querySelector('[data-runner-history]');if(history){history.replaceChildren();for(const intent of runner.Status?.Intents || []){if(!intent.HaltReason)continue;const entry=document.createElement('p');entry.textContent='Order #'+intent.OrderID+' · '+intent.Session+' · '+intent.HaltReason;history.appendChild(entry);}}
}
function updateReportStatus(channel,status){
 if(!status)return;
 const summary=document.querySelector('[data-report-summary="'+channel+'"]');if(summary)summary.textContent=(status.Configured?'Webhook saved.':'No webhook configured.')+' '+(status.Enabled?'Reports enabled.':'Reports disabled.');
 const error=document.querySelector('[data-report-error="'+channel+'"]');if(error){error.textContent=status.LastError || '';error.hidden=!status.LastError;}
 const last=document.querySelector('[data-report-last="'+channel+'"]');if(last)last.textContent=status.LastSent&&!status.LastSent.startsWith('0001')?'Last confirmed delivery: '+status.LastSent+' UTC':'No confirmed delivery yet.';
 const dropped=document.querySelector('[data-report-dropped="'+channel+'"]');if(dropped){dropped.textContent=(status.Dropped || 0)+' reports could not be queued earlier in this process (history).';dropped.hidden=!status.Dropped;}
}
const monitorInterval = document.querySelector('#connection-log-rows, [data-monitor="execution-state"], [data-profile-arming], #target-job-rows') ? 3000 : 10000;
async function monitor() {
  if (!document.hidden) {
    const healthPage=!!document.querySelector('#system-health-checks');
    const controller=new AbortController();
    const deadline=setTimeout(()=>controller.abort(),healthPage?4000:15000);
    try {
      const response = await fetch(healthPage?'/api/system-health':'/api/monitor', {credentials:'same-origin', cache:'no-store',signal:controller.signal});
      if (!response.ok) throw new Error('unavailable');
      const v = await response.json();
 document.querySelectorAll('[data-live-stale]').forEach(node=>node.hidden=true);
 if(healthPage){renderSystemHealth(v);setTimeout(monitor,monitorInterval);return;}
      const execution = document.querySelector('[data-execution-scope="manual"]') ? v.ManualExecution : v.Execution;
      put('execution-state', execution?.State || 'idle');
      put('execution-mode', v.Execution?.ModeLabel || (v.Execution?.RunRequested ? 'Automation running' : v.Execution?.Armed ? (v.Execution?.Automated ? 'Automatic trading active' : 'Manual trading active') : v.Execution?.Configured ? 'Execution configured · paused' : 'Read-only mode'));
      document.querySelectorAll('[data-profile-arming]').forEach(node => {
        const state = v.Profiles ? (v.Profiles[node.dataset.profileArming] || {}) : v.Execution;
        const running = state?.RunRequested && state?.RunProfileID === node.dataset.profileArming;
        const active = state?.Armed && state?.Automated && state?.AutomationProfileID === node.dataset.profileArming;
        const reason = state?.RunProfileID === node.dataset.profileArming ? state.RunMessage : '';
        const otherRun = state?.RunRequested && state.RunProfileID !== node.dataset.profileArming;
        const otherArmed = state?.Armed && state.Automated && !active;
        node.textContent = running ? state.RunMessage : active ? 'Session active' : otherRun ? 'Blocked · automation ' + state.RunProfileID + ' is running' : otherArmed ? 'Blocked · automation ' + state.AutomationProfileID + ' is armed' : state?.Armed && !state.Automated ? 'Paused · manual Practice session armed' : reason && reason !== 'Paused' ? 'Paused · ' + reason : 'Paused';
        document.querySelectorAll('[data-profile-start]').forEach(form => { if (form.dataset.profileStart === node.dataset.profileArming) form.hidden = running || active; });
        document.querySelectorAll('[data-profile-pause]').forEach(form => { if (form.dataset.profilePause === node.dataset.profileArming) form.hidden = !running && !active; });
      });
      document.querySelectorAll('[data-profile-summary]').forEach(node=>{const status=v.Profiles?.[node.dataset.profileSummary] || {};node.textContent=status.RunRequested?'Running':status.Armed?'Armed':node.dataset.routingMode==='automatic'?'Paused':'Review only';});
      updateReportStatus('system',v.SystemReports);updateReportStatus('trading',v.TradingReports);
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
 put('execution-exposure', (execution?.Orders || 0) + ' / ' + (execution?.Positions || 0));
 const gate = document.querySelector('[data-global-gate]');
 if (gate) gate.textContent = v.GlobalPaused ? 'Global pause active' : 'Global entry gate open';
 document.querySelectorAll('[data-global-resume]').forEach(form=>form.hidden=!v.GlobalPaused);
 const userStream = document.querySelector('[data-user-stream]');
 if (userStream) userStream.textContent = (v.UserData?.State || 'Unavailable') + ' · ' + (v.UserData?.Accounts || 0) + ' subscribed accounts';
 for (const runner of v.Runners || []) {
   const row = document.querySelector('[data-runner="' + runner.AccountID + '"]');
   if (!row) continue;
   updateRunnerLock(row,runner);
   const status = runner.Status || {};
   const state = row.querySelector('[data-runner-state]');
   if (state) state.textContent = (runner.Busy ? 'Processing delivery' : status.Armed ? 'Armed' : status.RecoveryPending ? 'Waiting for recovery' : status.RunRequested ? 'Waiting' : 'Paused / recovery') + ' · ' + (status.RunMessage || '');
   const management = row.querySelector('[data-runner-management]');
   if (management) management.textContent = [status.Error || '', status.SubmissionDetail?.Reason ? 'ProjectX rejection reason: ' + status.SubmissionDetail.Reason : '', ...(status.Intents || []).filter(i => !['closed','cancelled','rejected'].includes(i.State)).map(i => (i.Signal?.symbol || '') + ' ' + (i.Signal?.direction || '') + ' ' + i.State + ' · order #' + i.OrderID + ' · ' + i.Message)].join(' ');
 }
 const targets = document.querySelector('#target-job-rows');
 if (targets) {
   targets.replaceChildren();
   const duration = (a,b) => !a || !b || a.startsWith('0001') || b.startsWith('0001') ? '—' : (Date.parse(b)-Date.parse(a)).toFixed(1) + ' ms';
   for (const job of (v.TargetJobs || []).slice(0,100)) {
     const tr = document.createElement('tr');
     for (const value of [job.DeliveryID + ' / ' + job.ProfileID, job.AccountID ? '#' + job.AccountID : 'No account selected', (job.RoutingMode === 'round_robin' ? 'Distribution · ' : '') + job.State + ' · ' + job.Message, duration(job.ReceivedAt,job.DispatchedAt),duration(job.ChecksAt,job.SubmitAt),duration(job.SubmitAt,job.AcceptedAt),duration(job.ReceivedAt,job.AcceptedAt)]) {
       const td = document.createElement('td');td.textContent=value;tr.appendChild(td);
     }
     targets.appendChild(tr);
   }
 }
 put('execution-observed', execution?.ObservedAt || 'Not observed');
 put('execution-message', execution?.Message || '');
 document.querySelectorAll('[data-submission-reason]').forEach(node => { const scope=node.closest('[data-profile-execution-scope]'); const detail=scope ? v.Profiles?.[scope.dataset.profileExecutionScope]?.SubmissionDetail : execution?.SubmissionDetail; node.textContent=detail?.IntentID===node.dataset.submissionReason ? 'ProjectX rejection reason: ' + detail.Reason : ''; });
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
    } catch { document.querySelectorAll('[data-live-stale]').forEach(node=>node.hidden=false); put('monitor-health', 'Monitoring update failed. Reload or check the local service.'); put('connection-log-health', 'Log update failed. Check the local admin service.'); const stale=document.querySelector('#health-stale-warning');if(stale)stale.hidden=false; } finally {if(deadline)clearTimeout(deadline);}
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

const tradeManagerRouting = document.querySelector('[data-trade-manager-routing]');
function updateTradeManagerRouting() {
  if (!tradeManagerRouting) return;
  const multiple = tradeManagerRouting.value === 'multiple';
  document.querySelectorAll('[data-multi-account-options]').forEach(group => {
    group.hidden = !multiple;
    group.querySelectorAll('select').forEach(select => select.disabled = !multiple);
  });
  document.querySelectorAll('[data-account-risk-overrides]').forEach(group => {
    group.hidden = !multiple;
    group.querySelectorAll('textarea').forEach(field => field.disabled = !multiple);
  });
  document.querySelectorAll('[data-account-risk]').forEach(field => {
    const checkbox = document.querySelector('input[name="account_ids"][value="' + field.dataset.accountRisk + '"]');
    field.disabled = !multiple || !checkbox?.checked;
  });
}
tradeManagerRouting?.addEventListener('change', updateTradeManagerRouting);
updateTradeManagerRouting();

document.querySelectorAll('input[name="account_ids"]').forEach(field => field.addEventListener('change', updateTradeManagerRouting));
