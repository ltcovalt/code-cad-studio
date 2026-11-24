(function () {
  try {
    var saved = localStorage.getItem('code-cad-theme');
    if (saved) {
      document.documentElement.setAttribute('data-theme', saved);
    }
  } catch (_e) {
    // ignore storage access errors
  }
})();
