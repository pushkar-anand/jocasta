// The menu drawer that holds the sections below 60rem. The sidebar moves into
// the drawer while it is open and back when it closes, so the page carries
// one copy of the sections. The dialog element itself traps focus, closes on
// Escape and hands focus back to the menu button; this opens it, and closes
// it on a tap outside, on the close button, on picking a section, and when
// the screen widens enough for the sidebar to return.
//
// The CSP forbids inline script, so this ships as a file.
(function () {
    'use strict';

    var drawer = document.getElementById('drawer');
    var sidebar = document.querySelector('.sidebar');
    var open = document.querySelector('.topbar__menu');
    if (!drawer || !sidebar || !open || typeof drawer.showModal !== 'function') {
        return;
    }

    open.addEventListener('click', function () {
        drawer.append(sidebar);
        drawer.showModal();

        // Start on the section being shown rather than the close button.
        var current = sidebar.querySelector('[aria-current="page"]');
        if (current) {
            current.focus();
        }
    });

    drawer.addEventListener('close', function () {
        drawer.before(sidebar);
    });

    drawer.addEventListener('click', function (evt) {
        // The backdrop belongs to the dialog, so a tap on it arrives here
        // too; only its position tells it apart from a tap on the drawer.
        var box = drawer.getBoundingClientRect();
        var outside = evt.clientX > box.right || evt.clientY > box.bottom;
        if (outside || evt.target.closest('.drawer__close, a')) {
            drawer.close();
        }
    });

    var wide = window.matchMedia('(min-width: 60.0625rem)');
    wide.addEventListener('change', function () {
        if (wide.matches && drawer.open) {
            drawer.close();
        }
    });
})();
