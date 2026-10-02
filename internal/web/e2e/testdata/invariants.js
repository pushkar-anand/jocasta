// Layout checks run against a rendered page. Each finds one kind of fault a
// reader would notice: the page scrolling sideways, text cut off or spilling
// over its neighbour, controls drawn on top of each other, targets too small
// to tap, and text too small to read. It returns a list of problems, each
// naming the check and the element by a short selector.
(function (opts) {
    const problems = [];
    const vw = document.documentElement.clientWidth;

    const describe = (el) => {
        if (!el || el === document.body) return 'body';
        const parts = [];
        for (let n = el; n && n !== document.body && parts.length < 4; n = n.parentElement) {
            let s = n.tagName.toLowerCase();
            if (n.id) { parts.unshift(s + '#' + n.id); break; }
            const cls = [...n.classList].slice(0, 2).join('.');
            if (cls) s += '.' + cls;
            parts.unshift(s);
        }
        const text = (el.innerText || el.textContent || '').trim().replace(/\s+/g, ' ').slice(0, 40);
        return parts.join(' > ') + (text ? ' "' + text + '"' : '');
    };

    const add = (check, el, detail) => problems.push({ check, el: describe(el), detail });

    const visible = (el) => {
        if (!el.isConnected) return false;
        const cs = getComputedStyle(el);
        if (cs.visibility === 'hidden' || cs.display === 'none' || parseFloat(cs.opacity) === 0) return false;
        const r = el.getBoundingClientRect();
        if (r.width < 1 || r.height < 1) return false;
        // Visually hidden helpers (skip link, sr-only text, a table head
        // folded away on a phone) clip themselves or an ancestor to nothing.
        for (let n = el; n && n !== document.body; n = n.parentElement) {
            const ns = getComputedStyle(n);
            if (ns.clip === 'rect(0px, 0px, 0px, 0px)' || ns.clipPath === 'inset(50%)') return false;
        }
        // Closed <details> hide all but their summary without display:none.
        for (let n = el.parentElement; n; n = n.parentElement) {
            if (n.tagName === 'DETAILS' && !n.open && !(el.closest('summary') && el.closest('summary').parentElement === n)) return false;
        }
        return true;
    };

    // A modal on top: only what is inside it is in front of the reader.
    const modal = [...document.querySelectorAll('dialog[open]')].find((d) => d.matches(':modal'));
    const scope = modal || document.body;

    // Inside a box that scrolls on purpose, overflow is that box's business.
    const scrollsX = (el) => {
        for (let n = el.parentElement; n && n !== document.body; n = n.parentElement) {
            const o = getComputedStyle(n).overflowX;
            if (o === 'auto' || o === 'scroll' || o === 'hidden' || o === 'clip') return n;
        }
        return null;
    };

    // 1. The page scrolls sideways.
    if (document.documentElement.scrollWidth > vw + 1) {
        const wide = [...scope.querySelectorAll('*')].filter((el) => {
            if (!visible(el) || scrollsX(el)) return false;
            const r = el.getBoundingClientRect();
            return r.right > vw + 1;
        });
        // Report the outermost offenders: an element whose parent also
        // overflows says nothing new.
        const outer = wide.filter((el) => !wide.includes(el.parentElement)).slice(0, 5);
        add('page-scrolls-sideways', outer[0] || document.documentElement,
            'document is ' + document.documentElement.scrollWidth + 'px wide in a ' + vw + 'px viewport; ' +
            outer.map(describe).join(' | '));
    }

    const all = [...scope.querySelectorAll('*')].filter(visible);

    const ownText = (el) => [...el.childNodes].some((n) => n.nodeType === 3 && n.textContent.trim() !== '');

    // 2. Text cut off with no ellipsis, or spilling out of its box.
    for (const el of all) {
        if (!ownText(el) || el.closest('svg')) continue;
        const cs = getComputedStyle(el);
        if (cs.display === 'inline') continue;
        const over = el.scrollWidth - el.clientWidth;
        if (over <= 1) continue;
        // A scroller shows the rest on scroll.
        if (cs.overflowX === 'auto' || cs.overflowX === 'scroll') continue;
        // Form fields scroll their own text, and their value is not in the DOM.
        if (/^(INPUT|TEXTAREA|SELECT)$/.test(el.tagName)) continue;
        if (cs.overflowX === 'hidden' || cs.overflowX === 'clip') {
            if (cs.textOverflow === 'ellipsis' && el.title) continue;
            if (cs.textOverflow === 'ellipsis') {
                add('truncated-without-title', el, 'cut by ' + over + 'px with an ellipsis but no title to read the rest');
                continue;
            }
            add('text-clipped', el, 'cut by ' + over + 'px');
            continue;
        }
        add('text-spills', el, 'spills ' + over + 'px past its box');
    }

    // 3. Things drawn on top of each other. Only leaves: text-bearing
    // elements and controls, compared with every other leaf that is not its
    // ancestor or descendant.
    const leaves = all.filter((el) =>
        (ownText(el) || el.matches('a,button,input,select,textarea,summary,[role=button],[role=img],svg text'))
        && el.tagName !== 'svg');
    const outline = (el) => {
        const b = el.getBBox(), m = el.getScreenCTM();
        return [[b.x, b.y], [b.x + b.width, b.y], [b.x + b.width, b.y + b.height], [b.x, b.y + b.height]]
            .map(([x, y]) => [m.a * x + m.c * y + m.e, m.b * x + m.d * y + m.f]);
    };
    const outlines = (el) => (el instanceof SVGGraphicsElement && !(el instanceof SVGGElement) && !(el instanceof SVGAElement)
        ? [el] : [...el.querySelectorAll('circle,rect,text,path,line,polygon')].filter(visible))
        .map(outline);
    // The area two convex outlines share: one clipped by each edge of the
    // other, both wound the same way.
    const shared = (p, q) => {
        const wind = (poly) => poly.reduce((s, [x, y], i) => { const [u, v] = poly[(i + 1) % poly.length]; return s + x * v - u * y; }, 0);
        if (wind(p) < 0) p = [...p].reverse();
        if (wind(q) < 0) q = [...q].reverse();
        let out = p;
        for (let i = 0; i < q.length && out.length; i++) {
            const [ax, ay] = q[i], [bx, by] = q[(i + 1) % q.length];
            const side = ([x, y]) => (bx - ax) * (y - ay) - (by - ay) * (x - ax);
            const cut = (s, e) => { const t = side(s) / (side(s) - side(e)); return [s[0] + t * (e[0] - s[0]), s[1] + t * (e[1] - s[1])]; };
            const next = [];
            for (let j = 0; j < out.length; j++) {
                const s = out[j], e = out[(j + 1) % out.length];
                if (side(e) >= 0) {
                    if (side(s) < 0) next.push(cut(s, e));
                    next.push(e);
                } else if (side(s) >= 0) next.push(cut(s, e));
            }
            out = next;
        }
        return out.length < 3 ? 0 : Math.abs(wind(out)) / 2;
    };
    const rects = leaves.map((el) => {
        // Inline text wraps; its client rects are the boxes the eye sees.
        const boxes = getComputedStyle(el).display === 'inline' ? [...el.getClientRects()] : [el.getBoundingClientRect()];
        // Inside a map, text is turned along its branch and a link wraps a
        // dot and its name: the upright box round either is mostly empty
        // corners, so each shape is compared by its own turned outline.
        const shapes = el.closest('svg') ? outlines(el) : null;
        return { el, boxes, shapes };
    });
    // A popover is drawn over the page on purpose: an opaque positioned
    // panel is its own layer, compared only with what is inside it.
    const layerOf = (el) => {
        for (let n = el; n && n !== document.body; n = n.parentElement) {
            const cs = getComputedStyle(n);
            if ((cs.position === 'absolute' || cs.position === 'fixed') && !/rgba\(.*, 0\)|transparent/.test(cs.backgroundColor)) return n;
        }
        return null;
    };
    for (const r of rects) r.layer = layerOf(r.el);
    const seen = new Set();
    for (let i = 0; i < rects.length; i++) {
        for (let j = i + 1; j < rects.length; j++) {
            const a = rects[i], b = rects[j];
            if (a.el.contains(b.el) || b.el.contains(a.el)) continue;
            if (a.layer !== b.layer) continue;
            // Controls float over a map on purpose; the map pans out from
            // under them.
            if (!!a.el.closest('svg') !== !!b.el.closest('svg')) continue;
            // A button drawn inside a field, as the password toggle is.
            const inField = (x, y) => x.matches('input') && getComputedStyle(y).position === 'absolute' && y.parentElement === x.parentElement;
            if (inField(a.el, b.el) || inField(b.el, a.el)) continue;
            let worst = 0;
            if (a.shapes && b.shapes) {
                for (const pa of a.shapes) for (const pb of b.shapes) {
                    const area = shared(pa, pb);
                    if (area > 4) worst = Math.max(worst, area);
                }
            } else for (const ra of a.boxes) for (const rb of b.boxes) {
                const w = Math.min(ra.right, rb.right) - Math.max(ra.left, rb.left);
                const h = Math.min(ra.bottom, rb.bottom) - Math.max(ra.top, rb.top);
                if (w > 2 && h > 2) worst = Math.max(worst, w * h);
            }
            if (!worst) continue;
            // A label overlapping its own control's padding is styling, not a
            // fault: both sit in the same control.
            if (a.el.closest('label,button,a') && a.el.closest('label,button,a') === b.el.closest('label,button,a')) continue;
            const key = describe(a.el) + '|' + describe(b.el);
            if (seen.has(key)) continue;
            seen.add(key);
            add('overlap', a.el, 'overlaps ' + describe(b.el) + ' by ' + Math.round(worst) + 'px²');
        }
    }

    // 4. Targets too small to tap (WCAG 2.5.8: 24 by 24 CSS pixels), on a
    // touch layout only. A link inside a sentence is exempt.
    if (opts.touch) {
        // A map is zoomed to its targets; its own controls are checked.
        const targets = all.filter((el) => el.matches('a[href],button,input:not([type=hidden]),select,summary,[role=button]') && !el.closest('svg'))
            // A control inside a label is hit anywhere on the label.
            .map((el) => ({ el, r: (el.matches('input,select') && el.closest('label') ? el.closest('label') : el).getBoundingClientRect() }));
        for (const t of targets) {
            const r = t.r;
            if (r.width >= 24 && r.height >= 24) continue;
            if (t.el.tagName === 'A' && getComputedStyle(t.el).display === 'inline' && t.el.parentElement && ownText(t.el.parentElement)) continue;
            // WCAG's spacing exception: a 24px circle on the target's centre
            // reaches no other target.
            const cx = (r.left + r.right) / 2, cy = (r.top + r.bottom) / 2;
            const crowded = targets.some((o) => {
                if (o === t || o.el.contains(t.el) || t.el.contains(o.el)) return false;
                const dx = Math.max(o.r.left - cx, 0, cx - o.r.right), dy = Math.max(o.r.top - cy, 0, cy - o.r.bottom);
                return Math.hypot(dx, dy) < 12;
            });
            if (!crowded) continue;
            add('small-target', t.el, Math.round(r.width) + 'x' + Math.round(r.height));
        }
    }

    // 5. Text too small to read.
    for (const el of all) {
        if (!ownText(el) || el.closest('svg')) continue;
        const px = parseFloat(getComputedStyle(el).fontSize);
        if (px < 11) add('tiny-text', el, px + 'px');
    }

    // 6. Content pushed off the left edge, where no scroll reaches it.
    for (const el of all) {
        if (!ownText(el) || scrollsX(el)) continue;
        const r = el.getBoundingClientRect();
        if (r.left < -1) add('off-left-edge', el, 'starts at ' + Math.round(r.left) + 'px');
    }

    return problems;
})
