// Execute the real preview runtime against parsed composition fixtures. This
// small DOM double models tree queries and inherited visibility; animation
// painting stays with the browser. No npm dependency or browser is required.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const fixture = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'));
let context;
const listeners = [];
class Style {
  constructor(text = '') {
    this.values = new Map(); this.priorities = new Map();
    for (const declaration of text.split(';')) {
      const at = declaration.indexOf(':'); if (at < 0) continue;
      const name = declaration.slice(0, at).trim();
      const value = declaration.slice(at + 1).trim();
      this.setProperty(name, value.replace(/\s*!important$/, ''), value.endsWith('!important') ? 'important' : '');
    }
  }
  setProperty(k, v, priority = '') { this.values.set(k, String(v)); this.priorities.set(k, priority); }
  getPropertyValue(k) { return this.values.get(k) || ''; }
  getPropertyPriority(k) { return this.priorities.get(k) || ''; }
  removeProperty(k) { this.values.delete(k); this.priorities.delete(k); }
}
class Element {
  constructor(model) {
    this.model = model; this.tagName = model.tag.toUpperCase(); this.nodeType = model.tag === '#fragment' ? 11 : 1;
    this.attrs = {...model.attrs}; this.style = new Style(this.attrs.style); this.dataset = {};
    this.childNodes = []; this.parentElement = null; this.textContent = model.text || '';
    if (model.tag === 'template') {
      this.content = new Element({tag:'#fragment'});
      for (const child of model.children || []) this.content.appendChild(new Element(child));
    } else for (const child of model.children || []) this.appendChild(new Element(child));
  }
  hasAttribute(k) { return Object.hasOwn(this.attrs, k); }
  getAttribute(k) { return this.attrs[k] ?? null; }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  appendChild(node) {
    if (node.nodeType === 11) { for (const child of [...node.childNodes]) this.appendChild(child); return node; }
    node.remove(); this.childNodes.push(node); node.parentElement = this;
    if (this.tagName === 'BODY' && node.tagName === 'SCRIPT' && context) {
      try { vm.runInContext(node.textContent, context); } catch(error) { for (const fn of listeners) fn({message:error.message}); }
    }
    return node;
  }
  removeChild(node) { this.childNodes.splice(this.childNodes.indexOf(node), 1); node.parentElement = null; }
  remove() { if (this.parentElement) this.parentElement.removeChild(this); }
  matches(selector) {
    selector = selector.trim();
    if (selector.startsWith('#')) return this.getAttribute('id') === selector.slice(1);
    if (selector.startsWith('.')) return (this.getAttribute('class') || '').split(/\s+/).includes(selector.slice(1));
    const attr = /^\[([^=\]]+)(?:="([^"]*)")?\]$/.exec(selector);
    if (attr) return this.hasAttribute(attr[1]) && (attr[2] === undefined || this.getAttribute(attr[1]) === attr[2]);
    return this.tagName === selector.toUpperCase();
  }
  closest(selector) { return this.matches(selector) ? this : this.parentElement?.closest(selector) || null; }
  querySelectorAll(selector) {
    const result = [];
    for (const child of this.childNodes) {
      if (selector.split(',').some(s => child.matches(s))) result.push(child);
      result.push(...child.querySelectorAll(selector));
    }
    return result;
  }
  querySelector(selector) { return this.querySelectorAll(selector)[0] || null; }
}
const document = new Element(fixture.document);
document.documentElement = document.querySelector('html');
document.body = document.querySelector('body');
document.getElementById = id => document.querySelector('#'+id);
document.createElement = tag => new Element({tag});
document.createDocumentFragment = () => new Element({tag:'#fragment'});
document.createTextNode = text => {const node = new Element({tag:'#text',text}); node.nodeType=3; return node;};
document.importNode = node => new Element(node.model);
const computed = el => ({
  get visibility() { return el.style.getPropertyValue('visibility') || (el.parentElement ? computed(el.parentElement).visibility : 'visible'); },
  getPropertyValue(k) { return el.style.getPropertyValue(k) || (el.parentElement ? computed(el.parentElement).getPropertyValue(k) : ''); },
});
const timelines = [];
const gsap = {timeline() {
  const tl={at:0,disposed:false,fromTo(){return this;},to(){return this;},seek(t){this.at=t;return this;},revert(){this.disposed=true;}};
  timelines.push(tl); return tl;
}};
context=vm.createContext({document,gsap,getComputedStyle:computed,console,
  DOMParser:class {parseFromString(html) {return new Element(fixture.scenes[html]);}},
});
context.window=context;
context.addEventListener=(_name,fn)=>listeners.push(fn);
// The document's scripts run before the editor injects its runtime.
for (const script of document.querySelectorAll('script')) if (!script.hasAttribute('src')) vm.runInContext(script.textContent,context);
vm.runInContext(fs.readFileSync(process.argv[3], 'utf8'),context);
const preview=context.__video;
const payload=fixture.payload;
const visibleScenes=()=>payload.scenes.filter(s=>{
  const host=document.getElementById(s.hostId);
  const root=host.querySelector('[data-composition-id]');
  return root && computed(root).visibility !== 'hidden';
}).map(s=>s.compId);
const checkScene=(time,scene,end)=>{
  assert.equal(preview.seek(time,end),time,'transport time must stay at the requested position');
  assert.deepEqual(visibleScenes(),[scene],`wrong visible scene at ${time}`);
};
assert.deepEqual([...preview.mount(payload).errors],[]);
const count=document.querySelectorAll('[data-composition-src]').reduce((n,h)=>n+h.childNodes.length,0);
for (let pass=0;pass<3;pass++) {
  for (const scene of payload.scenes) checkScene(scene.start+scene.duration/2,scene.compId);
  const last=payload.scenes.at(-1);
  checkScene(payload.duration,last.compId);
  assert.ok(context.__timelines[last.compId].at < last.duration,'end seek must sample an existing frame');
  const old=context.__timelines[last.compId];
  assert.deepEqual([...preview.mount(payload).errors],[]);
  assert.ok(old.disposed,'remount must dispose the previous scene animation');
  assert.equal(document.querySelectorAll('[data-composition-src]').reduce((n,h)=>n+h.childNodes.length,0),count,'remount duplicated scene nodes');
}
for (const scene of payload.scenes) checkScene(scene.start+scene.duration,scene.compId,scene.start+scene.duration);
// An authored hidden element must keep its inline visibility and priority.
const authored=document.createElement('div');
authored.setAttribute('id','authored-hidden');
authored.setAttribute('data-start','0');authored.setAttribute('data-duration',String(payload.duration));
authored.style.setProperty('visibility','hidden','important');
document.getElementById(payload.rootId).appendChild(authored);
preview.mount(payload);preview.seek(1);preview.mount(payload);preview.seek(payload.duration);
assert.equal(authored.style.getPropertyValue('visibility'),'hidden');
assert.equal(authored.style.getPropertyPriority('visibility'),'important');
// A timeline's initial autoAlpha:0 must not permanently hide its clip.
const animated=document.createElement('div');
animated.setAttribute('id','animated-visibility');
animated.setAttribute('data-start','0');animated.setAttribute('data-duration',String(payload.duration/2));
animated.style.setProperty('visibility','hidden');
document.getElementById(payload.rootId).appendChild(animated);
const rootTimeline=context.__timelines[payload.rootId];
const rootSeek=rootTimeline.seek;
rootTimeline.seek=function(t) { animated.style.setProperty('visibility',t>0.1?'visible':'hidden');return rootSeek.call(this,t); };
preview.mount(payload);preview.seek(1);
assert.equal(computed(animated).visibility,'visible','animation stayed hidden after its first frame');
preview.seek(payload.duration);preview.seek(1);
assert.equal(computed(animated).visibility,'visible','timing mask overwrote animated visibility');
// Overrides reset to each composition's own defaults, including text bindings.
const bound=document.createElement('span');
bound.setAttribute('data-var-text','test-value');
document.getElementById(payload.rootId).appendChild(bound);
payload.declared=Object.assign({},payload.declared,{'test-value':'root default'});
preview.mount(payload);
preview.setVariables({'test-value':'updated','other-value':42});
assert.equal(bound.childNodes.at(-1).textContent,'updated');
assert.equal(preview.variables()['other-value'],42);
preview.setVariables({});
assert.equal(bound.childNodes.at(-1).textContent,'root default','reset kept a stale override');
assert.equal(preview.variables()['other-value'],undefined);
// A declared-but-unavailable animation must be an explicit preview error.
payload.timelines.push('missing-test-timeline');
assert.match(preview.mount(payload).errors.join(' '),/missing-test-timeline/);
console.log(`preview runtime: ${payload.scenes.length} scenes, end frames, repeated mounts and authored visibility passed`);
