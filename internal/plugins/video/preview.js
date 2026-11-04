// The video editor's preview runtime: a small port of HyperFrames' runtime
// to what the app needs to show a frame.
//
// It mounts a project's scenes into their hosts, keeps each composition's
// paused timeline at the playhead, and shows every timed element over
// [data-start, data-start + data-duration). Nothing here plays media: the
// editor scrubs, and the render's own engine plays sound.
(() => {
  "use strict";
  if (window.__video) return;

  const state = {
    mounted: false,
    time: 0,
    duration: 0,
    fps: 30,
    end: 0,
    hidden: new Map(),
    mounts: [],
    comps: [],
    errors: [],
    declared: {},
    overrides: {},
  };

  const num = (raw, fallback) => {
    const n = Number.parseFloat(String(raw ?? "").trim());
    return Number.isFinite(n) ? n : fallback;
  };

  const clamp = (v, lo, hi) => Math.min(hi, Math.max(lo, v));

  const record = (error) => {
    if (state.errors.length < 20) state.errors.push(String(error && error.message ? error.message : error));
  };

  const ID_PATTERN = /^[A-Za-z0-9_.:-]+$/;

  // startRef reads a data-start that names another clip, following
  // HyperFrames' grammar: the whole string may be an id, and an offset needs
  // whitespace before its sign ("intro + 0.5", never "intro-0.5").
  function startRef(raw) {
    const text = raw.trim();
    if (!text || Number.isFinite(Number(text))) return null;
    if (ID_PATTERN.test(text)) return { id: text, offset: 0 };
    const last = text.length - 1;
    if (!/[0-9]/.test(text[last])) return null;
    let cursor = last;
    while (cursor >= 0 && /[0-9]/.test(text[cursor])) cursor--;
    if (text[cursor] === ".") {
      cursor--;
      while (cursor >= 0 && /[0-9]/.test(text[cursor])) cursor--;
    }
    const magnitudeStart = cursor + 1;
    let op = magnitudeStart - 1;
    while (op >= 0 && /\s/.test(text[op])) op--;
    if (op < 0 || (text[op] !== "+" && text[op] !== "-")) return null;
    const id = text.slice(0, op).trim();
    if (!ID_PATTERN.test(id)) return null;
    const magnitude = Number(text.slice(magnitudeStart));
    if (!Number.isFinite(magnitude)) return null;
    return { id, offset: text[op] === "-" ? -magnitude : magnitude };
  }

  const isScalar = (value) => typeof value === "string" || typeof value === "number" || typeof value === "boolean";

  // cssValue keeps a value from closing the declaration it is written into.
  const cssValue = (value) => String(value).replace(/[;{}<>]/g, "").replace(/[\r\n]/g, " ");

  // safeURL is the scheme allowlist of a media variable: relative, http(s),
  // blob, and image data URIs.
  function safeURL(raw) {
    const url = String(raw).replace(/[\u0000-\u0020]/g, "");
    const scheme = /^([a-z][a-z0-9+.-]*):/i.exec(url);
    if (!scheme) return true;
    const proto = scheme[1].toLowerCase();
    if (proto === "https" || proto === "http" || proto === "blob") return true;
    return proto === "data" && /^data:image\//i.test(url);
  }

  // setOwnText replaces an element's own text and keeps its child elements,
  // as HyperFrames binds a text variable.
  function setOwnText(el, value) {
    for (const node of Array.from(el.childNodes)) {
      if (node.nodeType === 3) el.removeChild(node);
    }
    el.appendChild(document.createTextNode(String(value)));
  }

  const nearestComp = (el) => {
    const parent = el.parentElement;
    return parent ? parent.closest("[data-composition-id]") : null;
  };

  const mergedValues = (declared, overrides, mount) => Object.assign({}, declared, overrides, mount);

  // applyVariables writes a composition's values: a CSS custom property for
  // every scalar, its bound text and its bound sources.
  function applyVariables(comp) {
    const root = comp.el;
    if (!root || !comp.values) return;
    for (const [id, value] of Object.entries(comp.values)) {
      if (!isScalar(value)) continue;
      root.style.setProperty(`--${id}`, cssValue(value));
      if (!comp.window) document.documentElement.style.setProperty(`--${id}`, cssValue(value));
    }
    for (const el of root.querySelectorAll("[data-var-text]")) {
      if (nearestComp(el) !== root) continue;
      const id = el.getAttribute("data-var-text");
      if (id && Object.prototype.hasOwnProperty.call(comp.values, id)) setOwnText(el, comp.values[id]);
    }
    for (const el of root.querySelectorAll("[data-var-src]")) {
      if (nearestComp(el) !== root) continue;
      const id = el.getAttribute("data-var-src");
      if (!id || !Object.prototype.hasOwnProperty.call(comp.values, id)) continue;
      if (!/^(IMG|VIDEO|AUDIO|SOURCE)$/.test(el.tagName)) continue;
      const value = comp.values[id];
      if (!isScalar(value) || !safeURL(value)) continue;
      el.setAttribute("src", String(value));
    }
  }

  const globalVariables = () => Object.assign({}, state.declared, state.overrides);

  const timelineOf = (id) => {
    const tl = window.__timelines && window.__timelines[id];
    return tl && typeof tl.seek === "function" ? tl : null;
  };

  // timing reads a clip's window in its own composition, resolving a start
  // that names another clip to that clip's end.
  function timing(el, ids, depth = 0, seen = new Set()) {
    const raw = (el.getAttribute("data-start") || "").trim();
    let start = 0;
    if (raw) {
      const absolute = Number(raw);
      if (Number.isFinite(absolute)) start = Math.max(0, absolute);
      else {
        const ref = startRef(raw);
        if (ref && depth < 32 && !seen.has(ref.id)) {
          const target = ids.get(ref.id);
          if (target && target !== el) {
            seen.add(ref.id);
            const inner = timing(target, ids, depth + 1, seen);
            seen.delete(ref.id);
            start = Math.max(0, inner.start + inner.duration + ref.offset);
          }
        }
      }
    }
    let duration = num(el.getAttribute("data-duration"), Number.NaN);
    if (!Number.isFinite(duration)) {
      const end = num(el.getAttribute("data-end"), Number.NaN);
      duration = Number.isFinite(end) ? Math.max(0, end - start) : Number.POSITIVE_INFINITY;
    }
    return { start, duration, end: start + duration };
  }

  // clipsOf lists the timed elements of a composition: those whose nearest
  // composition root is this one, so a nested composition keeps its own time.
  function clipsOf(root, ids) {
    const clips = [];
    for (const el of root.querySelectorAll("[data-start], [data-duration]")) {
      const parent = el.parentElement;
      const nearest = parent ? parent.closest("[data-composition-id]") : null;
      if (nearest !== root) continue;
      clips.push({ el, timing: timing(el, ids), media: /^(VIDEO|AUDIO)$/.test(el.tagName) });
    }
    return clips;
  }

  // idsOf maps the ids a start may name, within one composition.
  function idsOf(root) {
    const ids = new Map();
    for (const el of root.querySelectorAll("[id]")) ids.set(el.getAttribute("id"), el);
    return ids;
  }

  // Preserve authored visibility separately from the preview's timing mask.
  // Removing a mask must restore inline visibility and its priority exactly.
  const setHidden = (el, hidden) => {
    if (el === document.documentElement || el === document.body) return;
    if (hidden) {
      if (!state.hidden.has(el)) state.hidden.set(el, {
        value: el.style.getPropertyValue("visibility"),
        priority: el.style.getPropertyPriority("visibility"),
      });
      el.style.setProperty("visibility", "hidden", "important");
    } else if (state.hidden.has(el)) {
      const original = state.hidden.get(el);
      if (original.value) el.style.setProperty("visibility", original.value, original.priority);
      else el.style.removeProperty("visibility");
      state.hidden.delete(el);
    }
  };

  // A reload may reuse the same document. Remove only nodes this runtime
  // mounted, dispose their animations, and restore every timing mask before
  // discovering authored visibility again. Root/inline timelines stay alive.
  function unmountScenes() {
    for (const el of Array.from(state.hidden.keys())) setHidden(el, false);
    for (const mounted of state.mounts) {
      const tl = mounted.timeline;
      if (tl && typeof tl.revert === "function") tl.revert();
      else if (tl && typeof tl.kill === "function") tl.kill();
      if (window.__timelines && window.__timelines[mounted.id] === tl) delete window.__timelines[mounted.id];
      for (const node of mounted.nodes) node.remove();
    }
    state.mounts = [];
    for (const el of Array.from(state.hidden.keys())) setHidden(el, false);
  }

  // mediaTime is where a media element's own time sits at composition time.
  function seekMedia(clip, local) {
    const el = clip.el;
    if (!Number.isFinite(clip.offset)) clip.offset = num(el.getAttribute("data-media-start"), 0);
    if (!Number.isFinite(clip.rate)) {
      const rate = num(el.getAttribute("data-playback-rate"), 1);
      clip.rate = rate > 0 ? rate : 1;
    }
    const target = clip.offset + Math.max(0, local - clip.timing.start) * clip.rate;
    if (!Number.isFinite(target)) return;
    try {
      if (!el.paused) el.pause();
      const limit = Number.isFinite(el.duration) ? el.duration : target;
      const at = clamp(target, 0, Math.max(0, limit - 0.001));
      if (Math.abs(el.currentTime - at) > 1 / 120) el.currentTime = at;
    } catch (error) {
      record(error);
    }
  }

  // apply shows the frame at t on every composition.
  function apply(t) {
    // Remove timing masks before seeking animations. GSAP can animate
    // visibility (autoAlpha); a hidden first frame is not authored permanence.
    for (const el of Array.from(state.hidden.keys())) setHidden(el, false);
    state.time = clamp(t, 0, Math.max(0, state.duration));
    // The transport may sit at the end, but there is no frame at that instant:
    // clips have half-open windows. Hold the last frame of the film/focus span.
    const frameTime = state.time >= state.end && state.end > 0
      ? Math.max(0, state.end - 1 / state.fps) : state.time;
    for (const comp of state.comps) {
      let local = frameTime;
      let active = true;
      if (comp.window) {
        active = frameTime >= comp.window.start && frameTime < comp.window.start + comp.window.duration;
        local = comp.playbackStart + (frameTime - comp.window.start) * comp.rate;
      }
      if (comp.el && comp.window) setHidden(comp.el, !active);
      if (!active) {
        for (const clip of comp.clips) setHidden(clip.el, true);
        continue;
      }
      const tl = timelineOf(comp.id);
      if (tl) {
        try {
          tl.seek(Math.max(0, local));
        } catch (error) {
          record(error);
        }
      }
      for (const clip of comp.clips) {
        const c = clip.timing;
        const visible = local >= c.start && local < c.start + c.duration;
        setHidden(clip.el, !visible);
        if (clip.media && visible) seekMedia(clip, local);
      }
    }
    return state.time;
  }

  // mountScene writes a scene file's markup into its host and runs the
  // scene's scripts, the way the HyperFrames runtime mounts a sub-composition.
  function mountScene(scene, values) {
    const host = document.getElementById(scene.hostId);
    if (!host) {
      record(`The scene's host #${scene.hostId} is missing.`);
      return null;
    }
    const doc = new DOMParser().parseFromString(scene.html, "text/html");
    const template = doc.querySelector("template");
    // A scene's markup sits in its template's content, which queries on the
    // parsed document do not reach.
    const source = template ? template.content : doc.body;
    const root = source.querySelector ? source.querySelector("[data-composition-id]") : null;
    if (!root) {
      record(`The scene file ${scene.file} has no composition root.`);
      return null;
    }
    // The scene's scripts run once, wrapped, after its markup is in place: a
    // script moved with its section would run unwrapped and collide with the
    // root's own variables.
    const scripts = [];
    if (template) for (const script of Array.from(template.content.querySelectorAll("script"))) scripts.push(script.textContent || "");
    for (const script of Array.from(doc.querySelectorAll("script"))) scripts.push(script.textContent || "");
    const fragment = document.createDocumentFragment();
    for (const child of Array.from(source.childNodes)) fragment.appendChild(document.importNode(child, true));
    for (const script of Array.from(fragment.querySelectorAll("script"))) script.remove();
    const nodes = Array.from(fragment.childNodes);
    host.appendChild(fragment);
    const mounted = host.querySelector("[data-composition-id]") || root;
    applyVariables({ el: mounted, values });
    for (const code of scripts) runScript(code);
    const id = scene.compId || mounted.getAttribute("data-composition-id");
    state.mounts.push({ id, nodes, timeline: timelineOf(id) });
    return mounted;
  }

  // runScript runs a composition script in its own scope, as the runtime
  // does, so two scenes may both declare `const tl`.
  function runScript(code) {
    if (!code.trim()) return;
    const el = document.createElement("script");
    el.textContent = `(function(){${code}})();`;
    try {
      document.body.appendChild(el);
    } catch (error) {
      record(error);
    } finally {
      el.remove();
    }
  }

  function mount(payload) {
    unmountScenes();
    state.errors = [];
    state.fps = num(payload.fps, 30);
    state.duration = num(payload.duration, 0);
    state.end = state.duration;
    state.comps = [];
    state.declared = payload.declared || {};
    state.overrides = payload.variables || {};
    window.__hyperframes = window.__hyperframes || {};
    if (typeof window.__hyperframes.getVariables !== "function") {
      window.__hyperframes.getVariables = globalVariables;
    }
    window.__hfVariables = globalVariables();
    const rootEl = document.querySelector(`[data-composition-id="${payload.rootId}"]`);
    if (!rootEl) {
      record(`The composition root #${payload.rootId} is missing.`);
      state.mounted = true;
      window.__videoReady = true;
      return info();
    }
    const comps = [{
      id: payload.rootId,
      el: rootEl,
      window: null,
      playbackStart: 0,
      rate: 1,
      declared: state.declared,
      mount: {},
    }];
    comps[0].values = mergedValues(comps[0].declared, state.overrides, {});
    for (const scene of payload.scenes || []) {
      const declared = scene.declared || {};
      const mount = scene.mount || {};
      const mounted = mountScene(scene, mergedValues(declared, state.overrides, mount));
      if (!mounted) continue;
      comps.push({
        id: scene.compId || mounted.getAttribute("data-composition-id") || "",
        el: mounted,
        window: { start: num(scene.start, 0), duration: Math.max(0, num(scene.duration, 0)) },
        playbackStart: num(scene.playbackStart, 0),
        rate: Math.max(0.01, num(scene.rate, 1)),
        declared,
        mount,
      });
    }
    // Compositions written inline in the root keep their own local time too.
    for (const el of rootEl.querySelectorAll("[data-composition-id]")) {
      if (el === rootEl || el.hasAttribute("data-composition-src")) continue;
      if (el.parentElement && el.parentElement.closest("[data-composition-src]")) continue;
      comps.push({
        id: el.getAttribute("data-composition-id") || "",
        el,
        window: {
          start: num(el.getAttribute("data-start"), 0),
          duration: Math.max(0, num(el.getAttribute("data-duration"), 0)),
        },
        playbackStart: num(el.getAttribute("data-playback-start"), 0),
        rate: Math.max(0.01, num(el.getAttribute("data-playback-rate"), 1)),
        declared: {},
        mount: {},
      });
    }
    for (const comp of comps) {
      comp.values = mergedValues(comp.declared, state.overrides, comp.mount);
      applyVariables(comp);
      const ids = idsOf(comp.el);
      comp.clips = clipsOf(comp.el, ids);
      for (const clip of comp.clips) {
        clip.el.dataset.hfClip = "";
      }
    }
    state.comps = comps;
    if (!(state.duration > 0)) {
      for (const comp of comps) {
        const offset = comp.window ? comp.window.start : 0;
        for (const clip of comp.clips) {
          if (Number.isFinite(clip.timing.end)) state.duration = Math.max(state.duration, offset + clip.timing.end);
        }
      }
      if (comps[0] && comps[0].el) {
        state.duration = Math.max(state.duration, num(comps[0].el.getAttribute("data-duration"), 0));
      }
    }
    state.end = state.duration;
    for (const id of payload.timelines || []) {
      if (!timelineOf(id)) record(`The animation timeline ${id} did not load. Check its scripts and assets.`);
    }
    state.mounted = true;
    window.__videoReady = true;
    apply(0);
    return info();
  }

  function info() {
    return {
      mounted: state.mounted,
      time: state.time,
      duration: state.duration,
      fps: state.fps,
      errors: state.errors.slice(),
      timelines: state.comps.filter((comp) => timelineOf(comp.id)).map((comp) => comp.id),
      scenes: state.comps.filter((comp) => comp.window).length,
    };
  }

  // gsapHere reports whether the composition's own scripts found GSAP, so
  // the editor can say why nothing animates.
  function gsapHere() {
    return typeof window.gsap !== "undefined";
  }

  window.addEventListener("error", (event) => {
    if (event && event.message) record(event.message);
  });

  window.__video = {
    mount(payload) {
      try {
        return mount(payload);
      } catch (error) {
        record(error);
        state.mounted = true;
        window.__videoReady = true;
        return info();
      }
    },
    seek(t, end = state.duration) {
      if (!state.mounted) return state.time;
      state.end = clamp(num(end, state.duration), 0, state.duration);
      return apply(t);
    },
    setVariables(values) {
      state.overrides = Object.assign({}, values || {});
      window.__hfVariables = globalVariables();
      for (const comp of state.comps) {
        comp.values = mergedValues(comp.declared, state.overrides, comp.mount);
        applyVariables(comp);
      }
      if (state.mounted) apply(state.time);
      return info();
    },
    variables() {
      return globalVariables();
    },
    info,
    gsap: gsapHere,
  };
})();
