// Progressive enhancement for the settings screens: the account menu, the
// add-user / create-token modals, password reveal, copy-to-clipboard, and
// post-revoke focus. The
// CSP forbids inline script, so this ships as a file. Everything degrades: the
// menu is a native <details>, and a create rejected server-side comes back with
// <dialog open> so its error shows without this running.
(function () {
    'use strict';

    // ---- Account menu: close on Escape and outside click ----
    var menu = document.querySelector('.usermenu');
    if (menu) {
        document.addEventListener('click', function (e) {
            if (menu.open && !menu.contains(e.target)) {
                menu.open = false;
            }
        });
        document.addEventListener('keydown', function (e) {
            if (e.key === 'Escape' && menu.open) {
                menu.open = false;
                var summary = menu.querySelector('summary');
                if (summary) {
                    summary.focus();
                }
            }
        });
    }

    // ---- Modals ----
    function dialogFor(el) {
        var id = el.getAttribute('data-open');
        return id ? document.getElementById(id) : null;
    }

    document.addEventListener('click', function (e) {
        var opener = e.target.closest('[data-open]');
        if (opener) {
            var dlg = dialogFor(opener);
            if (dlg && typeof dlg.showModal === 'function' && !dlg.open) {
                dlg.showModal();
            }
            return;
        }

        var closer = e.target.closest('[data-close]');
        if (closer) {
            var owner = closer.closest('dialog');
            if (owner) {
                owner.close();
            }
            return;
        }
    });

    document.querySelectorAll('dialog.modal').forEach(function (dlg) {
        // A dialog rendered open (a rejected submit) is inline, not modal:
        // reopen it properly so it traps focus and dims the page.
        if (dlg.open && typeof dlg.showModal === 'function' && !dlg.matches(':modal')) {
            dlg.close();
            dlg.showModal();
        }

        // Click on the backdrop (outside the dialog's box) closes it.
        dlg.addEventListener('click', function (e) {
            if (e.target !== dlg) {
                return;
            }
            var box = dlg.getBoundingClientRect();
            var inside = e.clientX >= box.left && e.clientX <= box.right &&
                e.clientY >= box.top && e.clientY <= box.bottom;
            if (!inside) {
                dlg.close();
            }
        });
    });

    // ---- Password reveal ----
    document.addEventListener('click', function (e) {
        var toggle = e.target.closest('[data-pw-toggle]');
        if (!toggle) {
            return;
        }
        var field = toggle.closest('.pw-field');
        var input = field && field.querySelector('input');
        if (!input) {
            return;
        }
        var revealed = input.type === 'text';
        input.type = revealed ? 'password' : 'text';
        toggle.textContent = revealed ? 'Show' : 'Hide';
        toggle.setAttribute('aria-pressed', String(!revealed));
    });

    // ---- Copy to clipboard ----
    function selectText(el) {
        try {
            var range = document.createRange();
            range.selectNodeContents(el);
            var sel = window.getSelection();
            sel.removeAllRanges();
            sel.addRange(range);
        } catch (err) {
            // Selection is a nicety; the value is still on screen.
        }
    }

    document.addEventListener('click', function (e) {
        var btn = e.target.closest('[data-copy]');
        if (!btn) {
            return;
        }
        var target = document.querySelector(btn.getAttribute('data-copy'));
        if (!target) {
            return;
        }
        var restore = function () {
            var was = btn.getAttribute('data-label') || btn.textContent;
            btn.setAttribute('data-label', was);
            btn.textContent = 'Copied';
            setTimeout(function () {
                btn.textContent = was;
            }, 1500);
        };
        if (navigator.clipboard && navigator.clipboard.writeText) {
            navigator.clipboard.writeText(target.textContent).then(restore, function () {
                selectText(target);
                restore();
            });
        } else {
            selectText(target);
            restore();
        }
    });

    // ---- Post-revoke focus ----
    // htmx swaps #token-list wholesale on a revoke; move focus to it so the
    // "Token revoked." status is where a screen reader and the keyboard land.
    document.body.addEventListener('htmx:afterSwap', function () {
        var list = document.getElementById('token-list');
        if (list && list.querySelector('[role="status"]')) {
            list.focus();
        }
    });
})();
