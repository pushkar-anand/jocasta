// Zoom, pan, search and selection for the network map. The map is an SVG laid out on the server
// and redrawn every minute; this only moves its viewBox, and puts the view
// back after each redraw so a refresh never throws away where you were.
(function() {
    var view = null; // {x, y, w, h} in the SVG's own units; null is the whole map

    function svgOf(el) { return el && el.closest ? el.closest('.netmap__svg') : null; }
    function mapSVG() { return document.querySelector('.netmap__svg'); }

    function base(svg) {
        return { x: 0, y: 0, w: +svg.dataset.width, h: +svg.dataset.height };
    }

    function apply(svg) {
        var v = view || base(svg);
        svg.setAttribute('viewBox', v.x + ' ' + v.y + ' ' + v.w + ' ' + v.h);
    }

    // point is where a client position falls in the SVG's units.
    function point(svg, cx, cy) {
        var m = svg.getScreenCTM();
        if (!m) return null;
        var p = new DOMPoint(cx, cy).matrixTransform(m.inverse());
        return { x: p.x, y: p.y };
    }

    // zoom scales the view by f around (px, py), in SVG units.
    function zoom(svg, f, px, py) {
        var b = base(svg), v = view || b;
        var w = Math.min(Math.max(v.w * f, b.w / 10), b.w * 1.5);
        f = w / v.w;
        view = { x: px - (px - v.x) * f, y: py - (py - v.y) * f, w: w, h: v.h * f };
        apply(svg);
    }

    function zoomCentre(svg, f) {
        var v = view || base(svg);
        zoom(svg, f, v.x + v.w / 2, v.y + v.h / 2);
    }

    document.addEventListener('wheel', function(e) {
        var svg = svgOf(e.target);
        if (!svg) return;
        e.preventDefault();
        var p = point(svg, e.clientX, e.clientY);
        if (p) zoom(svg, e.deltaY > 0 ? 1.15 : 1 / 1.15, p.x, p.y);
    }, { passive: false });

    document.addEventListener('dblclick', function(e) {
        var svg = svgOf(e.target);
        if (!svg) return;
        var p = point(svg, e.clientX, e.clientY);
        if (p) zoom(svg, e.shiftKey ? 2 : 0.5, p.x, p.y);
    });

    document.addEventListener('click', function(e) {
        var b = e.target.closest && e.target.closest('[data-netmap-zoom]');
        var svg = mapSVG();
        if (!b || !svg) return;
        var how = b.dataset.netmapZoom;
        if (how === 'in') zoomCentre(svg, 1 / 1.4);
        if (how === 'out') zoomCentre(svg, 1.4);
        if (how === 'reset') { view = null; apply(svg); }
    });

    // Dragging pans; two fingers pinch. A drag that moved is not a click, so
    // letting go over a device does not open it.
    var pointers = new Map();
    var moved = false;
    var pinch = 0;

    document.addEventListener('pointerdown', function(e) {
        var svg = svgOf(e.target);
        if (!svg) return;
        pointers.set(e.pointerId, { x: e.clientX, y: e.clientY });
        moved = false;
        pinch = 0;
    });

    document.addEventListener('pointermove', function(e) {
        var last = pointers.get(e.pointerId);
        var svg = mapSVG();
        if (!last || !svg) return;

        if (pointers.size === 2) {
            pointers.set(e.pointerId, { x: e.clientX, y: e.clientY });
            var ps = Array.from(pointers.values());
            var d = Math.hypot(ps[0].x - ps[1].x, ps[0].y - ps[1].y);
            if (pinch) {
                var mid = point(svg, (ps[0].x + ps[1].x) / 2, (ps[0].y + ps[1].y) / 2);
                if (mid) zoom(svg, pinch / d, mid.x, mid.y);
            }
            pinch = d;
            moved = true;
            return;
        }

        var dx = e.clientX - last.x, dy = e.clientY - last.y;
        if (!moved && Math.hypot(dx, dy) < 4) return;
        if (!moved) svg.classList.add('netmap__svg--dragging');
        moved = true;

        var v = view || base(svg);
        var rect = svg.getBoundingClientRect();
        var s = Math.max(v.w / rect.width, v.h / rect.height);
        view = { x: v.x - dx * s, y: v.y - dy * s, w: v.w, h: v.h };
        apply(svg);
        pointers.set(e.pointerId, { x: e.clientX, y: e.clientY });
    });

    function release(e) {
        pointers.delete(e.pointerId);
        if (pointers.size === 0) {
            var svg = mapSVG();
            if (svg) svg.classList.remove('netmap__svg--dragging');
        }
    }

    document.addEventListener('pointerup', release);
    document.addEventListener('pointercancel', release);

    document.addEventListener('click', function(e) {
        if (moved && svgOf(e.target)) {
            e.preventDefault();
            moved = false;
        }
    }, true);

    // Selecting a node lights the links that end on it and the nodes at their
    // other ends, and names the services on them; the rest dims. The first
    // click on a device selects it, a second opens its page.
    var selected = null;

    function applySelection(svg) {
        if (selected && !svg.querySelector('[data-key="' + selected + '"]')) selected = null;
        svg.classList.toggle('netmap__svg--selected', selected !== null);

        var peers = new Set();
        svg.querySelectorAll('[data-a]').forEach(function(el) {
            var on = selected !== null && (el.dataset.a === selected || el.dataset.b === selected);
            el.classList.toggle('is-on', on);
            if (on) {
                peers.add(el.dataset.a);
                peers.add(el.dataset.b);
            }
        });
        svg.querySelectorAll('[data-key]').forEach(function(n) {
            var k = n.dataset.key;
            n.classList.toggle('is-selected', k === selected);
            n.classList.toggle('is-peer', k !== selected && peers.has(k));
        });
    }

    function select(svg, key) {
        selected = key;
        applySelection(svg);
    }

    document.addEventListener('click', function(e) {
        var svg = svgOf(e.target);
        if (!svg || e.defaultPrevented) return;
        var node = e.target.closest('[data-key]');
        if (!node) {
            select(svg, null);
            return;
        }
        var key = node.dataset.key;
        if (key === selected && node.tagName.toLowerCase() === 'a') return;
        e.preventDefault();
        select(svg, key === selected ? null : key);
    });

    // Search dims what does not match. The box sits outside what the redraw
    // replaces, so the query is simply applied again to the new map.
    function query() {
        var box = document.getElementById('netmap-search');
        return box ? box.value.trim().toLowerCase() : '';
    }

    function search(svg) {
        var q = query();
        var found = [];
        svg.querySelectorAll('[data-name]').forEach(function(n) {
            var hit = q !== '' && n.dataset.name.toLowerCase().indexOf(q) !== -1;
            n.classList.toggle('netmap__dev--match', hit);
            if (hit) found.push(n);
        });
        svg.classList.toggle('netmap__svg--searching', q !== '');
        return found;
    }

    // focus zooms to a node, a quarter of the map across.
    function focus(svg, node) {
        var c = node.querySelector('circle');
        if (!c) return;
        var b = base(svg);
        var w = b.w / 4, h = b.h / 4;
        view = { x: +c.getAttribute('cx') - w / 2, y: +c.getAttribute('cy') - h / 2, w: w, h: h };
        apply(svg);
    }

    document.addEventListener('input', function(e) {
        var svg = mapSVG();
        if (e.target.id === 'netmap-search' && svg) search(svg);
    });

    document.addEventListener('keydown', function(e) {
        var svg = mapSVG();
        if (e.target.id !== 'netmap-search' || !svg) return;
        if (e.key === 'Enter') {
            e.preventDefault();
            var found = search(svg);
            if (found.length) {
                focus(svg, found[0]);
                select(svg, found[0].dataset.key);
            }
        }
        if (e.key === 'Escape') {
            e.target.value = '';
            search(svg);
        }
    });

    document.addEventListener('keydown', function(e) {
        var svg = mapSVG();
        if (e.key === 'Escape' && svg && selected !== null) select(svg, null);
    });

    // The minute's redraw replaces the SVG; give it back the view and the
    // search.
    document.addEventListener('htmx:afterSwap', function(e) {
        if (e.target && e.target.id === 'map-live') {
            var svg = mapSVG();
            if (svg) {
                apply(svg);
                search(svg);
                applySelection(svg);
            }
        }
    });
})();
