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
    }

    // Set initial theme early to prevent flash of unstyled content
    applyTheme(getTheme());

    document.addEventListener('DOMContentLoaded', function() {
        var toggle = document.getElementById('theme-toggle');
        if (toggle) {
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
