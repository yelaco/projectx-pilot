'use strict';
(() => {
  const button = document.getElementById('install');
  const help = document.getElementById('install-help');
  if (!button || !help) return;
  const standalone = window.matchMedia('(display-mode: standalone)');
  let pending = null;
  const update = () => { button.hidden = standalone.matches; if (standalone.matches) help.hidden = true; };
  update();
  standalone.addEventListener('change', update);
  window.addEventListener('beforeinstallprompt', event => {
    event.preventDefault();
    pending = event;
    update();
  });
  window.addEventListener('appinstalled', () => {
    pending = null;
    button.hidden = true;
    help.hidden = true;
  });
  button.addEventListener('click', async () => {
    if (!pending) { help.hidden = !help.hidden; return; }
    const prompt = pending;
    pending = null;
    button.disabled = true;
    try {
      await prompt.prompt();
      const choice = await prompt.userChoice;
      if (choice.outcome === 'accepted') button.hidden = true;
    } catch {
      help.hidden = false;
    } finally {
      button.disabled = false;
    }
  });
})();
