// Browser version guard (#387). It must stay a plain classic script in ES5 syntax: it has to run on the oldest
// browsers and before the application bundle does, so it is neither built nor loaded as a module.
//
// A browser below the supported minimum (browserslist in package.json) lacks CSS and script features the
// application depends on. Without cascade layers and color-mix()/oklch() the Tailwind styles are dropped, and a
// missing Object.hasOwn or Array.prototype.toSorted throws halfway through rendering and leaves a white page.
// This probes those features before the application starts. When one is missing it draws the Product Schema's
// notice into the page and sets window.__PLINTH_UNSUPPORTED_BROWSER__ so web/src/main.tsx does not start the app.
//
// The notice text comes from the Product Schema (ui.browser_unsupported.*) and is not duplicated here. Without
// the text the guard stays out of the way and lets the application try to start.
(function () {
  try {
    var probes = [
      ['Object.hasOwn', function () { return typeof Object.hasOwn === 'function'; }],
      ['Array.prototype.toSorted', function () { return typeof Array.prototype.toSorted === 'function'; }],
      ['structuredClone', function () { return typeof structuredClone === 'function'; }],
      ['CSS @layer', function () { return typeof CSSLayerBlockRule === 'function'; }],
      ['CSS color-mix()', function () { return CSS.supports('color', 'color-mix(in srgb, red 50%, blue)'); }],
      ['CSS oklch()', function () { return CSS.supports('color', 'oklch(0.5 0.1 200)'); }]
    ];
    var missing = [];
    for (var index = 0; index < probes.length; index += 1) {
      var present = false;
      try { present = !!probes[index][1](); } catch (error) { present = false; }
      if (!present) missing.push(probes[index][0]);
    }
    if (missing.length === 0) return;

    var schema = window.__ART_AI_PRODUCT_SCHEMA__;
    var messages = schema && schema.content && schema.content.messages;
    var title = messages && messages['ui.browser_unsupported.title'];
    var description = messages && messages['ui.browser_unsupported.description'];
    if (!title || !description) return;

    window.__PLINTH_UNSUPPORTED_BROWSER__ = true;
    var panel = document.createElement('main');
    panel.setAttribute('role', 'alert');
    panel.setAttribute('data-testid', 'browser-unsupported');
    panel.setAttribute('data-missing', missing.join(','));
    // Inline styles on purpose: the stylesheet is the very thing an unsupported browser cannot apply.
    panel.style.maxWidth = '32rem';
    panel.style.margin = '15vh auto 0';
    panel.style.padding = '1.5rem';
    panel.style.border = '1px solid #c8ccd2';
    panel.style.borderRadius = '0.5rem';
    panel.style.background = '#ffffff';
    panel.style.color = '#1f2328';
    panel.style.fontFamily = 'system-ui, -apple-system, "Segoe UI", sans-serif';
    panel.style.lineHeight = '1.6';
    var heading = document.createElement('h1');
    heading.textContent = title;
    heading.style.margin = '0 0 0.75rem';
    heading.style.fontSize = '1.25rem';
    var body = document.createElement('p');
    body.textContent = description;
    body.style.margin = '0';
    panel.appendChild(heading);
    panel.appendChild(body);
    (document.getElementById('root') || document.body).appendChild(panel);
    if (typeof console !== 'undefined' && console.warn) console.warn('[browser-support] unsupported browser, missing: ' + missing.join(', '));
  } catch (error) {
    // The guard must never be the reason the page fails.
  }
})();
