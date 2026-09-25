(function() {
    function getTheme() {
        var savedTheme = localStorage.getItem('theme');
        if (savedTheme) {
            return savedTheme;
        }
        return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
    }

    function applyTheme(theme) {
        document.documentElement.setAttribute('data-theme', theme);

        // Keep the toggle naming what it does next and exposing what is on now.
        // It does not exist yet on the first call (this runs in <head>); the
        // DOMContentLoaded pass re-applies once it does.
        var toggle = document.getElementById('theme-toggle');
        if (toggle) {
            toggle.setAttribute('aria-label',
                theme === 'dark' ? 'Switch to light theme' : 'Switch to dark theme');
            toggle.setAttribute('aria-pressed', theme === 'dark' ? 'true' : 'false');
        }
    }

    // Applied before the page paints, so it never flashes the wrong theme.
    applyTheme(getTheme());

    document.addEventListener('DOMContentLoaded', function() {
        var toggle = document.getElementById('theme-toggle');
        if (toggle) {
            applyTheme(document.documentElement.getAttribute('data-theme') || getTheme());
            toggle.addEventListener('click', function() {
                var current = document.documentElement.getAttribute('data-theme');
                var nextTheme = current === 'dark' ? 'light' : 'dark';
                applyTheme(nextTheme);
                localStorage.setItem('theme', nextTheme); // stored only when the user picks one
            });
        }
    });

    // Follow the system theme until the user picks one.
    window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', function(e) {
        if (!localStorage.getItem('theme')) {
            applyTheme(e.matches ? 'dark' : 'light');
        }
    });
})();
