// Where focus goes after htmx swaps content in, so the keyboard and a screen
// reader are never left stranded on <body> when the element they were on is
// gone. One rule for the whole app:
//
//   * a swapped-in region that announces an outcome (role=status / role=alert)
//     takes focus itself, so the reader lands on the announcement -- the region
//     needs a tabindex for this (its own, or an ancestor's);
//   * a region tagged data-focus-after-swap points at the control to focus
//     instead, by selector -- the device row uses it to send focus back to its
//     Edit button when the inline edit form closes;
//   * a region htmx has already focused (it honours [autofocus] in new content)
//     is left alone.
//
// The CSP forbids inline script, so this ships as a file.
(function () {
    'use strict';

    document.body.addEventListener('htmx:afterSwap', function (evt) {
        var swapped = evt.target;
        if (!swapped || typeof swapped.querySelector !== 'function') {
            return;
        }

        // htmx placed focus in this content already.
        if (swapped.matches('[autofocus]') || swapped.querySelector('[autofocus]')) {
            return;
        }

        var announce = swapped.matches('[role="status"], [role="alert"]')
            ? swapped
            : swapped.querySelector('[role="status"], [role="alert"]');
        if (announce) {
            // The announcement itself if it can hold focus, else the nearest
            // region that can -- the swap wrapper is often display:contents and
            // takes no focus.
            var host = announce.hasAttribute('tabindex') ? announce : announce.closest('[tabindex]');
            if (host) {
                host.focus();
                return;
            }
        }

        var hinted = swapped.matches('[data-focus-after-swap]')
            ? swapped
            : swapped.querySelector('[data-focus-after-swap]');
        if (hinted) {
            var target = hinted.querySelector(hinted.getAttribute('data-focus-after-swap'));
            if (target) {
                target.focus();
            }
        }
    });
})();
