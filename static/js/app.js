// WorkSpace — dark/light theme toggle
(function () {
  'use strict';

  const STORAGE_KEY = 'workspace-theme';
  const DARK = 'dark';
  const LIGHT = 'light';

  function getPreferred() {
    const stored = localStorage.getItem(STORAGE_KEY);
    if (stored === DARK || stored === LIGHT) return stored;
    return window.matchMedia('(prefers-color-scheme: dark)').matches ? DARK : LIGHT;
  }

  function applyTheme(theme) {
    document.documentElement.setAttribute('data-theme', theme);
    const btn = document.getElementById('themeToggle');
    if (btn) {
      btn.textContent = theme === DARK ? '☀️' : '🌙';
      btn.title = theme === DARK ? 'Switch to light mode' : 'Switch to dark mode';
    }
    localStorage.setItem(STORAGE_KEY, theme);
  }

  // Apply theme immediately to avoid flash
  applyTheme(getPreferred());

  document.addEventListener('DOMContentLoaded', function () {
    const btn = document.getElementById('themeToggle');
    if (!btn) return;

    btn.addEventListener('click', function () {
      const current = document.documentElement.getAttribute('data-theme') || LIGHT;
      applyTheme(current === DARK ? LIGHT : DARK);
    });
  });
})();
