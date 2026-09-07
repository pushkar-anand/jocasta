(function() {
    if (!window.htmx) return;

    var activeRequest;
    var requestedFilters;

    function currentFilters() {
        return new URLSearchParams(new FormData(document.getElementById('device-filters'))).toString();
    }

    function pending() {
        var status = document.getElementById('device-filter-status');
        var retry = document.getElementById('device-filter-retry');
        // The old count and empty state describe the previous filters. Do not
        // present them as results of a request that has not succeeded yet.
        document.getElementById('device-rows').hidden = true;
        status.textContent = 'Updating devices…';
        status.className = 'dim';
        if (document.activeElement === retry) document.getElementById('device-filters').elements.q.focus();
        retry.hidden = true;
    }

    // Delegate so filters restored by htmx history keep their feedback too.
    function changed(event) {
        if (event.target.closest('#device-filters')) pending();
    }
    document.addEventListener('input', changed);
    document.addEventListener('change', changed);

    document.addEventListener('htmx:historyRestore', function() {
        if (!document.getElementById('device-filters')) return;
        document.getElementById('device-rows').hidden = false;
        document.getElementById('device-filter-status').textContent = '';
        document.getElementById('device-filter-retry').hidden = true;
        activeRequest = null;
    });

    document.addEventListener('htmx:beforeRequest', function(event) {
        if (event.detail.elt !== document.getElementById('device-filters')) return;
        activeRequest = event.detail.xhr;
        requestedFilters = currentFilters();
        pending();
    });

    document.addEventListener('htmx:afterRequest', function(event) {
        if (event.detail.elt !== document.getElementById('device-filters') || event.detail.xhr !== activeRequest) return;
        // A response may arrive during the debounce for newer input.
        if (requestedFilters !== currentFilters()) {
            pending();
            return;
        }

        var rows = document.getElementById('device-rows');
        var status = document.getElementById('device-filter-status');
        var retry = document.getElementById('device-filter-retry');
        if (event.detail.successful) {
            rows.hidden = false;
            status.textContent = 'Devices updated.';
            status.className = 'dim';
            retry.hidden = true;
        } else {
            rows.hidden = true;
            status.textContent = 'Could not update devices. Check your connection and retry.';
            status.className = 'failure';
            retry.hidden = false;
        }
    });
})();
