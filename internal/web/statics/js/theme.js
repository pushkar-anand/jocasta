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

    // Set initial theme early to prevent flash of unstyled content
    applyTheme(getTheme());

    document.addEventListener('DOMContentLoaded', function() {
        var toggle = document.getElementById('theme-toggle');
        if (toggle) {
            applyTheme(document.documentElement.getAttribute('data-theme') || getTheme());
            toggle.addEventListener('click', function() {
                var current = document.documentElement.getAttribute('data-theme');
                var nextTheme = current === 'dark' ? 'light' : 'dark';
                applyTheme(nextTheme);
                localStorage.setItem('theme', nextTheme); // Only explicitly set on user action
            });
        }
    });

    // Listen for system theme changes
    window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', function(e) {
        if (!localStorage.getItem('theme')) {
            applyTheme(e.matches ? 'dark' : 'light');
        }
    });
})();
