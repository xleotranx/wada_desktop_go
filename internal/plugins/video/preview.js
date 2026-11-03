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
    comps: [],
    errors: [],
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

  const setHidden = (el, hidden) => {
    if (el === document.documentElement || el === document.body) return;
    if (hidden) el.style.setProperty("visibility", "hidden", "important");
    else el.style.removeProperty("visibility");
  };

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
    state.time = clamp(t, 0, Math.max(0, state.duration));
    for (const comp of state.comps) {
      let local = state.time;
      let active = true;
      if (comp.window) {
        active = state.time >= comp.window.start && state.time < comp.window.start + comp.window.duration;
        local = comp.playbackStart + (state.time - comp.window.start) * comp.rate;
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
        if (clip.authoredHidden === undefined) {
          clip.authoredHidden = getComputedStyle(clip.el).visibility === "hidden";
        }
        if (clip.authoredHidden) setHidden(clip.el, true);
        else setHidden(clip.el, !visible);
        if (clip.media && visible) seekMedia(clip, local);
      }
    }
    return state.time;
  }

  // mountScene writes a scene file's markup into its host and runs the
  // scene's scripts, the way the HyperFrames runtime mounts a sub-composition.
  function mountScene(scene) {
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
    host.appendChild(fragment);
    const mounted = host.querySelector("[data-composition-id]") || root;
    for (const code of scripts) runScript(code);
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
    state.errors = [];
    state.fps = num(payload.fps, 30);
    state.duration = num(payload.duration, 0);
    state.comps = [];
    const rootEl = document.querySelector(`[data-composition-id="${payload.rootId}"]`);
    if (!rootEl) {
      record(`The composition root #${payload.rootId} is missing.`);
      state.mounted = true;
      window.__videoReady = true;
      return info();
    }
    const comps = [{ id: payload.rootId, el: rootEl, window: null, playbackStart: 0, rate: 1 }];
    for (const scene of payload.scenes || []) {
      const mounted = mountScene(scene);
      if (!mounted) continue;
      comps.push({
        id: scene.compId || mounted.getAttribute("data-composition-id") || "",
        el: mounted,
        window: { start: num(scene.start, 0), duration: Math.max(0, num(scene.duration, 0)) },
        playbackStart: num(scene.playbackStart, 0),
        rate: Math.max(0.01, num(scene.rate, 1)),
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
      });
    }
    for (const comp of comps) {
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
    seek(t) {
      if (!state.mounted) return state.time;
      return apply(t);
    },
    info,
    gsap: gsapHere,
  };
})();
