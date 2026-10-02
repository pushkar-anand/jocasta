// When a filter form with data-live fetches its results. Its search box
// searches once typing pauses, and only with nothing in it or with at least
// three characters, which is the least that narrows a list usefully. The
// form's other fields fetch as soon as they change. A search box's own change
// event, which fires again on blur, is ignored: the pause already searched.
//
// htmx's own trigger filters would need eval, which the CSP refuses, so the
// form listens for one event, "filter", and this raises it.
//
// The device page swaps its whole traffic block, form included. Text typed
// while that request was out would be replaced by what the server echoed, so
// the box gets back what was in it.
(function () {
    'use strict';

    if (!window.htmx) {
        return;
    }

    var PAUSE = 300;
    var MIN = 3;

    function live(el) {
        return el && el.form && el.form.hasAttribute('data-live') ? el.form : null;
    }

    function isSearch(el) {
        return el.matches('input[type="search"]');
    }

    // The form as it is now: the traffic block may have been swapped since.
    function current(form) {
        return form.isConnected ? form : document.getElementById(form.id);
    }

    document.addEventListener('change', function (evt) {
        var form = live(evt.target);
        if (form && !isSearch(evt.target)) {
            htmx.trigger(form, 'filter');
        }
    });

    var timer;

    document.addEventListener('input', function (evt) {
        var form = live(evt.target);
        if (!form || !isSearch(evt.target)) {
            return;
        }

        clearTimeout(timer);
        form.removeAttribute('data-waiting');

        var q = evt.target.value.trim();
        if (q.length > 0 && q.length < MIN) {
            return;
        }

        // What the results show now: the last search, or the page's own.
        var searched = evt.target.dataset.searched || evt.target.defaultValue.trim();
        if (q === searched) {
            return;
        }

        form.setAttribute('data-waiting', '');
        timer = setTimeout(function () {
            var f = current(form);
            if (!f) {
                return;
            }

            f.removeAttribute('data-waiting');
            f.elements.q.dataset.searched = q;
            htmx.trigger(f, 'filter');
        }, PAUSE);
    });

    var typed;

    document.addEventListener('htmx:beforeSwap', function () {
        var el = document.activeElement;
        typed = el && el.id && live(el) && isSearch(el) ? { id: el.id, value: el.value } : null;
    });

    document.addEventListener('htmx:afterSwap', function () {
        if (!typed) {
            return;
        }

        var el = document.getElementById(typed.id);
        if (el && el.value !== typed.value) {
            el.dataset.searched = el.defaultValue.trim();
            el.value = typed.value;
        }

        typed = null;
    });
})();
